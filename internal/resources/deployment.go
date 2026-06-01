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
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/utils/ptr"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// DefaultReloaderImage is used when the operator is not configured with an
// explicit reloader image.
const DefaultReloaderImage = "harbor.bne1.ouchi.com.au/applications/hermes-reloader:latest"

// Deployment renders the agent Deployment: a base pod template (hermes
// container + reloader sidecar + init copy), strategic-merged with the user's
// podTemplate overlay, after which operator invariants are re-asserted (§3.6).
func Deployment(a *hermesv1alpha1.HermesAgent, configHash, reloaderImage string) (*appsv1.Deployment, error) {
	if reloaderImage == "" {
		reloaderImage = DefaultReloaderImage
	}

	base := basePodTemplate(a, configHash, reloaderImage)

	final := base
	if a.Spec.PodTemplate != nil {
		merged, err := applyPodTemplateOverlay(base, a.Spec.PodTemplate)
		if err != nil {
			return nil, fmt.Errorf("apply podTemplate overlay: %w", err)
		}
		final = merged
	}

	// Re-assert invariants the operator always wins on (after the overlay).
	reassertInvariants(a, &final, configHash)

	replicas := int32(1)
	if a.Spec.Replicas != nil {
		replicas = *a.Spec.Replicas
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AgentName(a),
			Namespace: a.Namespace,
			Labels:    Labels(a),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			// Recreate: the old pod must fully terminate and release gateway.lock
			// before the new pod starts — single-writer guarantee (§1.1).
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: SelectorLabels(a)},
			Template: final,
		},
	}, nil
}

func basePodTemplate(a *hermesv1alpha1.HermesAgent, configHash, reloaderImage string) corev1.PodTemplateSpec {
	live, ready := buildProbes(a)

	hermes := corev1.Container{
		Name:            ContainerHermes,
		Image:           a.Spec.Image,
		ImagePullPolicy: a.Spec.ImagePullPolicy,
		Args:            []string{"gateway", "run"},
		Env:             hermesEnv(a),
		EnvFrom:         hermesEnvFrom(a),
		VolumeMounts:    sharedVolumeMounts(),
		Resources:       a.Spec.Resources,
		LivenessProbe:   live,
		ReadinessProbe:  ready,
		SecurityContext: hermesSecurityContext(a),
	}

	reloader := corev1.Container{
		Name:            ContainerReloader,
		Image:           reloaderImage,
		ImagePullPolicy: a.Spec.ImagePullPolicy,
		Env:             reloaderEnv(a),
		VolumeMounts:    append(sharedVolumeMounts(), skillSourceMounts(a)...),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: ptr.To(true),
			RunAsUser:    ptr.To(a.Spec.HermesUID),
		},
	}

	initContainer := corev1.Container{
		Name:    InitConfig,
		Image:   a.Spec.Image,
		Command: configInitCommand(a),
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolShared, MountPath: HermesHome, SubPath: SubPathData},
			{Name: VolConfig, MountPath: ConfigSrcDir, ReadOnly: true},
		},
		SecurityContext: initSecurityContext(a),
	}

	initContainers := []corev1.Container{initContainer}
	if pipInstallEnabled(a) {
		initContainers = append(initContainers, pipInstallInitContainer(a))
	}
	if singularityInstallEnabled(a) {
		initContainers = append(initContainers, apptainerInitContainer(a))
	}

	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      Labels(a),
			Annotations: map[string]string{ConfigHashAnno: configHash},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:            ServiceAccountName(a),
			InitContainers:                initContainers,
			Containers:                    []corev1.Container{hermes, reloader},
			Volumes:                       baseVolumes(a),
			ImagePullSecrets:              a.Spec.ImagePullSecrets,
			SecurityContext:               podSecurityContext(a),
			TerminationGracePeriodSeconds: ptr.To(int64(60)),
		},
	}
	if a.Spec.ServiceAccount.AutomountToken != nil {
		pod.Spec.AutomountServiceAccountToken = a.Spec.ServiceAccount.AutomountToken
	}
	return pod
}

