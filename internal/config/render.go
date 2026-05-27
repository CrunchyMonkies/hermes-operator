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

// Package config renders a HermesAgent spec into the on-disk config.yaml the
// Hermes gateway consumes, plus the SOUL.md persona file. Keys mirror the
// verified upstream cli-config.yaml.example (tag v2026.5.16). See spec §3.4.
package config

import (
	"encoding/json"
	"sort"

	"sigs.k8s.io/yaml"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// ConfigVersion is the upstream _config_version at the pinned tag (v2026.5.16).
const ConfigVersion = 23

// RenderConfigYAML builds config.yaml from the typed spec, applies extraConfig
// per precedence, and marshals to YAML. `${VAR}` strings pass through untouched
// (Hermes interpolates them at runtime).
func RenderConfigYAML(spec *hermesv1alpha1.HermesAgentSpec) ([]byte, error) {
	root := renderTyped(spec)

	if spec.ExtraConfig != nil && len(spec.ExtraConfig.Raw) > 0 {
		extra := map[string]any{}
		if err := json.Unmarshal(spec.ExtraConfig.Raw, &extra); err != nil {
			return nil, err
		}
		if spec.ExtraConfigPrecedence == "override" {
			root = deepMerge(root, extra)
		} else {
			// merge: extraConfig fills gaps but typed fields win on conflict.
			root = deepMerge(extra, root)
		}
	}

	return yaml.Marshal(root)
}

// renderTyped converts the typed spec sections into a config.yaml map using the
// exact upstream key names.
func renderTyped(spec *hermesv1alpha1.HermesAgentSpec) map[string]any {
	root := map[string]any{
		"_config_version": ConfigVersion,
	}

	// model: — when provider names a CUSTOM providers entry, base_url/api_mode
	// default from it (so they aren't repeated); an explicit value still wins.
	// Built-in providers carry their own endpoint, so nothing is derived.
	model := map[string]any{}
	baseURL, apiMode := spec.Model.BaseURL, spec.Model.APIMode
	if sel := findProvider(spec.Model.Providers, spec.Model.Provider); sel != nil && sel.IsCustom() {
		if baseURL == "" {
			baseURL = sel.BaseURL
		}
		if apiMode == "" {
			apiMode = sel.APIMode
		}
	}
	putStr(model, "default", spec.Model.Default)
	putStr(model, "provider", spec.Model.Provider)
	putStr(model, "base_url", baseURL)
	putStr(model, "api_mode", apiMode)
	putNonZeroInt(model, "context_length", spec.Model.ContextLength)
	putNonZeroInt(model, "max_tokens", spec.Model.MaxTokens)
	putSection(root, "model", model)

	// custom_providers: (top-level) — CUSTOM endpoints only; built-ins aren't
	// emitted here (hermes knows their URLs).
	if cps := renderCustomProviders(spec.Model.Providers); len(cps) > 0 {
		root["custom_providers"] = cps
	}

	// agent:
	agent := map[string]any{}
	putNonZeroInt(agent, "max_turns", int64(spec.Agent.MaxTurns))
	putStr(agent, "reasoning_effort", spec.Agent.ReasoningEffort)
	if len(spec.Agent.DisabledToolsets) > 0 {
		agent["disabled_toolsets"] = spec.Agent.DisabledToolsets
	}
	putSection(root, "agent", agent)

	// compression: (hot-reloadable)
	comp := map[string]any{}
	putBoolPtr(comp, "enabled", spec.Compression.Enabled)
	putFloatStr(comp, "threshold", spec.Compression.Threshold)
	putFloatStr(comp, "target_ratio", spec.Compression.TargetRatio)
	putIntPtr(comp, "protect_last_n", spec.Compression.ProtectLastN)
	putIntPtr(comp, "protect_first_n", spec.Compression.ProtectFirstN)
	putSection(root, "compression", comp)

	// memory:
	mem := map[string]any{}
	putBoolPtr(mem, "memory_enabled", spec.Memory.MemoryEnabled)
	putBoolPtr(mem, "user_profile_enabled", spec.Memory.UserProfileEnabled)
	putSection(root, "memory", mem)

	// terminal: (from runtime)
	terminal := map[string]any{}
	putStr(terminal, "backend", spec.Runtime.TerminalBackend)
	putNonZeroInt(terminal, "timeout", int64(spec.Runtime.TerminalTimeout))
	if spec.Runtime.TerminalBackend == "docker" {
		terminal["docker_mount_cwd_to_workspace"] = spec.Runtime.Docker.MountCwdToWorkspace
		// Tools run in dind-spawned containers (a generic image) that inherit none
		// of the agent's ~/.kube/config, SA token, or brew prefix. Everything that
		// lives on the shared PVC is already mounted into the dind daemon at its
		// identical path, so bind-mount what's needed into each tool container and
		// set matching env. extraConfig can override any of this.
		var dockerVolumes []string
		dockerEnv := map[string]string{}
		var dockerExtraArgs []string

		if spec.Kubeconfig.Enabled {
			const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"
			dockerVolumes = append(dockerVolumes,
				"/opt/data/.kube/config:/root/.kube/config:ro",
				saDir+":"+saDir+":ro",
			)
			dockerEnv["KUBECONFIG"] = "/root/.kube/config"
			// Share the pod network so kubectl can resolve/reach kubernetes.default.svc.
			dockerExtraArgs = append(dockerExtraArgs, "--network=host")
		}

		// brew packages live on the shared PVC at the Homebrew prefix; mount it
		// (read-only) and put its bin on PATH so brew-installed tools are available
		// in the sandbox. (For packages a generic tool image lacks, prefer brew or a
		// custom terminal.docker_image via extraConfig.)
		if len(spec.Packages.Brew) > 0 {
			prefix := spec.Packages.HomebrewPrefix
			if prefix == "" {
				prefix = "/home/linuxbrew/.linuxbrew"
			}
			dockerVolumes = append(dockerVolumes, prefix+":"+prefix+":ro")
			dockerEnv["PATH"] = prefix + "/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		}

		if len(dockerVolumes) > 0 {
			terminal["docker_volumes"] = dockerVolumes
		}
		if len(dockerEnv) > 0 {
			terminal["docker_env"] = dockerEnv
		}
		if len(dockerExtraArgs) > 0 {
			terminal["docker_extra_args"] = dockerExtraArgs
		}
	}
	putSection(root, "terminal", terminal)

	// code_execution:
	codeExec := map[string]any{}
	putNonZeroInt(codeExec, "timeout", int64(spec.Runtime.CodeExecution.Timeout))
	putNonZeroInt(codeExec, "max_tool_calls", int64(spec.Runtime.CodeExecution.MaxToolCalls))
	putSection(root, "code_execution", codeExec)

	// delegation:
	deleg := map[string]any{}
	putNonZeroInt(deleg, "max_iterations", int64(spec.Runtime.Delegation.MaxIterations))
	putNonZeroInt(deleg, "max_concurrent_children", int64(spec.Runtime.Delegation.MaxConcurrentChildren))
	putSection(root, "delegation", deleg)

	// skills:
	skills := map[string]any{}
	if len(spec.Skills.Disabled) > 0 {
		skills["disabled"] = sortedCopy(spec.Skills.Disabled)
	}
	if len(spec.Skills.PlatformDisabled) > 0 {
		pd := map[string]any{}
		for platform, names := range spec.Skills.PlatformDisabled {
			pd[platform] = sortedCopy(names)
		}
		skills["platform_disabled"] = pd
	}
	if len(spec.Skills.ExternalDirs) > 0 {
		skills["external_dirs"] = spec.Skills.ExternalDirs
	}
	putNonZeroInt(skills, "creation_nudge_interval", int64(spec.Skills.CreationNudgeInterval))
	putSection(root, "skills", skills)

	return root
}

// findProvider returns the provider whose Name matches the selector
// (e.g. model.provider), or nil.
func findProvider(in []hermesv1alpha1.ProviderSpec, name string) *hermesv1alpha1.ProviderSpec {
	if name == "" {
		return nil
	}
	for i := range in {
		if in[i].Name == name {
			return &in[i]
		}
	}
	return nil
}

// renderCustomProviders emits config.yaml `custom_providers:` from the CUSTOM
// entries (those with a baseURL); built-in providers are skipped (hermes knows
// their endpoints). `models` becomes a map keyed by model name (the shape hermes'
// normalizer expects); key_env is the resolved env var the operator injects into.
func renderCustomProviders(in []hermesv1alpha1.ProviderSpec) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, cp := range in {
		if !cp.IsCustom() {
			continue
		}
		entry := map[string]any{}
		putStr(entry, "name", cp.Name)
		putStr(entry, "base_url", cp.BaseURL)
		putStr(entry, "api_mode", cp.APIMode)
		// Only point hermes at a key env var when a key is actually supplied
		// (explicit keyEnv or an operator-injected keySecretRef); otherwise leave it
		// unset so hermes falls back to auth.json / ambient env.
		if cp.KeyEnv != "" || cp.KeySecretRef != nil {
			putStr(entry, "key_env", cp.KeyEnvVar())
		}
		if len(cp.Models) > 0 {
			models := map[string]any{}
			for _, m := range cp.Models {
				mc := map[string]any{}
				putNonZeroInt(mc, "context_length", m.ContextLength)
				models[m.Name] = mc
			}
			entry["models"] = models
		}
		out = append(out, entry)
	}
	return out
}

// RenderSoul returns the SOUL.md content (empty spec.soul yields no file).
func RenderSoul(spec *hermesv1alpha1.HermesAgentSpec) string {
	return spec.Soul
}

// --- helpers ---

func putStr(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func putNonZeroInt(m map[string]any, k string, v int64) {
	if v != 0 {
		m[k] = v
	}
}

func putBoolPtr(m map[string]any, k string, v *bool) {
	if v != nil {
		m[k] = *v
	}
}

func putIntPtr(m map[string]any, k string, v *int32) {
	if v != nil {
		m[k] = *v
	}
}

// putFloatStr stores a numeric string (e.g. "0.50") as a float so config.yaml
// renders a YAML number, matching upstream's threshold/target_ratio types.
func putFloatStr(m map[string]any, k string, v *string) {
	if v == nil || *v == "" {
		return
	}
	// Best-effort numeric; fall back to the raw string if not parseable.
	var f float64
	if err := json.Unmarshal([]byte(*v), &f); err == nil {
		m[k] = f
		return
	}
	m[k] = *v
}

func putSection(root map[string]any, name string, section map[string]any) {
	if len(section) > 0 {
		root[name] = section
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// deepMerge returns base with overlay applied: overlay keys win, nested maps
// merge recursively. Inputs are not mutated.
func deepMerge(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range overlay {
		if bv, ok := out[k]; ok {
			bm, bok := bv.(map[string]any)
			om, ook := ov.(map[string]any)
			if bok && ook {
				out[k] = deepMerge(bm, om)
				continue
			}
		}
		out[k] = ov
	}
	return out
}
