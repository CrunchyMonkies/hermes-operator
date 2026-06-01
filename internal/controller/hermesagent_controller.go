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

package controller

import (
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
	"github.com/matthew/hermes-operator/internal/config"
	"github.com/matthew/hermes-operator/internal/resources"
)

const fieldOwner = client.FieldOwner("hermes-operator")

// HermesAgentReconciler reconciles a HermesAgent object.
type HermesAgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// ReloaderImage is the sidecar image injected into every agent pod.
	ReloaderImage string
}

// +kubebuilder:rbac:groups=hermes.nousresearch.io,resources=hermesagents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hermes.nousresearch.io,resources=hermesagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hermes.nousresearch.io,resources=hermesagents/finalizers,verbs=update
// +kubebuilder:rbac:groups=hermes.nousresearch.io,resources=hermesconfigpresets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps;persistentvolumeclaims;serviceaccounts;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile renders and applies the child objects for a HermesAgent and reports
// status. See specification §7.
func (r *HermesAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	agent := &hermesv1alpha1.HermesAgent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer-gated deletion (§13).
	if !agent.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, agent)
	}
	if !controllerutil.ContainsFinalizer(agent, resources.FinalizerName) {
		controllerutil.AddFinalizer(agent, resources.FinalizerName)
		if err := r.Update(ctx, agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 2. Resolve presetRef and deep-merge under the spec (CR wins).
	if agent.Spec.PresetRef != nil {
		preset := &hermesv1alpha1.HermesConfigPreset{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Spec.PresetRef.Name}, preset); err != nil {
			return ctrl.Result{}, fmt.Errorf("resolve presetRef %q: %w", agent.Spec.PresetRef.Name, err)
		}
		if err := mergePreset(&agent.Spec, &preset.Spec); err != nil {
			return ctrl.Result{}, fmt.Errorf("merge preset: %w", err)
		}
	}

	// 3. Render config.yaml + SOUL.md + skill payloads.
	configYAML, err := config.RenderConfigYAML(&agent.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("render config: %w", err)
	}
	soul := config.RenderSoul(&agent.Spec)
	skillPayloads := inlineSkillPayloads(agent)

	// 4. configHash (Secrets contribute resourceVersion only; never decoded).
	secretVersions, err := r.secretVersions(ctx, agent)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("collect secret versions: %w", err)
	}
	hash := config.ConfigHash(config.HashInputs{
		ConfigYAML:     configYAML,
		Soul:           soul,
		SkillPayloads:  skillPayloads,
		BrewPackages:   agent.Spec.Packages.Brew,
		SecretVersions: secretVersions,
	})

	// 5. Server-side apply all children (owner refs set).
	if err := r.applyChildren(ctx, agent, configYAML, soul, hash); err != nil {
		return ctrl.Result{}, err
	}

	// 6. Status.
	if err := r.updateStatus(ctx, agent, hash); err != nil {
		log.Error(err, "status update failed")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *HermesAgentReconciler) applyChildren(ctx context.Context, agent *hermesv1alpha1.HermesAgent, configYAML []byte, soul, hash string) error {
	// ServiceAccount + reloader RBAC.
	if sa := resources.ServiceAccount(agent); sa != nil {
		if err := r.apply(ctx, agent, sa); err != nil {
			return err
		}
		if role := resources.ReloaderRole(agent); role != nil {
			if err := r.apply(ctx, agent, role); err != nil {
				return err
			}
		}
		if rb := resources.ReloaderRoleBinding(agent); rb != nil {
			if err := r.apply(ctx, agent, rb); err != nil {
				return err
			}
		}
	}

	// PVC (skipped when existingClaim is set).
	if pvc := resources.SharedPVC(agent); pvc != nil {
		if err := r.apply(ctx, agent, pvc); err != nil {
			return err
		}
	}

	// ConfigMaps.
	if err := r.apply(ctx, agent, resources.ConfigMap(agent, configYAML, soul)); err != nil {
		return err
	}
	for _, cm := range resources.SkillConfigMaps(agent) {
		if err := r.apply(ctx, agent, cm); err != nil {
			return err
		}
	}

	// Service (only when a surface is exposed).
	if svc := resources.Service(agent); svc != nil {
		if err := r.apply(ctx, agent, svc); err != nil {
			return err
		}
	}

	// Ingresses.
	for _, ing := range resources.Ingresses(agent) {
		if err := r.apply(ctx, agent, ing); err != nil {
			return err
		}
	}

	// Deployment.
	dep, err := resources.Deployment(agent, hash, r.ReloaderImage)
	if err != nil {
		return fmt.Errorf("build deployment: %w", err)
	}
	return r.apply(ctx, agent, dep)
}

