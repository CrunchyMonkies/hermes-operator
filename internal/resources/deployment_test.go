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
	"slices"
	"strings"
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

func findVolume(vols []corev1.Volume, name string) *corev1.Volume {
	for i := range vols {
		if vols[i].Name == name {
			return &vols[i]
		}
	}
	return nil
}

// TestDockerBackendInjectsDindWithPVC is the core of the docker-backend feature:
// the dind sidecar must exist and back /var/lib/docker with a subPath on the
// shared PVC (not an emptyDir), plus the identical PVC subPath mounts and the
// agent's DOCKER_HOST wiring (§11.2).
func TestDockerBackendInjectsDindWithPVC(t *testing.T) {
	a := baseAgent()
	a.Spec.Runtime.TerminalBackend = "docker"

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template

	dind := findContainer(tpl.Spec.Containers, ContainerDind)
	if dind == nil {
		t.Fatal("dind sidecar not injected for docker backend")
	}

	// /var/lib/docker must be PVC-backed via the shared claim + dind subPath.
	dockerMount := findMount(dind, DindDockerDir)
	if dockerMount == nil {
		t.Fatalf("dind has no %s mount", DindDockerDir)
	}
	if dockerMount.Name != VolShared || dockerMount.SubPath != SubPathDind {
		t.Errorf("%s mount = {vol:%s subPath:%s}, want {vol:%s subPath:%s}",
			DindDockerDir, dockerMount.Name, dockerMount.SubPath, VolShared, SubPathDind)
	}
	// That volume must be the shared PVC, not an emptyDir.
	shared := findVolume(tpl.Spec.Volumes, VolShared)
	if shared == nil || shared.PersistentVolumeClaim == nil {
		t.Errorf("%s volume is not a PVC: %+v", VolShared, shared)
	}

	// Identical-path PVC subPaths must be mapped into dind so bind mounts resolve.
	for path, sub := range map[string]string{HermesHome: SubPathData, DotLocalPath: SubPathLocal, LinuxbrewPath: SubPathBrew} {
		m := findMount(dind, path)
		if m == nil || m.Name != VolShared || m.SubPath != sub {
			t.Errorf("dind mount for %s = %+v, want shared subPath %s", path, m, sub)
		}
	}

	// Standard (non-rootless) dind is privileged.
	if dind.SecurityContext == nil || dind.SecurityContext.Privileged == nil || !*dind.SecurityContext.Privileged {
		t.Error("non-rootless dind must be privileged")
	}

	// The unix socket is group-owned by the agent's GID so a non-root agent (and the
	// tool sandboxes that bind-mount it) can reach the daemon.
	hasGroup := false
	for _, arg := range dind.Args {
		if arg == "--group=10000" {
			hasGroup = true
		}
	}
	if !hasGroup {
		t.Errorf("dind args missing --group=10000 for the unix socket: %v", dind.Args)
	}

	// Agent wired to the daemon over the default unix socket.
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	var dockerHostVal string
	for _, e := range hermes.Env {
		if e.Name == "DOCKER_HOST" {
			dockerHostVal = e.Value
		}
	}
	if dockerHostVal != "unix://"+DindSocketDir+"/docker.sock" {
		t.Errorf("agent DOCKER_HOST = %q, want unix socket", dockerHostVal)
	}
	if findMount(hermes, DindSocketDir) == nil || findMount(dind, DindSocketDir) == nil {
		t.Error("unix socket emptyDir not mounted into both agent and dind")
	}
	if findVolume(tpl.Spec.Volumes, VolDindSocket) == nil {
		t.Errorf("%s socket volume missing", VolDindSocket)
	}
}

// TestDockerBackendReassertedAfterOverlay ensures the operator-owned dind
// survives (and is re-asserted over) a podTemplate overlay (§11.2 / §3.6).
func TestDockerBackendReassertedAfterOverlay(t *testing.T) {
	a := baseAgent()
	a.Spec.Runtime.TerminalBackend = "docker"
	// Overlay tries to weaken dind (drop privileged) and add an unrelated sidecar.
	a.Spec.PodTemplate = &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: ContainerDind, SecurityContext: &corev1.SecurityContext{Privileged: ptrBool(false)}},
				{Name: "log-tailer", Image: "busybox"},
			},
		},
	}

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template
	if findContainer(tpl.Spec.Containers, "log-tailer") == nil {
		t.Error("overlay sidecar dropped")
	}
	dind := findContainer(tpl.Spec.Containers, ContainerDind)
	if dind == nil || findMount(dind, DindDockerDir) == nil {
		t.Fatal("dind not re-asserted after overlay")
	}
	if dind.SecurityContext == nil || dind.SecurityContext.Privileged == nil || !*dind.SecurityContext.Privileged {
		t.Error("operator must re-assert privileged dind over the overlay")
	}
}

