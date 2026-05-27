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

package config

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

const ccMode = "chat_completions"

func ptrBool(b bool) *bool    { return &b }
func ptrStr(s string) *string { return &s }
func ptrI32(i int32) *int32   { return &i }

func mustParse(t *testing.T, b []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal rendered config: %v", err)
	}
	return out
}

func TestRenderCustomProviders(t *testing.T) {
	// provider names the custom provider; base_url/api_mode are NOT set on model
	// and must be derived from that provider.
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model: hermesv1alpha1.ModelSpec{
			Default:  "gemma-4-e4b-it",
			Provider: "llm",
			Providers: []hermesv1alpha1.ProviderSpec{{
				Name:    "llm",
				BaseURL: "https://llm.example/v1",
				APIMode: ccMode,
				Models: []hermesv1alpha1.ProviderModelSpec{
					{Name: "gemma-4-e4b-it", ContextLength: 131072},
					{Name: "qwen", ContextLength: 0}, // no per-model window -> empty map (auto-probe)
				},
			}},
		},
	}

	got := mustParse(t, mustRender(t, spec))

	// model.base_url / api_mode derived from the selected provider (not repeated).
	model := got["model"].(map[string]any)
	if model["base_url"] != "https://llm.example/v1" || model["api_mode"] != ccMode {
		t.Errorf("model endpoint not derived from provider: %v", model)
	}

	cps, ok := got["custom_providers"].([]any)
	if !ok || len(cps) != 1 {
		t.Fatalf("custom_providers = %v", got["custom_providers"])
	}
	cp := cps[0].(map[string]any)
	if cp["name"] != "llm" || cp["base_url"] != "https://llm.example/v1" || cp["api_mode"] != ccMode {
		t.Errorf("provider entry fields wrong: %v", cp)
	}
	// No key supplied (no keyEnv/keySecretRef) -> key_env omitted (falls back to auth.json).
	if _, ok := cp["key_env"]; ok {
		t.Errorf("key_env should be omitted when no key is supplied, got %v", cp["key_env"])
	}
	models := cp["models"].(map[string]any)
	gemma := models["gemma-4-e4b-it"].(map[string]any)
	if v, _ := gemma["context_length"].(float64); int(v) != 131072 {
		t.Errorf("gemma context_length = %v, want 131072", gemma["context_length"])
	}
	// A model without a context window renders an empty map (hermes auto-probes).
	qwen := models["qwen"].(map[string]any)
	if len(qwen) != 0 {
		t.Errorf("qwen entry should be empty (no context_length), got %v", qwen)
	}

	// An explicit model.baseURL overrides the derived value.
	spec.Model.BaseURL = "https://override/v1"
	got2 := mustParse(t, mustRender(t, spec))
	if got2["model"].(map[string]any)["base_url"] != "https://override/v1" {
		t.Errorf("explicit baseURL should override derived")
	}

	// With a keySecretRef, key_env is emitted (the operator injects the key there).
	spec.Model.BaseURL = ""
	spec.Model.Providers[0].KeySecretRef = &hermesv1alpha1.SecretKeyRef{Name: "k", Key: "llm"}
	got3 := mustParse(t, mustRender(t, spec))
	cp3 := got3["custom_providers"].([]any)[0].(map[string]any)
	if cp3["key_env"] != "LLM_API_KEY" {
		t.Errorf("key_env = %v, want LLM_API_KEY", cp3["key_env"])
	}
}

func TestRenderBuiltinProviderNoCustomRow(t *testing.T) {
	// A built-in provider (no baseURL) must NOT appear in custom_providers, and the
	// operator must not synthesize a model.base_url for it (hermes knows the URL).
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model: hermesv1alpha1.ModelSpec{
			Default:  "claude-opus-4-7",
			Provider: "claude",
			Providers: []hermesv1alpha1.ProviderSpec{{
				Name:         "claude",
				KeySecretRef: &hermesv1alpha1.SecretKeyRef{Name: "llm-keys", Key: "anthropic"},
			}},
		},
	}
	got := mustParse(t, mustRender(t, spec))
	if _, ok := got["custom_providers"]; ok {
		t.Errorf("built-in provider should not emit custom_providers: %v", got["custom_providers"])
	}
	if bu, ok := got["model"].(map[string]any)["base_url"]; ok {
		t.Errorf("built-in provider should not set model.base_url, got %v", bu)
	}
}

