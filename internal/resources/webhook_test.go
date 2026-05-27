/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resources

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

const testHost = "lana.example.com"

func servicePort(a *hermesv1alpha1.HermesAgent, name string) (int32, bool) {
	svc := Service(a)
	if svc == nil {
		return 0, false
	}
	for _, p := range svc.Spec.Ports {
		if p.Name == name {
			return p.Port, true
		}
	}
	return 0, false
}

func ingressBySurface(a *hermesv1alpha1.HermesAgent, surface string) *networkingv1.Ingress {
	for _, ing := range Ingresses(a) {
		if ing.Name == IngressName(a, surface) {
			return ing
		}
	}
	return nil
}

// telegram opting into a webhook with spec.host but no explicit port: the
// operator assigns a deterministic port, exposes it, renders the /webhooks/telegram
// ingress on spec.host, and sets the port + public URL env.
func TestTelegramWebhookAutoWired(t *testing.T) {
	a := baseAgent()
	a.Spec.Host = testHost
	a.Spec.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "telegram", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
	}

	port, ok := servicePort(a, channelPortName("telegram"))
	if !ok {
		t.Fatal("telegram webhook Service port missing")
	}
	if port < WebhookPortBase || port == APIPort || port == DashboardPort {
		t.Errorf("auto port %d not in the expected free range", port)
	}

	ing := ingressBySurface(a, "wh-telegram")
	if ing == nil {
		t.Fatal("telegram webhook Ingress missing")
	}
	rule := ing.Spec.Rules[0]
	if rule.Host != testHost {
		t.Errorf("ingress host = %q, want spec.host", rule.Host)
	}
	if got := rule.HTTP.Paths[0].Path; got != "/webhooks/telegram" {
		t.Errorf("ingress path = %q, want /webhooks/telegram", got)
	}
	if got := rule.HTTP.Paths[0].Backend.Service.Port.Name; got != channelPortName("telegram") {
		t.Errorf("ingress backend port name = %q", got)
	}

	if v, _, ok := envByName(a, "TELEGRAM_WEBHOOK_PORT"); !ok || v == "0" {
		t.Errorf("TELEGRAM_WEBHOOK_PORT = %q ok=%v", v, ok)
	}
	if v, _, ok := envByName(a, "TELEGRAM_WEBHOOK_URL"); !ok || v != "https://"+testHost+"/webhooks/telegram" {
		t.Errorf("TELEGRAM_WEBHOOK_URL = %q ok=%v", v, ok)
	}
	// The operator never sets the secret — it must come from the channel secretRef.
	if _, _, ok := envByName(a, "TELEGRAM_WEBHOOK_SECRET"); ok {
		t.Error("operator must not set TELEGRAM_WEBHOOK_SECRET")
	}
}

// An explicit webhookPort is honored as-is; auto-assignment skips reserved ports
// and is deterministic across calls.
func TestWebhookPortAssignment(t *testing.T) {
	// Explicit port wins, no auto-assignment.
	a := baseAgent()
	a.Spec.Host = testHost
	a.Spec.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "telegram", Enabled: true, WebhookPort: 9001, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
	}
	if p, _ := servicePort(a, channelPortName("telegram")); p != 9001 {
		t.Errorf("explicit webhookPort not honored: %d", p)
	}
	if v, _, _ := envByName(a, "TELEGRAM_WEBHOOK_PORT"); v != "9001" {
		t.Errorf("TELEGRAM_WEBHOOK_PORT = %q, want 9001", v)
	}

	// Auto-assignment skips a port reserved by the apiServer and is stable.
	b := baseAgent()
	b.Spec.Host = "h.example.com"
	b.Spec.APIServer = hermesv1alpha1.APIServerSpec{Enabled: true, Port: WebhookPortBase}
	b.Spec.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "telegram", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
	}
	got := resolvedWebhookPorts(b)
	if got[0] != WebhookPortBase+1 {
		t.Errorf("auto port = %d, want %d (skip reserved %d)", got[0], WebhookPortBase+1, WebhookPortBase)
	}
	if again := resolvedWebhookPorts(b); again[0] != got[0] {
		t.Errorf("port assignment not deterministic: %d vs %d", again[0], got[0])
	}
}

// Outbound-only channels are never auto-wired even with ingress.enabled + spec.host,
// and a polling (ingress-off) telegram gets no webhook env/objects.
func TestNonWebhookChannelsNotWired(t *testing.T) {
	a := baseAgent()
	a.Spec.Host = testHost
	a.Spec.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "discord", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
		{Type: "slack", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
		{Type: "telegram", Enabled: true}, // polling: ingress not enabled
	}

	if _, ok := servicePort(a, channelPortName("discord")); ok {
		t.Error("discord must not get a webhook Service port")
	}
	if _, ok := servicePort(a, channelPortName("slack")); ok {
		t.Error("slack must not get a webhook Service port")
	}
	if ingressBySurface(a, "wh-discord") != nil || ingressBySurface(a, "wh-slack") != nil {
		t.Error("outbound-only channels must not get a webhook Ingress")
	}
	// Polling telegram: spec.host set but ingress off -> no webhook flip (the lana guard).
	if ingressBySurface(a, "wh-telegram") != nil {
		t.Error("polling telegram must not get a webhook Ingress")
	}
	if _, _, ok := envByName(a, "TELEGRAM_WEBHOOK_URL"); ok {
		t.Error("polling telegram must not get TELEGRAM_WEBHOOK_URL")
	}
	if _, _, ok := envByName(a, "TELEGRAM_WEBHOOK_PORT"); ok {
		t.Error("polling telegram must not get TELEGRAM_WEBHOOK_PORT")
	}
}

// apiServer and dashboard ingresses inherit spec.host when their own host is empty,
// and a per-surface host still overrides it.
func TestSurfaceHostDefaultsToSpecHost(t *testing.T) {
	a := baseAgent()
	a.Spec.Host = "base.example.com"
	a.Spec.APIServer = hermesv1alpha1.APIServerSpec{
		Enabled: true,
		Ingress: hermesv1alpha1.IngressSpec{Enabled: true},
	}
	a.Spec.Dashboard = hermesv1alpha1.DashboardSpec{
		Enabled: true,
		Ingress: hermesv1alpha1.IngressSpec{Enabled: true, Host: "dash.example.com"},
	}

	api := ingressBySurface(a, "api")
	if api == nil || api.Spec.Rules[0].Host != "base.example.com" {
		t.Errorf("apiServer ingress should inherit spec.host, got %v", api)
	}
	dash := ingressBySurface(a, "dashboard")
	if dash == nil || dash.Spec.Rules[0].Host != "dash.example.com" {
		t.Errorf("dashboard ingress own host should win, got %v", dash)
	}
}
