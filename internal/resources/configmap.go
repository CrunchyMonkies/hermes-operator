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

// ConfigMap builds the ConfigMap holding the rendered config.yaml and (if set)
// SOUL.md. The init container copies these onto the writable PVC at boot.
func ConfigMap(a *hermesv1alpha1.HermesAgent, configYAML []byte, soul string) *corev1.ConfigMap {
	data := map[string]string{
		"config.yaml": string(configYAML),
	}
	if soul != "" {
		data["SOUL.md"] = soul
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(a),
			Namespace: a.Namespace,
			Labels:    Labels(a),
		},
		Data: data,
	}
}

// SkillConfigMaps builds one ConfigMap per inline custom skill (SKILL.md). For
// skills sourced from an existing ConfigMap/Secret (sourceRef), the reloader
// reads that object directly and no ConfigMap is rendered here.
func SkillConfigMaps(a *hermesv1alpha1.HermesAgent) []*corev1.ConfigMap {
	var out []*corev1.ConfigMap
	for _, s := range a.Spec.Skills.Custom {
		if s.Inline == "" {
			continue
		}
		out = append(out, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      SkillConfigMapName(a, s.Name),
				Namespace: a.Namespace,
				Labels:    Labels(a),
			},
			Data: map[string]string{"SKILL.md": s.Inline},
		})
	}
	return out
}
