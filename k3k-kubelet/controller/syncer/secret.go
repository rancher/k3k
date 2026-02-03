package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	secretControllerName = "secret-syncer"
	secretFinalizerName  = "secret.k3k.io/finalizer"
)

type SecretSyncer struct {
	// SyncerContext contains all client information for host and virtual cluster
	*SyncerContext
}

func (s *SecretSyncer) Name() string {
	return secretControllerName
}

// AddSecretSyncer adds secret syncer controller to the manager of the virtual cluster
func AddSecretSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := SecretSyncer{
		SyncerContext: &SyncerContext{
			VirtualClient: virtMgr.GetClient(),
			HostClient:    hostMgr.GetClient(),
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
		},
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, secretControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&v1.Secret{}).WithEventFilter(predicate.NewPredicateFuncs(reconciler.filterResources)).
		Complete(&reconciler)
}

func (r *SecretSyncer) filterResources(object client.Object) bool {
	var cluster v1beta1.Cluster

	ctx := context.Background()

	if err := r.HostClient.Get(ctx, types.NamespacedName{Name: r.ClusterName, Namespace: r.ClusterNamespace}, &cluster); err != nil {
		return false
	}

	// check for Secrets Sync Config
	syncConfig := cluster.Spec.Sync.Secrets

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

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (s *SecretSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", s.ClusterName, "clusterNamespace", s.ClusterName)
	ctx = ctrl.LoggerInto(ctx, log)

	var cluster v1beta1.Cluster

	if err := s.HostClient.Get(ctx, types.NamespacedName{Name: s.ClusterName, Namespace: s.ClusterNamespace}, &cluster); err != nil {
		return reconcile.Result{}, err
	}

	var virtualSecret v1.Secret

	if err := s.VirtualClient.Get(ctx, req.NamespacedName, &virtualSecret); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	syncedSecret := s.translateSecret(&virtualSecret)

	if err := controllerutil.SetOwnerReference(&cluster, syncedSecret, s.HostClient.Scheme()); err != nil {
		return reconcile.Result{}, err
	}

	// handle deletion
	if !virtualSecret.DeletionTimestamp.IsZero() {
		// deleting the synced secret if exist
		if err := s.HostClient.Delete(ctx, syncedSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced secret
		if controllerutil.RemoveFinalizer(&virtualSecret, secretFinalizerName) {
			if err := s.VirtualClient.Update(ctx, &virtualSecret); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtualSecret, secretFinalizerName) {
		if err := s.VirtualClient.Update(ctx, &virtualSecret); err != nil {
			return reconcile.Result{}, err
		}
	}

	var hostSecret v1.Secret
	if err := s.HostClient.Get(ctx, types.NamespacedName{Name: syncedSecret.Name, Namespace: syncedSecret.Namespace}, &hostSecret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the Secret for the first time on the host cluster")
			return reconcile.Result{}, s.HostClient.Create(ctx, syncedSecret)
		}

		return reconcile.Result{}, err
	}

	// TODO: Add option to keep labels/annotation set by the host cluster
	log.Info("updating Secret on the host cluster")

	return reconcile.Result{}, s.HostClient.Update(ctx, syncedSecret)
}

// translateSecret will translate a given secret created in the virtual cluster and
// translates it to host cluster object
func (s *SecretSyncer) translateSecret(secret *v1.Secret) *v1.Secret {
	hostSecret := secret.DeepCopy()

	if hostSecret.Type == v1.SecretTypeServiceAccountToken {
		hostSecret.Type = v1.SecretTypeOpaque
	}

	s.Translator.TranslateTo(hostSecret)

	return hostSecret
}
