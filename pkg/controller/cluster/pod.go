package cluster

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

const (
	podController = "k3k-pod-controller"
)

type PodReconciler struct {
	Client ctrlruntimeclient.Client
	Scheme *runtime.Scheme
}

// AddPodController adds a new controller for Pods to the manager.
// It will reconcile the Pods of the Host Cluster with the one of the Virtual Cluster.
func AddPodController(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int) error {
	reconciler := PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Named(podController).
		WithEventFilter(newClusterPredicate()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Complete(&reconciler)
}

func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling pod")

	var pod v1.Pod
	if err := r.Client.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	// get cluster from the object
	cluster := clusterNamespacedName(&pod)

	virtualClient, err := newVirtualClient(ctx, r.Client, cluster.Name, cluster.Namespace)
	if err != nil {
		return reconcile.Result{}, err
	}

	if !pod.DeletionTimestamp.IsZero() {
		virtName := pod.GetAnnotations()[translate.ResourceNameAnnotation]
		virtNamespace := pod.GetAnnotations()[translate.ResourceNamespaceAnnotation]

		virtPod := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      virtName,
				Namespace: virtNamespace,
			},
		}

		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(virtualClient.Delete(ctx, &virtPod))
	}

	return reconcile.Result{}, nil
}
