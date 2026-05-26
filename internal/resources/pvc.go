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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// SharedPVC builds the single ReadWriteOnce claim backing all mounts (§4). It
// returns nil when an existingClaim is configured (the operator does not own it).
func SharedPVC(a *hermesv1alpha1.HermesAgent) *corev1.PersistentVolumeClaim {
	if a.Spec.Storage.ExistingClaim != "" {
		return nil
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PVCName(a),
			Namespace: a.Namespace,
			Labels:    Labels(a),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	}

	if a.Spec.Storage.Size != nil {
		pvc.Spec.Resources = corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: *a.Spec.Storage.Size},
		}
	}
	if sc := a.Spec.Storage.StorageClassName; sc != "" {
		pvc.Spec.StorageClassName = &sc
	}
	return pvc
}