// TestDockerBackendRootlessAndTCP covers the rootless image swap (no privileged)
// and the tcp socket transport (no shared emptyDir).
func TestDockerBackendRootlessAndTCP(t *testing.T) {
	a := baseAgent()
	a.Spec.Runtime.TerminalBackend = "docker"
	a.Spec.Runtime.Docker.Rootless = true
	a.Spec.Runtime.Docker.SocketTransport = "tcp"

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template
	dind := findContainer(tpl.Spec.Containers, ContainerDind)
	if dind == nil {
		t.Fatal("dind not injected")
	}
	if dind.Image != DefaultDindRootlessImage {
		t.Errorf("rootless image = %q, want %q", dind.Image, DefaultDindRootlessImage)
	}
	if dind.SecurityContext != nil && dind.SecurityContext.Privileged != nil && *dind.SecurityContext.Privileged {
		t.Error("rootless dind must not be privileged")
	}
	// /var/lib/docker is still PVC-backed regardless of transport.
	if m := findMount(dind, DindDockerDir); m == nil || m.SubPath != SubPathDind {
		t.Error("rootless dind still needs the PVC-backed docker dir")
	}
	// tcp transport: no shared socket emptyDir.
	if findVolume(tpl.Spec.Volumes, VolDindSocket) != nil {
		t.Error("tcp transport must not create the socket emptyDir")
	}
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	var dh string
	for _, e := range hermes.Env {
		if e.Name == "DOCKER_HOST" {
			dh = e.Value
		}
	}
	if dh != "tcp://127.0.0.1:2375" {
		t.Errorf("agent DOCKER_HOST = %q, want tcp", dh)
	}
}

// TestLocalBackendHasNoDind is the negative: no sidecar for the default backend.
func TestLocalBackendHasNoDind(t *testing.T) {
	a := baseAgent() // TerminalBackend defaults to "" / local
	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	if findContainer(dep.Spec.Template.Spec.Containers, ContainerDind) != nil {
		t.Error("dind must not be injected for the local backend")
	}
}

func dockerHostEnv(c *corev1.Container) string {
	for _, e := range c.Env {
		if e.Name == "DOCKER_HOST" {
			return e.Value
		}
	}
	return ""
}

// TestDockerEnabledLocalBackendInjectsDind: docker.enabled turns the managed dind
// sidecar on even when tools run locally (terminalBackend stays local).
func TestDockerEnabledLocalBackendInjectsDind(t *testing.T) {
	a := baseAgent() // local backend
	a.Spec.Runtime.Docker.Enabled = ptrBool(true)

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template
	if findContainer(tpl.Spec.Containers, ContainerDind) == nil {
		t.Fatal("dind sidecar must be injected when docker.enabled, regardless of backend")
	}
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	if got := dockerHostEnv(hermes); got != "unix://"+DindSocketDir+"/docker.sock" {
		t.Errorf("agent DOCKER_HOST = %q, want managed dind unix socket", got)
	}
	if findVolume(tpl.Spec.Volumes, VolDindSocket) == nil {
		t.Errorf("%s socket volume missing", VolDindSocket)
	}
}

// TestExternalDockerNoSidecar: an externalHost skips the sidecar and points the
// agent at the remote daemon.
func TestExternalDockerNoSidecar(t *testing.T) {
	a := baseAgent()
	a.Spec.Runtime.Docker.ExternalHost = "tcp://dockerd.infra.svc:2375"

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template
	if findContainer(tpl.Spec.Containers, ContainerDind) != nil {
		t.Error("external docker must not inject a dind sidecar")
	}
	if findVolume(tpl.Spec.Volumes, VolDindSocket) != nil {
		t.Error("external docker must not create the dind socket volume")
	}
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	if got := dockerHostEnv(hermes); got != "tcp://dockerd.infra.svc:2375" {
		t.Errorf("agent DOCKER_HOST = %q, want the external host", got)
	}
}

