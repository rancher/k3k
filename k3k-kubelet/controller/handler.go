package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	k3klog "github.com/rancher/k3k/pkg/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ControllerHandler struct {
	sync.RWMutex
	// Mgr is the manager used to run new controllers - from the virtual cluster
	Mgr manager.Manager
	// Scheme is the scheme used to run new controllers - from the virtual cluster
	Scheme runtime.Scheme
	// HostClient is the client used to communicate with the host cluster
	HostClient client.Client
	// VirtualClient is the client used to communicate with the virtual cluster
	VirtualClient client.Client
	// Translater is the translater that will be used to adjust objects before they
	// are made on the host cluster
	Translater translate.ToHostTranslater
	// Logger is the logger that the controller will use to log errors
	Logger *k3klog.Logger
	// controllers are the controllers which are currently running
	controllers map[schema.GroupVersionKind]updateableReconciler
}

// updateableReconciler is a reconciler that only syncs specific resources (by name/namespace). This list can
// be altered through the Add and Remove methods
type updateableReconciler interface {
	reconcile.Reconciler
	AddResource(ctx context.Context, namespace string, name string) error
	RemoveResource(ctx context.Context, namespace string, name string) error
}

func (c *ControllerHandler) AddResource(ctx context.Context, obj client.Object) error {
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
			HostClient:    c.HostClient,
			VirtualClient: c.VirtualClient,
			// TODO: Need actual function
			TranslateFunc: func(s *v1.Secret) (*v1.Secret, error) {
				// note that this doesn't do any type safety - fix this
				// when generics work
				c.Translater.TranslateTo(s)
				return s, nil
			},
			logger: c.Logger,
		}
	case *v1.ConfigMap:
		r = &ConfigMapSyncer{
			HostClient:    c.HostClient,
			VirtualClient: c.VirtualClient,
			// TODO: Need actual function
			TranslateFunc: func(s *v1.ConfigMap) (*v1.ConfigMap, error) {
				c.Translater.TranslateTo(s)
				return s, nil
			},
			Logger: c.Logger,
		}
	default:
		return fmt.Errorf("unrecognized type: %T", obj)

	}

	if err := ctrl.NewControllerManagedBy(c.Mgr).
		For(&v1.ConfigMap{}).
		Complete(r); err != nil {
		return err
	}

	c.Lock()
	if c.controllers == nil {
		c.controllers = map[schema.GroupVersionKind]updateableReconciler{}
	}
	c.controllers[obj.GetObjectKind().GroupVersionKind()] = r
	c.Unlock()

	return r.AddResource(ctx, obj.GetNamespace(), obj.GetName())
}

func (c *ControllerHandler) RemoveResource(ctx context.Context, obj client.Object) error {
	// since we aren't adding a new controller, we don't need to lock
	c.RLock()
	ctrl, ok := c.controllers[obj.GetObjectKind().GroupVersionKind()]
	c.RUnlock()
	if !ok {
		return errors.New("no controller found for gvk" + obj.GetObjectKind().GroupVersionKind().String())
	}

	return ctrl.RemoveResource(ctx, obj.GetNamespace(), obj.GetName())
}
