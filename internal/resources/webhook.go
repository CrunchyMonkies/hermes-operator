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
// v2026.5.16 — RE-VERIFY WHEN BUMPING THE HERMES TAG.
//
// Only telegram qualifies today: gateway/platforms/telegram.py starts a webhook
// server when TELEGRAM_WEBHOOK_URL is set (with a REQUIRED TELEGRAM_WEBHOOK_SECRET)
// and otherwise long-polls. discord (gateway websocket), slack (socket mode), and
// whatsapp/signal/email (bridge/SMTP/outbound) never receive inbound webhooks, so
// they are intentionally absent and are never auto-wired. teams/google_chat have
// no adapter at this tag.
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
// channel's own ingress.host if set, else the agent-level spec.host.
func channelEffectiveHost(a *hermesv1alpha1.HermesAgent, ch hermesv1alpha1.ChannelSpec) string {
	if ch.Ingress.Host != "" {
		return ch.Ingress.Host
	}
	return a.Spec.Host
}

// webhookPath is the HTTP path a channel's inbound webhook is routed at.
func webhookPath(channelType string) string { return WebhookPathPrefix + channelType }

// webhookURL is the public HTTPS URL hermes registers with the platform.
// Telegram mandates a public HTTPS endpoint, so the scheme is always https.
func webhookURL(host, channelType string) string {
	return "https://" + host + webhookPath(channelType)
}

// resolvedWebhookPorts returns the effective webhook port per channel (indexed to
// a.Spec.Channels): the explicit ch.WebhookPort when >0, else a deterministically
// assigned free port for channels that opt into webhook mode, else 0 (polling /
// no Service port).
//
// Assignment reserves the apiServer/dashboard ports and every explicit channel
// webhook port, then hands out ports from WebhookPortBase upward in channel order,
// skipping reserved/used ones. This is stable across reconciles for a fixed spec.
func resolvedWebhookPorts(a *hermesv1alpha1.HermesAgent) []int32 {
	ports := make([]int32, len(a.Spec.Channels))

	reserved := map[int32]bool{APIPort: true, DashboardPort: true}
	if a.Spec.APIServer.Enabled && a.Spec.APIServer.Port != 0 {
		reserved[a.Spec.APIServer.Port] = true
	}
	if a.Spec.Dashboard.Enabled && a.Spec.Dashboard.Port != 0 {
		reserved[a.Spec.Dashboard.Port] = true
	}

	// First pass: honor explicit ports and reserve them.
	for i, ch := range a.Spec.Channels {
		if ch.WebhookPort > 0 {
			ports[i] = ch.WebhookPort
			reserved[ch.WebhookPort] = true
		}
	}

	// Second pass: auto-assign a stable free port to opted-in channels at 0.
	next := WebhookPortBase
	for i, ch := range a.Spec.Channels {
		if ports[i] != 0 || !channelWantsWebhook(ch) {
			continue
		}
		for reserved[next] {
			next++
		}
		ports[i] = next
		reserved[next] = true
		next++
	}
	return ports
}
