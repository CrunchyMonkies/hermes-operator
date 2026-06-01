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
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// sharedVolumeMounts are the three shared-PVC subPath mounts plus /dev/shm that
// the operator always asserts on the hermes (and dind) container (§4.1).
func sharedVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{Name: VolShared, MountPath: HermesHome, SubPath: SubPathData},
		{Name: VolShared, MountPath: DotLocalPath, SubPath: SubPathLocal},
		{Name: VolShared, MountPath: LinuxbrewPath, SubPath: SubPathBrew},
		{Name: VolShm, MountPath: "/dev/shm"},
	}
}

// baseVolumes builds the pod volumes the operator owns.
func baseVolumes(a *hermesv1alpha1.HermesAgent) []corev1.Volume {
	shm := corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
	if a.Spec.ShmSize != nil {
		size := *a.Spec.ShmSize
		shm.SizeLimit = &size
	}
	vols := []corev1.Volume{
		{
			Name: VolShared,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: PVCName(a)},
			},
		},
		{
			Name: VolConfig,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName(a)},
				},
			},
		},
		{Name: VolShm, VolumeSource: corev1.VolumeSource{EmptyDir: &shm}},
	}
	vols = append(vols, skillSourceVolumes(a)...)
	return vols
}

// skillVolumeName is the pod volume name for a custom skill's source object.
func skillVolumeName(skill string) string { return "skill-" + skill }

// skillSourceVolumes mounts each custom skill's source (inline ConfigMap, or a
// referenced ConfigMap/Secret) so the reloader can copy it onto the PVC.
func skillSourceVolumes(a *hermesv1alpha1.HermesAgent) []corev1.Volume {
	var out []corev1.Volume
	for _, s := range a.Spec.Skills.Custom {
		vol := corev1.Volume{Name: skillVolumeName(s.Name)}
		switch {
		case s.Inline != "":
			vol.VolumeSource = corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: SkillConfigMapName(a, s.Name)},
				},
			}
		case s.SourceRef != nil && s.SourceRef.ConfigMapName != "":
			vol.VolumeSource = corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.SourceRef.ConfigMapName},
				},
			}
		case s.SourceRef != nil && s.SourceRef.SecretName != "":
			vol.VolumeSource = corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: s.SourceRef.SecretName},
			}
		default:
			continue
		}
		out = append(out, vol)
	}
	return out
}

// skillSourceMounts mounts the skill source volumes into the reloader container.
func skillSourceMounts(a *hermesv1alpha1.HermesAgent) []corev1.VolumeMount {
	var out []corev1.VolumeMount
	for _, s := range a.Spec.Skills.Custom {
		out = append(out, corev1.VolumeMount{
			Name:      skillVolumeName(s.Name),
			MountPath: SkillSrcDir + "/" + s.Name,
			ReadOnly:  true,
		})
	}
	return out
}

