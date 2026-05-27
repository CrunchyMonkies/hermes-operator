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

func TestRenderTypedKeys(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		Model: hermesv1alpha1.ModelSpec{
			Default:       "anthropic/claude-opus-4.6",
			Provider:      "auto",
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
