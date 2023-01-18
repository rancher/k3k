package controller

import (
	"fmt"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func serverConfig(cluster *v1alpha1.Cluster, init bool, serviceIP string) v1.Secret {
	name := "k3k-server-config"
	if init {
		name = "k3k-init-server-config"
	}

	config := configData(serviceIP, cluster.Spec.Token)
	if init {
		config = initConfigData(cluster.Spec.Token)
	}
	return v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterNamespace(cluster),
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}
}

func configData(serviceIP, token string) string {
	return fmt.Sprintf(`cluster-init: true
server: https://%s:6443
token: %s
snapshotter: native
cluster-cidr: 10.40.0.0/16
service-cidr: 10.44.0.0/16
cluster-dns: 10.44.0.10`, serviceIP, token)
}

func initConfigData(token string) string {
	return fmt.Sprintf(`cluster-init: true
token: %s
snapshotter: native
cluster-cidr: 10.40.0.0/16
service-cidr: 10.44.0.0/16
cluster-dns: 10.44.0.10`, token)
}
