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

import "strings"

// ProviderSpec declares one model provider — either a hermes built-in (anthropic,
// openai, xai, openrouter, …; baseURL omitted, hermes knows the endpoint) or a
// custom OpenAI-compatible endpoint (baseURL set). Custom entries render into
// config.yaml `custom_providers:`; built-ins do not (only their credentials are
// injected). Select the active provider with model.provider == name.
type ProviderSpec struct {
	// name is the provider id: a hermes built-in name or alias (e.g. anthropic,
	// claude, openai, grok, xai, openrouter, zai, kimi, minimax, deepseek), or an
	// arbitrary name for a custom endpoint.
	// +required
	Name string `json:"name"`
	// baseURL marks this as a custom OpenAI-compatible endpoint and is rendered into
	// custom_providers[].base_url. Omit for a built-in provider.
	// +optional
	BaseURL string `json:"baseURL,omitempty"`
	// apiMode is the wire protocol/transport (chat_completions, anthropic_messages,
	// codex_responses, bedrock_converse). Optional; built-ins infer it.
	// +optional
	APIMode string `json:"apiMode,omitempty"`
	// keyEnv overrides the env var the API key is injected under. Defaults to the
	// built-in provider's known key env var, or <NAME>_API_KEY for a custom endpoint.
	// +optional
	KeyEnv string `json:"keyEnv,omitempty"`
	// keySecretRef supplies this provider's API key; the operator injects it as an
	// env var (see keyEnv) on the agent container. Leave unset for OAuth providers
	// (auth via `hermes login`/auth.json) or when the key is wired via spec.env.
	// +optional
	KeySecretRef *SecretKeyRef `json:"keySecretRef,omitempty"`
	// models lists this provider's models and their per-model context windows
	// (custom providers only — renders to custom_providers[].models).
	// +optional
	Models []ProviderModelSpec `json:"models,omitempty"`
}

// ProviderModelSpec is one model under a ProviderSpec.
type ProviderModelSpec struct {
	// name is the model id as the provider exposes it.
	// +required
	Name string `json:"name"`
	// contextLength is this model's context window in tokens.
	// -> custom_providers[].models.<name>.context_length
	// +optional
	ContextLength int64 `json:"contextLength,omitempty"`
}

// providerAlias maps hermes provider aliases to their canonical id. Mirrors
// third_party/hermes-agent/hermes_cli/providers.py — RE-VERIFY ON TAG BUMP.
var providerAlias = map[string]string{
	"claude":      "anthropic",
	"claude-code": "anthropic",
	"grok":        "xai",
	"x-ai":        "xai",
	"openai":      "openrouter", // bare "openai" routes through the OpenRouter aggregator
	"glm":         "zai",
	"z-ai":        "zai",
	"zhipu":       "zai",
	"kimi":        "kimi-coding",
	"moonshot":    "kimi-coding",
	"deep-seek":   "deepseek",
}

// providerKeyEnv maps a canonical built-in provider id to the env var hermes reads
// its API key from (auth.py ProviderConfig.api_key_env_vars[0]). RE-VERIFY ON TAG BUMP.
var providerKeyEnv = map[string]string{
	"anthropic":   "ANTHROPIC_API_KEY",
	"xai":         "XAI_API_KEY",
	"openrouter":  "OPENROUTER_API_KEY",
	"zai":         "GLM_API_KEY",
	"kimi-coding": "KIMI_API_KEY",
	"minimax":     "MINIMAX_API_KEY",
	"deepseek":    "DEEPSEEK_API_KEY",
	"mistral":     "MISTRAL_API_KEY",
}

// IsCustom reports whether this is a custom OpenAI-compatible endpoint (baseURL set)
// rather than a hermes built-in provider.
func (p ProviderSpec) IsCustom() bool { return p.BaseURL != "" }

// KeyEnvVar returns the env var name the provider's API key should be injected under:
// an explicit KeyEnv wins; else a built-in's known key env var; else a derived
// <NAME>_API_KEY for a custom endpoint. Returns "" for a built-in with no known key
// env (e.g. OAuth providers) — the caller should not inject a key in that case.
func (p ProviderSpec) KeyEnvVar() string {
	if p.KeyEnv != "" {
		return p.KeyEnv
	}
	if p.IsCustom() {
		return sanitizeEnvName(p.Name) + "_API_KEY"
	}
	canonical := strings.ToLower(p.Name)
	if alias, ok := providerAlias[canonical]; ok {
		canonical = alias
	}
	return providerKeyEnv[canonical]
}

// sanitizeEnvName upper-cases name and replaces every non-alphanumeric run with a
// single underscore (so "llm-bne1" -> "LLM_BNE1").
func sanitizeEnvName(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
