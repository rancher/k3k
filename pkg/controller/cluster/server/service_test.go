package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestService(t *testing.T) {
	tests := map[string]struct {
		clusterOpts []func(*v1beta1.Cluster)
		serviceOpts []func(*corev1.Service)
	}{
		"no expose": {
			serviceOpts: []func(*corev1.Service){
				func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					s.Spec.Ports = []corev1.ServicePort{
						{
							Name:       "k3s-server-port",
							Protocol:   corev1.ProtocolTCP,
							Port:       int32(443),
							TargetPort: intstr.FromInt(6443),
						},
						{
							Name:     "k3s-etcd-port",
							Protocol: corev1.ProtocolTCP,
							Port:     int32(2379),
						},
					}
				},
			},
		},
		"expose load balancer": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec = v1beta1.ClusterSpec{
						Expose: &v1beta1.ExposeConfig{
							LoadBalancer: &v1beta1.LoadBalancerConfig{
								LoadBalancerClass: ptr.To("example.com/internal"),
								ServerPort:        ptr.To[int32](9443),
							},
						},
					}
				},
			},
			serviceOpts: []func(*corev1.Service){
				func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeLoadBalancer
					s.Spec.LoadBalancerClass = ptr.To("example.com/internal")
					s.Spec.Ports = []corev1.ServicePort{
						{
							Name:       "k3s-server-port",
							Protocol:   corev1.ProtocolTCP,
							Port:       int32(9443),
							TargetPort: intstr.FromInt(6443),
						},
						{
							Name:     "k3s-etcd-port",
							Protocol: corev1.ProtocolTCP,
							Port:     int32(2379),
						},
					}
				},
			},
		},
		"expose load balancer with annotations": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.Expose = &v1beta1.ExposeConfig{
						Annotations: map[string]string{
							"example.com/testing": "test-annotation",
						},
						LoadBalancer: &v1beta1.LoadBalancerConfig{
							ServerPort: ptr.To[int32](9443),
						},
					}
				},
			},
			serviceOpts: []func(*corev1.Service){
				func(s *corev1.Service) {
					s.Annotations = map[string]string{
						"example.com/testing": "test-annotation",
					}
					s.Spec.Type = corev1.ServiceTypeLoadBalancer
					s.Spec.Ports = []corev1.ServicePort{
						{
							Name:       "k3s-server-port",
							Protocol:   corev1.ProtocolTCP,
							Port:       int32(9443),
							TargetPort: intstr.FromInt(6443),
						},
						{
							Name:     "k3s-etcd-port",
							Protocol: corev1.ProtocolTCP,
							Port:     int32(2379),
						},
					}
				},
			},
		},
		"expose node port": {
			clusterOpts: []func(*v1beta1.Cluster){
				func(c *v1beta1.Cluster) {
					c.Spec.Expose = &v1beta1.ExposeConfig{
						NodePort: &v1beta1.NodePortConfig{
							ServerPort: ptr.To[int32](7443),
						},
					}
				},
			},
			serviceOpts: []func(*corev1.Service){
				func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeNodePort
					s.Spec.Ports = []corev1.ServicePort{
						{
							Name:     "k3s-etcd-port",
							Protocol: corev1.ProtocolTCP,
							Port:     int32(2379),
						},
					}
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(tt.clusterOpts...)
			want := newTestService(cluster, tt.serviceOpts...)
			assert.Equal(t, want, Service(cluster))
		})
	}
}

func newTestService(cluster *v1beta1.Cluster, opts ...func(*corev1.Service)) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k3k-test-cluster-service",
			Namespace: cluster.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"cluster": "test-cluster",
				"role":    "server",
			},
		},
	}
	for _, opt := range opts {
		opt(svc)
	}

	return svc
}

func newTestCluster(opts ...func(*v1beta1.Cluster)) *v1beta1.Cluster {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.ClusterSpec{},
	}
	for _, opt := range opts {
		opt(cluster)
	}

	return cluster
}
