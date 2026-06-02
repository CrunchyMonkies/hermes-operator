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
	"strings"
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
	a.Spec.Ingress = hermesv1alpha1.IngressSpec{
		Host:        testHost,
		ClassName:   "nginx",
		Annotations: map[string]string{"cert-manager.io/cluster-issuer": "prod"},
		TLS:         []hermesv1alpha1.IngressTLS{{Hosts: []string{testHost}, SecretName: "lana-tls"}},
	}
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{
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
		t.Errorf("ingress host = %q, want spec.ingress.host", rule.Host)
	}
	if got := rule.HTTP.Paths[0].Path; got != "/webhooks/telegram" {
		t.Errorf("ingress path = %q, want /webhooks/telegram", got)
	}
	if got := rule.HTTP.Paths[0].Backend.Service.Port.Name; got != channelPortName("telegram") {
		t.Errorf("ingress backend port name = %q", got)
	}
	// The webhook ingress inherits the shared ingress' className/annotations/tls.
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingress className not inherited: %v", ing.Spec.IngressClassName)
	}
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "prod" {
		t.Errorf("ingress annotations not inherited: %v", ing.Annotations)
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "lana-tls" {
		t.Errorf("ingress tls not inherited: %v", ing.Spec.TLS)
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

// A named profile's webhook-capable channel gets a globally-unique Service port
// (wh-<n> scheme), a profile-scoped Ingress at /webhooks/<profile>/<type>, and the
// operator-computed PORT/URL appended to that profile's .env — while the default
// profile (no channels) gets no container webhook env.
func TestNamedProfileWebhookWired(t *testing.T) {
	a := baseAgent()
	a.Spec.Ingress.Host = testHost
	a.Spec.Profiles = []hermesv1alpha1.ProfileSpec{
		{
			Name: "support",
			ProfileConfig: hermesv1alpha1.ProfileConfig{
				Channels: []hermesv1alpha1.ChannelSpec{
					{Type: "telegram", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
				},
			},
		},
	}

	// Globally-unique Service port under the named-profile scheme.
	if _, ok := servicePort(a, "wh-0"); !ok {
		t.Error("named profile webhook should get a Service port wh-0")
	}
	// Profile-scoped Ingress path.
	ing := ingressBySurface(a, "wh-support-telegram")
	if ing == nil {
		t.Fatal("named profile webhook ingress missing")
	}
	if got := ing.Spec.Rules[0].HTTP.Paths[0].Path; got != "/webhooks/support/telegram" {
		t.Errorf("named webhook path = %q, want /webhooks/support/telegram", got)
	}
	// config-init appends webhook PORT/URL to profiles/support/.env (non-secret).
	script := configInitCommand(a)[2]
	if !strings.Contains(script, "TELEGRAM_WEBHOOK_PORT=") || !strings.Contains(script, ">> /opt/data/profiles/support/.env") {
		t.Errorf("named profile .env should get webhook port appended:\n%s", script)
	}
	if !strings.Contains(script, "TELEGRAM_WEBHOOK_URL=https://"+testHost+"/webhooks/support/telegram") {
		t.Errorf("named profile .env should get webhook url appended:\n%s", script)
	}
	// Default profile has no channels -> no container webhook env.
	if _, _, ok := envByName(a, "TELEGRAM_WEBHOOK_PORT"); ok {
		t.Error("default profile must not get webhook env when it has no channels")
	}
}

// An explicit webhookPort is honored as-is; auto-assignment skips reserved ports
// and is deterministic across calls.
func TestWebhookPortAssignment(t *testing.T) {
	// Explicit port wins, no auto-assignment.
	a := baseAgent()
	a.Spec.Ingress.Host = testHost
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{
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
	b.Spec.Ingress.Host = testHost
	b.Spec.APIServer = hermesv1alpha1.APIServerSpec{Enabled: true, Port: WebhookPortBase}
	b.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "telegram", Enabled: true, Ingress: hermesv1alpha1.IngressSpec{Enabled: true}},
	}
	got := webhookEndpoints(b)
	if len(got) != 1 || got[0].port != WebhookPortBase+1 {
		t.Errorf("auto port = %v, want %d (skip reserved %d)", got, WebhookPortBase+1, WebhookPortBase)
	}
	if again := webhookEndpoints(b); len(again) != 1 || again[0].port != got[0].port {
		t.Errorf("port assignment not deterministic: %v vs %v", again, got)
	}
}

// Outbound-only channels are never auto-wired even with ingress.enabled + a shared
// host, and a polling (ingress-off) telegram gets no webhook env/objects.
func TestNonWebhookChannelsNotWired(t *testing.T) {
	a := baseAgent()
	a.Spec.Ingress.Host = testHost
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{
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
	// Polling telegram: shared host set but ingress off -> no webhook flip (the lana guard).
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

// The dashboard is exposed via the shared spec.ingress (inheriting its host); a
// per-surface ingress.host (here on apiServer) still overrides the shared host.
func TestSharedIngressHostInheritance(t *testing.T) {
	a := baseAgent()
	a.Spec.Ingress = hermesv1alpha1.IngressSpec{Enabled: true, Host: "base.example.com"}
	a.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: true}
	a.Spec.APIServer = hermesv1alpha1.APIServerSpec{
		Enabled: true,
		Ingress: hermesv1alpha1.IngressSpec{Enabled: true, Host: "api.example.com"},
	}

	dash := ingressBySurface(a, "dashboard")
	if dash == nil || dash.Spec.Rules[0].Host != "base.example.com" {
		t.Errorf("dashboard ingress should use shared spec.ingress.host, got %v", dash)
	}
	api := ingressBySurface(a, "api")
	if api == nil || api.Spec.Rules[0].Host != "api.example.com" {
		t.Errorf("apiServer ingress own host should override shared, got %v", api)
	}
}
