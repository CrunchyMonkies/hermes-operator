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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

func baseAgent() *hermesv1alpha1.HermesAgent {
	return &hermesv1alpha1.HermesAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "research-bot", Namespace: "default"},
		Spec: hermesv1alpha1.HermesAgentSpec{
			Image:     "harbor.example/hermes-agent:v1",
			HermesUID: 10000,
			HermesGID: 10000,
			RunAsRoot: true,
			FSGroup:   10000,
		},
	}
}

func findMount(c *corev1.Container, path string) *corev1.VolumeMount {
	for i := range c.VolumeMounts {
		if c.VolumeMounts[i].MountPath == path {
			return &c.VolumeMounts[i]
		}
	}
	return nil
}

func TestDeploymentBaseInvariants(t *testing.T) {
	a := baseAgent()
	dep, err := Deployment(a, "sha256:abc", "harbor.example/hermes-reloader:v1")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}

	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy = %v, want Recreate", dep.Spec.Strategy.Type)
	}
	if dep.Spec.Template.Annotations[ConfigHashAnno] != "sha256:abc" {
		t.Errorf("config-hash annotation missing")
	}

	hermes := findContainer(dep.Spec.Template.Spec.Containers, ContainerHermes)
	if hermes == nil {
		t.Fatal("hermes container missing")
	}
	for _, p := range []string{HermesHome, DotLocalPath, LinuxbrewPath} {
		if findMount(hermes, p) == nil {
			t.Errorf("hermes missing shared mount at %s", p)
		}
	}
	// root-start for gosu.
	if hermes.SecurityContext == nil || hermes.SecurityContext.RunAsUser == nil || *hermes.SecurityContext.RunAsUser != 0 {
		t.Errorf("hermes should start as root (runAsRoot:true)")
	}
	if findContainer(dep.Spec.Template.Spec.Containers, ContainerReloader) == nil {
		t.Error("reloader sidecar missing")
	}
}

// AC#12: a podTemplate overlay adding a sidecar/nodeSelector/toleration/label
// applies, while shared-PVC mounts, config-hash, and root-start are preserved.
func TestPodTemplateOverlayPreservesInvariants(t *testing.T) {
	a := baseAgent()
	a.Spec.PodTemplate = &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"team": "research"},
			Annotations: map[string]string{"prometheus.io/scrape": "true"},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"kubernetes.io/arch": "amd64"},
			Tolerations: []corev1.Toleration{
				{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "hermes", Effect: corev1.TaintEffectNoSchedule},
			},
			Containers: []corev1.Container{
				{Name: "log-tailer", Image: "busybox"},
				// Attempt to clobber the hermes container's security to non-root.
				{Name: ContainerHermes, SecurityContext: &corev1.SecurityContext{RunAsUser: ptrI64(10000)}},
			},
		},
	}

	dep, err := Deployment(a, "sha256:xyz", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template

	// Overlay extras applied.
	if tpl.Spec.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Error("nodeSelector not applied")
	}
	if len(tpl.Spec.Tolerations) != 1 {
		t.Errorf("toleration not applied: %v", tpl.Spec.Tolerations)
	}
	if tpl.Labels["team"] != "research" {
		t.Error("pod label not applied")
	}
	if findContainer(tpl.Spec.Containers, "log-tailer") == nil {
		t.Error("extra sidecar not added")
	}

	// Invariants preserved despite the overlay's attempt to change them.
	if tpl.Annotations[ConfigHashAnno] != "sha256:xyz" {
		t.Error("config-hash annotation lost")
	}
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	if findMount(hermes, HermesHome) == nil || findMount(hermes, LinuxbrewPath) == nil {
		t.Error("shared mounts dropped by overlay")
	}
	if hermes.SecurityContext.RunAsUser == nil || *hermes.SecurityContext.RunAsUser != 0 {
		t.Error("overlay must not override root-start when runAsRoot:true")
	}
}

func TestProbeModeSelection(t *testing.T) {
	// exec by default (no api server).
	a := baseAgent()
	live, _ := buildProbes(a)
	if live.Exec == nil || live.HTTPGet != nil {
		t.Error("expected exec probe when apiServer disabled")
	}
	// http when api server enabled.
	a.Spec.APIServer.Enabled = true
	a.Spec.APIServer.Port = APIPort
	live2, _ := buildProbes(a)
	if live2.HTTPGet == nil || live2.HTTPGet.Path != "/health" {
		t.Error("expected http /health probe when apiServer enabled")
	}
}

func TestServicePortsDerived(t *testing.T) {
	a := baseAgent()
	a.Spec.APIServer.Enabled = true
	a.Spec.APIServer.Port = APIPort
	a.Spec.Dashboard.Enabled = true
	a.Spec.Dashboard.Port = DashboardPort
	a.Spec.Channels = []hermesv1alpha1.ChannelSpec{{Type: "telegram", WebhookPort: 8443}}

	svc := Service(a)
	if svc == nil {
		t.Fatal("expected a Service")
	}
	names := map[string]int32{}
	for _, p := range svc.Spec.Ports {
		names[p.Name] = p.Port
	}
	if names["api"] != APIPort || names["dashboard"] != DashboardPort || names["wh-telegram"] != 8443 {
		t.Errorf("unexpected service ports: %v", names)
	}
}

func TestIngressAnnotationsVerbatim(t *testing.T) {
	a := baseAgent()
	a.Spec.APIServer.Enabled = true
	a.Spec.APIServer.Ingress = hermesv1alpha1.IngressSpec{
		Enabled:   true,
		ClassName: "nginx",
		Host:      "api.example.com",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/proxy-body-size": "25m",
			"cert-manager.io/cluster-issuer":              "letsencrypt-prod",
		},
		TLS: []hermesv1alpha1.IngressTLS{{SecretName: "api-tls", Hosts: []string{"api.example.com"}}},
	}
	ings := Ingresses(a)
	if len(ings) != 1 {
		t.Fatalf("expected 1 ingress, got %d", len(ings))
	}
	ing := ings[0]
	if ing.Annotations["nginx.ingress.kubernetes.io/proxy-body-size"] != "25m" {
		t.Error("ingress annotation not carried verbatim")
	}
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Error("ingressClassName not set")
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].SecretName != "api-tls" {
		t.Error("tls not rendered")
	}
}

func ptrI64(i int64) *int64 { return &i }