func mustRender(t *testing.T, spec *hermesv1alpha1.HermesAgentSpec) []byte {
	t.Helper()
	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestRenderTypedKeys(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model: hermesv1alpha1.ModelSpec{
			Default:       "anthropic/claude-opus-4.6",
			Provider:      "auto",
			APIMode:       ccMode,
			ContextLength: 200000,
		},
		Agent: hermesv1alpha1.AgentSpec{MaxTurns: 60, ReasoningEffort: "medium"},
		Compression: hermesv1alpha1.CompressionSpec{
			Enabled:      ptrBool(true),
			Threshold:    ptrStr("0.50"),
			TargetRatio:  ptrStr("0.20"),
			ProtectLastN: ptrI32(20),
		},
		Memory: hermesv1alpha1.MemorySpec{MemoryEnabled: ptrBool(true)},
		Skills: hermesv1alpha1.SkillsSpec{
			Disabled:              []string{"zeta", "alpha"},
			CreationNudgeInterval: 15,
		},
		Runtime: hermesv1alpha1.RuntimeSpec{
			TerminalBackend: "local",
			TerminalTimeout: 180,
			CodeExecution:   hermesv1alpha1.CodeExecutionSpec{Timeout: 300, MaxToolCalls: 50},
			Delegation:      hermesv1alpha1.DelegationSpec{MaxIterations: 50, MaxConcurrentChildren: 3},
		},
	}

	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := mustParse(t, out)

	if v, _ := got["_config_version"].(float64); int(v) != ConfigVersion {
		t.Errorf("_config_version = %v, want %d", got["_config_version"], ConfigVersion)
	}

	model := got["model"].(map[string]any)
	if model["default"] != "anthropic/claude-opus-4.6" {
		t.Errorf("model.default = %v", model["default"])
	}
	if model["base_url"] != nil {
		t.Errorf("empty base_url should be omitted, got %v", model["base_url"])
	}
	if model["api_mode"] != ccMode {
		t.Errorf("model.api_mode = %v, want chat_completions", model["api_mode"])
	}

	comp := got["compression"].(map[string]any)
	if comp["threshold"].(float64) != 0.50 {
		t.Errorf("compression.threshold = %v, want 0.5", comp["threshold"])
	}
	if comp["protect_last_n"].(float64) != 20 {
		t.Errorf("compression.protect_last_n = %v", comp["protect_last_n"])
	}

	skills := got["skills"].(map[string]any)
	dis := skills["disabled"].([]any)
	if len(dis) != 2 || dis[0] != "alpha" {
		t.Errorf("skills.disabled not sorted/rendered: %v", dis)
	}

	codeExec := got["code_execution"].(map[string]any)
	if codeExec["max_tool_calls"].(float64) != 50 {
		t.Errorf("code_execution.max_tool_calls = %v", codeExec["max_tool_calls"])
	}

	terminal := got["terminal"].(map[string]any)
	if terminal["backend"] != "local" {
		t.Errorf("terminal.backend = %v", terminal["backend"])
	}
	// docker_mount_cwd_to_workspace only emitted for docker backend.
	if _, ok := terminal["docker_mount_cwd_to_workspace"]; ok {
		t.Errorf("docker_mount_cwd_to_workspace should be absent for local backend")
	}
}

func TestKubeconfigDockerVolumesRendered(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Runtime:    hermesv1alpha1.RuntimeSpec{TerminalBackend: "docker"},
		Kubeconfig: hermesv1alpha1.KubeconfigSpec{Enabled: true},
	}
	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	terminal := mustParse(t, out)["terminal"].(map[string]any)

	vols, ok := terminal["docker_volumes"].([]any)
	if !ok || len(vols) != 2 {
		t.Fatalf("docker_volumes = %v", terminal["docker_volumes"])
	}
	if vols[0] != "/opt/data/.kube/config:/root/.kube/config:ro" {
		t.Errorf("kubeconfig mount = %v", vols[0])
	}
	if vols[1] != "/var/run/secrets/kubernetes.io/serviceaccount:/var/run/secrets/kubernetes.io/serviceaccount:ro" {
		t.Errorf("sa-token mount = %v", vols[1])
	}
	if denv := terminal["docker_env"].(map[string]any); denv["KUBECONFIG"] != "/root/.kube/config" {
		t.Errorf("docker_env.KUBECONFIG = %v", denv["KUBECONFIG"])
	}
	if args := terminal["docker_extra_args"].([]any); len(args) != 1 || args[0] != "--network=host" {
		t.Errorf("docker_extra_args = %v", terminal["docker_extra_args"])
	}
}

func TestBrewMountedIntoDockerToolContainers(t *testing.T) {
	out, err := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime:  hermesv1alpha1.RuntimeSpec{TerminalBackend: "docker"},
		Packages: hermesv1alpha1.PackagesSpec{Brew: []string{"kubectl"}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	terminal := mustParse(t, out)["terminal"].(map[string]any)

	vols := terminal["docker_volumes"].([]any)
	if len(vols) != 1 || vols[0] != "/home/linuxbrew/.linuxbrew:/home/linuxbrew/.linuxbrew:ro" {
		t.Errorf("brew prefix mount = %v", terminal["docker_volumes"])
	}
	denv := terminal["docker_env"].(map[string]any)
	if denv["PATH"] != "/home/linuxbrew/.linuxbrew/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("docker_env.PATH = %v", denv["PATH"])
	}
	// local backend -> brew not mounted into tool containers (no docker sandbox).
	out2, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime:  hermesv1alpha1.RuntimeSpec{TerminalBackend: "local"},
		Packages: hermesv1alpha1.PackagesSpec{Brew: []string{"kubectl"}},
	})
	if _, ok := mustParse(t, out2)["terminal"].(map[string]any)["docker_volumes"]; ok {
		t.Error("no docker_volumes for local backend")
	}
}

