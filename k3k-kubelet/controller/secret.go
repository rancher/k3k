package controller

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
	k3klog "github.com/rancher/k3k/pkg/log"
)

const SecretSyncerName = "secret-syncer"

type SecretSyncer struct {
	mutex sync.RWMutex
	// VirtualClient is the client for the virtual cluster
	VirtualClient client.Client
	// CoreClient is the client for the host cluster
	HostClient client.Client
	// Scheme is the scheme of the virtual manager
	Scheme *runtime.Scheme
	// Scheme is the scheme of the host manager
	HostScheme *runtime.Scheme
	// TranslateFunc is the function that translates a given resource from it's virtual representation to the host
	// representation
	TranslateFunc func(*corev1.Secret) (*corev1.Secret, error)
	// Logger is the logger that the controller will use
	Logger *k3klog.Logger
	// objs are the objects that the syncer should watch/syncronize. Should only be manipulated
	// through add/remove
	objs sets.Set[types.NamespacedName]
	// Global determines if the syncer is only working with active resources or not
	Global bool
}

func (s *SecretSyncer) Name() string {
	return SecretSyncerName
}

// AddConfigMapSyncer adds configmap syncer controller to the manager of the virtual cluster
func AddSecretSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, secretSyncConfig v1alpha1.SecretSyncConfig) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := SecretSyncer{
		VirtualClient: virtMgr.GetClient(),
		HostClient:    hostMgr.GetClient(),
		Scheme:        virtMgr.GetScheme(),
		HostScheme:    hostMgr.GetScheme(),
		TranslateFunc: func(cm *corev1.Secret) (*corev1.Secret, error) {
			translator.TranslateTo(cm)
			return cm, nil
		},
		Global: true,
	}

	labelSelector := labels.SelectorFromSet(secretSyncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(SecretSyncerName).
		For(&corev1.Secret{}).WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
		return labelSelector.Matches(labels.Set(object.GetLabels()))
	})).
		Complete(&reconciler)
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (s *SecretSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if !s.isWatching(req.NamespacedName) && !s.Global {
		// return immediately without re-enqueueing. We aren't watching this resource
		return reconcile.Result{}, nil
	}

	var virtual corev1.Secret

	if err := s.VirtualClient.Get(ctx, req.NamespacedName, &virtual); err != nil {
		return reconcile.Result{
			Requeue: true,
		}, fmt.Errorf("unable to get secret %s/%s from virtual cluster: %w", req.Namespace, req.Name, err)
	}

	translated, err := s.TranslateFunc(&virtual)
	if err != nil {
		return reconcile.Result{
			Requeue: true,
		}, fmt.Errorf("unable to translate secret %s/%s from virtual cluster: %w", req.Namespace, req.Name, err)
	}

	translatedKey := types.NamespacedName{
		Namespace: translated.Namespace,
		Name:      translated.Name,
	}

	var host corev1.Secret
	if err = s.HostClient.Get(ctx, translatedKey, &host); err != nil {
		if apierrors.IsNotFound(err) {
			err = s.HostClient.Create(ctx, translated)
			// for simplicity's sake, we don't check for conflict errors. The existing object will get
			// picked up on in the next re-enqueue
			return reconcile.Result{
					Requeue: true,
				}, fmt.Errorf("unable to create host secret %s/%s for virtual secret %s/%s: %w",
					translated.Namespace, translated.Name, req.Namespace, req.Name, err)
		}

		return reconcile.Result{Requeue: true}, fmt.Errorf("unable to get host secret %s/%s: %w", translated.Namespace, translated.Name, err)
	}
	// we are going to use the host in order to avoid conflicts on update
	host.Data = translated.Data
	if host.Labels == nil {
		host.Labels = make(map[string]string, len(translated.Labels))
	}
	// we don't want to override labels made on the host cluster by other applications
	// but we do need to make sure the labels that the kubelet uses to track host cluster values
	// are being tracked appropriately
	for key, value := range translated.Labels {
		host.Labels[key] = value
	}

	if err = s.HostClient.Update(ctx, &host); err != nil {
		return reconcile.Result{
				Requeue: true,
			}, fmt.Errorf("unable to update host secret %s/%s for virtual secret %s/%s: %w",
				translated.Namespace, translated.Name, req.Namespace, req.Name, err)
	}

	return reconcile.Result{}, nil
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
	var vSecret corev1.Secret

	err := s.VirtualClient.Get(ctx, types.NamespacedName{
		Namespace: virtualNamespace,
		Name:      virtualName,
	}, &vSecret)
	if err != nil {
		return fmt.Errorf("unable to get virtual secret %s/%s: %w", virtualNamespace, virtualName, err)
	}

	translated, err := s.TranslateFunc(&vSecret)
	if err != nil {
		return fmt.Errorf("unable to translate virtual secret: %s/%s: %w", virtualNamespace, virtualName, err)
	}

	return s.HostClient.Delete(ctx, translated)
}
