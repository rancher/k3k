package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	pdbControllerName = "pdb-syncer-controller"
	pdbFinalizerName  = "poddisruptionbudget.k3k.io/finalizer"
)

// PDBReconciler syncs PodDisruptionBudgets of the virtual cluster to the host
// cluster. Pods of a virtual cluster run as regular pods on the host, so a
// drain or eviction on the host only consults host-side PodDisruptionBudgets —
// a PDB that exists only inside the virtual cluster protects nothing. The
// synced host PDB gets its selector scoped to the pods of the originating
// virtual cluster and namespace.
type PDBReconciler struct {
	*SyncerContext
}

// AddPodDisruptionBudgetSyncer adds the PodDisruptionBudget syncer controller
// to the manager of the virtual cluster.
func AddPodDisruptionBudgetSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := PDBReconciler{
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

	name := reconciler.Translator.TranslateName(clusterNamespace, pdbControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&policyv1.PodDisruptionBudget{}).
		WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *PDBReconciler) filterResources(object ctrlruntimeclient.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	syncConfig := cluster.Spec.Sync.PodDisruptionBudgets

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

func (r *PDBReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", r.ClusterName, "clusterNamespace", r.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, log)

	var (
		virtPDB policyv1.PodDisruptionBudget
		cluster v1beta1.Cluster
	)

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.VirtualClient.Get(ctx, req.NamespacedName, &virtPDB); err != nil {
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	syncedPDB := r.pdb(&virtPDB)

	if err := controllerutil.SetOwnerReference(&cluster, syncedPDB, r.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtPDB.DeletionTimestamp.IsZero() {
		// deleting the synced pdb if it exists
		if err := r.HostClient.Delete(ctx, syncedPDB); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced pdb
		if controllerutil.RemoveFinalizer(&virtPDB, pdbFinalizerName) {
			if err := r.VirtualClient.Update(ctx, &virtPDB); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtPDB, pdbFinalizerName) {
		if err := r.VirtualClient.Update(ctx, &virtPDB); err != nil {
			return reconcile.Result{}, err
		}
	}

	// create or update the pdb on host
	var hostPDB policyv1.PodDisruptionBudget

	if err := r.HostClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(syncedPDB), &hostPDB); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the pdb for the first time on the host cluster")
			return reconcile.Result{}, r.HostClient.Create(ctx, syncedPDB)
		}

		return reconcile.Result{}, err
	}

	hostPDB.Labels = syncedPDB.Labels
	hostPDB.Annotations = syncedPDB.Annotations
	hostPDB.Spec = syncedPDB.Spec

	log.Info("updating pdb on the host cluster")

	return reconcile.Result{}, r.HostClient.Update(ctx, &hostPDB)
}

// pdb translates the virtual PodDisruptionBudget to its host cluster
// counterpart. All virtual namespaces collapse into the single host namespace
// of the cluster, so the pod selector is additionally scoped to the pods of
// the originating virtual cluster and namespace — otherwise it could match
// same-labeled pods of other virtual namespaces (or other virtual clusters
// sharing the host namespace).
func (r *PDBReconciler) pdb(obj *policyv1.PodDisruptionBudget) *policyv1.PodDisruptionBudget {
	hostPDB := obj.DeepCopy()
	r.Translator.TranslateTo(hostPDB)

	if hostPDB.Spec.Selector == nil {
		hostPDB.Spec.Selector = &metav1.LabelSelector{}
	}

	if hostPDB.Spec.Selector.MatchLabels == nil {
		hostPDB.Spec.Selector.MatchLabels = map[string]string{}
	}

	hostPDB.Spec.Selector.MatchLabels[translate.ClusterNameLabel] = r.ClusterName
	hostPDB.Spec.Selector.MatchLabels[translate.NamespaceNameLabel] = obj.Namespace

	// status is owned by the host disruption controller
	hostPDB.Status = policyv1.PodDisruptionBudgetStatus{}

	return hostPDB
}
