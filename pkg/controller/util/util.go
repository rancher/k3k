package util

import "github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"

const (
	NamespacePrefix = "k3k-"
)

func ClusterNamespace(cluster *v1alpha1.Cluster) string {
	return NamespacePrefix + cluster.Name
}
