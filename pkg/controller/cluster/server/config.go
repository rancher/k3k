package server

import (
	"fmt"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/sets"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

// serverConfig are few options from k3s server options that will
// construct the yaml config file for k3s server
type serverConfig struct {
	ClusterCIDR        string   `yaml:"cluster-cidr,omitempty"`
	ClusterDNS         string   `yaml:"cluster-dns,omitempty"`
	ClusterInit        bool     `yaml:"cluster-init,omitempty"`
	DisableAgent       bool     `yaml:"disable-agent,omitempty"`
	Disable            []string `yaml:"disable,omitempty"`
	EgressSelectorMode string   `yaml:"egress-selector-mode,omitempty"`
	KubeApiServerArg   []string `yaml:"kube-apiserver-arg,omitempty"`
	Server             string   `yaml:"server,omitempty"`
	ServiceCIDR        string   `yaml:"service-cidr,omitempty"`
	TLSSAN             []string `yaml:"tls-san,omitempty"`
	Token              string   `yaml:"token,omitempty"`
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
		TLSSAN:      cluster.Status.TLSSANs,
		ServiceCIDR: cluster.Status.ServiceCIDR,
		ClusterCIDR: cluster.Status.ClusterCIDR,
		ClusterDNS:  cluster.Spec.ClusterDNS,
	}

	if !initServer {
		serverConfig.Server = "https://" + serviceIP
	}

	// shared and hcp modes both run K3s with --disable-agent (agentless server).
	switch cluster.Spec.Mode {
	case "", v1beta1.SharedClusterMode:
		serverConfig.DisableAgent = true
		serverConfig.EgressSelectorMode = "disabled"
		serverConfig.Disable = []string{"servicelb", "traefik", "metrics-server", "local-storage"}
	case v1beta1.HCPClusterMode:
		serverConfig.DisableAgent = true
		// Tunnel apiserver egress through the k3s-agent WebSocket: the
		// apiserver has no route to the virtual cluster's pod CIDR and
		// bypasses kube-proxy when dialing pod IPs (webhooks, log/exec).
		// "cluster" is the only safe mode — "agent" lets pod dials go
		// direct (no route, fails); "pod" only permits pod IPs the agent
		// has already watched, so a newly-created pod's IP is rejected
		// and tears down the remotedialer session, making kubelet streams
		// flaky. See k3s pkg/agent/tunnel/tunnel.go.
		serverConfig.EgressSelectorMode = "cluster"
		// Disable the apiserver's built-in endpoint reconciler so K3k can
		// own default/kubernetes Endpoints and point it at the externally
		// reachable host:port (NodePort / LB / Ingress).
		serverConfig.KubeApiServerArg = append(serverConfig.KubeApiServerArg, "endpoint-reconciler-type=none")
	case v1beta1.VirtualClusterMode:
		// no extra config for virtual mode
	}

	return serverConfig
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}

	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}
