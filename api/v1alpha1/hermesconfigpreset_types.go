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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HermesConfigPresetSpec is a reusable bundle of defaults deep-merged under a
// HermesAgent via presetRef (CR wins). See specification §3.2.
type HermesConfigPresetSpec struct {
	// +optional
	Model ModelSpec `json:"model,omitempty"`
	// +optional
	Agent AgentSpec `json:"agent,omitempty"`
	// +optional
	Compression CompressionSpec `json:"compression,omitempty"`
	// +optional
	Memory MemorySpec `json:"memory,omitempty"`
	// +optional
	Skills SkillsSpec `json:"skills,omitempty"`
	// +optional
	Packages PackagesSpec `json:"packages,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	ExtraConfig *runtime.RawExtension `json:"extraConfig,omitempty"`
}

// HermesConfigPresetStatus defines the observed state of HermesConfigPreset.
type HermesConfigPresetStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HermesConfigPreset is the Schema for the hermesconfigpresets API.
type HermesConfigPreset struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +required
	Spec HermesConfigPresetSpec `json:"spec"`
	// +optional
	Status HermesConfigPresetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HermesConfigPresetList contains a list of HermesConfigPreset.
type HermesConfigPresetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HermesConfigPreset `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HermesConfigPreset{}, &HermesConfigPresetList{})
}
