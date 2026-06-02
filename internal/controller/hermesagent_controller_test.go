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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
	"github.com/matthew/hermes-operator/internal/resources"
)

var _ = Describe("HermesAgent Controller", func() {
	Context("When reconciling a valid resource", func() {
		const resourceName = "test-agent"
		ctx := context.Background()
		nn := types.NamespacedName{Name: resourceName, Namespace: "default"}

		AfterEach(func() {
			resource := &hermesv1alpha1.HermesAgent{}
			if err := k8sClient.Get(ctx, nn, resource); err == nil {
				// Remove finalizer so the object can actually delete in envtest.
				controllerutil.RemoveFinalizer(resource, resources.FinalizerName)
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}
		})

		It("creates the shared PVC, ConfigMap, and a Recreate Deployment with the reloader sidecar", func() {
			By("creating a valid HermesAgent")
			size := resource.MustParse("1Gi")
			agent := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:     "harbor.example/hermes-agent:v1",
					Replicas:  ptr.To(int32(1)),
					HermesUID: 10000,
					HermesGID: 10000,
					RunAsRoot: true,
					FSGroup:   10000,
					Storage:   hermesv1alpha1.StorageSpec{Size: &size, ReclaimPolicy: "Retain"},
					ServiceAccount: hermesv1alpha1.ServiceAccountSpec{
						Create:         true,
						AutomountToken: ptr.To(true),
					},
				},
			}
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())

			By("reconciling")
			r := &HermesAgentReconciler{
				Client:        k8sClient,
				Scheme:        k8sClient.Scheme(),
				ReloaderImage: "harbor.example/hermes-reloader:v1",
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("setting the finalizer")
			got := &hermesv1alpha1.HermesAgent{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(got, resources.FinalizerName)).To(BeTrue())

			By("creating one shared PVC of the requested size")
			pvc := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-data", Namespace: "default"}, pvc)).To(Succeed())
			Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteOnce))

			By("rendering config.yaml into a ConfigMap")
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-config", Namespace: "default"}, cm)).To(Succeed())
			Expect(cm.Data).To(HaveKey("config.yaml"))
			Expect(cm.Data["config.yaml"]).To(ContainSubstring("_config_version"))

			By("creating a Recreate Deployment with the hermes + reloader containers and shared mounts")
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, nn, dep)).To(Succeed())
			Expect(dep.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))
			Expect(*dep.Spec.Replicas).To(Equal(int32(1)))

			names := map[string]bool{}
			for _, c := range dep.Spec.Template.Spec.Containers {
				names[c.Name] = true
			}
			Expect(names).To(HaveKey(resources.ContainerHermes))
			Expect(names).To(HaveKey(resources.ContainerReloader))
			Expect(dep.Spec.Template.Annotations).To(HaveKey(resources.ConfigHashAnno))

			By("creating the operator-managed ServiceAccount + reloader RBAC")
			sa := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, nn, sa)).To(Succeed())
		})
	})

	Context("CEL validation", func() {
		ctx := context.Background()
		size := resource.MustParse("1Gi")

		It("rejects replicas > 1 (singleton)", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-replicas", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:    "img:v1",
					Replicas: ptr.To(int32(2)),
					Storage:  hermesv1alpha1.StorageSpec{Size: &size},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects storage.size together with existingClaim", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-storage", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size, ExistingClaim: "pre-made"},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects apiServer.enabled without keySecretRef", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-api", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:     "img:v1",
					Storage:   hermesv1alpha1.StorageSpec{Size: &size},
					APIServer: hermesv1alpha1.APIServerSpec{Enabled: true, Host: "0.0.0.0"},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects an mcp server with neither command nor url", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-mcp-none", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:          "img:v1",
					Storage:        hermesv1alpha1.StorageSpec{Size: &size},
					DefaultProfile: hermesv1alpha1.ProfileConfig{MCP: hermesv1alpha1.MCPSpec{Servers: []hermesv1alpha1.MCPServerSpec{{Name: "x"}}}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects an mcp server with both command and url", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-mcp-both", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					DefaultProfile: hermesv1alpha1.ProfileConfig{MCP: hermesv1alpha1.MCPSpec{Servers: []hermesv1alpha1.MCPServerSpec{
						{Name: "x", Command: "npx", URL: "https://mcp/x"},
					}}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects a reserved profile name", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-profile-reserved", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:    "img:v1",
					Storage:  hermesv1alpha1.StorageSpec{Size: &size},
					Profiles: []hermesv1alpha1.ProfileSpec{{Name: "default"}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("rejects a profile name that violates the pattern", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-profile-pattern", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:    "img:v1",
					Storage:  hermesv1alpha1.StorageSpec{Size: &size},
					Profiles: []hermesv1alpha1.ProfileSpec{{Name: "Staging!"}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("accepts valid named profiles", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "good-profile", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					Profiles: []hermesv1alpha1.ProfileSpec{
						{Name: "staging"},
						{Name: "support", ProfileConfig: hermesv1alpha1.ProfileConfig{Soul: "be helpful"}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, a)).To(Succeed())
		})

		It("rejects duplicate profile names", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "dup-profile", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					Profiles: []hermesv1alpha1.ProfileSpec{
						{Name: "staging"},
						{Name: "staging"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("accepts valid stdio and http mcp servers", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "good-mcp", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					DefaultProfile: hermesv1alpha1.ProfileConfig{MCP: hermesv1alpha1.MCPSpec{Servers: []hermesv1alpha1.MCPServerSpec{
						{Name: "local", Command: "npx", Args: []string{"-y", "srv"}},
						{Name: "remote", Transport: "sse", URL: "https://mcp/x"},
					}}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).To(Succeed())
		})

		It("rejects enabled bitwarden without accessTokenSecretRef", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-bitwarden", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					DefaultProfile: hermesv1alpha1.ProfileConfig{Secrets: hermesv1alpha1.SecretsSpec{Bitwarden: &hermesv1alpha1.BitwardenSpec{
						Enabled: ptr.To(true),
					}}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).NotTo(Succeed())
		})

		It("accepts enabled bitwarden with a custom server and token ref", func() {
			a := &hermesv1alpha1.HermesAgent{
				ObjectMeta: metav1.ObjectMeta{Name: "good-bitwarden", Namespace: "default"},
				Spec: hermesv1alpha1.HermesAgentSpec{
					Image:   "img:v1",
					Storage: hermesv1alpha1.StorageSpec{Size: &size},
					DefaultProfile: hermesv1alpha1.ProfileConfig{Secrets: hermesv1alpha1.SecretsSpec{Bitwarden: &hermesv1alpha1.BitwardenSpec{
						Enabled:              ptr.To(true),
						ServerURL:            "https://vault.example.com",
						AccessTokenSecretRef: &hermesv1alpha1.SecretKeyRef{Name: "bw-creds", Key: "token"},
					}}},
				},
			}
			Expect(k8sClient.Create(ctx, a)).To(Succeed())
		})
	})

	Context("When reconciling a missing resource", func() {
		It("does not error", func() {
			r := &HermesAgentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := r.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
