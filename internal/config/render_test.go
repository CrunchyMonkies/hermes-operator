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
	if !ok || len(vols) != 3 { // dind socket + kubeconfig + sa token
		t.Fatalf("docker_volumes = %v", terminal["docker_volumes"])
	}
	if vols[0] != "/var/run/dind/docker.sock:/var/run/dind/docker.sock" {
		t.Errorf("dind socket mount = %v", vols[0])
	}
	if vols[1] != "/opt/data/.kube/config:/root/.kube/config:ro" {
		t.Errorf("kubeconfig mount = %v", vols[1])
	}
	if vols[2] != "/var/run/secrets/kubernetes.io/serviceaccount:/var/run/secrets/kubernetes.io/serviceaccount:ro" {
		t.Errorf("sa-token mount = %v", vols[2])
	}
	denv := terminal["docker_env"].(map[string]any)
	if denv["DOCKER_HOST"] != "unix:///var/run/dind/docker.sock" {
		t.Errorf("docker_env.DOCKER_HOST = %v", denv["DOCKER_HOST"])
	}
	if denv["KUBECONFIG"] != "/root/.kube/config" {
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
	if len(vols) != 2 || vols[0] != "/var/run/dind/docker.sock:/var/run/dind/docker.sock" ||
		vols[1] != "/home/linuxbrew/.linuxbrew:/home/linuxbrew/.linuxbrew:ro" {
		t.Errorf("docker_volumes = %v", terminal["docker_volumes"])
	}
	denv := terminal["docker_env"].(map[string]any)
	if denv["PATH"] != "/home/linuxbrew/.linuxbrew/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("docker_env.PATH = %v", denv["PATH"])
	}
	// unix transport + no kubeconfig -> the socket is bind-mounted; no --network=host needed.
	if _, ok := terminal["docker_extra_args"]; ok {
		t.Errorf("docker_extra_args should be absent, got %v", terminal["docker_extra_args"])
	}
	// local backend -> no docker sandbox wiring at all.
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
	if len(terminal["docker_volumes"].([]any)) != 4 { // dind socket + kubeconfig + sa token + brew
		t.Errorf("expected 4 docker_volumes, got %v", terminal["docker_volumes"])
	}
	denv := terminal["docker_env"].(map[string]any)
	if denv["DOCKER_HOST"] == nil || denv["KUBECONFIG"] == nil || denv["PATH"] == nil {
		t.Errorf("docker_env should have DOCKER_HOST, KUBECONFIG and PATH: %v", denv)
	}
}

func TestDockerSocketAlwaysExposed(t *testing.T) {
	// docker backend, nothing else -> docker_volumes has just the dind socket (so the
	// agent can run docker inside its sandbox), DOCKER_HOST set, no --network=host.
	out, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime: hermesv1alpha1.RuntimeSpec{TerminalBackend: "docker"},
	})
	terminal := mustParse(t, out)["terminal"].(map[string]any)
	vols, ok := terminal["docker_volumes"].([]any)
	if !ok || len(vols) != 1 || vols[0] != "/var/run/dind/docker.sock:/var/run/dind/docker.sock" {
		t.Errorf("docker_volumes should be just the dind socket, got %v", terminal["docker_volumes"])
	}
	if terminal["docker_env"].(map[string]any)["DOCKER_HOST"] != "unix:///var/run/dind/docker.sock" {
		t.Errorf("DOCKER_HOST not set: %v", terminal["docker_env"])
	}
	if _, ok := terminal["docker_extra_args"]; ok {
		t.Errorf("docker_extra_args should be absent (unix, no kubeconfig), got %v", terminal["docker_extra_args"])
	}

	// tcp transport -> DOCKER_HOST tcp + --network=host, no socket mount.
	outTCP, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime: hermesv1alpha1.RuntimeSpec{
			TerminalBackend: "docker",
			Docker:          hermesv1alpha1.DockerRuntimeSpec{SocketTransport: "tcp"},
		},
	})
	tt := mustParse(t, outTCP)["terminal"].(map[string]any)
	if tt["docker_env"].(map[string]any)["DOCKER_HOST"] != "tcp://127.0.0.1:2375" {
		t.Errorf("tcp DOCKER_HOST = %v", tt["docker_env"])
	}
	if _, ok := tt["docker_volumes"]; ok {
		t.Errorf("tcp transport should not bind the socket, got %v", tt["docker_volumes"])
	}
	if args := tt["docker_extra_args"].([]any); len(args) != 1 || args[0] != "--network=host" {
		t.Errorf("tcp docker_extra_args = %v", tt["docker_extra_args"])
	}

	// local backend -> no docker keys at all.
	out2, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime:    hermesv1alpha1.RuntimeSpec{TerminalBackend: "local"},
		Kubeconfig: hermesv1alpha1.KubeconfigSpec{Enabled: true},
	})
	if _, ok := mustParse(t, out2)["terminal"].(map[string]any)["docker_volumes"]; ok {
		t.Error("docker_volumes should be absent for local backend")
	}
}

