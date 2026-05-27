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
	for _, n := range []string{"SEARXNG_URL", "HONCHO_BASE_URL", "HONCHO_API_KEY"} {
		if _, _, ok := envByName(a, n); ok {
			t.Errorf("%s should be absent when unset", n)
		}
	}
}
