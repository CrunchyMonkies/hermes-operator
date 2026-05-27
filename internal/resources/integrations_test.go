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

func TestSearxngAndHonchoRenderEnv(t *testing.T) {
	a := baseAgent()
	a.Spec.Searxng.URL = "https://searxng.example/"
	a.Spec.Honcho.BaseURL = "http://honcho.llm:8000"
	a.Spec.Honcho.APIKeySecretRef = &hermesv1alpha1.SecretKeyRef{Name: "h", Key: "api-key"}

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
	a.Spec.Honcho.BaseURL = "http://honcho.llm:8000"

	dep, err := Deployment(a, "h", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	ic := findContainer(dep.Spec.Template.Spec.InitContainers, InitHoncho)
	if ic == nil {
		t.Fatal("install-honcho init container missing when honcho is in use")
	}
	script := ic.Command[len(ic.Command)-1]
	if !strings.Contains(script, HonchoPackageSpec) || !strings.Contains(script, HonchoSitePackages) {
		t.Errorf("install script missing package/target:\n%s", script)
	}
	if ic.Image != DefaultHonchoInstallImage {
		t.Errorf("install image = %q", ic.Image)
	}
	if findMount(ic, DotLocalPath) == nil {
		t.Error("install-honcho missing the dotlocal subPath mount")
	}
	if v, _, ok := envByName(a, "PYTHONPATH"); !ok || v != HonchoSitePackages {
		t.Errorf("PYTHONPATH = %q ok=%v, want %s", v, ok, HonchoSitePackages)
	}
}

func TestHonchoInstallSkipped(t *testing.T) {
	// installPackage=false -> no init container even though honcho is in use.
	off := baseAgent()
	off.Spec.Honcho.BaseURL = "http://h:8000"
	disabled := false
	off.Spec.Honcho.InstallPackage = &disabled
	dep, _ := Deployment(off, "h", "")
	if findContainer(dep.Spec.Template.Spec.InitContainers, InitHoncho) != nil {
		t.Error("install-honcho should be absent when installPackage=false")
	}
	// honcho not in use -> no init container.
	dep2, _ := Deployment(baseAgent(), "h", "")
	if findContainer(dep2.Spec.Template.Spec.InitContainers, InitHoncho) != nil {
		t.Error("install-honcho should be absent when honcho is not in use")
	}
}
