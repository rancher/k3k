package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestIngress(t *testing.T) {
	tests := map[string]struct {
		clusterOpts []func(*v1beta1.Cluster)
		ingressOpts []func(*networkingv1.Ingress)
	}{
		"no expose": {},
		"expose ingress without tlsSANs has no rules": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{},
					}
				},
			},
		},
		"expose ingress with only IP tlsSANs has no rules": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.TLSSANs = []string{"10.0.0.5", "::1"}
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{},
					}
				},
			},
		},
		"expose ingress with a wildcard tlsSAN": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.TLSSANs = []string{"*.example.com"}
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{},
					}
				},
			},
			ingressOpts: []func(*networkingv1.Ingress){
				func(i *networkingv1.Ingress) {
					i.Spec.Rules = []networkingv1.IngressRule{testIngressRule("*.example.com")}
				},
			},
		},
		"expose ingress skips IP tlsSANs": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.TLSSANs = []string{"10.0.0.5", "my-cluster.example.com"}
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{},
					}
				},
			},
			ingressOpts: []func(*networkingv1.Ingress){
				func(i *networkingv1.Ingress) {
					i.Spec.Rules = []networkingv1.IngressRule{testIngressRule("my-cluster.example.com")}
				},
			},
		},
		"expose ingress with multiple hosts": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.TLSSANs = []string{"my-cluster.example.com", "other.example.com"}
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{},
					}
				},
			},
			ingressOpts: []func(*networkingv1.Ingress){
				func(i *networkingv1.Ingress) {
					i.Spec.Rules = []networkingv1.IngressRule{
						testIngressRule("my-cluster.example.com"),
						testIngressRule("other.example.com"),
					}
				},
			},
		},
		"expose ingress with class and annotations": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.TLSSANs = []string{"my-cluster.example.com"}
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Ingress: &v1beta1.IngressConfig{
							IngressClassName: "nginx",
							Annotations: map[string]string{
								"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
							},
						},
					}
				},
			},
			ingressOpts: []func(*networkingv1.Ingress){
				func(i *networkingv1.Ingress) {
					i.Annotations = map[string]string{
						"nginx.ingress.kubernetes.io/ssl-passthrough": "true",
					}
					i.Spec.IngressClassName = ptr.To("nginx")
					i.Spec.Rules = []networkingv1.IngressRule{testIngressRule("my-cluster.example.com")}
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(tt.clusterOpts...)
			want := newTestIngress(cluster, tt.ingressOpts...)
			assert.Equal(t, *want, Ingress(cluster))
		})
	}
}

func newTestIngress(cluster *v1beta1.Cluster, opts ...func(*networkingv1.Ingress)) *networkingv1.Ingress {
	ingress := &networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Ingress",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k3k-test-cluster-ingress",
			Namespace: cluster.Namespace,
		},
	}
	for _, opt := range opts {
		opt(ingress)
	}

	return ingress
}

func testIngressRule(host string) networkingv1.IngressRule {
	return networkingv1.IngressRule{
		Host: host,
		IngressRuleValue: networkingv1.IngressRuleValue{
			HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{
					{
						Path:     "/",
						PathType: ptr.To(networkingv1.PathTypePrefix),
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: "k3k-test-cluster-service",
								Port: networkingv1.ServiceBackendPort{Number: 443},
							},
						},
					},
				},
			},
		},
	}
}
