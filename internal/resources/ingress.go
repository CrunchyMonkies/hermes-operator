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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hermesv1alpha1 "github.com/matthew/hermes-operator/api/v1alpha1"
)

// Ingresses builds one Ingress per enabled HTTP surface (spec §3.5). Annotations
// are carried verbatim so the object is controller-agnostic.
func Ingresses(a *hermesv1alpha1.HermesAgent) []*networkingv1.Ingress {
	var out []*networkingv1.Ingress

	if a.Spec.APIServer.Enabled && a.Spec.APIServer.Ingress.Enabled {
		host := surfaceHost(a, a.Spec.APIServer.Ingress.Host)
		out = append(out, buildIngress(a, "api", "api", host, a.Spec.APIServer.Ingress.Path, a.Spec.APIServer.Ingress))
	}
	if a.Spec.Dashboard.Enabled && a.Spec.Dashboard.Ingress.Enabled {
		host := surfaceHost(a, a.Spec.Dashboard.Ingress.Host)
		out = append(out, buildIngress(a, "dashboard", "dashboard", host, a.Spec.Dashboard.Ingress.Path, a.Spec.Dashboard.Ingress))
	}
	whPorts := resolvedWebhookPorts(a)
	for i, ch := range a.Spec.Channels {
		if !ch.Ingress.Enabled || whPorts[i] <= 0 {
			continue
		}
		host := channelEffectiveHost(a, ch)
		if host == "" {
			continue
		}
		// Webhook-capable channels default to /webhooks/<type>; an explicit
		// non-root ingress.path still wins.
		path := ch.Ingress.Path
		if webhookCapable(ch.Type) && (path == "" || path == "/") {
			path = webhookPath(ch.Type)
		}
		out = append(out, buildIngress(a, "wh-"+ch.Type, channelPortName(ch.Type), host, path, ch.Ingress))
	}
	return out
}

// surfaceHost resolves an ingress host: the surface's own host if set, else the
// agent-level spec.host default.
func surfaceHost(a *hermesv1alpha1.HermesAgent, host string) string {
	if host != "" {
		return host
	}
	return a.Spec.Host
}

func buildIngress(a *hermesv1alpha1.HermesAgent, surface, portName, host, rawPath string, in hermesv1alpha1.IngressSpec) *networkingv1.Ingress {
	pathType := networkingv1.PathType(in.PathType)
	if pathType == "" {
		pathType = networkingv1.PathTypePrefix
	}
	path := rawPath
	if path == "" {
		path = "/"
	}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        IngressName(a, surface),
			Namespace:   a.Namespace,
			Labels:      Labels(a),
			Annotations: copyMap(in.Annotations),
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: ServiceName(a),
											Port: networkingv1.ServiceBackendPort{Name: portName},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	if in.ClassName != "" {
		cn := in.ClassName
		ing.Spec.IngressClassName = &cn
	}
	for _, t := range in.TLS {
		ing.Spec.TLS = append(ing.Spec.TLS, networkingv1.IngressTLS{
			Hosts:      t.Hosts,
			SecretName: t.SecretName,
		})
	}
	return ing
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
