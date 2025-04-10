package server

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	httpsPort     = 443
	k3sServerPort = 6443
	etcdPort      = 2379
)

func IngressName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "ingress")
}

func Ingress(ctx context.Context, cluster *v1alpha1.Cluster) networkingv1.Ingress {
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
			Rules: ingressRules(cluster),
		},
	}

	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		ingressConfig := cluster.Spec.Expose.Ingress

		if ingressConfig.IngressClassName != "" {
			ingress.Spec.IngressClassName = ptr.To(ingressConfig.IngressClassName)
		}

		if ingressConfig.Annotations != nil {
			ingress.Annotations = ingressConfig.Annotations
		}
	}

	return ingress
}

func ingressRules(cluster *v1alpha1.Cluster) []networkingv1.IngressRule {
	var ingressRules []networkingv1.IngressRule

	if cluster.Spec.Expose == nil || cluster.Spec.Expose.Ingress == nil {
		return ingressRules
	}

	path := networkingv1.HTTPIngressPath{
		Path:     "/",
		PathType: ptr.To(networkingv1.PathTypePrefix),
		Backend: networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: ServiceName(cluster.Name),
				Port: networkingv1.ServiceBackendPort{
					Number: k3sServerPort,
				},
			},
		},
	}

	hosts := cluster.Spec.TLSSANs
	for _, host := range hosts {
		ingressRules = append(ingressRules, networkingv1.IngressRule{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{path},
				},
			},
		})
	}

	return ingressRules
}
