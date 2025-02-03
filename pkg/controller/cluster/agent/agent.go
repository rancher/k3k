package agent

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	configName = "agent-config"
)

func configSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, configName)
}

func (s *SharedAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, s.cluster, obj, s.client, s.scheme)
}

func (v *VirtualAgent) ensureObject(ctx context.Context, obj ctrlruntimeclient.Object) error {
	return ensureObject(ctx, v.cluster, obj, v.client, v.scheme)
}

func ensureObject(ctx context.Context, cluster *v1alpha1.Cluster, obj ctrlruntimeclient.Object, ctrlClient ctrlruntimeclient.Client, scheme *runtime.Scheme) error {
	result, err := controllerutil.CreateOrUpdate(ctx, ctrlClient, obj, func() error {
		fmt.Printf("call FN mut %v - %s\n", cluster, client.ObjectKeyFromObject(obj))

		if err := controllerutil.SetControllerReference(cluster, obj, scheme); err != nil {
			return err
		}
		return nil
	})

	fmt.Printf("ensureObject %s - %s\n", result, client.ObjectKeyFromObject(obj))

	return err
}
