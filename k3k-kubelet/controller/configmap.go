package controller

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ConfigMapSyncer struct {
	sync.RWMutex
	// VirtualClient is the client for the virtual cluster
	VirtualClient client.Client
	// CoreClient is the client for the host cluster
	HostClient client.Client
	// TranslateFunc is the function that translates a given resource from it's virtual representation to the host
	// representation
	TranslateFunc func(*corev1.ConfigMap) (*corev1.ConfigMap, error)
	// objs are the objects that the syncer should watch/syncronize. Should only be manipulated
	// through add/remove
	objs sets.Set[types.NamespacedName]
}

// Reconcile implements reconcile.Reconciler and synchronizes the objects in objs to the host cluster
func (s *ConfigMapSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if !s.isWatching(req.NamespacedName) {
		// return immediately without re-enqueueing. We aren't watching this resource
		return reconcile.Result{}, nil
	}
	var virtual corev1.ConfigMap
	err := s.VirtualClient.Get(ctx, req.NamespacedName, &virtual)
	if err != nil {
		return reconcile.Result{
			Requeue: true,
		}, fmt.Errorf("unable to get configmap %s/%s from virtual cluster: %w", req.Namespace, req.Name, err)
	}
	translated, err := s.TranslateFunc(&virtual)
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
	err = s.HostClient.Get(ctx, translatedKey, &host)
	if apierrors.IsNotFound(err) {
		err = s.HostClient.Create(ctx, translated)
		// for simplicity's sake, we don't check for conflict errors. The existing object will get
		// picked up on in the next re-enqueue
		return reconcile.Result{
				Requeue: true,
			}, fmt.Errorf("unable to create host configmap %s/%s for virtual configmap %s/%s: %w",
				translated.Namespace, translated.Name, req.Namespace, req.Name, err)
	}
	// we are going to use the host in order to avoid conflicts on update
	host.Data = translated.Data
	if host.Labels == nil {
		host.Labels = map[string]string{}
	}
	// we don't want to override labels made on the host cluster by other applications
	// but we do need to make sure the labels that the kubelet uses to track host cluster values
	// are being tracked appropriately
	for key, value := range translated.Labels {
		host.Labels[key] = value
	}
	err = s.HostClient.Update(ctx, &host)
	if err != nil {
		return reconcile.Result{
				Requeue: true,
			}, fmt.Errorf("unable to update host configmap %s/%s for virtual configmap %s/%s: %w",
				translated.Namespace, translated.Name, req.Namespace, req.Name, err)

	}
	return reconcile.Result{}, nil
}

// isWatching is a utility method to determine if a key is in objs without the caller needing
// to handle mutex lock/unlock.
func (s *ConfigMapSyncer) isWatching(key types.NamespacedName) bool {
	s.RLock()
	defer s.RUnlock()
	return s.objs.Has(key)
}

// Add adds a given resource to the list of resources that will be synced. Safe to call multiple times for the
// same resource.
func (s *ConfigMapSyncer) AddResource(ctx context.Context, namespace string, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	// if we already sync this object, no need to writelock/add it
	if s.isWatching(objKey) {
		return nil
	}
	// lock in write mode since we are now adding the key
	s.Lock()
	if s.objs == nil {
		s.objs = sets.Set[types.NamespacedName]{}
	}
	s.objs = s.objs.Insert(objKey)
	s.Unlock()
	_, err := s.Reconcile(ctx, reconcile.Request{
		NamespacedName: objKey,
	})
	if err != nil {
		return fmt.Errorf("unable to reconcile new object %s/%s: %w", objKey.Namespace, objKey.Name, err)
	}
	return nil
}

// Remove removes a given resource from the list of resources that will be synced. Safe to call for an already
// removed resource.
func (s *ConfigMapSyncer) RemoveResource(ctx context.Context, namespace string, name string) error {
	objKey := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	// if we don't sync this object, no need to writelock/add it
	if !s.isWatching(objKey) {
		return nil
	}
	// lock in write mode since we are now adding the key
	err := s.removeHostConfigMap(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("unable to remove host configmap: %w", err)
	}
	s.Lock()
	if s.objs == nil {
		s.objs = sets.Set[types.NamespacedName]{}
	}
	s.objs = s.objs.Delete(objKey)
	s.Unlock()
	return nil
}

func (s *ConfigMapSyncer) removeHostConfigMap(ctx context.Context, virtualNamespace string, virtualName string) error {
	var vConfigMap corev1.ConfigMap
	err := s.VirtualClient.Get(ctx, types.NamespacedName{
		Namespace: virtualNamespace,
		Name:      virtualName,
	}, &vConfigMap)
	if err != nil {
		return fmt.Errorf("unable to get virtual configmap %s/%s: %w", virtualNamespace, virtualName, err)
	}
	translated, err := s.TranslateFunc(&vConfigMap)
	if err != nil {
		return fmt.Errorf("unable to translate virtual secret: %s/%s: %w", virtualNamespace, virtualName, err)
	}
	return s.HostClient.Delete(ctx, translated)
}
