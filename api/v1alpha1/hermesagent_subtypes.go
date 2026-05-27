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

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ModelSpec renders into config.yaml `model:`.
type ModelSpec struct {
	// default model name, e.g. anthropic/claude-opus-4.6. -> model.default
	// +optional
	Default string `json:"default,omitempty"`
	// provider routing mode. -> model.provider
	// +kubebuilder:default=auto
	// +optional
	Provider string `json:"provider,omitempty"`
	// baseURL overrides the provider endpoint. -> model.base_url
	// +optional
	BaseURL string `json:"baseURL,omitempty"`
	// contextLength is the total context window (hot-reloadable). -> model.context_length
	// +optional
	ContextLength int64 `json:"contextLength,omitempty"`
	// maxTokens caps output tokens. -> model.max_tokens
	// +optional
	MaxTokens int64 `json:"maxTokens,omitempty"`
}

// AgentSpec renders into config.yaml `agent:`.
type AgentSpec struct {
	// maxTurns is the max tool-calling iterations. -> agent.max_turns
	// +optional
	MaxTurns int32 `json:"maxTurns,omitempty"`
	// reasoningEffort -> agent.reasoning_effort
	// +kubebuilder:validation:Enum=xhigh;high;medium;low;minimal;none
	// +optional
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// disabledToolsets lists platform toolsets to disable. -> agent.disabled_toolsets
	// +optional
	DisabledToolsets []string `json:"disabledToolsets,omitempty"`
}

// CompressionSpec renders into config.yaml `compression:` (hot-reloadable).
type CompressionSpec struct {
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	Threshold *string `json:"threshold,omitempty"`
	// +optional
	TargetRatio *string `json:"targetRatio,omitempty"`
	// +optional
	ProtectLastN *int32 `json:"protectLastN,omitempty"`
	// +optional
	ProtectFirstN *int32 `json:"protectFirstN,omitempty"`
}

// MemorySpec renders into config.yaml `memory:`.
type MemorySpec struct {
	// +optional
	MemoryEnabled *bool `json:"memoryEnabled,omitempty"`
	// +optional
	UserProfileEnabled *bool `json:"userProfileEnabled,omitempty"`
}

// APIServerSpec controls the OpenAI-compatible HTTP API (port 8642).
type APIServerSpec struct {
	// enabled turns on the API server platform. Requires keySecretRef and a
	// non-localhost host.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:default="0.0.0.0"
	// +optional
	Host string `json:"host,omitempty"`
	// +kubebuilder:default=8642
	// +optional
	Port int32 `json:"port,omitempty"`
	// keySecretRef holds the API_SERVER_KEY. Required when enabled.
	// +optional
	KeySecretRef *SecretKeyRef `json:"keySecretRef,omitempty"`
	// +optional
	CORSOrigins []string `json:"corsOrigins,omitempty"`
	// +optional
	Service ServiceSpec `json:"service,omitempty"`
	// +optional
	Ingress IngressSpec `json:"ingress,omitempty"`
}

// DashboardSpec controls the web dashboard (port 9119).
type DashboardSpec struct {
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:default="0.0.0.0"
	// +optional
	Host string `json:"host,omitempty"`
	// +kubebuilder:default=9119
	// +optional
	Port int32 `json:"port,omitempty"`
	// +optional
	Service ServiceSpec `json:"service,omitempty"`
	// +optional
	Ingress IngressSpec `json:"ingress,omitempty"`
}

// ChannelSpec binds one messaging platform. See specification §3.3 for the
// per-platform env surface (provided via secretRef / envFrom).
type ChannelSpec struct {
	// type of the messaging platform.
	// +kubebuilder:validation:Enum=telegram;discord;slack;whatsapp;signal;email;teams;google_chat
	// +required
	Type string `json:"type"`
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// secretRef names a Secret whose keys carry the platform credentials.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
	// webhookPort: 0 = polling; >0 exposes a port on the Service.
	// +kubebuilder:default=0
	// +optional
	WebhookPort int32 `json:"webhookPort,omitempty"`
	// ingress is only meaningful when webhookPort > 0.
	// +optional
	Ingress IngressSpec `json:"ingress,omitempty"`
}