func TestKubeconfigAndBrewDockerEnvMerge(t *testing.T) {
	out, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime:    hermesv1alpha1.RuntimeSpec{TerminalBackend: "docker"},
		Kubeconfig: hermesv1alpha1.KubeconfigSpec{Enabled: true},
		Packages:   hermesv1alpha1.PackagesSpec{Brew: []string{"kubectl"}},
	})
	terminal := mustParse(t, out)["terminal"].(map[string]any)
	if len(terminal["docker_volumes"].([]any)) != 3 { // kubeconfig + sa token + brew
		t.Errorf("expected 3 docker_volumes, got %v", terminal["docker_volumes"])
	}
	denv := terminal["docker_env"].(map[string]any)
	if denv["KUBECONFIG"] == nil || denv["PATH"] == nil {
		t.Errorf("docker_env should have both KUBECONFIG and PATH: %v", denv)
	}
}

func TestKubeconfigDockerVolumesAbsentOtherwise(t *testing.T) {
	// docker backend but kubeconfig disabled -> no kube mounts.
	out, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime: hermesv1alpha1.RuntimeSpec{TerminalBackend: "docker"},
	})
	if _, ok := mustParse(t, out)["terminal"].(map[string]any)["docker_volumes"]; ok {
		t.Error("docker_volumes should be absent when kubeconfig disabled")
	}
	// local backend with kubeconfig -> no docker keys.
	out2, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime:    hermesv1alpha1.RuntimeSpec{TerminalBackend: "local"},
		Kubeconfig: hermesv1alpha1.KubeconfigSpec{Enabled: true},
	})
	if _, ok := mustParse(t, out2)["terminal"].(map[string]any)["docker_volumes"]; ok {
		t.Error("docker_volumes should be absent for local backend")
	}
}

func TestExtraConfigMergeTypedWins(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model:                 hermesv1alpha1.ModelSpec{Default: "typed-model"},
		ExtraConfigPrecedence: "merge",
		ExtraConfig: &runtime.RawExtension{
			Raw: []byte(`{"web":{"backend":"tavily"},"model":{"default":"extra-model","provider":"openrouter"}}`),
		},
	}
	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := mustParse(t, out)

	// Unmodeled section comes through verbatim.
	web := got["web"].(map[string]any)
	if web["backend"] != "tavily" {
		t.Errorf("extraConfig web.backend = %v", web["backend"])
	}
	// On conflict under merge, typed wins; extra fills the gap (provider).
	model := got["model"].(map[string]any)
	if model["default"] != "typed-model" {
		t.Errorf("merge: typed should win, got model.default = %v", model["default"])
	}
	if model["provider"] != "openrouter" {
		t.Errorf("merge: extra should fill provider, got %v", model["provider"])
	}
}

func TestExtraConfigOverride(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model:                 hermesv1alpha1.ModelSpec{Default: "typed-model"},
		ExtraConfigPrecedence: "override",
		ExtraConfig: &runtime.RawExtension{
			Raw: []byte(`{"model":{"default":"override-model"}}`),
		},
	}
	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := mustParse(t, out)
	model := got["model"].(map[string]any)
	if model["default"] != "override-model" {
		t.Errorf("override: extra should win, got %v", model["default"])
	}
}

func TestVarInterpolationPassthrough(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model: hermesv1alpha1.ModelSpec{BaseURL: "${CUSTOM_BASE_URL}"},
	}
	out, err := RenderConfigYAML(spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := mustParse(t, out)
	model := got["model"].(map[string]any)
	if model["base_url"] != "${CUSTOM_BASE_URL}" {
		t.Errorf("var should pass through untouched, got %v", model["base_url"])
	}
}

func TestConfigHashStableAndSensitive(t *testing.T) {
	base := HashInputs{
		ConfigYAML:     []byte("a: 1\n"),
		Soul:           "persona",
		SkillPayloads:  map[string]string{"pkg": "SKILL", "other": "X"},
		BrewPackages:   []string{"gh", "fd"},
		SecretVersions: map[string]string{"s1": "100", "s2": "200"},
	}
	h1 := ConfigHash(base)
	// Reordering map-derived inputs must not change the hash.
	reordered := base
	reordered.BrewPackages = []string{"fd", "gh"}
	if ConfigHash(reordered) != h1 {
		t.Errorf("hash changed on reordering equivalent inputs")
	}
	// A secret rotation must change the hash.
	rotated := base
	rotated.SecretVersions = map[string]string{"s1": "101", "s2": "200"}
	if ConfigHash(rotated) == h1 {
		t.Errorf("hash should change when a secret resourceVersion changes")
	}
}
