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
// +kubebuilder:validation:XValidation:rule="(!has(self.defaultProfile.channels) || self.defaultProfile.channels.all(c, !has(c.ingress) || !c.ingress.enabled || has(c.ingress.host) || has(self.ingress.host))) && (!has(self.profiles) || self.profiles.all(p, !has(p.channels) || p.channels.all(c, !has(c.ingress) || !c.ingress.enabled || has(c.ingress.host) || has(self.ingress.host))))",message="channel ingress.enabled requires a host (channels[].ingress.host or spec.ingress.host)"
// +kubebuilder:validation:XValidation:rule="self.serviceAccount.create || (has(self.serviceAccount.name) && size(self.serviceAccount.name) != 0)",message="serviceAccount.name is required when serviceAccount.create is false"
// +kubebuilder:validation:XValidation:rule="!has(self.kubeconfig) || !self.kubeconfig.enabled || !has(self.serviceAccount.automountToken) || self.serviceAccount.automountToken",message="kubeconfig.enabled requires the ServiceAccount token (serviceAccount.automountToken must not be false)"
// +kubebuilder:validation:XValidation:rule="!has(self.profiles) || self.profiles.all(p, !(p.name in ['hermes','default','test','tmp','root','sudo']))",message="profiles[].name must not be a reserved hermes profile name (hermes/default/test/tmp/root/sudo)"
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

	// defaultProfile is the implicit "default" hermes profile at $HERMES_HOME
	// (/opt/data) — the agent the main container runs via `gateway run`. It uses the
	// shared ProfileConfig, the SAME object as each spec.profiles[] entry.
	// +optional
	DefaultProfile ProfileConfig `json:"defaultProfile,omitempty"`

	// profiles declares additional hermes profiles hosted in the same pod: each is
	// a complete, independent agent instance (own config.yaml/SOUL.md/model/skills/
	// memory/sessions/channels and its OWN gateway process and bot token) rendered
	// under $HERMES_HOME/profiles/<name>/, auto-started in-pod by the upstream
	// image. A profile's config sections override defaultProfile; unset sections
	// inherit it.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=50
	Profiles []ProfileSpec `json:"profiles,omitempty"`

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

	// packages declares runtime package installation onto the shared PVC (pip/brew),
	// shared by all profiles in the pod.
	// +optional
	Packages PackagesSpec `json:"packages,omitempty"`
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

// AllProfileConfigs returns every profile's config in the pod: the default
// profile first, then each named profile (its config un-resolved — inheritance is
// applied at render time by the controller). Useful for pod-level unions (pip
// deps, dind injection, webhook port allocation).
func (s *HermesAgentSpec) AllProfileConfigs() []ProfileConfig {
	out := make([]ProfileConfig, 0, 1+len(s.Profiles))
	out = append(out, s.DefaultProfile)
	for _, p := range s.Profiles {
		out = append(out, p.ProfileConfig)
	}
	return out
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

// ProfileStatus reports the reconciled state of one hosted profile.
type ProfileStatus struct {
	// +required
	Name string `json:"name"`
	// home is the profile's $HERMES_HOME directory on the shared PVC.
	// +optional
	Home string `json:"home,omitempty"`
	// enabled mirrors whether the profile's gateway is set to auto-start.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
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
	// profiles reports the hosted named profiles (spec.profiles).
	// +listType=map
	// +listMapKey=name
	// +optional
	Profiles []ProfileStatus `json:"profiles,omitempty"`
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
