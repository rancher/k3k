package util

import (
	"context"

	"github.com/galal-hussein/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// return all the nodes external addresses, if not found then return internal addresses
func Addresses(ctx context.Context, client client.Client) ([]string, error) {
	addresses := []string{}
	nodeList := v1.NodeList{}
	if err := client.List(ctx, &nodeList); err != nil {
		return nil, err
	}

	for _, node := range nodeList.Items {
		addresses = append(addresses, getNodeAddress(&node))
	}

	return addresses, nil
}

func getNodeAddress(node *v1.Node) string {
	externalIP := ""
	internalIP := ""
	for _, ip := range node.Status.Addresses {
		if ip.Type == "ExternalIP" && ip.Address != "" {
			externalIP = ip.Address
			break
		} else if ip.Type == "InternalIP" && ip.Address != "" {
			internalIP = ip.Address
		}
	}
	if externalIP != "" {
		return externalIP
	}

	return internalIP
}
