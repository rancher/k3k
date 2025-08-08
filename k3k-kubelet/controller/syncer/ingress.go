package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

const (
	ingressControllerName = "ingress-syncer-controller"
	ingressFinalizerName  = "ingress.k3k.io/finalizer"
)

type IngressReconciler struct {
	*SyncerContext
}

// AddIngressSyncer adds ingress syncer controller to the manager of the virtual cluster
func AddIngressSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := IngressReconciler{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			Host: &ClusterClient{
				Client: hostMgr.GetClient(),
			},
			Virtual: &ClusterClient{
				Client: virtMgr.GetClient(),
			},
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, ingressControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&networkingv1.Ingress{}).
		Complete(&reconciler)
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	log.Info("reconciling ingress object")

	var (
		virtIngress networkingv1.Ingress
		cluster     v1alpha1.Cluster
	)

	if err := r.Host.Client.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.Virtual.Client.Get(ctx, req.NamespacedName, &virtIngress); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedIngress := r.ingress(&virtIngress)
	if err := controllerutil.SetControllerReference(&cluster, syncedIngress, r.Host.Client.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtIngress.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		if err := r.Host.Client.Delete(ctx, syncedIngress); err != nil {
			return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.RemoveFinalizer(&virtIngress, ingressFinalizerName) {
			if err := r.Virtual.Client.Update(ctx, &virtIngress); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist

	if controllerutil.AddFinalizer(&virtIngress, ingressFinalizerName) {
		if err := r.Virtual.Client.Update(ctx, &virtIngress); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the ingress on host
	var hostIngress networkingv1.Ingress
	if err := r.Host.Client.Get(ctx, types.NamespacedName{Name: syncedIngress.Name, Namespace: r.ClusterNamespace}, &hostIngress); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the ingress for the first time on the host cluster")
			return reconcile.Result{}, r.Host.Client.Create(ctx, syncedIngress)
		}

		return reconcile.Result{}, err
	}

	log.Info("updating ingress on the host cluster")

	return reconcile.Result{}, r.Host.Client.Update(ctx, syncedIngress)
}

func (s *IngressReconciler) ingress(obj *networkingv1.Ingress) *networkingv1.Ingress {
	hostIngress := obj.DeepCopy()
	s.Translator.TranslateTo(hostIngress)

	for _, rule := range hostIngress.Spec.Rules {
		// modify services in rules to point to the synced services
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service != nil {
					path.Backend.Service.Name = s.Translator.TranslateName(obj.GetNamespace(), path.Backend.Service.Name)
				}
			}
		}
	}
	// don't sync finalizers to the host
	return hostIngress
}
