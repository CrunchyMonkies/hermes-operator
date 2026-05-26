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
)

// ServiceSpec configures the Service fronting an HTTP surface (api server,
// dashboard, or a channel webhook port). See specification §3.5.
type ServiceSpec struct {
	// type is the Service type for this surface.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// annotations are merged verbatim onto the Service object — e.g. MetalLB or
	// cloud load-balancer annotations.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressSpec configures an optional Ingress for an HTTP surface. The operator
// owns the rendered Ingress object (owner-ref'd) and merges annotations
// verbatim so it stays controller-agnostic. See specification §3.5.
type IngressSpec struct {
	// enabled toggles rendering of an Ingress for this surface.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// className maps to spec.ingressClassName on the Ingress.
	// +optional
	ClassName string `json:"className,omitempty"`

	// host is the Ingress host. Required when enabled.
	// +optional
	Host string `json:"host,omitempty"`

	// path is the HTTP path to route to this surface.
	// +kubebuilder:default=/
	// +optional
	Path string `json:"path,omitempty"`

	// pathType is the Kubernetes Ingress path matching mode.
	// +kubebuilder:validation:Enum=Exact;Prefix;ImplementationSpecific
	// +kubebuilder:default=Prefix
	// +optional
	PathType string `json:"pathType,omitempty"`

	// annotations are merged verbatim onto the Ingress object (nginx, Traefik,
	// cert-manager, external-dns, auth, rate-limit, body-size, …).
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// tls lists TLS blocks rendered onto the Ingress. secretName may reference a
	// Secret that cert-manager creates later (existence not required at admission).
	// +optional
	TLS []IngressTLS `json:"tls,omitempty"`
}

// IngressTLS mirrors networkingv1.IngressTLS.
type IngressTLS struct {
	// hosts included in the TLS certificate.
	// +optional
	Hosts []string `json:"hosts,omitempty"`

	// secretName names the TLS Secret.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// SecretKeyRef references a single key within a Secret.
type SecretKeyRef struct {
	// name of the Secret.
	// +required
	Name string `json:"name"`

	// key within the Secret holding the value.
	// +required
	Key string `json:"key"`
}

// ProbeSettings tunes a single probe's timing. Fields map directly onto the
// corev1.Probe of the same name.
type ProbeSettings struct {
	// +optional
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
	// +optional
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
	// +optional
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
	// +optional
	SuccessThreshold int32 `json:"successThreshold,omitempty"`
}
