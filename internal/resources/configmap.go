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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// KubeConfigKey is the config-ConfigMap data key holding the rendered kubeconfig
// the init container copies to ~/.kube/config (spec.kubeconfig.enabled).
const KubeConfigKey = "kube-config"

// KubeconfigYAML renders an in-cluster kubeconfig that authenticates as the
// pod's ServiceAccount via the projected token + CA bundle. kubelet rotates the
// token file; kubectl re-reads tokenFile each call, so it never goes stale. The
// context is scoped to the agent's namespace.
func KubeconfigYAML(a *hermesv1alpha1.HermesAgent) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
  - name: in-cluster
    cluster:
      server: https://kubernetes.default.svc
      certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
users:
  - name: %[1]s
    user:
      tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
contexts:
  - name: %[2]s
    context:
      cluster: in-cluster
      user: %[1]s
      namespace: %[2]s
current-context: %[2]s
`, a.Name, a.Namespace)
}

// ConfigMap builds the ConfigMap holding the rendered config.yaml and (if set)
// SOUL.md, plus the kubeconfig when spec.kubeconfig.enabled. The init container
// copies these onto the writable PVC at boot.
func ConfigMap(a *hermesv1alpha1.HermesAgent, configYAML []byte, soul string) *corev1.ConfigMap {
	data := map[string]string{
		"config.yaml": string(configYAML),
	}
	if soul != "" {
		data["SOUL.md"] = soul
	}
	if a.Spec.Kubeconfig.Enabled {
		data[KubeConfigKey] = KubeconfigYAML(a)
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
