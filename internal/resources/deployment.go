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
	reassertInvariants(a, &final, configHash, reloaderImage)

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

	pod := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      Labels(a),
			Annotations: map[string]string{ConfigHashAnno: configHash},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName:            ServiceAccountName(a),
			InitContainers:                []corev1.Container{initContainer},
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
	var skillNames []string
	for _, s := range a.Spec.Skills.Custom {
		skillNames = append(skillNames, s.Name)
	}
	env := []corev1.EnvVar{
		{Name: "RELOADER_AGENT_NAME", ValueFrom: fieldRef("metadata.labels['" + InstanceLabel + "']")},
		{Name: "RELOADER_AGENT_NAMESPACE", ValueFrom: fieldRef("metadata.namespace")},
		{Name: "RELOADER_HOMEBREW_PREFIX", Value: HomebrewPrefix(a)},
		{Name: "RELOADER_HOMEBREW_DIST", Value: "/opt/homebrew-dist"},
		{Name: "RELOADER_BREW_PACKAGES", Value: strings.Join(a.Spec.Packages.Brew, " ")},
		{Name: "RELOADER_APT_PACKAGES", Value: strings.Join(a.Spec.Packages.Apt, " ")},
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

// hermesSecurityContext: start as root so the entrypoint can usermod/gosu/apt,
// unless runAsRoot is false (then run directly as the hermes user; §4.2).
func hermesSecurityContext(a *hermesv1alpha1.HermesAgent) *corev1.SecurityContext {
	if a.Spec.RunAsRoot {
		return &corev1.SecurityContext{
			RunAsUser:    ptr.To(int64(0)),
			RunAsNonRoot: ptr.To(false),
		}
	}
	return &corev1.SecurityContext{
		RunAsUser:    ptr.To(a.Spec.HermesUID),
		RunAsNonRoot: ptr.To(true),
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
func reassertInvariants(a *hermesv1alpha1.HermesAgent, pod *corev1.PodTemplateSpec, configHash, reloaderImage string) {
	// config-hash annotation.
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[ConfigHashAnno] = configHash

	// Selector labels must always be present on the pod.
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	for k, v := range Labels(a) {
		pod.Labels[k] = v
	}

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

// AptPackagesEnvValue is exported for tests/inspection of the apt env wiring.
func AptPackagesEnvValue(a *hermesv1alpha1.HermesAgent) string {
	return strings.Join(a.Spec.Packages.Apt, " ")
}

// UIDString is a small helper for status/debug rendering.
func UIDString(a *hermesv1alpha1.HermesAgent) string { return strconv.FormatInt(a.Spec.HermesUID, 10) }
