package config

import (
	"fmt"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/galal-hussein/k3k/pkg/controller/util"
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
	opts := serverOptions(cluster)
	return fmt.Sprintf(`cluster-init: true
server: https://%s:6443
%s`, serviceIP, opts)
}

func initConfigData(cluster *v1alpha1.Cluster) string {
	opts := serverOptions(cluster)
	return fmt.Sprintf(`cluster-init: true
%s`, opts)
}

func serverOptions(cluster *v1alpha1.Cluster) string {
	opts := ""
	// TODO: generate token if not found
	if cluster.Spec.Token != "" {
		opts = fmt.Sprintf("token: %s\n", cluster.Spec.Token)
	}
	if cluster.Spec.ClusterCIDR != "" {
		opts = fmt.Sprintf("%scluster-cidr: %s\n", opts, cluster.Spec.ClusterCIDR)
	}
	if cluster.Spec.ServiceCIDR != "" {
		opts = fmt.Sprintf("%sservice-cidr: %s\n", opts, cluster.Spec.ServiceCIDR)
	}
	if cluster.Spec.ClusterDNS != "" {
		opts = fmt.Sprintf("%scluster-dns: %s\n", opts, cluster.Spec.ClusterDNS)
	}
	return opts
}