// hermesEnv assembles the env for the agent container from typed surfaces.
func hermesEnv(a *hermesv1alpha1.HermesAgent) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "HERMES_UID", Value: strconv.FormatInt(a.Spec.HermesUID, 10)},
		{Name: "HERMES_GID", Value: strconv.FormatInt(a.Spec.HermesGID, 10)},
		{Name: "PYTHONUNBUFFERED", Value: "1"},
	}

	if a.Spec.APIServer.Enabled {
		host := a.Spec.APIServer.Host
		port := a.Spec.APIServer.Port
		if port == 0 {
			port = APIPort
		}
		env = append(env,
			corev1.EnvVar{Name: "API_SERVER_ENABLED", Value: "1"},
			corev1.EnvVar{Name: "API_SERVER_HOST", Value: host},
			corev1.EnvVar{Name: "API_SERVER_PORT", Value: strconv.Itoa(int(port))},
		)
		if ref := a.Spec.APIServer.KeySecretRef; ref != nil {
			env = append(env, corev1.EnvVar{
				Name: "API_SERVER_KEY",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
						Key:                  ref.Key,
					},
				},
			})
		}
		if len(a.Spec.APIServer.CORSOrigins) > 0 {
			env = append(env, corev1.EnvVar{Name: "API_SERVER_CORS_ORIGINS", Value: strings.Join(a.Spec.APIServer.CORSOrigins, ",")})
		}
	}

	if a.Spec.Dashboard.Enabled {
		port := a.Spec.Dashboard.Port
		if port == 0 {
			port = DashboardPort
		}
		env = append(env,
			corev1.EnvVar{Name: "HERMES_DASHBOARD", Value: "1"},
			corev1.EnvVar{Name: "HERMES_DASHBOARD_HOST", Value: a.Spec.Dashboard.Host},
			corev1.EnvVar{Name: "HERMES_DASHBOARD_PORT", Value: strconv.Itoa(int(port))},
		)
	}

	// Webhook env for webhook-capable channels. The local listen port is set
	// whenever a (resolved) port is present; the public webhook URL is set only
	// when the channel opts into webhook mode (ingress.enabled) and a host is
	// available — that URL is what flips the platform from polling to webhook.
	// The verification secret (e.g. TELEGRAM_WEBHOOK_SECRET) is NOT set here; it
	// must be supplied via the channel secretRef (it arrives via envFrom).
	whPorts := resolvedWebhookPorts(a)
	for i, ch := range a.Spec.Channels {
		wp, ok := webhookPlatforms[ch.Type]
		if !ok {
			continue
		}
		port := whPorts[i]
		if port <= 0 {
			continue
		}
		env = append(env, corev1.EnvVar{Name: wp.portEnv, Value: strconv.Itoa(int(port))})
		if channelWantsWebhook(ch) {
			if host := channelEffectiveHost(a, ch); host != "" {
				env = append(env, corev1.EnvVar{Name: wp.urlEnv, Value: webhookURL(host, ch.Type)})
			}
		}
	}

	if ref := a.Spec.AuthJSONBootstrapSecretRef; ref != nil {
		env = append(env, corev1.EnvVar{
			Name: "HERMES_AUTH_JSON_BOOTSTRAP",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					Key:                  ref.Key,
				},
			},
		})
	}

	if url := a.Spec.Searxng.URL; url != "" {
		env = append(env, corev1.EnvVar{Name: "SEARXNG_URL", Value: url})
	}
	if a.Spec.Honcho.BaseURL != "" {
		env = append(env, corev1.EnvVar{Name: "HONCHO_BASE_URL", Value: a.Spec.Honcho.BaseURL})
	}
	if ref := a.Spec.Honcho.APIKeySecretRef; ref != nil {
		env = append(env, corev1.EnvVar{
			Name: "HONCHO_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					Key:                  ref.Key,
				},
			},
		})
	}
	// Model provider API keys: inject each declared provider's key under its
	// resolved env var (built-in's known var, explicit keyEnv, or a derived name for
	// custom endpoints). All are injected so hermes /model switching works. Providers
	// without a keySecretRef or a resolvable env var (e.g. OAuth) are skipped.
	for _, p := range a.Spec.Model.Providers {
		ref := p.KeySecretRef
		envName := p.KeyEnvVar()
		if ref == nil || envName == "" {
			continue
		}
		env = append(env, corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					Key:                  ref.Key,
				},
			},
		})
	}

	// MCP server credentials: each server's secretEnv binds a Secret key to an env
	// var the agent references via ${name} in headers/env (see MCPServerSpec).
	for _, s := range a.Spec.MCP.Servers {
		for _, se := range s.SecretEnv {
			if se.Name == "" {
				continue
			}
			env = append(env, corev1.EnvVar{
				Name: se.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: se.SecretRef.Name},
						Key:                  se.SecretRef.Key,
					},
				},
			})
		}
	}

	// Bitwarden: inject the machine-account access token under its configured env
	// var (default BWS_ACCESS_TOKEN) so hermes' bws sync can authenticate.
	if bw := a.Spec.Secrets.Bitwarden; bw != nil && bw.AccessTokenSecretRef != nil {
		name := bw.AccessTokenEnv
		if name == "" {
			name = "BWS_ACCESS_TOKEN"
		}
		env = append(env, corev1.EnvVar{
			Name: name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: bw.AccessTokenSecretRef.Name},
					Key:                  bw.AccessTokenSecretRef.Key,
				},
			},
		})
	}

	// Point Python at the pip-installed packages on the shared PVC (the pip-install
	// init container writes them to the dotlocal user-site).
	if pipInstallEnabled(a) {
		env = append(env, corev1.EnvVar{Name: "PYTHONPATH", Value: PipSitePackages})
	}

	// User-provided env appended last (can override operator defaults by name
	// only if duplicated; Kubernetes keeps the last occurrence).
	env = append(env, a.Spec.Env...)
	return env
}

