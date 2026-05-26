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

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// channelWebhookPortEnv maps the platforms whose webhook port is a verified env
// var (spec §3.3). Platforms not listed simply expose a Service port.
var channelWebhookPortEnv = map[string]string{
	"telegram": "TELEGRAM_WEBHOOK_PORT",
	"teams":    "TEAMS_PORT",
}

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

	if len(a.Spec.Packages.Apt) > 0 {
		env = append(env, corev1.EnvVar{Name: "HERMES_APT_PACKAGES", Value: strings.Join(a.Spec.Packages.Apt, " ")})
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

	for _, ch := range a.Spec.Channels {
		if ch.WebhookPort > 0 {
			if envName, ok := channelWebhookPortEnv[ch.Type]; ok {
				env = append(env, corev1.EnvVar{Name: envName, Value: strconv.Itoa(int(ch.WebhookPort))})
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
	return []string{"sh", "-c", script}
}