func reloaderEnv(a *hermesv1alpha1.HermesAgent) []corev1.EnvVar {
	skillNames := make([]string, 0, len(a.Spec.Skills.Custom))
	for _, s := range a.Spec.Skills.Custom {
		skillNames = append(skillNames, s.Name)
	}
	env := []corev1.EnvVar{
		{Name: "RELOADER_AGENT_NAME", ValueFrom: fieldRef("metadata.labels['" + InstanceLabel + "']")},
		{Name: "RELOADER_AGENT_NAMESPACE", ValueFrom: fieldRef("metadata.namespace")},
		{Name: "RELOADER_HOMEBREW_PREFIX", Value: HomebrewPrefix(a)},
		{Name: "RELOADER_HOMEBREW_DIST", Value: "/opt/homebrew-dist"},
		{Name: "RELOADER_BREW_PACKAGES", Value: strings.Join(a.Spec.Packages.Brew, " ")},
		{Name: "RELOADER_CUSTOM_SKILLS", Value: strings.Join(skillNames, ",")},
		{Name: "RELOADER_SKILL_SRC_DIR", Value: SkillSrcDir},
		{Name: "HERMES_HOME", Value: HermesHome},
	}
	return env
}

func fieldRef(path string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{
		FieldRef: &corev1.ObjectFieldSelector{FieldPath: path},
	}
}

func podSecurityContext(a *hermesv1alpha1.HermesAgent) *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{FSGroup: ptr.To(a.Spec.FSGroup)}
}

// hermesSecurityContext returns the main agent container's security context.
// The upstream image boots via s6-overlay (/init = PID 1), which MUST start as
// root to run its cont-init bootstrap (UID remap to HERMES_UID, volume chown,
// config seed, skills sync) and then drop the workload to the configured UID
// via `s6-setuidgid hermes` (main-wrapper.sh). The hermes PROCESS therefore
// always runs unprivileged (HermesUID, passed via the HERMES_UID env) even
// though the container's PID 1 is root — so we always start this container as
// root. spec.runAsRoot no longer affects this container (under s6 the process
// cannot stay root through the entrypoint); it still governs the operator's own
// init containers (see initSecurityContext).
func hermesSecurityContext(_ *hermesv1alpha1.HermesAgent) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsUser:    ptr.To(int64(0)),
		RunAsNonRoot: ptr.To(false),
	}
}

func initSecurityContext(a *hermesv1alpha1.HermesAgent) *corev1.SecurityContext {
	if a.Spec.RunAsRoot {
		return &corev1.SecurityContext{RunAsUser: ptr.To(int64(0))}
	}
	return &corev1.SecurityContext{RunAsUser: ptr.To(a.Spec.HermesUID), RunAsNonRoot: ptr.To(true)}
}

// applyPodTemplateOverlay strategic-merges the user overlay over the base pod
// template, respecting patch-merge keys (containers/volumes by name, etc.) so
// users extend rather than clobber the operator's template.
func applyPodTemplateOverlay(base corev1.PodTemplateSpec, overlay *corev1.PodTemplateSpec) (corev1.PodTemplateSpec, error) {
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return base, err
	}
	overlayJSON, err := json.Marshal(overlay)
	if err != nil {
		return base, err
	}
	mergedJSON, err := strategicpatch.StrategicMergePatch(baseJSON, overlayJSON, corev1.PodTemplateSpec{})
	if err != nil {
		return base, err
	}
	var merged corev1.PodTemplateSpec
	if err := json.Unmarshal(mergedJSON, &merged); err != nil {
		return base, err
	}
	return merged, nil
}

