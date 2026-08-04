package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	kubeSystemNamespace        = "kube-system"
	coreDNSCustomConfigMapName = "coredns-custom"
)

// GenerateCustomConfigMap generates a ConfigMap with a custom CoreDNS configuration
//
// If no snippets are provided the returned ConfigMap is nil.
func GenerateCustomConfigMap(cluster *v1beta1.Cluster) *corev1.ConfigMap {
	data := generateCoreDNSConfigMap(cluster)
	if data == nil {
		return nil
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      coreDNSCustomConfigMapName,
			Namespace: kubeSystemNamespace,
		},
	}

	cm.Data = data

	return cm
}

// generateCoreDNSConfigMap generates a map of CoreDNS custom configuration directives.
//
// returns nil if no configured customisations are provided in the cluster.
func generateCoreDNSConfigMap(cluster *v1beta1.Cluster) map[string]string {
	if cluster.Spec.DNS == nil || cluster.Spec.DNS.CoreDNS == nil || len(cluster.Spec.DNS.CoreDNS.CustomConfig) == 0 {
		return nil
	}

	data := make(map[string]string)
	for _, c := range cluster.Spec.DNS.CoreDNS.CustomConfig {
		data[c.Name] = c.Value
	}

	return data
}
