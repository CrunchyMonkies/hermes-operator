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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HermesAgentSpec defines the desired state of a single Hermes gateway. One CR
// renders to one Deployment(Recreate, replicas≤1) + one shared PVC + ConfigMaps
// + Service (+ Ingress) + a reloader sidecar. See specification §3.1.
//
// +kubebuilder:validation:XValidation:rule="(has(self.storage.existingClaim) && size(self.storage.existingClaim) != 0) != (has(self.storage.size) || (has(self.storage.storageClassName) && size(self.storage.storageClassName) != 0))",message="storage.existingClaim is mutually exclusive with storage.size/storageClassName; set exactly one"
// +kubebuilder:validation:XValidation:rule="!self.apiServer.enabled || (has(self.apiServer.keySecretRef) && self.apiServer.host != '127.0.0.1' && self.apiServer.host != 'localhost')",message="apiServer.enabled requires keySecretRef and a non-localhost host"
// +kubebuilder:validation:XValidation:rule="self.probes.mode != 'http' || self.apiServer.enabled",message="probes.mode=http requires apiServer.enabled"
// +kubebuilder:validation:XValidation:rule="!self.ingress.enabled || (self.dashboard.enabled && has(self.ingress.host) && size(self.ingress.host) != 0)",message="ingress.enabled requires dashboard.enabled and ingress.host"
// +kubebuilder:validation:XValidation:rule="!has(self.apiServer.ingress) || !self.apiServer.ingress.enabled || (self.apiServer.enabled && ((has(self.apiServer.ingress.host) && size(self.apiServer.ingress.host) != 0) || (has(self.ingress.host) && size(self.ingress.host) != 0)))",message="apiServer.ingress.enabled requires apiServer.enabled and a host (apiServer.ingress.host or spec.ingress.host)"
// +kubebuilder:validation:XValidation:rule="!has(self.channels) || self.channels.all(c, !has(c.ingress) || !c.ingress.enabled || ((has(c.ingress.host) && size(c.ingress.host) != 0) || (has(self.ingress.host) && size(self.ingress.host) != 0)))",message="channel ingress.enabled requires a host (channels[].ingress.host or spec.ingress.host)"
// +kubebuilder:validation:XValidation:rule="!has(self.skills) || !has(self.skills.custom) || self.skills.custom.all(s, has(s.sourceRef) != (has(s.inline) && size(s.inline) != 0))",message="each skills.custom entry must set exactly one of sourceRef or inline"
// +kubebuilder:validation:XValidation:rule="self.serviceAccount.create || (has(self.serviceAccount.name) && size(self.serviceAccount.name) != 0)",message="serviceAccount.name is required when serviceAccount.create is false"
// +kubebuilder:validation:XValidation:rule="!has(self.kubeconfig) || !self.kubeconfig.enabled || !has(self.serviceAccount.automountToken) || self.serviceAccount.automountToken",message="kubeconfig.enabled requires the ServiceAccount token (serviceAccount.automountToken must not be false)"
// +kubebuilder:validation:XValidation:rule="!has(self.mcp) || !has(self.mcp.servers) || self.mcp.servers.all(s, (has(s.command) && size(s.command) != 0) != (has(s.url) && size(s.url) != 0))",message="each mcp.servers entry must set exactly one of command (stdio) or url (http/sse)"
// +kubebuilder:validation:XValidation:rule="!has(self.mcp) || !has(self.mcp.servers) || self.mcp.servers.all(s, !has(s.transport) || (s.transport == 'stdio' ? (has(s.command) && size(s.command) != 0) : (has(s.url) && size(s.url) != 0)))",message="mcp.servers transport=stdio requires command; transport=http/sse requires url"
// +kubebuilder:validation:XValidation:rule="!has(self.mcp) || !has(self.mcp.servers) || self.mcp.servers.all(s, !has(s.tools) || !((has(s.tools.include) && size(s.tools.include) != 0) && (has(s.tools.exclude) && size(s.tools.exclude) != 0)))",message="mcp.servers tools must set at most one of include or exclude"
// +kubebuilder:validation:XValidation:rule="!has(self.secrets) || !has(self.secrets.bitwarden) || !has(self.secrets.bitwarden.enabled) || !self.secrets.bitwarden.enabled || has(self.secrets.bitwarden.accessTokenSecretRef)",message="secrets.bitwarden.enabled requires accessTokenSecretRef"
// +kubebuilder:validation:XValidation:rule="!has(self.profile) || !(self.profile.name in ['hermes','default','test','tmp','root','sudo'])",message="profile.name must not be a reserved hermes profile name (hermes/default/test/tmp/root/sudo)"
type HermesAgentSpec struct {
	// image is the agent container image (the operator's derived image with brew).
	// +required
	Image string `json:"image"`
	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// presetRef deep-merges a HermesConfigPreset under this spec (CR wins).
	// +optional
	PresetRef *PresetRef `json:"presetRef,omitempty"`

	// profile runs the agent under a named hermes profile (isolated home under
	// $HERMES_HOME/profiles/<name>/). Omit to use the default home.
	// +optional
	Profile *ProfileSpec `json:"profile,omitempty"`

	// soul renders to /opt/data/SOUL.md (agent persona).
	// +optional
	Soul string `json:"soul,omitempty"`

	// +optional
	Model ModelSpec `json:"model,omitempty"`
	// +optional
	Agent AgentSpec `json:"agent,omitempty"`
	// +optional
	Compression CompressionSpec `json:"compression,omitempty"`
	// +optional
	Memory MemorySpec `json:"memory,omitempty"`
	// searxng wires a self-hosted SearXNG instance for free web search (sets
	// SEARXNG_URL on the agent).
	// +optional
	Searxng SearxngSpec `json:"searxng,omitempty"`
	// honcho wires cross-session user modeling via a Honcho instance (sets
	// HONCHO_BASE_URL and, if set, HONCHO_API_KEY on the agent).
	// +optional
	Honcho HonchoSpec `json:"honcho,omitempty"`

	// mcp configures Model Context Protocol servers (config.yaml `mcp_servers`).
	// +optional
	MCP MCPSpec `json:"mcp,omitempty"`

	// secrets wires external secret managers (config.yaml `secrets`).
	// +optional
	Secrets SecretsSpec `json:"secrets,omitempty"`

	// ingress is the agent's single, shared Ingress configuration. Its host,
	// className, annotations, tls, and pathType are the defaults for every HTTP
	// surface; apiServer and channel-webhook ingresses inherit them unless they
	// set their own. When ingress.enabled (and dashboard.enabled), the dashboard
	// is exposed at ingress.path (default /). ingress.host is the base hostname,
	// also used for webhook-capable channels' auto Ingress at /webhooks/<channel>.
	// +optional
	Ingress IngressSpec `json:"ingress,omitempty"`

	// +optional
	APIServer APIServerSpec `json:"apiServer,omitempty"`
	// +optional
	Dashboard DashboardSpec `json:"dashboard,omitempty"`

	// +optional
	Channels []ChannelSpec `json:"channels,omitempty"`

	// +optional
	Skills SkillsSpec `json:"skills,omitempty"`
	// +optional
	Packages PackagesSpec `json:"packages,omitempty"`
	// +optional
	Runtime RuntimeSpec `json:"runtime,omitempty"`
	// +optional
	Cron CronSpec `json:"cron,omitempty"`

	// envFrom mirrors corev1 EnvFromSource entries onto the agent container.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
	// env mirrors corev1 EnvVar entries onto the agent container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
	// authJSONBootstrapSecretRef seeds auth.json once on first boot.
	// +optional
	AuthJSONBootstrapSecretRef *SecretKeyRef `json:"authJSONBootstrapSecretRef,omitempty"`

	// +optional
	ServiceAccount ServiceAccountSpec `json:"serviceAccount,omitempty"`

	// extraConfig is deep-merged into config.yaml (free-form, preserved verbatim).
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	ExtraConfig *runtime.RawExtension `json:"extraConfig,omitempty"`
	// extraConfigPrecedence controls whether extraConfig merges or overrides.
	// +kubebuilder:validation:Enum=merge;override
	// +kubebuilder:default=merge
	// +optional
	ExtraConfigPrecedence string `json:"extraConfigPrecedence,omitempty"`

	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// replicas must be 0 (paused) or 1 (running) — the gateway is a singleton.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// shmSize sizes the /dev/shm emptyDir for browser tools.
	// +optional
	ShmSize *resource.Quantity `json:"shmSize,omitempty"`

	// podTemplate is strategic-merged over the operator-rendered base pod
	// template. Operator invariants are re-asserted after the overlay. See §3.6.
	// +optional
	PodTemplate *corev1.PodTemplateSpec `json:"podTemplate,omitempty"`

	// +kubebuilder:default=10000
	// +optional
	HermesUID int64 `json:"hermesUID,omitempty"`
	// +kubebuilder:default=10000
	// +optional
	HermesGID int64 `json:"hermesGID,omitempty"`
	// runAsRoot governs the operator's own init containers (config seeding, pip
	// install, apptainer install): true starts them as root, false as the hermes
	// user. The MAIN agent container always starts as root regardless — the
	// upstream image boots via s6-overlay (/init = PID 1), which requires root
	// for its cont-init bootstrap and then drops the hermes process to hermesUID
	// via s6-setuidgid.
	// +kubebuilder:default=true
	// +optional
	RunAsRoot bool `json:"runAsRoot,omitempty"`
	// +kubebuilder:default=10000
	// +optional
	FSGroup int64 `json:"fsGroup,omitempty"`

	// +optional
	Probes ProbesSpec `json:"probes,omitempty"`

	// kubeconfig, when enabled, writes an in-cluster kubeconfig to ~/.kube/config
	// (the agent's $HOME/.kube/config on the shared PVC) pointing at the pod's
	// projected ServiceAccount token + CA, so kubectl and k8s client tools use
	// the pod's SA identity with no extra config. Requires the SA token to be
	// automounted (serviceAccount.automountToken != false); pair with a suitable
	// Role/RoleBinding for the access the agent needs.
	// +optional
	Kubeconfig KubeconfigSpec `json:"kubeconfig,omitempty"`
}

