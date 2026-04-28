package server

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

func (s *Server) Config(init bool, serviceIP string) (*corev1.Secret, error) {
	name := configSecretName(s.cluster.Name, init)

	sans := sets.NewString(s.cluster.Spec.TLSSANs...)
	sans.Insert(
		serviceIP,
		ServiceName(s.cluster.Name),
		fmt.Sprintf("%s.%s", ServiceName(s.cluster.Name), s.cluster.Namespace),
	)

	s.cluster.Status.TLSSANs = sans.List()

	config := serverConfigData(serviceIP, s.cluster, s.token)
	if init {
		config = initConfigData(s.cluster, s.token)
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
			"config.yaml": []byte(config),
		},
	}, nil
}

func serverConfigData(serviceIP string, cluster *v1beta1.Cluster, token string) string {
	return serverOptions(cluster, token, "cluster-init: true", "server: https://"+serviceIP)
}

func initConfigData(cluster *v1beta1.Cluster, token string) string {
	return serverOptions(cluster, token, "cluster-init: true")
}

func serverOptions(cluster *v1beta1.Cluster, token string, configOptions ...string) string {
	var opts []string

	for _, i := range configOptions {
		opts = append(opts, i)
	}

	// TODO: generate token if not found
	if token != "" {
		opts = append(opts, "token: "+token)
	}

	if cluster.Status.ClusterCIDR != "" {
		opts = append(opts, "cluster-cidr: "+cluster.Status.ClusterCIDR)
	}

	if cluster.Status.ServiceCIDR != "" {
		opts = append(opts, "service-cidr: "+cluster.Status.ServiceCIDR)
	}

	if cluster.Spec.ClusterDNS != "" {
		opts = append(opts, "cluster-dns: "+cluster.Spec.ClusterDNS)
	}

	if len(cluster.Status.TLSSANs) > 0 {
		opts = append(opts, "tls-san:")
		for _, addr := range cluster.Status.TLSSANs {
			opts = append(opts, "- "+addr)
		}
	}

	if cluster.Spec.Mode != agent.VirtualNodeMode {
		opts = append(opts, "disable-agent: true")
		opts = append(opts, "egress-selector-mode: disabled")
		opts = append(opts, "disable:")
		opts = append(opts, "- servicelb")
		opts = append(opts, "- traefik")
		opts = append(opts, "- metrics-server")
		opts = append(opts, "- local-storage")
	}

	// log to both file and console.
	opts = append(opts, "log: /var/log/k3s.log")
	opts = append(opts, "alsologtostderr: true")

	// TODO: Add extra args to the options

	return strings.Join(opts, "\n")
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}

	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}
