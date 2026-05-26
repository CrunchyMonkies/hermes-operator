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
)

// HermesChannelSpec is a standalone messaging-platform binding referenced by a
// HermesAgent (v1beta1 surface; modeled here so the type is stable). v1 uses
// inline spec.channels. See specification §3.3.
type HermesChannelSpec struct {
	// inlines the same channel shape used on HermesAgent.spec.channels[].
	ChannelSpec `json:",inline"`
}

// HermesChannelStatus defines the observed state of HermesChannel.
type HermesChannelStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HermesChannel is the Schema for the hermeschannels API.
type HermesChannel struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +required
	Spec HermesChannelSpec `json:"spec"`
	// +optional
	Status HermesChannelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HermesChannelList contains a list of HermesChannel.
type HermesChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HermesChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HermesChannel{}, &HermesChannelList{})
}
