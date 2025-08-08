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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller"
)

const (
	configMapControllerName = "configmap-syncer"
	configMapFinalizerName  = "configmap.k3k.io/finalizer"
)

type ConfigMapSyncer struct {
	mutex sync.RWMutex
	// objs are the objects that the syncer should watch/syncronize. Should only be manipulated
	// through add/remove
	objs sets.Set[types.NamespacedName]
	// Global determines if the syncer is only working with active resources or not
	Global bool

	// SyncerContext contains all client information for host and virtual cluster
	*SyncerContext
}

func (c *ConfigMapSyncer) Name() string {
	return configMapControllerName
}

// AddConfigMapSyncer adds configmap syncer controller to the manager of the virtual cluster
func AddConfigMapSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, configMapSyncConfig v1alpha1.ConfigMapSyncConfig) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := ConfigMapSyncer{
		SyncerContext: &SyncerContext{
			Virtual: &ClusterClient{
				Client: virtMgr.GetClient(),
			},
			Host: &ClusterClient{
				Client: hostMgr.GetClient(),
			},
			Translator: translator,
		},
		Global: true,
	}

	labelSelector := labels.SelectorFromSet(configMapSyncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	name := reconciler.Translator.TranslateName(clusterNamespace, configMapControllerName)

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(name).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return labelSelector.Matches(labels.Set(object.GetLabels()))
		})).
		Complete(&reconciler)
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (c *ConfigMapSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("cluster", c.ClusterName, "clusterNamespace", c.ClusterName)
	ctx = ctrl.LoggerInto(ctx, log)

	if !c.isWatching(req.NamespacedName) && !c.Global {
		// return immediately without re-enqueueing. We aren't watching this resource
		return reconcile.Result{}, nil
	}

	var virtualConfigMap corev1.ConfigMap

	if err := c.Virtual.Client.Get(ctx, req.NamespacedName, &virtualConfigMap); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	syncedConfigMap := c.translateConfigMap(&virtualConfigMap)

	// handle deletion
	if !virtualConfigMap.DeletionTimestamp.IsZero() {
		// deleting the synced configMap if exist
		if err := c.Host.Client.Delete(ctx, syncedConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		// remove the finalizer after cleaning up the synced configMap
		if controllerutil.RemoveFinalizer(&virtualConfigMap, configMapFinalizerName) {
			if err := c.Virtual.Client.Update(ctx, &virtualConfigMap); err != nil {
				return reconcile.Result{}, err
			}
		}

		return reconcile.Result{}, nil
	}

	// Add finalizer if it does not exist
	if controllerutil.AddFinalizer(&virtualConfigMap, configMapFinalizerName) {
		if err := c.Virtual.Client.Update(ctx, &virtualConfigMap); err != nil {
			return reconcile.Result{}, err
		}
	}

	var hostConfigMap corev1.ConfigMap
	if err := c.Host.Client.Get(ctx, types.NamespacedName{Name: syncedConfigMap.Name, Namespace: syncedConfigMap.Namespace}, &hostConfigMap); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating the ConfigMap for the first time on the host cluster")
			return reconcile.Result{}, c.Host.Client.Create(ctx, syncedConfigMap)
		}

		return reconcile.Result{}, err
	}

	// TODO: Add option to keep labels/annotation set by the host cluster
	log.Info("updating ConfigMap on the host cluster")

	return reconcile.Result{}, c.Host.Client.Update(ctx, syncedConfigMap)
}

// isWatching is a utility method to determine if a key is in objs without the caller needing
// to handle mutex lock/unlock.
func (c *ConfigMapSyncer) isWatching(key types.NamespacedName) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.objs.Has(key)
}

// AddResource adds a given resource to the list of resources that will be synced. Safe to call multiple times for the
// same resource.
func (c *ConfigMapSyncer) AddResource(ctx context.Context, namespace, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}

	// if we already sync this object, no need to writelock/add it
	if c.isWatching(objKey) {
		return nil
	}

	// lock in write mode since we are now adding the key
	c.mutex.Lock()

	if c.objs == nil {
		c.objs = sets.Set[types.NamespacedName]{}
	}

	c.objs = c.objs.Insert(objKey)
	c.mutex.Unlock()

	_, err := c.Reconcile(ctx, reconcile.Request{
		NamespacedName: objKey,
	})
	if err != nil {
		return fmt.Errorf("unable to reconcile new object %s/%s: %w", objKey.Namespace, objKey.Name, err)
	}

	return nil
}

// RemoveResource removes a given resource from the list of resources that will be synced. Safe to call for an already
// removed resource.
func (c *ConfigMapSyncer) RemoveResource(ctx context.Context, namespace, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	// if we don't sync this object, no need to writelock/add it
	if !c.isWatching(objKey) {
		return nil
	}

	if err := retry.OnError(controller.Backoff, func(err error) bool {
		return err != nil
	}, func() error {
		return c.removeHostConfigMap(ctx, namespace, name)
	}); err != nil {
		return fmt.Errorf("unable to remove configmap: %w", err)
	}

	c.mutex.Lock()

	if c.objs == nil {
		c.objs = sets.Set[types.NamespacedName]{}
	}

	c.objs = c.objs.Delete(objKey)
	c.mutex.Unlock()

	return nil
}

func (c *ConfigMapSyncer) removeHostConfigMap(ctx context.Context, virtualNamespace, virtualName string) error {
	var vConfigMap corev1.ConfigMap

	key := types.NamespacedName{
		Namespace: virtualNamespace,
		Name:      virtualName,
	}

	if err := c.Virtual.Client.Get(ctx, key, &vConfigMap); err != nil {
		return fmt.Errorf("unable to get virtual configmap %s/%s: %w", virtualNamespace, virtualName, err)
	}

	translated := vConfigMap.DeepCopy()

	c.Translator.TranslateTo(translated)

	return c.Host.Client.Delete(ctx, translated)
}

// translateConfigMap will translate a given configMap created in the virtual cluster and
// translates it to host cluster object
func (c *ConfigMapSyncer) translateConfigMap(configMap *corev1.ConfigMap) *corev1.ConfigMap {
	hostConfigMap := configMap.DeepCopy()
	c.Translator.TranslateTo(hostConfigMap)

	return hostConfigMap
}