// apply server-side applies a child object with the operator as field owner and
// an owner reference back to the agent (for GC).
func (r *HermesAgentReconciler) apply(ctx context.Context, owner *hermesv1alpha1.HermesAgent, obj client.Object) error {
	if err := controllerutil.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return err
	}
	gvk, err := r.GroupVersionKindFor(obj)
	if err != nil {
		return err
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	obj.SetManagedFields(nil)
	return r.Patch(ctx, obj, client.Apply, fieldOwner, client.ForceOwnership)
}

// inlineSkillPayloads returns the rendered SKILL.md for inline custom skills so
// they participate in the configHash.
func inlineSkillPayloads(agent *hermesv1alpha1.HermesAgent) map[string]string {
	out := map[string]string{}
	for _, s := range agent.Spec.Skills.Custom {
		if s.Inline != "" {
			out[s.Name] = s.Inline
		}
	}
	return out
}

// secretVersions reads the resourceVersion of every referenced Secret. Values
// are never decoded — a rotation rolls the pod via the changed resourceVersion.
func (r *HermesAgentReconciler) secretVersions(ctx context.Context, agent *hermesv1alpha1.HermesAgent) (map[string]string, error) {
	names := map[string]struct{}{}
	add := func(n string) {
		if n != "" {
			names[n] = struct{}{}
		}
	}
	for _, ef := range agent.Spec.EnvFrom {
		if ef.SecretRef != nil {
			add(ef.SecretRef.Name)
		}
	}
	for _, e := range agent.Spec.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			add(e.ValueFrom.SecretKeyRef.Name)
		}
	}
	for _, ch := range agent.Spec.Channels {
		if ch.SecretRef != nil {
			add(ch.SecretRef.Name)
		}
	}
	if agent.Spec.APIServer.KeySecretRef != nil {
		add(agent.Spec.APIServer.KeySecretRef.Name)
	}
	if agent.Spec.AuthJSONBootstrapSecretRef != nil {
		add(agent.Spec.AuthJSONBootstrapSecretRef.Name)
	}
	for _, s := range agent.Spec.MCP.Servers {
		for _, se := range s.SecretEnv {
			add(se.SecretRef.Name)
		}
	}
	if bw := agent.Spec.Secrets.Bitwarden; bw != nil && bw.AccessTokenSecretRef != nil {
		add(bw.AccessTokenSecretRef.Name)
	}

	out := map[string]string{}
	for name := range names {
		s := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: name}, s); err != nil {
			if apierrors.IsNotFound(err) {
				// Missing secret: record a sentinel so the hash is stable until it appears.
				out[name] = "absent"
				continue
			}
			return nil, err
		}
		out[name] = s.ResourceVersion
	}
	return out, nil
}

func (r *HermesAgentReconciler) updateStatus(ctx context.Context, agent *hermesv1alpha1.HermesAgent, hash string) error {
	dep := &appsv1.Deployment{}
	ready := int32(0)
	if err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: resources.AgentName(agent)}, dep); err == nil {
		ready = dep.Status.ReadyReplicas
	}

	patch := client.MergeFrom(agent.DeepCopy())
	agent.Status.ObservedGeneration = agent.Generation
	agent.Status.ConfigHash = hash
	agent.Status.ReadyReplicas = ready
	agent.Status.Phase = derivePhase(agent, ready)
	if svc := resources.Service(agent); svc != nil {
		agent.Status.ServiceName = svc.Name
	}
	agent.Status.Endpoints = deriveEndpoints(agent)

	setCondition(agent, "VolumeBound", agent.Spec.Storage.ExistingClaim != "" || agent.Spec.Storage.Size != nil)
	setCondition(agent, "ConfigInSync", true)
	setReadyCondition(agent, ready)

	return r.Status().Patch(ctx, agent, patch)
}

