package server

import (
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) Config(init bool, serviceIP string) (*v1.Secret, error) {
	name := configSecretName(s.cluster.Name, init)
	s.cluster.Status.TLSSANs = append(s.cluster.Spec.TLSSANs,
		serviceIP,
		ServiceName(s.cluster.Name),
		fmt.Sprintf("%s.%s", ServiceName(s.cluster.Name), s.cluster.Namespace),
	)

	config := serverConfigData(serviceIP, s.cluster, s.token)
	if init {
		config = initConfigData(s.cluster, s.token)
	}
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}, nil
}

func serverConfigData(serviceIP string, cluster *v1alpha1.Cluster, token string) string {
	return "cluster-init: true\nserver: https://" + serviceIP + ":6443\n" + serverOptions(cluster, token)
}

func initConfigData(cluster *v1alpha1.Cluster, token string) string {
	return "cluster-init: true\n" + serverOptions(cluster, token)
}

func serverOptions(cluster *v1alpha1.Cluster, token string) string {
	var opts string

	// TODO: generate token if not found
	if token != "" {
		opts = "token: " + token + "\n"
	}
	if cluster.Status.ClusterCIDR != "" {
		opts = opts + "cluster-cidr: " + cluster.Status.ClusterCIDR + "\n"
	}
	if cluster.Status.ServiceCIDR != "" {
		opts = opts + "service-cidr: " + cluster.Status.ServiceCIDR + "\n"
	}
	if cluster.Spec.ClusterDNS != "" {
		opts = opts + "cluster-dns: " + cluster.Spec.ClusterDNS + "\n"
	}
	if len(cluster.Status.TLSSANs) > 0 {
		opts = opts + "tls-san:\n"
		for _, addr := range cluster.Status.TLSSANs {
			opts = opts + "- " + addr + "\n"
		}
	}
	if cluster.Spec.Mode != agent.VirtualNodeMode {
		opts = opts + "disable-agent: true\negress-selector-mode: disabled\ndisable:\n- servicelb\n- traefik\n- metrics-server"
	}
	// TODO: Add extra args to the options

	return opts
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}
	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}
