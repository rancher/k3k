package config

import (
	"fmt"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/galal-hussein/k3k/pkg/controller/util"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AgentConfig(cluster *v1alpha1.Cluster, serviceIP string) v1.Secret {
	config := agentConfigData(serviceIP, cluster.Spec.Token)
	return v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k3k-agent-config",
			Namespace: util.ClusterNamespace(cluster),
		},
		Data: map[string][]byte{
			"config.yaml": []byte(config),
		},
	}
}

func agentConfigData(serviceIP, token string) string {
	return fmt.Sprintf(`server: https://%s:6443
token: %s`, serviceIP, token)
}
