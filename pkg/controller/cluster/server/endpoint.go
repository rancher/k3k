package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// ServerURL returns the URL at which the K3s API server of a virtual cluster
// is reachable. The second return value reports whether that URL is routable
// from outside the host cluster (true for NodePort/LoadBalancer/Ingress, false
// for plain ClusterIP exposition).
//
// hostServerIP is used as the address when the underlying Service is a
// NodePort. serverPort, when non-zero, overrides the port discovered from the
// Service.
func ServerURL(ctx context.Context, c client.Client, cluster *v1beta1.Cluster, hostServerIP string, serverPort int) (string, bool, error) {
	key := types.NamespacedName{
		Name:      ServiceName(cluster.Name),
		Namespace: cluster.Namespace,
	}

	var k3kService corev1.Service
	if err := c.Get(ctx, key, &k3kService); err != nil {
		return "", false, err
	}

	ip := k3kService.Spec.ClusterIP
	port := int32(httpsPort)
	external := false

	if len(k3kService.Spec.Ports) == 0 {
		logrus.Warn("No ports exposed by the cluster service.")
	}

	switch k3kService.Spec.Type {
	case corev1.ServiceTypeNodePort:
		ip = hostServerIP
		external = true

		if len(k3kService.Spec.Ports) > 0 {
			port = k3kService.Spec.Ports[0].NodePort
		}
	case corev1.ServiceTypeLoadBalancer:
		external = true

		if len(k3kService.Status.LoadBalancer.Ingress) > 0 {
			ip = k3kService.Status.LoadBalancer.Ingress[0].IP
		} else {
			logrus.Warn("No ingress found in LoadBalancer service.")
		}

		if len(k3kService.Spec.Ports) > 0 {
			port = k3kService.Spec.Ports[0].Port
		}
	}

	if serverPort != 0 {
		port = int32(serverPort)
	}

	if !slices.Contains(cluster.Status.TLSSANs, ip) {
		logrus.Warnf("IP %s not in tlsSANs.", ip)

		if len(cluster.Spec.TLSSANs) > 0 {
			logrus.Warnf("Using the first TLS SAN in the spec as a fallback: %s", cluster.Spec.TLSSANs[0])

			ip = cluster.Spec.TLSSANs[0]
		} else if len(cluster.Status.TLSSANs) > 0 {
			logrus.Warnf("No explicit tlsSANs specified. Trying to use the first TLS SAN in the status: %s", cluster.Status.TLSSANs[0])

			ip = cluster.Status.TLSSANs[0]
		} else {
			logrus.Warn("IP not found in tlsSANs. This could cause issue with the certificate validation.")
		}
	}

	url := "https://" + ip
	if port != httpsPort {
		url = fmt.Sprintf("%s:%d", url, port)
	}

	// if ingress is specified, use the ingress host
	if cluster.Spec.Expose != nil && cluster.Spec.Expose.Ingress != nil {
		var k3kIngress networkingv1.Ingress

		ingressKey := types.NamespacedName{
			Name:      IngressName(cluster.Name),
			Namespace: cluster.Namespace,
		}

		if err := c.Get(ctx, ingressKey, &k3kIngress); err != nil {
			return "", external, err
		}

		if len(k3kIngress.Spec.Rules) > 0 {
			url = fmt.Sprintf("https://%s", k3kIngress.Spec.Rules[0].Host)
			external = true
		}
	}

	return url, external, nil
}
