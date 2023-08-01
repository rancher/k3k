package server

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	wildcardDNS = ".sslip.io"

	nginxSSLPassthroughAnnotation  = "nginx.ingress.kubernetes.io/ssl-passthrough"
	nginxBackendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"
	nginxSSLRedirectAnnotation     = "nginx.ingress.kubernetes.io/ssl-redirect"
)

func Ingress(ctx context.Context, cluster *v1alpha1.Cluster, client client.Client) (*networkingv1.Ingress, error) {
	addresses, err := util.Addresses(ctx, client)
	if err != nil {
		return nil, err
	}

	ingressRules := ingressRules(cluster, addresses)
	ingress := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-server-ingress",
			Namespace: util.ClusterNamespace(cluster),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &cluster.Spec.Expose.Ingress.IngressClassName,
			Rules:            ingressRules,
		},
	}

	configureIngressOptions(ingress, cluster.Spec.Expose.Ingress.IngressClassName)

	return ingress, nil
}

func ingressRules(cluster *v1alpha1.Cluster, addresses []string) []networkingv1.IngressRule {
	var ingressRules []networkingv1.IngressRule
	pathTypePrefix := networkingv1.PathTypePrefix

	for _, address := range addresses {
		rule := networkingv1.IngressRule{
			Host: cluster.Name + "." + address + wildcardDNS,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{
						{
							Path:     "/",
							PathType: &pathTypePrefix,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "k3k-server-service",
									Port: networkingv1.ServiceBackendPort{
										Number: 6443,
									},
								},
							},
						},
					},
				},
			},
		}
		ingressRules = append(ingressRules, rule)
	}

	return ingressRules
}

// configureIngressOptions will configure the ingress object by
// adding tls passthrough capabilities and TLS needed annotations
// it depends on the ingressclassname to configure each ingress
// TODO: add treafik support through ingresstcproutes
func configureIngressOptions(ingress *networkingv1.Ingress, ingressClassName string) {
	// initial support for nginx ingress via annotations
	if ingressClassName == "nginx" {
		ingress.Annotations = make(map[string]string)
		ingress.Annotations[nginxSSLPassthroughAnnotation] = "true"
		ingress.Annotations[nginxSSLRedirectAnnotation] = "true"
		ingress.Annotations[nginxBackendProtocolAnnotation] = "HTTPS"
	}
}
