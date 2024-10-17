package server

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) Service(cluster *v1alpha1.Cluster) *v1.Service {
	serviceType := v1.ServiceTypeClusterIP
	if cluster.Spec.Expose != nil {
		if cluster.Spec.Expose.NodePort != nil {
			if cluster.Spec.Expose.NodePort.Enabled {
				serviceType = v1.ServiceTypeNodePort
			}
		}
	}

	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName(s.cluster.Name),
			Namespace: cluster.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type: serviceType,
			Selector: map[string]string{
				"cluster": cluster.Name,
				"role":    "server",
			},
			Ports: []v1.ServicePort{
				{
					Name:     "k3s-server-port",
					Protocol: v1.ProtocolTCP,
					Port:     serverPort,
				},
				{
					Name:     "k3s-etcd-port",
					Protocol: v1.ProtocolTCP,
					Port:     etcdPort,
				},
			},
		},
	}
}

func (s *Server) StatefulServerService() *v1.Service {
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      headlessServiceName(s.cluster.Name),
			Namespace: s.cluster.Namespace,
		},
		Spec: v1.ServiceSpec{
			Type:      v1.ServiceTypeClusterIP,
			ClusterIP: v1.ClusterIPNone,
			Selector: map[string]string{
				"cluster": s.cluster.Name,
				"role":    "server",
			},
			Ports: []v1.ServicePort{
				{
					Name:     "k3s-server-port",
					Protocol: v1.ProtocolTCP,
					Port:     serverPort,
				},
				{
					Name:     "k3s-etcd-port",
					Protocol: v1.ProtocolTCP,
					Port:     etcdPort,
				},
			},
		},
	}
}

func ServiceName(clusterName string) string {
	return controller.ObjectName(clusterName, &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
	})
}

func headlessServiceName(clusterName string) string {
	return controller.ObjectName(clusterName, &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		}}, "-headless")
}
