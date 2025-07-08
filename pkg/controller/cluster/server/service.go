package server

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func Service(cluster *v1alpha1.Cluster) *v1.Service {
	service := &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName(cluster.Name),
			Namespace: cluster.Namespace,
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"cluster": cluster.Name,
				"role":    "server",
			},
		},
	}

	k3sServerPort := v1.ServicePort{
		Name:       "k3s-server-port",
		Protocol:   v1.ProtocolTCP,
		Port:       httpsPort,
		TargetPort: intstr.FromInt(k3sServerPort),
	}

	etcdPort := v1.ServicePort{
		Name:       "k3s-etcd-port",
		Protocol:   v1.ProtocolTCP,
		Port:       etcdPort,
		TargetPort: intstr.FromInt(etcdPort),
	}

	// If no expose is specified, default to ClusterIP
	if cluster.Spec.Expose == nil {
		service.Spec.Type = v1.ServiceTypeClusterIP
		service.Spec.Ports = append(service.Spec.Ports, k3sServerPort, etcdPort)
	}

	// If expose is specified, set the type to the appropriate type
	if cluster.Spec.Expose != nil {
		expose := cluster.Spec.Expose

		switch {
		case expose.LoadBalancer != nil:
			service.Spec.Type = v1.ServiceTypeLoadBalancer
			addLoadBalancerPorts(service, *expose.LoadBalancer, k3sServerPort, etcdPort)
		case expose.NodePort != nil:
			service.Spec.Type = v1.ServiceTypeNodePort
			addNodePortPorts(service, *expose.NodePort, k3sServerPort, etcdPort)
		default:
			// default to clusterIP for ingress or empty expose config
			service.Spec.Type = v1.ServiceTypeClusterIP
			service.Spec.Ports = append(service.Spec.Ports, k3sServerPort, etcdPort)
		}
	}

	return service
}

// addLoadBalancerPorts adds the load balancer ports to the service
func addLoadBalancerPorts(service *v1.Service, loadbalancerConfig v1alpha1.LoadBalancerConfig, k3sServerPort, etcdPort v1.ServicePort) {
	// If the server port is not specified, use the default port
	if loadbalancerConfig.ServerPort == nil {
		service.Spec.Ports = append(service.Spec.Ports, k3sServerPort)
	} else if *loadbalancerConfig.ServerPort > 0 {
		// If the server port is specified, set the port, otherwise the service will not be exposed
		k3sServerPort.Port = *loadbalancerConfig.ServerPort
		service.Spec.Ports = append(service.Spec.Ports, k3sServerPort)
	}

	// If the etcd port is not specified, use the default port
	if loadbalancerConfig.ETCDPort == nil {
		service.Spec.Ports = append(service.Spec.Ports, etcdPort)
	} else if *loadbalancerConfig.ETCDPort > 0 {
		// If the etcd port is specified, set the port, otherwise the service will not be exposed
		etcdPort.Port = *loadbalancerConfig.ETCDPort
		service.Spec.Ports = append(service.Spec.Ports, etcdPort)
	}
}

// addNodePortPorts adds the node port ports to the service
func addNodePortPorts(service *v1.Service, nodePortConfig v1alpha1.NodePortConfig, k3sServerPort, etcdPort v1.ServicePort) {
	// If the server port is not specified Kubernetes will set the node port to a random port between 30000-32767
	if nodePortConfig.ServerPort == nil {
		service.Spec.Ports = append(service.Spec.Ports, k3sServerPort)
	} else {
		serverNodePort := *nodePortConfig.ServerPort

		// If the server port is in the range of 30000-32767, set the node port
		// otherwise the service will not be exposed
		if serverNodePort >= 30000 && serverNodePort <= 32767 {
			k3sServerPort.NodePort = serverNodePort
			service.Spec.Ports = append(service.Spec.Ports, k3sServerPort)
		}
	}

	// If the etcd port is not specified Kubernetes will set the node port to a random port between 30000-32767
	if nodePortConfig.ETCDPort == nil {
		service.Spec.Ports = append(service.Spec.Ports, etcdPort)
	} else {
		etcdNodePort := *nodePortConfig.ETCDPort

		// If the etcd port is in the range of 30000-32767, set the node port
		// otherwise the service will not be exposed
		if etcdNodePort >= 30000 && etcdNodePort <= 32767 {
			etcdPort.NodePort = etcdNodePort
			service.Spec.Ports = append(service.Spec.Ports, etcdPort)
		}
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
					Name:       "k3s-server-port",
					Protocol:   v1.ProtocolTCP,
					Port:       httpsPort,
					TargetPort: intstr.FromInt(k3sServerPort),
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
	return controller.SafeConcatNameWithPrefix(clusterName, "service")
}

func headlessServiceName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, "service", "headless")
}