// TestExternalDockerTLS: docker.tls mounts the cert Secret and sets the TLS env.
func TestExternalDockerTLS(t *testing.T) {
	a := baseAgent()
	a.Spec.Runtime.Docker.ExternalHost = "tcp://dockerd.infra.svc:2376"
	a.Spec.Runtime.Docker.TLS = &hermesv1alpha1.DockerTLSSpec{SecretName: "docker-client-certs"}

	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	tpl := dep.Spec.Template
	vol := findVolume(tpl.Spec.Volumes, VolDockerCerts)
	if vol == nil || vol.Secret == nil || vol.Secret.SecretName != "docker-client-certs" {
		t.Fatalf("docker-certs volume not wired to the Secret: %+v", vol)
	}
	hermes := findContainer(tpl.Spec.Containers, ContainerHermes)
	m := findMount(hermes, hermesv1alpha1.DockerCertMountPath)
	if m == nil || m.Name != VolDockerCerts || !m.ReadOnly {
		t.Errorf("agent cert mount = %+v, want read-only %s", m, VolDockerCerts)
	}
	want := map[string]string{"DOCKER_TLS_VERIFY": "1", "DOCKER_CERT_PATH": hermesv1alpha1.DockerCertMountPath}
	for _, e := range hermes.Env {
		if v, ok := want[e.Name]; ok {
			if e.Value != v {
				t.Errorf("%s = %q, want %q", e.Name, e.Value, v)
			}
			delete(want, e.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing TLS env vars: %v", want)
	}
}

// reloaderBrew returns the RELOADER_BREW_PACKAGES env value (space-joined list).
func reloaderBrew(t *testing.T, a *hermesv1alpha1.HermesAgent) []string {
	t.Helper()
	dep, err := Deployment(a, "sha256:abc", "")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	reloader := findContainer(dep.Spec.Template.Spec.Containers, ContainerReloader)
	if reloader == nil {
		t.Fatal("reloader container missing")
	}
	for _, e := range reloader.Env {
		if e.Name == "RELOADER_BREW_PACKAGES" {
			return strings.Fields(e.Value)
		}
	}
	return nil
}

// TestDockerInstallCLI: enabling docker auto-installs the `docker` CLI via brew,
// merges with the user's list (no dup), opts out via installCLI=false, and is
// absent when docker is off.
func TestDockerInstallCLI(t *testing.T) {
	// enabled, no user brew -> just docker.
	a := baseAgent()
	a.Spec.Runtime.Docker.Enabled = ptrBool(true)
	if got := reloaderBrew(t, a); !slices.Contains(got, "docker") {
		t.Errorf("docker.enabled should auto-install docker, got brew %v", got)
	}

	// merges with the user's list, no duplicate when already present.
	a.Spec.Packages.Brew = []string{"kubectl", "docker"}
	got := reloaderBrew(t, a)
	dockerCount := 0
	for _, p := range got {
		if p == "docker" {
			dockerCount++
		}
	}
	if !slices.Contains(got, "kubectl") || dockerCount != 1 {
		t.Errorf("brew should be {kubectl, docker} with no dup, got %v", got)
	}

	// external host also auto-installs the CLI (needed to reach the remote daemon).
	ext := baseAgent()
	ext.Spec.Runtime.Docker.ExternalHost = "tcp://dockerd:2375"
	if got := reloaderBrew(t, ext); !slices.Contains(got, "docker") {
		t.Errorf("externalHost should auto-install docker, got %v", got)
	}

	// opt-out: installCLI=false keeps only the user's list.
	off := baseAgent()
	off.Spec.Runtime.Docker.Enabled = ptrBool(true)
	off.Spec.Runtime.Docker.InstallCLI = ptrBool(false)
	off.Spec.Packages.Brew = []string{"jq"}
	if got := reloaderBrew(t, off); slices.Contains(got, "docker") {
		t.Errorf("installCLI=false must not add docker, got %v", got)
	}

	// docker off (local, not enabled) -> no docker.
	none := baseAgent()
	if got := reloaderBrew(t, none); slices.Contains(got, "docker") {
		t.Errorf("docker disabled must not install docker, got %v", got)
	}
}

func ptrBool(b bool) *bool { return &b }

func ptrI64(i int64) *int64 { return &i }
