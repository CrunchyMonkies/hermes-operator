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

// Package resources renders the Kubernetes child objects for a HermesAgent.
// Builders are pure functions; the controller sets owner references and applies
// them. Verified upstream paths/ports (tag v2026.5.16) live as constants here.
package resources

import (
	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

const (
	// Group-namespaced annotation/label/finalizer keys.
	Domain         = "hermes.nousresearch.io"
	ConfigHashAnno = Domain + "/config-hash"
	FinalizerName  = Domain + "/cleanup"
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "hermes-operator"
	InstanceLabel  = "app.kubernetes.io/instance"
	NameLabel      = "app.kubernetes.io/name"
	ComponentLabel = "app.kubernetes.io/component"
	ComponentValue = "hermes-agent"

	// Container names (patch-merge keys for the podTemplate overlay).
	ContainerHermes   = "hermes"
	ContainerReloader = "reloader"
	ContainerDind     = "dind"
	InitConfig        = "config-init"
	InitPipInstall    = "pip-install"

	// Volume names.
	VolShared     = "shared-data"
	VolConfig     = "config"
	VolShm        = "dev-shm"
	VolDindSocket = "dind-socket"

	// Verified upstream mount points and subPaths (spec §4.1).
	HermesHome    = "/opt/data"
	DotLocalPath  = "/opt/data/.local"
	LinuxbrewPath = "/home/linuxbrew/.linuxbrew"
	SubPathData   = "data"
	SubPathLocal  = "dotlocal"
	SubPathBrew   = "linuxbrew"

	// DinD sidecar (docker terminal backend, §11.2). The daemon's image/layer
	// store at /var/lib/docker is a dedicated subPath on the SAME shared PVC
	// (keeps the single-PVC invariant, §4) so pulled images persist across pod
	// restarts. DindSocketDir is the emptyDir shared with the agent container
	// for the unix-socket transport.
	DindDockerDir = "/var/lib/docker"
	SubPathDind   = "dind"
	DindSocketDir = "/var/run/dind"

	// Where the config ConfigMap is mounted for the init container to copy from.
	ConfigSrcDir = "/etc/hermes-config"
	// Where custom-skill ConfigMaps are mounted for the reloader to copy from.
	SkillSrcDir = "/etc/hermes-skills"

	// Verified upstream ports (spec §1).
	APIPort       int32 = 8642
	DashboardPort int32 = 9119

	// Default Homebrew prefix on the shared PVC.
	DefaultHomebrewPrefix = "/home/linuxbrew/.linuxbrew"

	// pip installs (spec.packages.pip, plus honcho-ai when honcho is in use) go
	// into the python user-site on the shared PVC's dotlocal subPath via the
	// pip-install init container. Path is pinned to the hermes python (3.13 at tag
	// v2026.5.16; re-verify when bumping the pin).
	DefaultPipImage   = "python:3.13-slim"
	PipSitePackages   = DotLocalPath + "/lib/python3.13/site-packages"
	HonchoPackageSpec = "honcho-ai>=2.0.1,<3"

	// Default DinD images (mirror the CRD defaults / spec §11.2). The rootless
	// variant is selected when runtime.docker.rootless is set and the image is
	// left at the default.
	DefaultDindImage         = "docker:27-dind"
	DefaultDindRootlessImage = "docker:27-dind-rootless"
)

// AgentName returns the HermesAgent object name (used as the base for children).
func AgentName(a *hermesv1alpha1.HermesAgent) string { return a.Name }

// ConfigMapName is the name of the rendered config.yaml/SOUL.md ConfigMap.
func ConfigMapName(a *hermesv1alpha1.HermesAgent) string { return a.Name + "-config" }

// SkillConfigMapName is the name the operator gives a materialized inline skill.
func SkillConfigMapName(a *hermesv1alpha1.HermesAgent, skill string) string {
	return a.Name + "-skill-" + skill
}

// PVCName returns the shared claim name (or the existingClaim when set).
func PVCName(a *hermesv1alpha1.HermesAgent) string {
	if a.Spec.Storage.ExistingClaim != "" {
		return a.Spec.Storage.ExistingClaim
	}
	return a.Name + "-data"
}

// ServiceName is the single Service carrying all enabled ports.
func ServiceName(a *hermesv1alpha1.HermesAgent) string { return a.Name }

// ServiceAccountName resolves the SA name: explicit, else the agent name.
func ServiceAccountName(a *hermesv1alpha1.HermesAgent) string {
	if a.Spec.ServiceAccount.Name != "" {
		return a.Spec.ServiceAccount.Name
	}
	return a.Name
}

// ReloaderRoleName / ReloaderRoleBindingName for the scoped reloader RBAC.
func ReloaderRoleName(a *hermesv1alpha1.HermesAgent) string        { return a.Name + "-reloader" }
func ReloaderRoleBindingName(a *hermesv1alpha1.HermesAgent) string { return a.Name + "-reloader" }

// IngressName for a given surface ("api", "dashboard", or "wh-<channel>").
func IngressName(a *hermesv1alpha1.HermesAgent, surface string) string {
	return a.Name + "-" + surface
}

// Labels returns the standard label set applied to every child object.
func Labels(a *hermesv1alpha1.HermesAgent) map[string]string {
	return map[string]string{
		NameLabel:      ComponentValue,
		InstanceLabel:  a.Name,
		ComponentLabel: ComponentValue,
		ManagedByLabel: ManagedByValue,
	}
}

// SelectorLabels returns the immutable pod-selector subset.
func SelectorLabels(a *hermesv1alpha1.HermesAgent) map[string]string {
	return map[string]string{
		InstanceLabel: a.Name,
		NameLabel:     ComponentValue,
	}
}

// HomebrewPrefix resolves the configured brew prefix (default if unset).
func HomebrewPrefix(a *hermesv1alpha1.HermesAgent) string {
	if a.Spec.Packages.HomebrewPrefix != "" {
		return a.Spec.Packages.HomebrewPrefix
	}
	return DefaultHomebrewPrefix
}