// hermesEnvFrom collects spec.envFrom plus each channel's secretRef.
func hermesEnvFrom(a *hermesv1alpha1.HermesAgent) []corev1.EnvFromSource {
	out := append([]corev1.EnvFromSource(nil), a.Spec.EnvFrom...)
	seen := map[string]bool{}
	for _, ch := range a.Spec.Channels {
		if ch.SecretRef != nil && ch.SecretRef.Name != "" && !seen[ch.SecretRef.Name] {
			seen[ch.SecretRef.Name] = true
			out = append(out, corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ch.SecretRef.Name},
				},
			})
		}
	}
	return out
}

// buildProbes returns (liveness, readiness) per the resolved probe mode (§1.2).
func buildProbes(a *hermesv1alpha1.HermesAgent) (*corev1.Probe, *corev1.Probe) {
	mode := resolveProbeMode(a)

	var handler corev1.ProbeHandler
	if mode == "http" {
		port := a.Spec.APIServer.Port
		if port == 0 {
			port = APIPort
		}
		handler = corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt32(port),
			},
		}
	} else {
		handler = corev1.ProbeHandler{
			Exec: &corev1.ExecAction{Command: []string{"hermes", "gateway", "status"}},
		}
	}

	live := &corev1.Probe{ProbeHandler: handler}
	applyProbeSettings(live, a.Spec.Probes.Liveness, 40, 30, 4)
	ready := &corev1.Probe{ProbeHandler: *handler.DeepCopy()}
	applyProbeSettings(ready, a.Spec.Probes.Readiness, 15, 15, 3)
	return live, ready
}

// resolveProbeMode resolves "auto" to http (when apiServer.enabled) or exec.
func resolveProbeMode(a *hermesv1alpha1.HermesAgent) string {
	mode := a.Spec.Probes.Mode
	if mode == "" || mode == "auto" {
		if a.Spec.APIServer.Enabled {
			return "http"
		}
		return "exec"
	}
	return mode
}

func applyProbeSettings(p *corev1.Probe, s hermesv1alpha1.ProbeSettings, defInitial, defPeriod, defFailure int32) {
	p.InitialDelaySeconds = pick(s.InitialDelaySeconds, defInitial)
	p.PeriodSeconds = pick(s.PeriodSeconds, defPeriod)
	p.FailureThreshold = pick(s.FailureThreshold, defFailure)
	if s.TimeoutSeconds > 0 {
		p.TimeoutSeconds = s.TimeoutSeconds
	}
	if s.SuccessThreshold > 0 {
		p.SuccessThreshold = s.SuccessThreshold
	}
}

func pick(v, def int32) int32 {
	if v > 0 {
		return v
	}
	return def
}

// dockerBackend reports whether the agent runs tools in a Docker-in-Docker
// sidecar (terminal.backend == docker, §11.2).
func dockerBackend(a *hermesv1alpha1.HermesAgent) bool {
	return a.Spec.Runtime.TerminalBackend == "docker"
}

// dockerHost is the DOCKER_HOST the agent uses to reach the dind daemon, and the
// address the daemon listens on. unix (default) shares an emptyDir socket; tcp
// uses intra-pod localhost.
func dockerHost(a *hermesv1alpha1.HermesAgent) string {
	if a.Spec.Runtime.Docker.SocketTransport == "tcp" {
		return "tcp://127.0.0.1:2375"
	}
	return "unix://" + DindSocketDir + "/docker.sock"
}

// dindImage resolves the dind sidecar image, swapping to the rootless variant
// when rootless is requested and the image is left at the default.
func dindImage(a *hermesv1alpha1.HermesAgent) string {
	img := a.Spec.Runtime.Docker.Image
	if img == "" {
		img = DefaultDindImage
	}
	if a.Spec.Runtime.Docker.Rootless && img == DefaultDindImage {
		img = DefaultDindRootlessImage
	}
	return img
}

