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
	"strings"
	"testing"
)

func TestKubeconfigEnabledRendersConfigMapAndInit(t *testing.T) {
	a := baseAgent() // namespace "default", name "research-bot"
	a.Spec.Kubeconfig.Enabled = true

	cm := ConfigMap(a, []byte("model: {}\n"), "")
	kc, ok := cm.Data[KubeConfigKey]
	if !ok {
		t.Fatalf("ConfigMap missing %q key when kubeconfig enabled", KubeConfigKey)
	}
	for _, want := range []string{
		"server: https://kubernetes.default.svc",
		"certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		"tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token",
		"namespace: default", // scoped to the agent's namespace
		"name: research-bot", // user = agent name
	} {
		if !strings.Contains(kc, want) {
			t.Errorf("kubeconfig missing %q\n---\n%s", want, kc)
		}
	}

	// config-init must write ~/.kube/config (HOME=/opt/data) with kubectl-safe perms.
	script := configInitCommand(a)[2]
	for _, want := range []string{
		HermesHome + "/.kube/config",
		"cp " + ConfigSrcDir + "/" + KubeConfigKey + " " + HermesHome + "/.kube/config",
		"chmod 600 " + HermesHome + "/.kube/config",
		"chmod 700 " + HermesHome + "/.kube",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("init script missing %q\n---\n%s", want, script)
		}
	}
}

func TestKubeconfigDisabledByDefault(t *testing.T) {
	a := baseAgent()
	cm := ConfigMap(a, []byte("x: y\n"), "")
	if _, ok := cm.Data[KubeConfigKey]; ok {
		t.Errorf("ConfigMap has %q key when kubeconfig disabled", KubeConfigKey)
	}
	if strings.Contains(configInitCommand(a)[2], ".kube/config") {
		t.Error("init script writes ~/.kube/config when kubeconfig disabled")
	}
}
