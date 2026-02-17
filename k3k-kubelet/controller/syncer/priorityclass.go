package syncer

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	PriorityClassGlobalDefaultAnnotation = "priorityclass.k3k.io/globalDefault"

	priorityClassControllerName = "priorityclass-syncer-controller"
	priorityClassFinalizerName  = "priorityclass.k3k.io/finalizer"
)

type PriorityClassSyncer struct {
	*SyncerContext
}

// AddPriorityClassSyncer adds a PriorityClass reconciler to k3k-kubelet
func AddPriorityClassSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	// initialize a new Reconciler
	reconciler := PriorityClassSyncer{
		SyncerContext: &SyncerContext{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			VirtualClient:    virtMgr.GetClient(),
			HostClient:       hostMgr.GetClient(),
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, priorityClassControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&schedulingv1.PriorityClass{}).WithEventFilter(ignoreSystemPrefixPredicate).
		WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

// IgnoreSystemPrefixPredicate filters out resources whose names start with "system-".
var ignoreSystemPrefixPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		return !strings.HasPrefix(e.ObjectOld.GetName(), "system-")
	},
	CreateFunc: func(e event.CreateEvent) bool {
		return !strings.HasPrefix(e.Object.GetName(), "system-")
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return !strings.HasPrefix(e.Object.GetName(), "system-")
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return !strings.HasPrefix(e.Object.GetName(), "system-")
	},
}

func (r *PriorityClassSyncer) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for priorityClassConfig
	syncConfig := cluster.Spec.Sync.PriorityClasses

	// If syncing is disabled, only process deletions to allow for cleanup.
	if !syncConfig.Enabled {
		return object.GetDeletionTimestamp() != nil
	}

	labelSelector := labels.SelectorFromSet(syncConfig.Selector)
	if labelSelector.Empty() {
		return true
	}

	return labelSelector.Matches(labels.Set(object.GetLabels()))
}

func (r *PriorityClassSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		priorityClass schedulingv1.PriorityClass
		cluster       v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &priorityClass); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	hostPriorityClass := r.translatePriorityClass(priorityClass)

	if err := controllerutil.SetOwnerReference(&cluster, hostPriorityClass, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !priorityClass.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		// TODO add test for previous implementation without err != nil check, and also check the other controllers
		if err := r.HostClient.Delete(ctx, hostPriorityClass); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.RemoveFinalizer(&priorityClass, priorityClassFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &priorityClass); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&priorityClass, priorityClassFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &priorityClass); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create the priorityClass on the host
	log.Info("creating the priorityClass for the first time on the host cluster")

	err := r.HostClient.Create(ctx, hostPriorityClass)
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, r.HostClient.Update(ctx, hostPriorityClass)
	}

	return reconcile.Result{}, nil
}

func (r *PriorityClassSyncer) translatePriorityClass(priorityClass schedulingv1.PriorityClass) *schedulingv1.PriorityClass {
	hostPriorityClass := priorityClass.DeepCopy()
	r.Translator.TranslateTo(hostPriorityClass)

	if hostPriorityClass.Annotations == nil {
		hostPriorityClass.Annotations = make(map[string]string)
	}

	if hostPriorityClass.GlobalDefault {
		hostPriorityClass.GlobalDefault = false
		hostPriorityClass.Annotations[PriorityClassGlobalDefaultAnnotation] = "true"
	}

	return hostPriorityClass
}
