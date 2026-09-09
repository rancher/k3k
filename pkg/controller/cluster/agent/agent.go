// Package agent builds the host cluster resources that run a virtual cluster's
// agents, in either shared or virtual mode.
package agent

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	configName = "agent-config"
)

// ResourceEnsurer is implemented by the agent modes, which each create and update the
// resources their mode needs.
type ResourceEnsurer interface {
	EnsureResources(context.Context) error
}

// Config carries the cluster and client that the agents create their resources with.
type Config struct {
	cluster *v1beta1.Cluster
	client  ctrlruntimeclient.Client
	scheme  *runtime.Scheme
}

// NewConfig returns a Config for the given cluster, taking the scheme from the client.
func NewConfig(cluster *v1beta1.Cluster, client ctrlruntimeclient.Client) *Config {
	return &Config{
		cluster: cluster,
		client:  client,
		scheme:  client.Scheme(),
	}
}

func configSecretName(clusterName string) string {
	return controller.SafeConcatNameWithPrefix(clusterName, configName)
}

func ensureObject(ctx context.Context, cfg *Config, obj ctrlruntimeclient.Object) error {
	key := ctrlruntimeclient.ObjectKeyFromObject(obj)
	log := ctrl.LoggerFrom(ctx).WithValues("key", key)

	if err := controllerutil.SetControllerReference(cfg.cluster, obj, cfg.scheme); err != nil {
		return err
	}

	if err := cfg.client.Create(ctx, obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.V(1).Info(fmt.Sprintf("Resource %T already exists, updating.", obj))

			return cfg.client.Update(ctx, obj)
		}

		return err
	}

	log.V(1).Info(fmt.Sprintf("Creating %T.", obj))

	return nil
}
