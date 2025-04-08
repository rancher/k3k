package agent

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	configName = "agent-config"
)

type ResourceEnsurer interface {
	EnsureResources(context.Context) error
}

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

	key := ctrlruntimeclient.ObjectKeyFromObject(obj)

	log.Info("ensuring %T", obj, "key", key)

	if err := controllerutil.SetControllerReference(cfg.cluster, obj, cfg.scheme); err != nil {
		return err
	}

	if err := cfg.client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return cfg.client.Update(ctx, obj)
		}
		return err
	}

	return nil
}
