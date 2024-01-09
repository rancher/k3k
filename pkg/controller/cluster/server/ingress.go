package server

import (
	"context"

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
	serverPort                     = 6443
	etcdPort                       = 2379
)

func (s *Server) Ingress(ctx context.Context, client client.Client) (*networkingv1.Ingress, error) {
	addresses, err := util.Addresses(ctx, client)
	if err != nil {
		return nil, err
	}
	ingressRules := s.ingressRules(addresses)
	ingress := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cluster.Name + "-server-ingress",
			Namespace: util.ClusterNamespace(s.cluster),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &s.cluster.Spec.Expose.Ingress.IngressClassName,
			Rules:            ingressRules,
		},
	}

	configureIngressOptions(ingress, s.cluster.Spec.Expose.Ingress.IngressClassName)

	return ingress, nil
}

func (s *Server) ingressRules(addresses []string) []networkingv1.IngressRule {
	var ingressRules []networkingv1.IngressRule
	pathTypePrefix := networkingv1.PathTypePrefix
	for _, address := range addresses {
		rule := networkingv1.IngressRule{
			Host: s.cluster.Name + "." + address + wildcardDNS,
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
										Number: serverPort,
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
