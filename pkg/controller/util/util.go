package util

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespacePrefix = "k3k-"
	k3SImageName    = "rancher/k3s"
	AdminCommonName = "system:admin"
	ServerPort      = 6443
)

const (
	K3kSystemNamespace = namespacePrefix + "system"
)

func ClusterNamespace(cluster *v1alpha1.Cluster) string {
	return namespacePrefix + cluster.Name
}

func K3SImage(cluster *v1alpha1.Cluster) string {
	return k3SImageName + ":" + cluster.Spec.Version
}

func LogAndReturnErr(errString string, err error) error {
	klog.Errorf("%s: %v", errString, err)
	return err
}

func nodeAddress(node *v1.Node) string {
	var externalIP string
	var internalIP string

	for _, ip := range node.Status.Addresses {
		if ip.Type == "ExternalIP" && ip.Address != "" {
			externalIP = ip.Address
			break
		}
		if ip.Type == "InternalIP" && ip.Address != "" {
			internalIP = ip.Address
		}
	}
	if externalIP != "" {
		return externalIP
	}

	return internalIP
}

// return all the nodes external addresses, if not found then return internal addresses
func Addresses(ctx context.Context, client client.Client) ([]string, error) {
	var nodeList v1.NodeList
	if err := client.List(ctx, &nodeList); err != nil {
		return nil, err
	}

	var addresses []string
	for _, node := range nodeList.Items {
		addresses = append(addresses, nodeAddress(&node))
	}

	return addresses, nil
}