// reassertInvariants re-applies the operator-owned guarantees after the overlay
// (§3.6): shared-PVC volume + its subPath mounts on the hermes container, the
// config-hash annotation, root-start, and the typed resources/probes.
func reassertInvariants(a *hermesv1alpha1.HermesAgent, pod *corev1.PodTemplateSpec, configHash string) {
	// config-hash annotation.
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[ConfigHashAnno] = configHash

	// Selector labels must always be present on the pod.
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	maps.Copy(pod.Labels, Labels(a))

	// Shared PVC + config + shm volumes (upsert by name; the operator owns these).
	for _, v := range baseVolumes(a) {
		upsertVolume(&pod.Spec.Volumes, v)
	}

	// hermes container: protected mounts + security context + resources/probes.
	hermes := findContainer(pod.Spec.Containers, ContainerHermes)
	if hermes != nil {
		for _, m := range sharedVolumeMounts() {
			upsertMount(&hermes.VolumeMounts, m)
		}
		hermes.SecurityContext = mergeSecurityContext(hermes.SecurityContext, hermesSecurityContext(a))
		hermes.Resources = a.Spec.Resources
		live, ready := buildProbes(a)
		hermes.LivenessProbe = live
		hermes.ReadinessProbe = ready
	}

	// reloader sidecar must keep the shared mounts (the overlay may not drop them).
	reloader := findContainer(pod.Spec.Containers, ContainerReloader)
	if reloader != nil {
		for _, m := range sharedVolumeMounts() {
			upsertMount(&reloader.VolumeMounts, m)
		}
	}

	// Pod-level security: fsGroup is authoritative.
	if pod.Spec.SecurityContext == nil {
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{}
	}
	pod.Spec.SecurityContext.FSGroup = ptr.To(a.Spec.FSGroup)

	// ServiceAccount is authoritative over any overlay value (§3.7).
	pod.Spec.ServiceAccountName = ServiceAccountName(a)

	// DinD sidecar (docker backend) is operator-owned and re-asserted after the
	// overlay (§11.2): the sidecar, its shared socket volume, and the agent's
	// DOCKER_HOST + socket mount.
	if dockerBackend(a) {
		applyDockerBackend(a, pod)
	}
}

// applyDockerBackend injects (and re-asserts) the operator-managed Docker-in-
// Docker sidecar and wires the agent container to it (§11.2). Idempotent —
// upserts by name so it is safe before or after the podTemplate overlay.
func applyDockerBackend(a *hermesv1alpha1.HermesAgent, pod *corev1.PodTemplateSpec) {
	if v := dindSocketVolume(a); v != nil {
		upsertVolume(&pod.Spec.Volumes, *v)
	}
	// Append/replace the dind container; this may reslice Containers, so re-find
	// the agent container afterwards before mutating it.
	upsertContainer(&pod.Spec.Containers, dindContainer(a))
	if hermes := findContainer(pod.Spec.Containers, ContainerHermes); hermes != nil {
		upsertEnv(&hermes.Env, corev1.EnvVar{Name: "DOCKER_HOST", Value: dockerHost(a)})
		if m := dindSocketMount(a); m != nil {
			upsertMount(&hermes.VolumeMounts, *m)
		}
	}
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func upsertVolume(vols *[]corev1.Volume, v corev1.Volume) {
	for i := range *vols {
		if (*vols)[i].Name == v.Name {
			(*vols)[i] = v
			return
		}
	}
	*vols = append(*vols, v)
}

func upsertMount(mounts *[]corev1.VolumeMount, m corev1.VolumeMount) {
	for i := range *mounts {
		if (*mounts)[i].MountPath == m.MountPath {
			(*mounts)[i] = m
			return
		}
	}
	*mounts = append(*mounts, m)
}

func upsertContainer(containers *[]corev1.Container, c corev1.Container) {
	for i := range *containers {
		if (*containers)[i].Name == c.Name {
			(*containers)[i] = c
			return
		}
	}
	*containers = append(*containers, c)
}

func upsertEnv(env *[]corev1.EnvVar, e corev1.EnvVar) {
	for i := range *env {
		if (*env)[i].Name == e.Name {
			(*env)[i] = e
			return
		}
	}
	*env = append(*env, e)
}

// mergeSecurityContext overlays the operator's required fields onto any
// user-provided container security context (operator wins on the run-as fields).
func mergeSecurityContext(user, required *corev1.SecurityContext) *corev1.SecurityContext {
	if user == nil {
		return required
	}
	user.RunAsUser = required.RunAsUser
	user.RunAsNonRoot = required.RunAsNonRoot
	return user
}

// UIDString is a small helper for status/debug rendering.
func UIDString(a *hermesv1alpha1.HermesAgent) string { return strconv.FormatInt(a.Spec.HermesUID, 10) }
