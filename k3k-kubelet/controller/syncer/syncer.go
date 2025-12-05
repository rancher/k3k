package syncer

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

type SyncerContext struct {
	ClusterName      string
	ClusterNamespace string
	VirtualClient    client.Client
	HostClient       client.Client
	Translator       translate.ToHostTranslator
}

func createOrUpdate(ctx context.Context, logger logr.Logger, hostClient client.Client, obj client.Object) (reconcile.Result, error) {
	objKind := obj.GetObjectKind().GroupVersionKind().Kind
	logger = logger.WithValues("kind", objKind, "name", obj.GetName())

	var hostObj client.Object
	if err := hostClient.Get(ctx, client.ObjectKeyFromObject(obj), hostObj); err != nil {
		if apierrors.IsNotFound(err) {
			if err := hostClient.Create(ctx, obj); err != nil {
				logger.Error(err, "Error creating object on the host cluster")
				return reconcile.Result{}, err
			}

			logger.Info("Created object for the first time on the host cluster")

			return reconcile.Result{}, nil
		}

		return reconcile.Result{}, err
	}

	if err := hostClient.Update(ctx, obj); err != nil {
		logger.Error(err, "Error updating object on the host cluster")
		return reconcile.Result{}, err
	}

	logger.Info("Updated object on the host cluster", objKind)

	return reconcile.Result{}, nil
}
