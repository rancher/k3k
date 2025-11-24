package server

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/util/sets"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
)

var k3sNetpolVersions = []string{"v1.31.14", "v1.32.10", "v1.33.6", "v1.34.2"}

func (s *Server) Config(init bool, serviceIP string) (*v1.Secret, error) {
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

func serverConfigData(serviceIP string, cluster *v1beta1.Cluster, token string) string {
	return "cluster-init: true\nserver: https://" + serviceIP + "\n" + serverOptions(cluster, token)
}

func initConfigData(cluster *v1beta1.Cluster, token string) string {
	return "cluster-init: true\n" + serverOptions(cluster, token)
}

func serverOptions(cluster *v1beta1.Cluster, token string) string {
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
		opts = opts + "disable-agent: true\negress-selector-mode: disabled\ndisable:\n- servicelb\n- traefik\n- metrics-server\n- local-storage\n"
	}

	// Adding a check for the version here to workaround issue https://github.com/rancher/k3k/issues/477
	// in older versions of k3s
	if requiresNetworkPolicyDisable(cluster) {
		opts = opts + "disable-network-policy: true\n"
	}

	return opts
}

func configSecretName(clusterName string, init bool) string {
	if !init {
		return controller.SafeConcatNameWithPrefix(clusterName, configName)
	}

	return controller.SafeConcatNameWithPrefix(clusterName, initConfigName)
}

func requiresNetworkPolicyDisable(cluster *v1beta1.Cluster) bool {
	imageVersion := "latest"

	if cluster.Spec.Version != "" {
		imageVersion = cluster.Spec.Version
	} else if cluster.Status.HostVersion != "" {
		imageVersion = cluster.Status.HostVersion
	}
	fmt.Printf("image version 1 %s\n", imageVersion)
	if imageVersion == "latest" {
		return false
	}

	for _, fixedVersion := range k3sNetpolVersions {
		if semver.MajorMinor(imageVersion) == semver.MajorMinor(fixedVersion) {

			// if the major and minor match then check if the patch version is less than fixed version
			version, _ := strings.CutSuffix(imageVersion, "-k3s1")
			if semver.Compare(version, fixedVersion) == -1 {
				return true
			}
			return false
		}
	}
	return false
}
