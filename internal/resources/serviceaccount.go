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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// ServiceAccount builds the per-agent SA when serviceAccount.create is true.
// Returns nil otherwise (an existing SA is used unmanaged). Annotations carry
// keyless cloud workload-identity (§3.7).
func ServiceAccount(a *hermesv1alpha1.HermesAgent) *corev1.ServiceAccount {
	if !a.Spec.ServiceAccount.Create {
		return nil
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ServiceAccountName(a),
			Namespace:   a.Namespace,
			Labels:      Labels(a),
			Annotations: copyMap(a.Spec.ServiceAccount.Annotations),
		},
	}
	if a.Spec.ServiceAccount.AutomountToken != nil {
		sa.AutomountServiceAccountToken = a.Spec.ServiceAccount.AutomountToken
	}
	return sa
}

// ReloaderRole is the scoped namespaced Role the reloader sidecar runs under:
// read its own CR + named ConfigMaps, write the owning HermesAgent /status. It
// is a strict subset of the operator ClusterRole, so the operator may bind it
// without an explicit escalate/bind grant (§10).
func ReloaderRole(a *hermesv1alpha1.HermesAgent) *rbacv1.Role {
	if !a.Spec.ServiceAccount.Create {
		return nil
	}
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReloaderRoleName(a),
			Namespace: a.Namespace,
			Labels:    Labels(a),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{Domain},
				Resources: []string{"hermesagents"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{Domain},
				Resources: []string{"hermesagents/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

// ReloaderRoleBinding binds the reloader Role to the agent SA.
func ReloaderRoleBinding(a *hermesv1alpha1.HermesAgent) *rbacv1.RoleBinding {
	if !a.Spec.ServiceAccount.Create {
		return nil
	}
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReloaderRoleBindingName(a),
			Namespace: a.Namespace,
			Labels:    Labels(a),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     ReloaderRoleName(a),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      ServiceAccountName(a),
				Namespace: a.Namespace,
			},
		},
	}
}
