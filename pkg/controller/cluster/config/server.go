package config

import (
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ServerConfig(cluster *v1alpha1.Cluster, init bool, serviceIP string) (*v1.Secret, error) {
	name := "k3k-server-config"
	if init {
		name = "k3k-init-server-config"
	}

	config := serverConfigData(serviceIP, cluster)
	if init {
		config = initConfigData(cluster)
	}
	return &v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: util.ClusterNamespace(cluster),
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}, nil
}

func serverConfigData(serviceIP string, cluster *v1alpha1.Cluster) string {
	return "cluster-init: true\nserver: https://" + serviceIP + ":6443" + serverOptions(cluster)
}

func initConfigData(cluster *v1alpha1.Cluster) string {
	return "cluster-init: true\n" + serverOptions(cluster)
}

func serverOptions(cluster *v1alpha1.Cluster) string {
	var opts string

	// TODO: generate token if not found
	if cluster.Spec.Token != "" {
		opts = "token: " + cluster.Spec.Token + "\n"
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
	if len(cluster.Spec.TLSSANs) > 0 {
		opts = opts + "tls-san:\n"
		for _, addr := range cluster.Spec.TLSSANs {
			opts = opts + "- " + addr + "\n"
		}
	}
	// TODO: Add extra args to the options

	return opts
}
