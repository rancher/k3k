package syncer

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	k3klog "github.com/rancher/k3k/pkg/log"
)

type SyncerContext struct {
	ClusterName      string
	ClusterNamespace string
	VirtualClient    client.Client
	HostClient       client.Client
	Translator       translate.ToHostTranslator
}

type GenericControllerHandler struct {
	sync.RWMutex
	// Logger is the logger that the controller will use to log errors
	Logger *k3klog.Logger
	// controllers are the controllers which are currently running
	controllers map[schema.GroupVersionKind]updateableReconciler

	*SyncerContext
}

// updateableReconciler is a reconciler that only syncs specific resources (by name/namespace). This list can
// be altered through the Add and Remove methods
type updateableReconciler interface {
	reconcile.Reconciler
	Name() string
	AddResource(ctx context.Context, namespace string, name string) error
	RemoveResource(ctx context.Context, namespace string, name string) error
}

func (c *GenericControllerHandler) AddResource(ctx context.Context, obj client.Object, virtualManager manager.Manager) error {
	c.RLock()

	controllers := c.controllers
	if controllers != nil {
		if r, ok := c.controllers[obj.GetObjectKind().GroupVersionKind()]; ok {
			err := r.AddResource(ctx, obj.GetNamespace(), obj.GetName())

			c.RUnlock()

			return err
		}
	}

	// we need to manually lock/unlock since we intned on write locking to add a new controller
	c.RUnlock()

	var r updateableReconciler

	switch obj.(type) {
	case *v1.Secret:
		r = &SecretSyncer{
			SyncerContext: c.SyncerContext,
		}
	case *v1.ConfigMap:
		r = &ConfigMapSyncer{
			SyncerContext: c.SyncerContext,
		}
	default:
		// TODO: Technically, the configmap/secret syncers are relatively generic, and this
		// logic could be used for other types.
		return fmt.Errorf("unrecognized type: %T", obj)
	}

	err := ctrl.NewControllerManagedBy(virtualManager).
		Named(r.Name()).
		For(&v1.ConfigMap{}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("unable to start configmap controller: %w", err)
	}

	c.Lock()

	if c.controllers == nil {
		c.controllers = map[schema.GroupVersionKind]updateableReconciler{}
	}

	c.controllers[obj.GetObjectKind().GroupVersionKind()] = r

	c.Unlock()

	return r.AddResource(ctx, obj.GetNamespace(), obj.GetName())
}

func (c *GenericControllerHandler) RemoveResource(ctx context.Context, obj client.Object) error {
	// since we aren't adding a new controller, we don't need to lock
	c.RLock()
	ctrl, ok := c.controllers[obj.GetObjectKind().GroupVersionKind()]
	c.RUnlock()

	if !ok {
		return fmt.Errorf("no controller found for gvk %s", obj.GetObjectKind().GroupVersionKind())
	}

	return ctrl.RemoveResource(ctx, obj.GetNamespace(), obj.GetName())
}
