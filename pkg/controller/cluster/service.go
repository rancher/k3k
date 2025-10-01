package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	serviceController = "k3k-service-controller"
)

type ServiceReconciler struct {
	HostClient ctrlruntimeclient.Client
}

// Add adds a new controller to the manager
func AddServiceController(ctx context.Context, mgr manager.Manager, maxConcurrentReconciles int) error {
	reconciler := ServiceReconciler{
		HostClient: mgr.GetClient(),
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(serviceController).
		For(&v1.Service{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *ServiceReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	_, hasClusterNameLabel := object.GetLabels()[translate.ClusterNameLabel]

	_, hasNameAnnotation := object.GetAnnotations()[translate.ResourceNameAnnotation]

	_, hasNamespaceAnnotation := object.GetAnnotations()[translate.ResourceNamespaceAnnotation]

	// Return true only if all were found
	return hasClusterNameLabel && hasNameAnnotation && hasNamespaceAnnotation
}

func (r *ServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("ensuring service status to virtual cluster")

	var (
		virtServiceName, virtServiceNamespace string
		hostService, virtService              v1.Service
	)

	if err := r.HostClient.Get(ctx, req.NamespacedName, &hostService); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	// get cluster information from the object
	labels := hostService.GetLabels()

	clusterName, ok := labels[translate.ClusterNameLabel]
	if !ok {
		return reconcile.Result{}, fmt.Errorf("cluster name label is not found")
	}

	clusterNamespace := hostService.GetNamespace()

	virtClient, err := r.newVirtualClient(ctx, &hostService, clusterName, clusterNamespace)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get cluster info: %v", err)
	}

	virtServiceName = hostService.Annotations[translate.ResourceNameAnnotation]
	virtServiceNamespace = hostService.Annotations[translate.ResourceNamespaceAnnotation]

	if !hostService.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	if err := virtClient.Get(ctx, types.NamespacedName{Name: virtServiceName, Namespace: virtServiceNamespace}, &virtService); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get virt service: %v", err)
	}

	if !equality.Semantic.DeepEqual(virtService.Status.LoadBalancer, hostService.Status.LoadBalancer) {
		virtService.Status.LoadBalancer = hostService.Status.LoadBalancer
		if err := virtClient.Status().Update(ctx, &virtService); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ServiceReconciler) newVirtualClient(ctx context.Context, obj ctrlruntimeclient.Object, clusterName, clusterNamespace string) (ctrlruntimeclient.Client, error) {
	var clusterKubeConfig v1.Secret

	kubeconfigSecretName := controller.SafeConcatNameWithPrefix(clusterName, "kubeconfig")

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: kubeconfigSecretName, Namespace: clusterNamespace}, &clusterKubeConfig); err != nil {
		return nil, err
	}

	virtConfig, err := clientcmd.RESTConfigFromKubeConfig(clusterKubeConfig.Data["kubeconfig.yaml"])
	if err != nil {
		return nil, fmt.Errorf("failed to create config from kubeconfig file: %v", err)
	}

	return ctrlruntimeclient.New(virtConfig, ctrlruntimeclient.Options{})
}
