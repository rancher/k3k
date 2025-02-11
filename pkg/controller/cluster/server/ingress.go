package server

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	wildcardDNS = ".sslip.io"

	nginxSSLPassthroughAnnotation  = "nginx.ingress.kubernetes.io/ssl-passthrough"
	nginxBackendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"
	nginxSSLRedirectAnnotation     = "nginx.ingress.kubernetes.io/ssl-redirect"

	servicePort = 443
	serverPort  = 6443
	etcdPort    = 2379
)

func IngressName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "ingress")
}

func Ingress(ctx context.Context, addresses []string, cluster *v1alpha1.Cluster) networkingv1.Ingress {
	ingressRules := ingressRules(addresses, cluster)

	ingress := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		},
		Spec: networkingv1.IngressSpec{
			Rules: ingressRules,
		},
	}

	var ingressClassName string
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		ingressClassName = cluster.Spec.Expose.Ingress.IngressClassName
	}

	ingress.Spec.IngressClassName = &ingressClassName
	configureIngressOptions(&ingress, ingressClassName)

	return ingress
}

func ingressRules(addresses []string, cluster *v1alpha1.Cluster) []networkingv1.IngressRule {
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
									Name: ServiceName(cluster.Name),
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
