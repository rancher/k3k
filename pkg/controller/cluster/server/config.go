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
	// hcp additionally relies on this to satisfy the PRD requirement that the
	// control plane never runs a kubelet and is not enumerated as a node.
	if cluster.Spec.Mode != v1beta1.VirtualClusterMode {
		opts = opts + "disable-agent: true\ndisable:\n- servicelb\n- traefik\n- metrics-server\n- local-storage\n"
	}

	// In shared mode workloads run on the host cluster, so the apiserver pod
	// can reach them directly via the host pod network and the egress
	// selector is unnecessary.
	//
	// In hcp mode the apiserver pod has NO route to the virtual cluster's
	// pod CIDR (which only exists on joined external worker nodes), and the
	// kube-apiserver bypasses kube-proxy when calling webhooks / proxying
	// to pods: it resolves Service -> Endpoints itself and dials the Pod IP
	// directly. We therefore tunnel apiserver egress through the WebSocket
	// each k3s-agent maintains back to the server.
	//
	// We pick "cluster" rather than "pod" or "agent" because the agent-side
	// authorizer differs by mode (k3s pkg/agent/tunnel/tunnel.go):
	//   - agent:   only kubelet calls are tunneled; pod-IP dials go direct
	//              and fail in HCP (no route to virtual pod CIDR).
	//   - pod:     authorizer only allows pod IPs the agent has *already
	//              watched*. A newly-created pod's IP is rejected with
	//              "connect not allowed", which terminates the entire
	//              remotedialer session and 502s in-flight kubelet streams
	//              -> kubectl logs / exec / webhooks become flaky.
	//   - cluster: authorizer pre-populates the cluster CIDR + node IPs as
	//              non-hostNet entries, so every pod IP and every node port
	//              is permitted. No race, no per-port allowlist. This is
	//              what we want for a managed control plane.
	switch cluster.Spec.Mode {
	case v1beta1.SharedClusterMode:
		opts = opts + "egress-selector-mode: disabled\n"
	case v1beta1.HCPClusterMode:
		opts = opts + "egress-selector-mode: cluster\n"
	}

	// In hcp mode the apiserver pod IP is unreachable from external worker
	// nodes, so the kube-apiserver's default lease-based endpoint reconciler
	// would publish a broken default/kubernetes Endpoints (advertise-address +
	// secure-port). Disable it so K3k can own that Endpoints object and point
	// it at the externally-reachable host:port (NodePort / LB / Ingress).
	if cluster.Spec.Mode == v1beta1.HCPClusterMode {
		opts = opts + "kube-apiserver-arg:\n- endpoint-reconciler-type=none\n"
	}

	return serverConfig
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}

	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}