// dindSocketVolume is the emptyDir carrying the daemon's unix socket, shared
// between the agent and dind containers. Returns nil for the tcp transport.
func dindSocketVolume(a *hermesv1alpha1.HermesAgent) *corev1.Volume {
	if a.Spec.Runtime.Docker.SocketTransport == "tcp" {
		return nil
	}
	return &corev1.Volume{
		Name:         VolDindSocket,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
}

// dindSocketMount mounts the shared socket emptyDir (unix transport only).
func dindSocketMount(a *hermesv1alpha1.HermesAgent) *corev1.VolumeMount {
	if a.Spec.Runtime.Docker.SocketTransport == "tcp" {
		return nil
	}
	return &corev1.VolumeMount{Name: VolDindSocket, MountPath: DindSocketDir}
}

// dindContainer builds the operator-managed Docker-in-Docker sidecar (§11.2).
// It maps the SAME shared-PVC subPaths at IDENTICAL paths so the agent's
// host-path bind mounts resolve on the daemon's filesystem, and backs
// /var/lib/docker with a dedicated subPath on the shared PVC so pulled images
// persist across restarts.
func dindContainer(a *hermesv1alpha1.HermesAgent) corev1.Container {
	mounts := []corev1.VolumeMount{
		{Name: VolShared, MountPath: HermesHome, SubPath: SubPathData},
		{Name: VolShared, MountPath: DotLocalPath, SubPath: SubPathLocal},
		{Name: VolShared, MountPath: LinuxbrewPath, SubPath: SubPathBrew},
		// The PVC-backed image/layer store the request is about.
		{Name: VolShared, MountPath: DindDockerDir, SubPath: SubPathDind},
	}
	if m := dindSocketMount(a); m != nil {
		mounts = append(mounts, *m)
	}

	// Pin the daemon to our socket/address; empty TLS dir disables the dind
	// entrypoint's auto-TLS so it listens plaintext intra-pod.
	args := []string{"--host=" + dockerHost(a)}
	// For the unix socket, own it by the agent's GID (dockerd accepts a numeric
	// group) so a NON-root agent — and the tool sandboxes that bind-mount it — can
	// reach the daemon. Without this the socket is root:docker(0660) and a
	// runAsRoot:false agent gets "permission denied". (tcp transport has no socket.)
	if a.Spec.Runtime.Docker.SocketTransport != "tcp" {
		args = append(args, "--group="+strconv.FormatInt(a.Spec.HermesGID, 10))
	}

	c := corev1.Container{
		Name:            ContainerDind,
		Image:           dindImage(a),
		ImagePullPolicy: a.Spec.ImagePullPolicy,
		Args:            args,
		Env: []corev1.EnvVar{
			{Name: "DOCKER_TLS_CERTDIR", Value: ""},
		},
		VolumeMounts: mounts,
		Resources:    a.Spec.Runtime.Docker.Resources,
	}
	// Standard DinD needs privileged; the rootless variant must not set it.
	if !a.Spec.Runtime.Docker.Rootless {
		c.SecurityContext = &corev1.SecurityContext{Privileged: ptr.To(true)}
	}
	return c
}

// honchoInUse reports whether the agent uses Honcho (baseURL or API key set).
func honchoInUse(a *hermesv1alpha1.HermesAgent) bool {
	return a.Spec.Honcho.BaseURL != "" || a.Spec.Honcho.APIKeySecretRef != nil
}

// honchoInstallEnabled reports whether honcho-ai should be installed (honcho in
// use and installPackage not disabled).
func honchoInstallEnabled(a *hermesv1alpha1.HermesAgent) bool {
	return honchoInUse(a) && (a.Spec.Honcho.InstallPackage == nil || *a.Spec.Honcho.InstallPackage)
}

// pipPackages is the de-duplicated set of pip packages installed into the shared
// PVC: spec.packages.pip, plus the deps for each enabled feature whose base image
// lacks them — honcho-ai (honcho), the messaging-platform SDKs (channels), and the
// remote terminal-backend SDKs (runtime). Per-feature installDeps toggles (default
// true) opt out, falling back to hermes' runtime lazy-install.
func pipPackages(a *hermesv1alpha1.HermesAgent) []string {
	var out []string
	seen := map[string]bool{}
	add := func(specs ...string) {
		for _, s := range specs {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}

	add(a.Spec.Packages.Pip...)
	if honchoInstallEnabled(a) {
		add(HonchoPackageSpec)
	}
	for _, ch := range a.Spec.Channels {
		if ch.InstallDeps == nil || *ch.InstallDeps {
			add(channelPipDeps[ch.Type]...)
		}
	}
	if a.Spec.Runtime.InstallDeps == nil || *a.Spec.Runtime.InstallDeps {
		add(backendPipDeps[a.Spec.Runtime.TerminalBackend]...)
	}
	return out
}

// pipInstallEnabled reports whether the pip-install init container is needed.
func pipInstallEnabled(a *hermesv1alpha1.HermesAgent) bool {
	return len(pipPackages(a)) > 0
}

// pipInstallInitContainer installs pipPackages into the shared PVC's dotlocal
// subPath (the python user-site) at boot, persisted across restarts (generalizes
// lana's hand-rolled honcho-ai install). The hermes container imports them via
// the user-site / PYTHONPATH.
func pipInstallInitContainer(a *hermesv1alpha1.HermesAgent) corev1.Container {
	img := a.Spec.Packages.PipImage
	if img == "" {
		img = DefaultPipImage
	}
	quoted := make([]string, 0, len(pipPackages(a)))
	for _, p := range pipPackages(a) {
		quoted = append(quoted, "'"+strings.ReplaceAll(p, "'", `'\''`)+"'")
	}
	script := fmt.Sprintf(`set -e
TARGET=%s
mkdir -p "$TARGET"
pip install --no-cache-dir --target "$TARGET" %s
`, PipSitePackages, strings.Join(quoted, " "))
	return corev1.Container{
		Name:            InitPipInstall,
		Image:           img,
		ImagePullPolicy: a.Spec.ImagePullPolicy,
		Command:         []string{"sh", "-c", script},
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolShared, MountPath: HermesHome, SubPath: SubPathData},
			{Name: VolShared, MountPath: DotLocalPath, SubPath: SubPathLocal},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  ptr.To(a.Spec.HermesUID),
			RunAsGroup: ptr.To(a.Spec.HermesGID),
		},
	}
}

