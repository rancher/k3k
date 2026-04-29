package server

import (
	"fmt"

	"go.yaml.in/yaml/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

// serverConfig are few options from k3s server options that will
// construct the yaml config file for k3s server
type serverConfig struct {
	ClusterInit        bool     `yaml:"cluster-init,omitempty"`
	DisableAgent       bool     `yaml:"disable-agent,omitempty"`
	EgressSelectorMode string   `yaml:"egress-selector-mode,omitempty"`
	TlsSAN             []string `yaml:"tls-san,omitempty"`
	Disable            []string `yaml:"disable,omitempty"`
	Server             string   `yaml:"server,omitempty"`
	Token              string   `yaml:"token,omitempty"`
	ClusterCIDR        string   `yaml:"cluster-cidr,omitempty"`
	ServiceCIDR        string   `yaml:"service-cidr,omitempty"`
	ClusterDNS         string   `yaml:"cluster-dns,omitempty"`
}

func (s *Server) Config(init bool, serviceIP string) (*corev1.Secret, error) {
	name := configSecretName(s.cluster.Name, init)

	serverConfig := buildServerConfig(s.cluster, init, serviceIP, s.token)

	config, err := yaml.Marshal(serverConfig)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.cluster.Namespace,
		},
		Data: map[string][]byte{
			"config.yaml": config,
		},
	}, nil
}

func buildServerConfig(cluster *v1beta1.Cluster, initServer bool, serviceIP, token string) serverConfig {
	sans := sets.NewString(cluster.Spec.TLSSANs...)
	sans.Insert(
		serviceIP,
		ServiceName(cluster.Name),
		fmt.Sprintf("%s.%s", ServiceName(cluster.Name), cluster.Namespace),
	)

	cluster.Status.TLSSANs = sans.List()

	serverConfig := serverConfig{
		ClusterInit: true,
		Token:       token,
		TlsSAN:      cluster.Status.TLSSANs,
		ServiceCIDR: cluster.Status.ServiceCIDR,
		ClusterCIDR: cluster.Status.ClusterCIDR,
		ClusterDNS:  cluster.Spec.ClusterDNS,
	}

	if !initServer {
		serverConfig.Server = "https://" + serviceIP
	}

	if cluster.Spec.Mode != agent.VirtualNodeMode {
		serverConfig.DisableAgent = true
		serverConfig.EgressSelectorMode = "disabled"
		serverConfig.Disable = []string{"servicelb", "traefik", "metrics-server", "local-storage"}
	}

	return serverConfig
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}

	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}
