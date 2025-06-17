package controller

import (
	"context"
	"strings"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	PriorityClassGlobalDefaultAnnotation = "priorityclass.k3k.io/globalDefault"

	priorityClassControllerName = "priorityclass-syncer-controller"
	priorityClassFinalizerName  = "priorityclass.k3k.io/finalizer"
)

type PriorityClassReconciler struct {
	clusterName      string
	clusterNamespace string

	virtualClient ctrlruntimeclient.Client
	hostClient    ctrlruntimeclient.Client
	Scheme        *runtime.Scheme
	HostScheme    *runtime.Scheme
	Translator    translate.ToHostTranslator
}

// AddPriorityClassReconciler adds a PriorityClass reconciler to k3k-kubelet
func AddPriorityClassReconciler(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	// initialize a new Reconciler
	reconciler := PriorityClassReconciler{
		clusterName:      clusterName,
		clusterNamespace: clusterNamespace,

		virtualClient: virtMgr.GetClient(),
		hostClient:    hostMgr.GetClient(),
		Scheme:        virtMgr.GetScheme(),
		HostScheme:    hostMgr.GetScheme(),
		Translator:    translator,
	}

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(priorityClassControllerName).
		For(&schedulingv1.PriorityClass{}).
		WithEventFilter(ignoreSystemPrefixPredicate).
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

func (r *PriorityClassReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.clusterName, "clusterNamespace", r.clusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		priorityClass schedulingv1.PriorityClass
		cluster       v1alpha1.Cluster
	)

	if err := r.hostClient.Get(ctx, types.NamespacedName{Name: r.clusterName, Namespace: r.clusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.virtualClient.Get(ctx, req.NamespacedName, &priorityClass); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	hostPriorityClass := r.translatePriorityClass(priorityClass)

	// handle deletion
	if !priorityClass.DeletionTimestamp.IsZero() {
		// deleting the synced service if exists
		// TODO add test for previous implementation without err != nil check, and also check the other controllers
		if err := r.hostClient.Delete(ctx, hostPriorityClass); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced service
		if controllerutil.RemoveFinalizer(&priorityClass, priorityClassFinalizerName) {
			if err := r.virtualClient.Update(ctx, &priorityClass); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&priorityClass, priorityClassFinalizerName) {
		if err := r.virtualClient.Update(ctx, &priorityClass); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create the priorityClass on the host
	log.Info("creating the priorityClass for the first time on the host cluster")

	err := r.hostClient.Create(ctx, hostPriorityClass)
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *PriorityClassReconciler) translatePriorityClass(priorityClass schedulingv1.PriorityClass) *schedulingv1.PriorityClass {
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
