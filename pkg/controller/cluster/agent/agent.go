package agent

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	configName = "agent-config"
)

type Config struct {
	cluster *v1alpha1.Cluster
	client  ctrlruntimeclient.Client
	scheme  *runtime.Scheme
}

func NewConfig(cluster *v1alpha1.Cluster, client ctrlruntimeclient.Client, scheme *runtime.Scheme) *Config {
	return &Config{
		cluster: cluster,
		client:  client,
		scheme:  scheme,
	}
}

func configSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, configName)
}

func ensureObject(ctx context.Context, cfg *Config, obj ctrlruntimeclient.Object) error {
	log := ctrl.LoggerFrom(ctx)

	result, err := controllerutil.CreateOrUpdate(ctx, cfg.client, obj, func() error {
		return controllerutil.SetControllerReference(cfg.cluster, obj, cfg.scheme)
	})

	if result != controllerutil.OperationResultNone {
		key := client.ObjectKeyFromObject(obj)
		objectKind := obj.GetObjectKind().GroupVersionKind().Kind
		log.Info(fmt.Sprintf("ensureObject: %s object %s was %s", objectKind, key, result))
	}

	return err
}
