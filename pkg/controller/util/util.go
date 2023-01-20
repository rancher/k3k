package util

import (
	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	"k8s.io/klog"
)

const (
	NamespacePrefix = "k3k-"
	K3SImageName    = "rancher/k3s"
)

func ClusterNamespace(cluster *v1alpha1.Cluster) string {
	return NamespacePrefix + cluster.Name
}

func K3SImage(cluster *v1alpha1.Cluster) string {
	return K3SImageName + ":" + cluster.Spec.Version
}

func WrapErr(errString string, err error) error {
	klog.Errorf("%s: %v", errString, err)
	return err
}
