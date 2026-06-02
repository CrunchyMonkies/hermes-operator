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

	corev1 "k8s.io/api/core/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

func envByName(a *hermesv1alpha1.HermesAgent, name string) (string, bool, bool) {
	for _, e := range hermesEnv(a) {
		if e.Name == name {
			return e.Value, e.ValueFrom != nil, true
		}
	}
	return "", false, false
}

func TestDashboardInsecureEnv(t *testing.T) {
	// Derived: dashboard exposed via the shared ingress -> HERMES_DASHBOARD_INSECURE=1
	// (operator-set, not patched), no explicit field needed.
	a := baseAgent()
	a.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: true}
	a.Spec.Ingress = hermesv1alpha1.IngressSpec{Enabled: true, Host: "lana.example.com"}
	if v, _, ok := envByName(a, "HERMES_DASHBOARD_INSECURE"); !ok || v != "1" {
		t.Errorf("HERMES_DASHBOARD_INSECURE = %q ok=%v, want \"1\" (derived from ingress)", v, ok)
	}

	// Dashboard enabled, no ingress -> not derived insecure (auth gate engages).
	b := baseAgent()
	b.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: true}
	if _, _, ok := envByName(b, "HERMES_DASHBOARD_INSECURE"); ok {
		t.Error("HERMES_DASHBOARD_INSECURE must be absent when not exposed via ingress")
	}

	// Explicit override wins: insecure=false even though the ingress is enabled.
	c := baseAgent()
	c.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: true, Insecure: ptrBool(false)}
	c.Spec.Ingress = hermesv1alpha1.IngressSpec{Enabled: true, Host: "lana.example.com"}
	if _, _, ok := envByName(c, "HERMES_DASHBOARD_INSECURE"); ok {
		t.Error("explicit insecure=false must override the ingress-derived default")
	}

	// Explicit override true without ingress -> set.
	d := baseAgent()
	d.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: true, Insecure: ptrBool(true)}
	if v, _, ok := envByName(d, "HERMES_DASHBOARD_INSECURE"); !ok || v != "1" {
		t.Errorf("explicit insecure=true should set the env (got %q ok=%v)", v, ok)
	}

	// Dashboard disabled -> never set, even with ingress.
	e := baseAgent()
	e.Spec.Dashboard = hermesv1alpha1.DashboardSpec{Enabled: false}
	e.Spec.Ingress = hermesv1alpha1.IngressSpec{Enabled: true, Host: "lana.example.com"}
	if _, _, ok := envByName(e, "HERMES_DASHBOARD_INSECURE"); ok {
		t.Error("HERMES_DASHBOARD_INSECURE must be absent when dashboard is disabled")
	}
}

func TestProviderKeyInjection(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.Model.Providers = []hermesv1alpha1.ProviderSpec{
		{Name: "claude", KeySecretRef: &hermesv1alpha1.SecretKeyRef{Name: "llm-keys", Key: "anthropic"}},
		{Name: "llm", BaseURL: "https://llm/v1", KeySecretRef: &hermesv1alpha1.SecretKeyRef{Name: "llm-keys", Key: "llm"}},
		{Name: "nous"}, // OAuth, no keySecretRef -> nothing injected
	}
	// Built-in claude -> ANTHROPIC_API_KEY (from secret).
	if _, fromSecret, ok := envByName(a, "ANTHROPIC_API_KEY"); !ok || !fromSecret {
		t.Errorf("ANTHROPIC_API_KEY should be injected from a secretKeyRef (ok=%v fromSecret=%v)", ok, fromSecret)
	}
	// Custom llm endpoint -> derived LLM_API_KEY (from secret).
	if _, fromSecret, ok := envByName(a, "LLM_API_KEY"); !ok || !fromSecret {
		t.Errorf("LLM_API_KEY should be injected from a secretKeyRef (ok=%v fromSecret=%v)", ok, fromSecret)
	}
}

