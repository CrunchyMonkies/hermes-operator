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
	"fmt"
	"maps"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// webhookPlatform describes the env a channel type needs to run hermes' inbound
// HTTP webhook server. portEnv carries the local listen port; urlEnv carries the
// full public webhook URL; secretEnv names the verification-secret env (supplied
// by the user via the channel secretRef/envFrom, never set by the operator).
type webhookPlatform struct {
	portEnv   string
	urlEnv    string
	secretEnv string
}

// webhookPlatforms is the registry of channel types that actually serve an
// INBOUND webhook in hermes. Verified against third_party/hermes-agent at tag
// v2026.6.5 — RE-VERIFY WHEN BUMPING THE HERMES TAG.
//
// Of the channel types the operator exposes, only telegram qualifies:
// gateway/platforms/telegram.py starts a webhook server when TELEGRAM_WEBHOOK_URL
// is set (with a REQUIRED TELEGRAM_WEBHOOK_SECRET) and otherwise long-polls.
// discord (gateway websocket), slack (socket mode), and whatsapp/signal/email
// (bridge/SMTP/outbound) never receive inbound webhooks, so they are
// intentionally absent and are never auto-wired. teams/google_chat have no
// adapter. (Upstream added wecom_callback/matrix/dingtalk/feishu adapters at
// this tag, but the operator does not expose those channel types.)
var webhookPlatforms = map[string]webhookPlatform{
	"telegram": {
		portEnv:   "TELEGRAM_WEBHOOK_PORT",
		urlEnv:    "TELEGRAM_WEBHOOK_URL",
		secretEnv: "TELEGRAM_WEBHOOK_SECRET",
	},
}

// webhookCapable reports whether a channel type serves an inbound webhook.
func webhookCapable(channelType string) bool {
	_, ok := webhookPlatforms[channelType]
	return ok
}

// channelWantsWebhook reports whether a channel has opted into webhook mode: it
// must be enabled, request an ingress, and be a webhook-capable type. spec.host
// alone never flips a polling channel into webhook mode.
func channelWantsWebhook(ch hermesv1alpha1.ChannelSpec) bool {
	return ch.Enabled && ch.Ingress.Enabled && webhookCapable(ch.Type)
}

// channelEffectiveHost returns the host for a channel's webhook ingress: the
// channel's own ingress.host if set, else the shared spec.ingress.host.
func channelEffectiveHost(a *hermesv1alpha1.HermesAgent, ch hermesv1alpha1.ChannelSpec) string {
	if ch.Ingress.Host != "" {
		return ch.Ingress.Host
	}
	return a.Spec.Ingress.Host
}

// mergedIngress overlays a per-surface IngressSpec onto the shared spec.ingress:
// host/className/pathType/tls are inherited when the surface leaves them unset,
// and annotations are merged (surface keys win). enabled and path stay per-surface.
func mergedIngress(shared, surface hermesv1alpha1.IngressSpec) hermesv1alpha1.IngressSpec {
	out := surface
	if out.Host == "" {
		out.Host = shared.Host
	}
	if out.ClassName == "" {
		out.ClassName = shared.ClassName
	}
	if out.PathType == "" {
		out.PathType = shared.PathType
	}
	if len(out.TLS) == 0 {
		out.TLS = shared.TLS
	}
	if len(shared.Annotations) > 0 || len(surface.Annotations) > 0 {
		merged := make(map[string]string, len(shared.Annotations)+len(surface.Annotations))
		maps.Copy(merged, shared.Annotations)
		maps.Copy(merged, surface.Annotations)
		out.Annotations = merged
	}
	return out
}

// webhookPath is the HTTP path a default-profile channel's inbound webhook is
// routed at.
func webhookPath(channelType string) string { return WebhookPathPrefix + channelType }

// webhookURL builds the public HTTPS URL hermes registers with the platform.
// Telegram mandates a public HTTPS endpoint, so the scheme is always https.
func webhookURL(host, path string) string { return "https://" + host + path }