// TestDockerBackendExternalHost: the docker backend can target an external daemon
// (no socket bind; --network=host; TLS certs mounted + env when tls is set).
func TestDockerBackendExternalHost(t *testing.T) {
	out, _ := RenderConfigYAML(&hermesv1alpha1.HermesAgentSpec{
		Runtime: hermesv1alpha1.RuntimeSpec{
			TerminalBackend: "docker",
			Docker: hermesv1alpha1.DockerRuntimeSpec{
				ExternalHost: "tcp://dockerd.infra.svc:2376",
				TLS:          &hermesv1alpha1.DockerTLSSpec{SecretName: "docker-client-certs"},
			},
		},
	})
	terminal := mustParse(t, out)["terminal"].(map[string]any)
	denv := terminal["docker_env"].(map[string]any)
	if denv["DOCKER_HOST"] != "tcp://dockerd.infra.svc:2376" {
		t.Errorf("DOCKER_HOST = %v, want the external host", denv["DOCKER_HOST"])
	}
	if denv["DOCKER_TLS_VERIFY"] != "1" || denv["DOCKER_CERT_PATH"] != hermesv1alpha1.DockerCertMountPath {
		t.Errorf("TLS env not set in sandbox: %v", denv)
	}
	// tcp/external -> no socket bind, but the cert dir is mounted read-only.
	certBind := hermesv1alpha1.DockerCertMountPath + ":" + hermesv1alpha1.DockerCertMountPath + ":ro"
	foundCert := false
	for _, v := range terminal["docker_volumes"].([]any) {
		if v == certBind {
			foundCert = true
		}
		if v == "tcp://dockerd.infra.svc:2376" { // sanity: never bind a tcp host as a volume
			t.Errorf("tcp host must not be bind-mounted: %v", v)
		}
	}
	if !foundCert {
		t.Errorf("cert dir not mounted into sandbox: %v", terminal["docker_volumes"])
	}
	if args := terminal["docker_extra_args"].([]any); len(args) != 1 || args[0] != "--network=host" {
		t.Errorf("external host needs --network=host, got %v", terminal["docker_extra_args"])
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

func TestRenderMCPServers(t *testing.T) {
	spec := &hermesv1alpha1.HermesAgentSpec{
		MCP: hermesv1alpha1.MCPSpec{
			Servers: []hermesv1alpha1.MCPServerSpec{
				{
					Name:                      "github",
					Command:                   "npx",
					Args:                      []string{"-y", "@modelcontextprotocol/server-github"},
					Env:                       map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "${GH_TOKEN}"},
					SupportsParallelToolCalls: ptrBool(true),
					TimeoutSeconds:            ptrI32(120),
					Tools:                     &hermesv1alpha1.MCPToolFilter{Include: []string{"create_issue"}},
				},
				{
					Name:      "remote",
					Transport: "sse",
					URL:       "https://mcp.example.com/sse",
					Headers:   map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
					SSLVerify: ptrBool(false),
					Enabled:   ptrBool(false),
					// extraConfig fills the long tail; a typed key (url) must still win.
					ExtraConfig: &runtime.RawExtension{
						Raw: []byte(`{"url":"https://ignored","sampling":{"enabled":true,"max_rpm":10}}`),
					},
				},
			},
		},
	}

	got := mustParse(t, mustRender(t, spec))
	servers, ok := got["mcp_servers"].(map[string]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("mcp_servers = %v", got["mcp_servers"])
	}

	// stdio server
	gh := servers["github"].(map[string]any)
	if gh["command"] != "npx" {
		t.Errorf("github.command = %v", gh["command"])
	}
	if args := gh["args"].([]any); len(args) != 2 || args[0] != "-y" {
		t.Errorf("github.args = %v", gh["args"])
	}
	if env := gh["env"].(map[string]any); env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${GH_TOKEN}" {
		t.Errorf("github.env = %v (interpolation must pass through untouched)", gh["env"])
	}
	if gh["supports_parallel_tool_calls"] != true {
		t.Errorf("github.supports_parallel_tool_calls = %v", gh["supports_parallel_tool_calls"])
	}
	if v, _ := gh["timeout"].(float64); int(v) != 120 {
		t.Errorf("github.timeout = %v", gh["timeout"])
	}
	if tf := gh["tools"].(map[string]any); tf["include"].([]any)[0] != "create_issue" {
		t.Errorf("github.tools = %v", gh["tools"])
	}
	if _, ok := gh["url"]; ok {
		t.Errorf("stdio server must not render url: %v", gh["url"])
	}

	// http/sse server + per-server extraConfig (typed wins)
	rem := servers["remote"].(map[string]any)
	if rem["url"] != "https://mcp.example.com/sse" {
		t.Errorf("remote.url = %v (typed must win over extraConfig)", rem["url"])
	}
	if rem["transport"] != "sse" {
		t.Errorf("remote.transport = %v", rem["transport"])
	}
	if hdr := rem["headers"].(map[string]any); hdr["Authorization"] != "Bearer ${REMOTE_TOKEN}" {
		t.Errorf("remote.headers = %v", rem["headers"])
	}
	if rem["ssl_verify"] != false {
		t.Errorf("remote.ssl_verify = %v", rem["ssl_verify"])
	}
	if rem["enabled"] != false {
		t.Errorf("remote.enabled = %v", rem["enabled"])
	}
	if s := rem["sampling"].(map[string]any); s["enabled"] != true {
		t.Errorf("remote.sampling (from extraConfig) = %v", rem["sampling"])
	}
}

func TestRenderMCPServersOmittedWhenEmpty(t *testing.T) {
	got := mustParse(t, mustRender(t, &hermesv1alpha1.HermesAgentSpec{}))
	if _, ok := got["mcp_servers"]; ok {
		t.Errorf("mcp_servers should be omitted when no servers declared, got %v", got["mcp_servers"])
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
