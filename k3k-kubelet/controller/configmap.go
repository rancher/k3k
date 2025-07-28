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

const ConfigMapSyncerName = "configmap-syncer"

type ConfigMapSyncer struct {
	mutex sync.RWMutex
	// VirtualClient is the client for the virtual cluster
	VirtualClient client.Client
	// CoreClient is the client for the host cluster
	HostClient client.Client
	// TranslateFunc is the function that translates a given resource from it's virtual representation to the host
	// representation
	TranslateFunc func(*corev1.ConfigMap) (*corev1.ConfigMap, error)
	// Logger is the logger that the controller will use
	Logger *k3klog.Logger
	// Scheme is the scheme of the virtual manager
	Scheme *runtime.Scheme
	// Scheme is the scheme of the host manager
	HostScheme *runtime.Scheme
	// objs are the objects that the syncer should watch/syncronize. Should only be manipulated
	// through add/remove
	objs sets.Set[types.NamespacedName]
	// Global determines if the syncer is only working with active resources or not
	Global bool
}

func (c *ConfigMapSyncer) Name() string {
	return ConfigMapSyncerName
}

// AddConfigMapSyncer adds configmap syncer controller to the manager of the virtual cluster
func AddConfigMapSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, configMapSyncConfig v1alpha1.ConfigMapSyncConfig) error {
	translator := translate.ToHostTranslator{
		ClusterName:      clusterName,
		ClusterNamespace: clusterNamespace,
	}

	reconciler := ConfigMapSyncer{
		VirtualClient: virtMgr.GetClient(),
		HostClient:    hostMgr.GetClient(),
		Scheme:        virtMgr.GetScheme(),
		HostScheme:    hostMgr.GetScheme(),
		TranslateFunc: func(cm *corev1.ConfigMap) (*corev1.ConfigMap, error) {
			translator.TranslateTo(cm)
			return cm, nil
		},
		Global: true,
	}

	labelSelector := labels.SelectorFromSet(configMapSyncConfig.Selector)
	if labelSelector.Empty() {
		labelSelector = labels.Everything()
	}

	return ctrl.NewControllerManagedBy(virtMgr).
		Named(ConfigMapSyncerName).
		For(&corev1.ConfigMap{}).WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
		return labelSelector.Matches(labels.Set(object.GetLabels()))
	})).
		Complete(&reconciler)
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (c *ConfigMapSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if !c.isWatching(req.NamespacedName) && !c.Global {
		// return immediately without re-enqueueing. We aren't watching this resource
		return reconcile.Result{}, nil
	}

	var virtual corev1.ConfigMap

	if err := c.VirtualClient.Get(ctx, req.NamespacedName, &virtual); err != nil {
		return reconcile.Result{
			Requeue: true,
		}, fmt.Errorf("unable to get configmap %s/%s from virtual cluster: %w", req.Namespace, req.Name, err)
	}

	translated, err := c.TranslateFunc(&virtual)
	if err != nil {
		return reconcile.Result{
			Requeue: true,
		}, fmt.Errorf("unable to translate configmap %s/%s from virtual cluster: %w", req.Namespace, req.Name, err)
	}

	translatedKey := types.NamespacedName{
		Namespace: translated.Namespace,
		Name:      translated.Name,
	}

	var host corev1.ConfigMap
	if err = c.HostClient.Get(ctx, translatedKey, &host); err != nil {
		if apierrors.IsNotFound(err) {
			err = c.HostClient.Create(ctx, translated)
			// for simplicity's sake, we don't check for conflict errors. The existing object will get
			// picked up on in the next re-enqueue
			return reconcile.Result{
					Requeue: true,
				}, fmt.Errorf("unable to create host configmap %s/%s for virtual configmap %s/%s: %w",
					translated.Namespace, translated.Name, req.Namespace, req.Name, err)
		}

		return reconcile.Result{Requeue: true}, fmt.Errorf("unable to get host configmap %s/%s: %w", translated.Namespace, translated.Name, err)
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

	if err = c.HostClient.Update(ctx, &host); err != nil {
		return reconcile.Result{
				Requeue: true,
			}, fmt.Errorf("unable to update host configmap %s/%s for virtual configmap %s/%s: %w",
				translated.Namespace, translated.Name, req.Namespace, req.Name, err)
	}

	return reconcile.Result{}, nil
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

	if err := c.VirtualClient.Get(ctx, key, &vConfigMap); err != nil {
		return fmt.Errorf("unable to get virtual configmap %s/%s: %w", virtualNamespace, virtualName, err)
	}

	translated, err := c.TranslateFunc(&vConfigMap)
	if err != nil {
		return fmt.Errorf("unable to translate virtual secret: %s/%s: %w", virtualNamespace, virtualName, err)
	}

	return c.HostClient.Delete(ctx, translated)
}