// webhookEndpoint is one resolved inbound-webhook endpoint for a (profile,
// channel). profile == "" is the default profile.
type webhookEndpoint struct {
	profile     string
	channelType string
	port        int32
	portName    string
	host        string
	path        string
	wantsURL    bool
	ch          hermesv1alpha1.ChannelSpec
}

// url is the public webhook URL for this endpoint (empty when no host).
func (e webhookEndpoint) url() string {
	if e.host == "" {
		return ""
	}
	return webhookURL(e.host, e.path)
}

// webhookEndpoints flattens every profile's webhook-capable channels (default
// profile first, then each named profile) into a pod-wide list with GLOBALLY
// unique ports — all profile gateways share the pod network namespace, so ports
// must not collide. Assignment reserves apiServer/dashboard and every explicit
// ch.WebhookPort, then hands out ports from WebhookPortBase upward; stable across
// reconciles for a fixed spec.
//
// Service port names are ≤15 chars: the default profile keeps wh-<type>
// (back-compat); named-profile endpoints use a short wh-<n> scheme. Paths are
// /webhooks/<type> for the default profile and /webhooks/<profile>/<type> for
// named profiles so routing is unambiguous.
func webhookEndpoints(a *hermesv1alpha1.HermesAgent) []webhookEndpoint {
	reserved := map[int32]bool{APIPort: true, DashboardPort: true}
	if a.Spec.APIServer.Enabled && a.Spec.APIServer.Port != 0 {
		reserved[a.Spec.APIServer.Port] = true
	}
	if a.Spec.Dashboard.Enabled && a.Spec.Dashboard.Port != 0 {
		reserved[a.Spec.Dashboard.Port] = true
	}

	type group struct {
		name     string
		channels []hermesv1alpha1.ChannelSpec
	}
	groups := make([]group, 0, 1+len(a.Spec.Profiles))
	groups = append(groups, group{name: "", channels: a.Spec.DefaultProfile.Channels})
	for _, p := range a.Spec.Profiles {
		groups = append(groups, group{name: p.Name, channels: p.Channels})
	}

	// First pass: reserve explicit ports across all profiles.
	for _, g := range groups {
		for _, ch := range g.channels {
			if ch.WebhookPort > 0 {
				reserved[ch.WebhookPort] = true
			}
		}
	}

	var out []webhookEndpoint
	next := WebhookPortBase
	namedIdx := 0
	for _, g := range groups {
		for _, ch := range g.channels {
			if !webhookCapable(ch.Type) {
				continue
			}
			var port int32
			switch {
			case ch.WebhookPort > 0:
				port = ch.WebhookPort
			case channelWantsWebhook(ch):
				for reserved[next] {
					next++
				}
				port = next
				reserved[next] = true
				next++
			default:
				continue // polling: no Service port
			}
			host := channelEffectiveHost(a, ch)
			ep := webhookEndpoint{
				profile:     g.name,
				channelType: ch.Type,
				port:        port,
				host:        host,
				wantsURL:    channelWantsWebhook(ch) && host != "",
				ch:          ch,
			}
			if g.name == "" {
				ep.portName = channelPortName(ch.Type)
				ep.path = webhookPath(ch.Type)
			} else {
				ep.portName = fmt.Sprintf("wh-%d", namedIdx)
				ep.path = WebhookPathPrefix + g.name + "/" + ch.Type
				namedIdx++
			}
			out = append(out, ep)
		}
	}
	return out
}

// profileWebhookEndpoints returns the endpoints for one profile (name "" = default).
func profileWebhookEndpoints(a *hermesv1alpha1.HermesAgent, profile string) []webhookEndpoint {
	var out []webhookEndpoint
	for _, ep := range webhookEndpoints(a) {
		if ep.profile == profile {
			out = append(out, ep)
		}
	}
	return out
}