func TestMCPSecretEnvInjection(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.MCP.Servers = []hermesv1alpha1.MCPServerSpec{
		{
			Name: "remote",
			URL:  "https://mcp.example.com/mcp",
			SecretEnv: []hermesv1alpha1.MCPSecretEnv{
				{Name: "REMOTE_TOKEN", SecretRef: hermesv1alpha1.SecretKeyRef{Name: "mcp-secrets", Key: "remote"}},
			},
		},
		{Name: "local", Command: "npx"}, // no secretEnv -> nothing injected
	}

	// The env var is injected from the right Secret name+key.
	var found *corev1.EnvVar
	for _, e := range hermesEnv(a) {
		if e.Name == "REMOTE_TOKEN" {
			ee := e
			found = &ee
		}
	}
	if found == nil || found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("REMOTE_TOKEN should be injected from a secretKeyRef, got %+v", found)
	}
	if ref := found.ValueFrom.SecretKeyRef; ref.Name != "mcp-secrets" || ref.Key != "remote" {
		t.Errorf("REMOTE_TOKEN secretKeyRef = %s/%s, want mcp-secrets/remote", ref.Name, ref.Key)
	}
}

func bitwardenAgent() *hermesv1alpha1.HermesAgent {
	a := baseAgent()
	a.Spec.DefaultProfile.Secrets.Bitwarden = &hermesv1alpha1.BitwardenSpec{
		Enabled:              ptrBool(true),
		AccessTokenSecretRef: &hermesv1alpha1.SecretKeyRef{Name: "bw-creds", Key: "token"},
	}
	return a
}

func TestBitwardenAccessTokenInjection(t *testing.T) {
	a := bitwardenAgent()

	var found *corev1.EnvVar
	for _, e := range hermesEnv(a) {
		if e.Name == "BWS_ACCESS_TOKEN" {
			ee := e
			found = &ee
		}
	}
	if found == nil || found.ValueFrom == nil || found.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("BWS_ACCESS_TOKEN should be injected from a secretKeyRef, got %+v", found)
	}
	if ref := found.ValueFrom.SecretKeyRef; ref.Name != "bw-creds" || ref.Key != "token" {
		t.Errorf("BWS_ACCESS_TOKEN secretKeyRef = %s/%s, want bw-creds/token", ref.Name, ref.Key)
	}
}

func TestBitwardenAccessTokenCustomEnv(t *testing.T) {
	a := bitwardenAgent()
	a.Spec.DefaultProfile.Secrets.Bitwarden.AccessTokenEnv = "BW_TOKEN"

	if _, fromSecret, ok := envByName(a, "BW_TOKEN"); !ok || !fromSecret {
		t.Errorf("custom BW_TOKEN should be injected from a secretKeyRef (ok=%v fromSecret=%v)", ok, fromSecret)
	}
	if _, _, ok := envByName(a, "BWS_ACCESS_TOKEN"); ok {
		t.Errorf("default BWS_ACCESS_TOKEN must not be injected when accessTokenEnv overrides it")
	}
}

func TestBitwardenNoInjectionWhenAbsent(t *testing.T) {
	if _, _, ok := envByName(baseAgent(), "BWS_ACCESS_TOKEN"); ok {
		t.Errorf("BWS_ACCESS_TOKEN must not be injected when bitwarden is unset")
	}
}

func TestSearxngAndHonchoRenderEnv(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.Searxng.URL = "https://searxng.example/"
	a.Spec.DefaultProfile.Honcho.BaseURL = "http://honcho.llm:8000"
	a.Spec.DefaultProfile.Honcho.APIKeySecretRef = &hermesv1alpha1.SecretKeyRef{Name: "h", Key: "api-key"}

	if v, _, ok := envByName(a, "SEARXNG_URL"); !ok || v != "https://searxng.example/" {
		t.Errorf("SEARXNG_URL = %q ok=%v", v, ok)
	}
	if v, _, ok := envByName(a, "HONCHO_BASE_URL"); !ok || v != "http://honcho.llm:8000" {
		t.Errorf("HONCHO_BASE_URL = %q ok=%v", v, ok)
	}
	if _, fromSecret, ok := envByName(a, "HONCHO_API_KEY"); !ok || !fromSecret {
		t.Errorf("HONCHO_API_KEY should come from a secretKeyRef (ok=%v fromSecret=%v)", ok, fromSecret)
	}
}

