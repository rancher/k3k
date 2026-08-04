package controller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	coreDNSCustomConfigMapName = "coredns-custom"
	coreDNSCustomConfigKey     = "custom.override"
)

// GenerateCustomConfigMap generates a ConfigMap with a custom CoreDNS configuration
// for the given cluster, including any forwarding zones specified in the cluster's CustomDNS spec.
func GenerateCustomConfigMap(cluster *v1beta1.Cluster) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SafeConcatNameWithPrefix(cluster.Name, coreDNSCustomConfigMapName),
			Namespace: cluster.Namespace,
		},
	}

	if cluster.Spec.CustomDNS == nil || len(cluster.Spec.CustomDNS.Forwarders) == 0 {
		return cm
	}

	cm.Data = map[string]string{
		coreDNSCustomConfigKey: generateForwardingConfig(cluster.Spec.CustomDNS.Forwarders),
	}

	return cm
}

// generateForwardingConfig returns CoreDNS server blocks for each configured forwarder.
func generateForwardingConfig(forwarders []v1beta1.CustomDNSForwarder) string {
	var sb strings.Builder
	for _, f := range forwarders {
		zone := "."
		if f.Domain != "" {
			zone = f.Domain
		}
		fmt.Fprintf(&sb, "%s:53 {\n    forward . %s\n", zone, strings.Join(f.Forwarders, " "))
		if f.Log {
			sb.WriteString("    log\n")
		}
		sb.WriteString("}\n")
	}
	return sb.String()
}
