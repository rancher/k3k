package server

import (
	"strconv"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Service(cluster *v1alpha1.Cluster) *v1.Service {
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
			Name:      "k3k-server-service",
			Namespace: util.ClusterNamespace(cluster),
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
					Port:     6443,
				},
			},
		},
	}
}

func StatefulServerService(cluster *v1alpha1.Cluster, init bool) *v1.Service {
	name := serverName
	if init {
		name = initServerName
	}
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-" + name + "-headless",
			Namespace: util.ClusterNamespace(cluster),
		},
		Spec: v1.ServiceSpec{
			Type:      v1.ServiceTypeClusterIP,
			ClusterIP: v1.ClusterIPNone,
			Selector: map[string]string{
				"cluster": cluster.Name,
				"role":    "server",
				"init":    strconv.FormatBool(init),
			},
			Ports: []v1.ServicePort{
				{
					Name:     "k3s-server-port",
					Protocol: v1.ProtocolTCP,
					Port:     6443,
				},
			},
		},
	}
}
