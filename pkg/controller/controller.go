package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namePrefix      = "k3k"
	k3SImageName    = "rancher/k3s"
	AdminCommonName = "system:admin"
)

// Backoff is the cluster creation duration backoff
var Backoff = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   2,
	Jitter:   0.1,
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
func Addresses(ctx context.Context, client ctrlruntimeclient.Client) ([]string, error) {
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

// SafeConcatNameWithPrefix runs the SafeConcatName with extra prefix.
func SafeConcatNameWithPrefix(name ...string) string {
	return SafeConcatName(append([]string{namePrefix}, name...)...)
}

// SafeConcatName concatenates the given strings and ensures the returned name is under 64 characters
// by cutting the string off at 57 characters and setting the last 6 with an encoded version of the concatenated string.
func SafeConcatName(name ...string) string {
	fullPath := strings.Join(name, "-")
	if len(fullPath) < 64 {
		return fullPath
	}
	digest := sha256.Sum256([]byte(fullPath))
	// since we cut the string in the middle, the last char may not be compatible with what is expected in k8s
	// we are checking and if necessary removing the last char
	c := fullPath[56]
	if 'a' <= c && c <= 'z' || '0' <= c && c <= '9' {
		return fullPath[0:57] + "-" + hex.EncodeToString(digest[0:])[0:5]
	}

	return fullPath[0:56] + "-" + hex.EncodeToString(digest[0:])[0:6]
}