func TestSearxngHonchoOmittedWhenUnset(t *testing.T) {
	a := baseAgent()
	for _, n := range []string{"SEARXNG_URL", "HONCHO_BASE_URL", "HONCHO_API_KEY", "PYTHONPATH"} {
		if _, _, ok := envByName(a, n); ok {
			t.Errorf("%s should be absent when unset", n)
		}
	}
}

func TestHonchoPipInstallInitContainer(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.Honcho.BaseURL = "http://honcho.llm:8000"

	dep, err := Deployment(a, "h", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	ic := findContainer(dep.Spec.Template.Spec.InitContainers, InitPipInstall)
	if ic == nil {
		t.Fatal("install-honcho init container missing when honcho is in use")
	}
	script := ic.Command[len(ic.Command)-1]
	if !strings.Contains(script, HonchoPackageSpec) || !strings.Contains(script, PipSitePackages) {
		t.Errorf("install script missing package/target:\n%s", script)
	}
	if ic.Image != DefaultPipImage {
		t.Errorf("install image = %q", ic.Image)
	}
	if findMount(ic, DotLocalPath) == nil {
		t.Error("install-honcho missing the dotlocal subPath mount")
	}
	if v, _, ok := envByName(a, "PYTHONPATH"); !ok || v != PipSitePackages {
		t.Errorf("PYTHONPATH = %q ok=%v, want %s", v, ok, PipSitePackages)
	}
}

func TestHonchoInstallSkipped(t *testing.T) {
	// installPackage=false -> no init container even though honcho is in use.
	off := baseAgent()
	off.Spec.DefaultProfile.Honcho.BaseURL = "http://h:8000"
	disabled := false
	off.Spec.DefaultProfile.Honcho.InstallPackage = &disabled
	dep, _ := Deployment(off, "h", "")
	if findContainer(dep.Spec.Template.Spec.InitContainers, InitPipInstall) != nil {
		t.Error("install-honcho should be absent when installPackage=false")
	}
	// honcho not in use -> no init container.
	dep2, _ := Deployment(baseAgent(), "h", "")
	if findContainer(dep2.Spec.Template.Spec.InitContainers, InitPipInstall) != nil {
		t.Error("pip-install should be absent when nothing needs installing")
	}
}

func TestPipPackagesInitContainer(t *testing.T) {
	// Standalone pip packages (no honcho) install via the pip-install init.
	a := baseAgent()
	a.Spec.Packages.Pip = []string{"ruff"}
	a.Spec.Packages.PipImage = "python:3.13-custom"
	dep, _ := Deployment(a, "h", "")
	ic := findContainer(dep.Spec.Template.Spec.InitContainers, InitPipInstall)
	if ic == nil {
		t.Fatal("pip-install init missing for packages.pip")
	}
	if ic.Image != "python:3.13-custom" {
		t.Errorf("pipImage override not used: %q", ic.Image)
	}
	if !strings.Contains(ic.Command[2], "'ruff'") {
		t.Errorf("pip package not shell-quoted in script:\n%s", ic.Command[2])
	}
	if v, _, ok := envByName(a, "PYTHONPATH"); !ok || v != PipSitePackages {
		t.Errorf("PYTHONPATH = %q ok=%v", v, ok)
	}

	// honcho + packages.pip compose into a single pip-install init.
	b := baseAgent()
	b.Spec.DefaultProfile.Honcho.BaseURL = "http://h:8000"
	b.Spec.Packages.Pip = []string{"ruff"}
	dep2, _ := Deployment(b, "h", "")
	inits := dep2.Spec.Template.Spec.InitContainers
	if n := countContainers(inits, InitPipInstall); n != 1 {
		t.Fatalf("expected exactly 1 pip-install init, got %d", n)
	}
	s := findContainer(inits, InitPipInstall).Command[2]
	if !strings.Contains(s, "'ruff'") || !strings.Contains(s, HonchoPackageSpec) {
		t.Errorf("union install missing packages:\n%s", s)
	}
}

func countContainers(cs []corev1.Container, name string) int {
	n := 0
	for i := range cs {
		if cs[i].Name == name {
			n++
		}
	}
	return n
}

func pipScript(t *testing.T, a *hermesv1alpha1.HermesAgent) string {
	t.Helper()
	dep, err := Deployment(a, "h", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	ic := findContainer(dep.Spec.Template.Spec.InitContainers, InitPipInstall)
	if ic == nil {
		t.Fatal("pip-install init container missing")
	}
	return ic.Command[len(ic.Command)-1]
}

func TestChannelAndBackendPipDeps(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{
		{Type: "telegram"}, {Type: "discord"}, {Type: "teams"}, // teams has no deps
	}
	a.Spec.DefaultProfile.Runtime.TerminalBackend = "modal"
	s := pipScript(t, a)
	for _, want := range []string{
		"python-telegram-bot[webhooks]==22.6",
		"discord.py[voice]==2.7.1", "brotlicffi==1.2.0.1",
		"modal==1.3.4",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pip script missing %q:\n%s", want, s)
		}
	}
}

func TestInstallDepsTogglesOff(t *testing.T) {
	off := false
	a := baseAgent()
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{{Type: "slack", InstallDeps: &off}}
	a.Spec.DefaultProfile.Runtime.TerminalBackend = "daytona"
	a.Spec.DefaultProfile.Runtime.InstallDeps = &off
	dep, _ := Deployment(a, "h", "")
	if findContainer(dep.Spec.Template.Spec.InitContainers, InitPipInstall) != nil {
		t.Error("pip-install init should be absent when installDeps are off and nothing else needs installing")
	}
}

func TestPipDepsDeduped(t *testing.T) {
	// slack pulls aiohttp==3.13.4; an explicit packages.pip entry for it must not duplicate.
	a := baseAgent()
	a.Spec.DefaultProfile.Channels = []hermesv1alpha1.ChannelSpec{{Type: "slack"}}
	a.Spec.Packages.Pip = []string{"aiohttp==3.13.4"}
	s := pipScript(t, a)
	if n := strings.Count(s, "aiohttp==3.13.4"); n != 1 {
		t.Errorf("aiohttp should appear once, got %d:\n%s", n, s)
	}
}

func TestSingularityApptainerInstall(t *testing.T) {
	a := baseAgent()
	a.Spec.DefaultProfile.Runtime.TerminalBackend = "singularity"
	dep, err := Deployment(a, "h", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	ic := findContainer(dep.Spec.Template.Spec.InitContainers, InitApptainer)
	if ic == nil {
		t.Fatal("install-apptainer init missing for the singularity backend")
	}
	if ic.Image != DefaultSingularityInstallImage {
		t.Errorf("install image = %q", ic.Image)
	}
	s := ic.Command[len(ic.Command)-1]
	for _, want := range []string{
		ApptainerInstallScriptURL, ApptainerPrefix,
		DotLocalBin + "/apptainer", DotLocalBin + "/singularity",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("apptainer init script missing %q:\n%s", want, s)
		}
	}

	// installDeps=false -> no apptainer init.
	off := false
	b := baseAgent()
	b.Spec.DefaultProfile.Runtime.TerminalBackend = "singularity"
	b.Spec.DefaultProfile.Runtime.InstallDeps = &off
	dep2, _ := Deployment(b, "h", "")
	if findContainer(dep2.Spec.Template.Spec.InitContainers, InitApptainer) != nil {
		t.Error("install-apptainer should be absent when installDeps=false")
	}

	// non-singularity backend -> no apptainer init.
	c := baseAgent()
	c.Spec.DefaultProfile.Runtime.TerminalBackend = "docker"
	dep3, _ := Deployment(c, "h", "")
	if findContainer(dep3.Spec.Template.Spec.InitContainers, InitApptainer) != nil {
		t.Error("install-apptainer should be absent for a non-singularity backend")
	}
}
