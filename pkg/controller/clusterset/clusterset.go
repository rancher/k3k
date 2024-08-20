package clusterset

import (
	"context"
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	clusterSetController    = "k3k-clusterset-controller"
	maxConcurrentReconciles = 1
)

type ClusterSetReconciler struct {
	Client ctrlruntimeclient.Client
	Scheme *runtime.Scheme
}

// Add adds a new controller to the manager
func Add(ctx context.Context, mgr manager.Manager) error {
	// initialize a new Reconciler
	reconciler := ClusterSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	// create a new controller and add it to the manager
	//this can be replaced by the new builder functionality in controller-runtime
	controller, err := controller.New(clusterSetController, mgr, controller.Options{
		Reconciler:              &reconciler,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	})
	if err != nil {
		return err
	}

	return controller.Watch(&source.Kind{Type: &v1alpha1.ClusterSet{}}, &handler.EnqueueRequestForObject{})
}

func (c *ClusterSetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var clusterSet v1alpha1.ClusterSet
	if err := c.Client.Get(ctx, types.NamespacedName{Name: req.Name}, &clusterSet); err != nil {
		return reconcile.Result{}, fmt.Errorf("unable to get the clusterset: %w", err)
	}
	klog.Infof("got a clusterset: %v", clusterSet)
	if clusterSet.Spec.MaxLimits != nil {
		quota := v1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clusterset-quota",
				Namespace: clusterSet.Namespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						UID:        clusterSet.UID,
						Name:       clusterSet.Name,
						APIVersion: clusterSet.APIVersion,
						Kind:       clusterSet.Kind,
					},
				},
			},
		}
		quota.Spec.Hard = clusterSet.Spec.MaxLimits
		err := c.Client.Create(ctx, &quota)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to create resource quota from cluster set: %w", err)
		}
	}
	return reconcile.Result{}, nil
}