// ProfileName returns the hermes profile the agent runs under, or "" for the
// default home.
func (s *HermesAgentSpec) ProfileName() string {
	if s.Profile != nil {
		return s.Profile.Name
	}
	return ""
}

// EndpointsStatus reports the in-cluster service endpoints.
type EndpointsStatus struct {
	// +optional
	API string `json:"api,omitempty"`
	// +optional
	Dashboard string `json:"dashboard,omitempty"`
}

// SkillsStatus reports skill reconciliation state (written by the reloader).
type SkillsStatus struct {
	// +optional
	Synced int32 `json:"synced,omitempty"`
	// +optional
	Disabled int32 `json:"disabled,omitempty"`
	// +optional
	CustomActive []string `json:"customActive,omitempty"`
}

// PackagesStatus reports package reconciliation state (written by the reloader).
type PackagesStatus struct {
	// +optional
	BrewInstalled []string `json:"brewInstalled,omitempty"`
}

// HermesAgentStatus defines the observed state of HermesAgent.
type HermesAgentStatus struct {
	// phase is a coarse lifecycle summary.
	// +kubebuilder:validation:Enum=Pending;Provisioning;Running;Degraded;Failed;Paused
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	ConfigHash string `json:"configHash,omitempty"`
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	// +optional
	ServiceName string `json:"serviceName,omitempty"`
	// +optional
	Endpoints EndpointsStatus `json:"endpoints,omitempty"`
	// +optional
	Skills SkillsStatus `json:"skills,omitempty"`
	// +optional
	Packages PackagesStatus `json:"packages,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hagent
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HermesAgent is the Schema for the hermesagents API.
type HermesAgent struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec HermesAgentSpec `json:"spec"`

	// +optional
	Status HermesAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HermesAgentList contains a list of HermesAgent.
type HermesAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HermesAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HermesAgent{}, &HermesAgentList{})
}
