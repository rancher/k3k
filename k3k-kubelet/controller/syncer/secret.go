package syncer

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	SecretControllerName = "secret-syncer"
	secretFinalizerName  = "secret.k3k.io/finalizer"
)

type SecretSyncer struct {
	mutex sync.RWMutex
	// objs are the objects that the syncer should watch/syncronize. Should only be manipulated
	// through add/remove
	objs sets.Set[types.NamespacedName]
	// Global determines if the syncer is only working with active resources or not
	Global bool

	// SyncerContext contains all client information for host and virtual cluster
	*SyncerContext
}

func (s *SecretSyncer) Name() string {
	return SecretControllerName
}

// AddConfigMapSyncer adds configmap syncer controller to the manager of the virtual cluster
func AddSecretSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, secretSyncConfig v1alpha1.SecretSyncConfig) error {
	reconciler := SecretSyncer{
		SyncerContext: &SyncerContext{
			Virtual: &ClusterClient{
				Manager: virtMgr,
				Client:  virtMgr.GetClient(),
				Scheme:  virtMgr.GetScheme(),
			},
			Host: &ClusterClient{
				Manager: hostMgr,
				Client:  hostMgr.GetClient(),
				Scheme:  hostMgr.GetScheme(),
			},
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
		Global: true,
	}

	labelSelector := labels.SelectorFromSet(secretSyncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	name := reconciler.Translator.TranslateName("", SecretControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&v1.Secret{}).WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
		return labelSelector.Matches(labels.Set(object.GetLabels()))
	})).
		Complete(&reconciler)
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (s *SecretSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", s.ClusterName, "clusterNamespace", s.ClusterName)
	ctx = ctrl.LoggerInto(ctx, log)

	if !s.isWatching(req.NamespacedName) && !s.Global {
		// return immediately without re-enqueueing. We aren't watching this resource
		return reconcile.Result{}, nil
	}

	var virtualSecret v1.Secret

	if err := s.Virtual.Client.Get(ctx, req.NamespacedName, &virtualSecret); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	syncedSecret := s.translateSecret(&virtualSecret)

	// handle deletion
	if !virtualSecret.DeletionTimestamp.IsZero() {
		// deleting the synced secret if exist
		if err := s.Host.Client.Delete(ctx, syncedSecret); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced configMap
		if controllerutil.RemoveFinalizer(&virtualSecret, secretFinalizerName) {
			if err := s.Virtual.Client.Update(ctx, &virtualSecret); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtualSecret, secretFinalizerName) {
		if err := s.Virtual.Client.Update(ctx, &virtualSecret); err != nil {
			return reconcile.Result{}, err
		}
	}

	var hostSecret v1.Secret
	if err := s.Host.Client.Get(ctx, types.NamespacedName{Name: syncedSecret.Name, Namespace: syncedSecret.Namespace}, &hostSecret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the Secret for the first time on the host cluster")
			return reconcile.Result{}, s.Host.Client.Create(ctx, syncedSecret)
		}

		return reconcile.Result{}, err
	}

	// TODO: Add option to keep labels/annotation set by the host cluster
	log.Info("updating Secret on the host cluster")

	return reconcile.Result{}, s.Host.Client.Update(ctx, syncedSecret)
}

// isWatching is a utility method to determine if a key is in objs without the caller needing
// to handle mutex lock/unlock.
func (s *SecretSyncer) isWatching(key types.NamespacedName) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.objs.Has(key)
}

// AddResource adds a given resource to the list of resources that will be synced. Safe to call multiple times for the
// same resource.
func (s *SecretSyncer) AddResource(ctx context.Context, namespace, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}

	// if we already sync this object, no need to writelock/add it
	if s.isWatching(objKey) {
		return nil
	}

	// lock in write mode since we are now adding the key
	s.mutex.Lock()

	if s.objs == nil {
		s.objs = sets.Set[types.NamespacedName]{}
	}

	s.objs = s.objs.Insert(objKey)
	s.mutex.Unlock()

	_, err := s.Reconcile(ctx, reconcile.Request{
		NamespacedName: objKey,
	})
	if err != nil {
		return fmt.Errorf("unable to reconcile new object %s/%s: %w", objKey.Namespace, objKey.Name, err)
	}

	return nil
}

// RemoveResource removes a given resource from the list of resources that will be synced. Safe to call for an already
// removed resource.
func (s *SecretSyncer) RemoveResource(ctx context.Context, namespace, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	// if we don't sync this object, no need to writelock/add it
	if !s.isWatching(objKey) {
		return nil
	}
	// lock in write mode since we are now adding the key
	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		return s.removeHostSecret(ctx, namespace, name)
	}); err != nil {
		return fmt.Errorf("unable to remove secret: %w", err)
	}

	s.mutex.Lock()

	if s.objs == nil {
		s.objs = sets.Set[types.NamespacedName]{}
	}

	s.objs = s.objs.Delete(objKey)
	s.mutex.Unlock()

	return nil
}

func (s *SecretSyncer) removeHostSecret(ctx context.Context, virtualNamespace, virtualName string) error {
	var vSecret v1.Secret

	err := s.Virtual.Client.Get(ctx, types.NamespacedName{
		Namespace: virtualNamespace,
		Name:      virtualName,
	}, &vSecret)
	if err != nil {
		return fmt.Errorf("unable to get virtual secret %s/%s: %w", virtualNamespace, virtualName, err)
	}

	translated := vSecret.DeepCopy()

	s.Translator.TranslateTo(translated)

	return s.Host.Client.Delete(ctx, translated)
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