// singularityInstallEnabled reports whether the operator should install Apptainer
// (terminalBackend=singularity and runtime.installDeps not disabled).
func singularityInstallEnabled(a *hermesv1alpha1.HermesAgent) bool {
	return a.Spec.Runtime.TerminalBackend == "singularity" &&
		(a.Spec.Runtime.InstallDeps == nil || *a.Spec.Runtime.InstallDeps)
}

// apptainerInitContainer installs Apptainer (for the singularity backend) onto
// the shared PVC at boot using Apptainer's official unprivileged, relocatable
// installer — idempotent, run as the hermes user — and symlinks the binary into
// ~/.local/bin (already on PATH). Running containers still needs the node to
// allow unprivileged user namespaces.
func apptainerInitContainer(a *hermesv1alpha1.HermesAgent) corev1.Container {
	img := a.Spec.Runtime.Singularity.InstallImage
	if img == "" {
		img = DefaultSingularityInstallImage
	}
	script := fmt.Sprintf(`set -e
PREFIX=%[1]s
if [ ! -x "$PREFIX/bin/apptainer" ]; then
  curl -fsSL %[2]s | bash -s - "$PREFIX"
fi
mkdir -p %[3]s
ln -sf "$PREFIX/bin/apptainer" %[3]s/apptainer
ln -sf "$PREFIX/bin/apptainer" %[3]s/singularity
`, ApptainerPrefix, ApptainerInstallScriptURL, DotLocalBin)
	return corev1.Container{
		Name:            InitApptainer,
		Image:           img,
		ImagePullPolicy: a.Spec.ImagePullPolicy,
		Command:         []string{"sh", "-c", script},
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolShared, MountPath: HermesHome, SubPath: SubPathData},
			{Name: VolShared, MountPath: DotLocalPath, SubPath: SubPathLocal},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  ptr.To(a.Spec.HermesUID),
			RunAsGroup: ptr.To(a.Spec.HermesGID),
		},
	}
}

// configInitCommand copies the operator-rendered config files onto the writable
// PVC at boot (avoids read-only subPath ConfigMap mounts and the chmod caveat,
// spec §4.3 / open question #2). Re-applied every start ⇒ operator-owned config.
func configInitCommand(a *hermesv1alpha1.HermesAgent) []string {
	uidgid := fmt.Sprintf("%d:%d", a.Spec.HermesUID, a.Spec.HermesGID)
	script := fmt.Sprintf(`set -e
for f in config.yaml SOUL.md; do
  if [ -f %[1]s/$f ]; then
    cp %[1]s/$f %[2]s/$f
    chown %[3]s %[2]s/$f 2>/dev/null || true
  fi
done
chmod 640 %[2]s/config.yaml 2>/dev/null || true
`, ConfigSrcDir, HermesHome, uidgid)
	if a.Spec.Kubeconfig.Enabled {
		// Write ~/.kube/config (HOME=/opt/data) owned by the hermes user, mode
		// 0600, with a writable 0700 .kube dir so kubectl's cache works.
		script += fmt.Sprintf(`mkdir -p %[2]s/.kube
cp %[1]s/%[4]s %[2]s/.kube/config
chown %[3]s %[2]s/.kube %[2]s/.kube/config 2>/dev/null || true
chmod 700 %[2]s/.kube 2>/dev/null || true
chmod 600 %[2]s/.kube/config 2>/dev/null || true
`, ConfigSrcDir, HermesHome, uidgid, KubeConfigKey)
	}
	return []string{"sh", "-c", script}
}
