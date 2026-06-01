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
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// channelPortName returns the Service port name for a channel webhook.
func channelPortName(channelType string) string { return "wh-" + channelType }

// Service builds the single Service carrying every enabled HTTP surface port
// (spec §3.5). Returns nil when no surface is exposed. The Service type is taken
// from the apiServer surface when enabled, else the dashboard surface; LoadBalancer
// wins if any surface requests it. Annotations from all enabled surfaces merge.
func Service(a *hermesv1alpha1.HermesAgent) *corev1.Service {
	var ports []corev1.ServicePort

	if a.Spec.APIServer.Enabled {
		port := a.Spec.APIServer.Port
		if port == 0 {
			port = APIPort
		}
		ports = append(ports, corev1.ServicePort{
			Name:       "api",
			Port:       port,
			TargetPort: intstr.FromInt32(port),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	if a.Spec.Dashboard.Enabled {
		port := a.Spec.Dashboard.Port
		if port == 0 {
			port = DashboardPort
		}
		ports = append(ports, corev1.ServicePort{
			Name:       "dashboard",
			Port:       port,
			TargetPort: intstr.FromInt32(port),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	whPorts := resolvedWebhookPorts(a)
	for i, ch := range a.Spec.Channels {
		if p := whPorts[i]; p > 0 {
			ports = append(ports, corev1.ServicePort{
				Name:       channelPortName(ch.Type),
				Port:       p,
				TargetPort: intstr.FromInt32(p),
				Protocol:   corev1.ProtocolTCP,
			})
		}
	}

	if len(ports) == 0 {
		return nil
	}

	svcType, annotations := resolveServiceTypeAndAnnotations(a)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ServiceName(a),
			Namespace:   a.Namespace,
			Labels:      Labels(a),
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: SelectorLabels(a),
			Ports:    ports,
		},
	}
}

func resolveServiceTypeAndAnnotations(a *hermesv1alpha1.HermesAgent) (corev1.ServiceType, map[string]string) {
	annotations := map[string]string{}
	merge := func(in map[string]string) {
		maps.Copy(annotations, in)
	}

	// Collect enabled surface service blocks in priority order.
	var blocks []hermesv1alpha1.ServiceSpec
	if a.Spec.APIServer.Enabled {
		blocks = append(blocks, a.Spec.APIServer.Service)
	}
	if a.Spec.Dashboard.Enabled {
		blocks = append(blocks, a.Spec.Dashboard.Service)
	}

	svcType := corev1.ServiceTypeClusterIP
	if len(blocks) > 0 && blocks[0].Type != "" {
		svcType = blocks[0].Type
	}
	for _, b := range blocks {
		merge(b.Annotations)
		if b.Type == corev1.ServiceTypeLoadBalancer {
			svcType = corev1.ServiceTypeLoadBalancer
		}
	}
	if len(annotations) == 0 {
		annotations = nil
	}
	return svcType, annotations
}
