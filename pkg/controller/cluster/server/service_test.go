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

func TestNoExposeService(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.ClusterSpec{},
	}

	want := &corev1.Service{
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
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
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
			},
		},
	}

	service := Service(cluster)
	assert.Equal(t, want, service)
}

func TestExposeLoadBalancerService(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.ClusterSpec{
			Expose: &v1beta1.ExposeConfig{
				LoadBalancer: &v1beta1.LoadBalancerConfig{
					ServerPort: ptr.To[int32](9443),
				},
			},
		},
	}

	want := &corev1.Service{
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
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
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
			},
		},
	}

	service := Service(cluster)
	assert.Equal(t, want, service)
}

func TestExposeLoadBalancerServiceWithAnnotations(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.ClusterSpec{
			Expose: &v1beta1.ExposeConfig{
				Annotations: map[string]string{
					"example.com/testing": "test-annotation",
				},
				LoadBalancer: &v1beta1.LoadBalancerConfig{
					ServerPort: ptr.To[int32](9443),
				},
			},
		},
	}

	want := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k3k-test-cluster-service",
			Namespace: cluster.Namespace,
			Annotations: map[string]string{
				"example.com/testing": "test-annotation",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"cluster": "test-cluster",
				"role":    "server",
			},
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
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
			},
		},
	}

	service := Service(cluster)
	assert.Equal(t, want, service)
}

func TestExposeNodePortService(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.ClusterSpec{
			Expose: &v1beta1.ExposeConfig{
				NodePort: &v1beta1.NodePortConfig{
					ServerPort: ptr.To[int32](7443),
				},
			},
		},
	}

	want := &corev1.Service{
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
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name:     "k3s-etcd-port",
					Protocol: corev1.ProtocolTCP,
					Port:     int32(2379),
				},
			},
		},
	}

	service := Service(cluster)
	assert.Equal(t, want, service)
}
