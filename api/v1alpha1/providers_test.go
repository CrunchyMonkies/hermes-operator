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

package v1alpha1

import "testing"

func TestProviderKeyEnvVar(t *testing.T) {
	cases := []struct {
		name string
		p    ProviderSpec
		want string
	}{
		{"claude alias", ProviderSpec{Name: "claude"}, "ANTHROPIC_API_KEY"},
		{"claude-code alias", ProviderSpec{Name: "claude-code"}, "ANTHROPIC_API_KEY"},
		{"grok alias", ProviderSpec{Name: "grok"}, "XAI_API_KEY"},
		{"openai->openrouter", ProviderSpec{Name: "openai"}, "OPENROUTER_API_KEY"},
		{"xai direct", ProviderSpec{Name: "xai"}, "XAI_API_KEY"},
		{"deepseek", ProviderSpec{Name: "deepseek"}, "DEEPSEEK_API_KEY"},
		{"explicit keyEnv wins", ProviderSpec{Name: "anthropic", KeyEnv: "MY_KEY"}, "MY_KEY"},
		{"custom derives from name", ProviderSpec{Name: "llm-bne1", BaseURL: "https://x/v1"}, "LLM_BNE1_API_KEY"},
		{"unknown built-in -> empty", ProviderSpec{Name: "nous"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.KeyEnvVar(); got != c.want {
				t.Errorf("KeyEnvVar() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestProviderIsCustom(t *testing.T) {
	if (ProviderSpec{Name: "anthropic"}).IsCustom() {
		t.Error("built-in must not be custom")
	}
	if !(ProviderSpec{Name: "llm", BaseURL: "https://x/v1"}).IsCustom() {
		t.Error("baseURL entry must be custom")
	}
}
