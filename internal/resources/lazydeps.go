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

// Feature pip dependencies, mirroring third_party/hermes-agent/tools/lazy_deps.py
// at the pinned hermes tag (v2026.5.29.2). hermes lazy-installs these at first use
// into the image's ephemeral .venv; the operator instead pre-installs them onto
// the shared PVC (the pip-install init container, importable via PYTHONPATH) so
// they persist across restarts and need no runtime network/lazy-install.
//
// RE-VERIFY THESE PINS WHEN BUMPING THE HERMES TAG (they must match lazy_deps.py).
var (
	// channelPipDeps maps a messaging platform (channels[].type) to the packages
	// the base image lacks. Platforms not listed (teams, google_chat, …) are
	// bundled and need nothing.
	channelPipDeps = map[string][]string{
		"telegram": {"python-telegram-bot[webhooks]==22.6"},
		"discord":  {"discord.py[voice]==2.7.1", "brotlicffi==1.2.0.1"},
		"slack":    {"slack-bolt==1.27.0", "slack-sdk==3.40.1", "aiohttp==3.13.4"},
	}

	// backendPipDeps maps a remote terminal backend (runtime.terminalBackend) to
	// its SDK. local/docker are bundled; ssh/singularity need system binaries (not
	// pip) and are not handled here. (vercel_sandbox was removed upstream at
	// v2026.5.29.2 — terminal.vercel no longer exists in lazy_deps.py.)
	backendPipDeps = map[string][]string{
		"modal":   {"modal==1.3.4"},
		"daytona": {"daytona==0.155.0"},
	}
)