// CustomSkill is an operator-shipped skill written into the PVC and activated.
type CustomSkill struct {
	// +required
	Name string `json:"name"`
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// sourceRef references a ConfigMap or Secret holding SKILL.md (+files).
	// Mutually exclusive with inline.
	// +optional
	SourceRef *SkillSourceRef `json:"sourceRef,omitempty"`
	// inline is the literal SKILL.md content. Mutually exclusive with sourceRef.
	// +optional
	Inline string `json:"inline,omitempty"`
}

// SkillSourceRef points at a ConfigMap or Secret carrying skill files.
type SkillSourceRef struct {
	// +optional
	ConfigMapName string `json:"configMapName,omitempty"`
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// SkillsSpec controls skill enablement and operator-provided custom skills.
type SkillsSpec struct {
	// disabled globally disables bundled skills. -> skills.disabled
	// +optional
	Disabled []string `json:"disabled,omitempty"`
	// platformDisabled disables skills per platform. -> skills.platform_disabled
	// +optional
	PlatformDisabled map[string][]string `json:"platformDisabled,omitempty"`
	// externalDirs are read-only external skill dirs. -> skills.external_dirs
	// +optional
	ExternalDirs []string `json:"externalDirs,omitempty"`
	// +kubebuilder:default=15
	// +optional
	CreationNudgeInterval int32 `json:"creationNudgeInterval,omitempty"`
	// custom skills written into the PVC and auto-activated.
	// +optional
	Custom []CustomSkill `json:"custom,omitempty"`
	// enablePackageManagementSkill ensures the built-in package-management skill
	// is present and enabled.
	// +kubebuilder:default=true
	// +optional
	EnablePackageManagementSkill *bool `json:"enablePackageManagementSkill,omitempty"`
}

// PackagesSpec declares runtime package installation.
type PackagesSpec struct {
	// apt packages installed at boot as root (NOT persisted across recreation).
	// +optional
	Apt []string `json:"apt,omitempty"`
	// brew packages installed to the shared PVC (persisted, no sudo).
	// +optional
	Brew []string `json:"brew,omitempty"`
	// homebrewPrefix overrides the default Homebrew prefix on the PVC.
	// +kubebuilder:default="/home/linuxbrew/.linuxbrew"
	// +optional
	HomebrewPrefix string `json:"homebrewPrefix,omitempty"`
}

// CodeExecutionSpec renders into config.yaml `code_execution:`.
type CodeExecutionSpec struct {
	// +optional
	Timeout int32 `json:"timeout,omitempty"`
	// +optional
	MaxToolCalls int32 `json:"maxToolCalls,omitempty"`
}

// DelegationSpec renders into config.yaml `delegation:`.
type DelegationSpec struct {
	// +optional
	MaxConcurrentChildren int32 `json:"maxConcurrentChildren,omitempty"`
	// +optional
	MaxIterations int32 `json:"maxIterations,omitempty"`
}

// DockerRuntimeStorage sizes the dind image/layer store (a subPath on the
// shared PVC).
type DockerRuntimeStorage struct {
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`
}

// DockerRuntimeSpec configures the Docker-in-Docker sidecar the operator injects
// when runtime.terminalBackend is docker (§11.2).
type DockerRuntimeSpec struct {
	// +kubebuilder:default="docker:27-dind"
	// +optional
	Image string `json:"image,omitempty"`
	// +kubebuilder:default=false
	// +optional
	Rootless bool `json:"rootless,omitempty"`
	// +kubebuilder:validation:Enum=unix;tcp
	// +kubebuilder:default=unix
	// +optional
	SocketTransport string `json:"socketTransport,omitempty"`
	// +kubebuilder:default=false
	// +optional
	MountCwdToWorkspace bool `json:"mountCwdToWorkspace,omitempty"`
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// +optional
	Storage DockerRuntimeStorage `json:"storage,omitempty"`
}

// RuntimeSpec selects the tool/code execution backend.
type RuntimeSpec struct {
	// terminalBackend selects where tools run. -> terminal.backend
	// +kubebuilder:validation:Enum=local;docker;ssh;modal;daytona;vercel_sandbox;singularity
	// +kubebuilder:default=local
	// +optional
	TerminalBackend string `json:"terminalBackend,omitempty"`
	// terminalTimeout -> terminal.timeout
	// +optional
	TerminalTimeout int32 `json:"terminalTimeout,omitempty"`
	// +optional
	CodeExecution CodeExecutionSpec `json:"codeExecution,omitempty"`
	// +optional
	Delegation DelegationSpec `json:"delegation,omitempty"`
	// +optional
	Docker DockerRuntimeSpec `json:"docker,omitempty"`
}

// KubeconfigSpec configures an in-cluster kubeconfig written to ~/.kube/config
// in the agent container (from the pod's projected ServiceAccount token + CA).
type KubeconfigSpec struct {
	// enabled writes ~/.kube/config at boot so kubectl/k8s tools work with no
	// extra setup, using the pod's ServiceAccount identity.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// CronJob is a declaratively-seeded scheduled task. See specification §12.
type CronJob struct {
	// +required
	Name string `json:"name"`
	// schedule is a cron expression evaluated by the in-gateway scheduler.
	// +required
	Schedule string `json:"schedule"`
	// prompt is the instruction the agent runs on each fire.
	// +required
	Prompt string `json:"prompt"`
}

// CronSpec reconciles a declared subset of /opt/data/cron/jobs.json.
type CronSpec struct {
	// pruneUnmanaged makes the declared set authoritative (operator removes
	// agent-created jobs).
	// +kubebuilder:default=false
	// +optional
	PruneUnmanaged bool `json:"pruneUnmanaged,omitempty"`
	// +optional
	Jobs []CronJob `json:"jobs,omitempty"`
}

// StorageSpec configures the single shared PVC. See specification §4.
type StorageSpec struct {
	// size of the shared claim. Mutually exclusive with existingClaim.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// existingClaim uses a pre-created PVC instead of provisioning one.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`
	// reclaimPolicy governs the PVC on HermesAgent deletion.
	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	// +optional
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
}

// ServiceAccountSpec configures the SA the agent pod + reloader run as.
type ServiceAccountSpec struct {
	// create makes the operator provision a per-agent SA and bind the reloader Role.
	// +kubebuilder:default=true
	// +optional
	Create bool `json:"create,omitempty"`
	// name of the SA to create/use. Defaults to the agent name. Required when create=false.
	// +optional
	Name string `json:"name,omitempty"`
	// annotations applied to the SA — the mechanism for keyless cloud provider auth.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
	// automountToken controls the projected service account token.
	// +kubebuilder:default=true
	// +optional
	AutomountToken *bool `json:"automountToken,omitempty"`
}

// ProbesSpec selects the probe mode and tunes timings. See specification §1.2.
type ProbesSpec struct {
	// mode: auto selects http when apiServer.enabled else exec.
	// +kubebuilder:validation:Enum=auto;exec;http
	// +kubebuilder:default=auto
	// +optional
	Mode string `json:"mode,omitempty"`
	// +optional
	Liveness ProbeSettings `json:"liveness,omitempty"`
	// +optional
	Readiness ProbeSettings `json:"readiness,omitempty"`
}

// EnvVar references an env entry sourced from a Secret/ConfigMap or literal.
// We reuse corev1.EnvVar directly in the spec.

// PresetRef references a HermesConfigPreset to deep-merge into the spec.
type PresetRef struct {
	// +required
	Name string `json:"name"`
}
