package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	v1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/controller"
)

// newVirtualClient creates a new Client that can be used to interact with the virtual cluster
func newVirtualClient(ctx context.Context, hostClient ctrlruntimeclient.Client, clusterName, clusterNamespace string) (ctrlruntimeclient.Client, error) {
	var clusterKubeConfig v1.Secret

	kubeconfigSecretName := types.NamespacedName{
		Name:      controller.SafeConcatNameWithPrefix(clusterName, "kubeconfig"),
		Namespace: clusterNamespace,
	}

	if err := hostClient.Get(ctx, kubeconfigSecretName, &clusterKubeConfig); err != nil {
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(clusterKubeConfig.Data["kubeconfig.yaml"])
	if err != nil {
		return nil, fmt.Errorf("failed to create config from kubeconfig file: %w", err)
	}

	return ctrlruntimeclient.New(restConfig, ctrlruntimeclient.Options{})
}