func derivePhase(agent *hermesv1alpha1.HermesAgent, ready int32) string {
	if agent.Spec.Replicas != nil && *agent.Spec.Replicas == 0 {
		return "Paused"
	}
	if ready >= 1 {
		return "Running"
	}
	return "Provisioning"
}

func deriveEndpoints(agent *hermesv1alpha1.HermesAgent) hermesv1alpha1.EndpointsStatus {
	ep := hermesv1alpha1.EndpointsStatus{}
	svcName := resources.ServiceName(agent)
	if agent.Spec.APIServer.Enabled {
		port := agent.Spec.APIServer.Port
		if port == 0 {
			port = resources.APIPort
		}
		ep.API = fmt.Sprintf("%s:%d", svcName, port)
	}
	if agent.Spec.Dashboard.Enabled {
		port := agent.Spec.Dashboard.Port
		if port == 0 {
			port = resources.DashboardPort
		}
		ep.Dashboard = fmt.Sprintf("%s:%d", svcName, port)
	}
	return ep
}

func setCondition(agent *hermesv1alpha1.HermesAgent, condType string, ok bool) {
	status := metav1.ConditionFalse
	reason := "NotReady"
	if ok {
		status = metav1.ConditionTrue
		reason = "Reconciled"
	}
	meta := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		ObservedGeneration: agent.Generation,
		LastTransitionTime: metav1.Now(),
	}
	upsertCondition(&agent.Status.Conditions, meta)
}

func setReadyCondition(agent *hermesv1alpha1.HermesAgent, ready int32) {
	status := metav1.ConditionFalse
	reason := "GatewayProvisioning"
	if ready >= 1 {
		status = metav1.ConditionTrue
		reason = "GatewayHealthy"
	}
	upsertCondition(&agent.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		ObservedGeneration: agent.Generation,
		LastTransitionTime: metav1.Now(),
	})
}

func upsertCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			if (*conds)[i].Status == c.Status {
				c.LastTransitionTime = (*conds)[i].LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

// finalize runs the ordered teardown before removing the finalizer (§13).
func (r *HermesAgentReconciler) finalize(ctx context.Context, agent *hermesv1alpha1.HermesAgent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Scale the Deployment to 0 and wait for pod termination so gateway.lock
	//    (flock on the RWO volume) is released before we touch the PVC.
	dep := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: resources.AgentName(agent)}, dep)
	switch {
	case err == nil:
		if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
			zero := int32(0)
			dep.Spec.Replicas = &zero
			if err := r.Update(ctx, dep); err != nil {
				return ctrl.Result{}, err
			}
		}
		if dep.Status.Replicas != 0 {
			// Pod still terminating; requeue until the writer is gone.
			log.Info("waiting for agent pod to terminate before PVC teardown")
			return ctrl.Result{Requeue: true}, nil
		}
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, err
	}

	// 2. Honor reclaimPolicy: delete the PVC only when Delete and we own it.
	if agent.Spec.Storage.ReclaimPolicy == "Delete" && agent.Spec.Storage.ExistingClaim == "" {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: resources.PVCName(agent), Namespace: agent.Namespace},
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// 3. Owner-ref'd children are GC'd by Kubernetes; drop the finalizer.
	controllerutil.RemoveFinalizer(agent, resources.FinalizerName)
	if err := r.Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller to own its children and remap presets.
func (r *HermesAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hermesv1alpha1.HermesAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(
			&hermesv1alpha1.HermesConfigPreset{},
			handler.EnqueueRequestsFromMapFunc(r.agentsForPreset),
			builder.WithPredicates(),
		).
		Named("hermesagent").
		Complete(r)
}

// agentsForPreset maps a HermesConfigPreset change to the agents referencing it.
func (r *HermesAgentReconciler) agentsForPreset(ctx context.Context, obj client.Object) []ctrl.Request {
	list := &hermesv1alpha1.HermesAgentList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []ctrl.Request
	for i := range list.Items {
		a := &list.Items[i]
		if a.Spec.PresetRef != nil && a.Spec.PresetRef.Name == obj.GetName() {
			reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Name}})
		}
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Name < reqs[j].Name })
	return reqs
}
