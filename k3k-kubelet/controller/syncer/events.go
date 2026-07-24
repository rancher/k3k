package syncer

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

const (
	eventControllerName = "event-syncer"
)

type EventSyncer struct {
	// virtEventRecorder is a K8s EventRecorder to emit events into the
	// virtual cluster.
	virtEventRecorder record.EventRecorder

	// SyncerContext contains all client information for host and virtual
	// cluster.
	*SyncerContext
}

func (s *EventSyncer) Name() string {
	return eventControllerName
}

// AddEventSyncer adds event syncer controller to the manager of the virtual
// cluster.
func AddEventSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string, virtEventRecorder record.EventRecorder) error {
	reconciler := EventSyncer{
		virtEventRecorder: virtEventRecorder,
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

	name := reconciler.Translator.TranslateName(clusterNamespace, eventControllerName)

	return ctrl.NewControllerManagedBy(hostMgr).
		Named(name).
		For(&corev1.Event{}).
		Complete(&reconciler)
}

// Reconcile implements reconcile.Reconciler and synchronizes relevant events
// between the host and virtual clusters.
func (s *EventSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithValues("cluster", s.ClusterName, "clusterNamespace", s.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, logger)

	event := &corev1.Event{}
	if err := s.HostClient.Get(ctx, req.NamespacedName, event); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(fmt.Errorf("could not load event: %w", err))
	}

	if event.InvolvedObject.Kind != "Pod" {
		return reconcile.Result{}, nil
	}

	hostPod := &corev1.Pod{}
	if err := s.HostClient.Get(ctx, client.ObjectKey{Name: event.InvolvedObject.Name, Namespace: event.InvolvedObject.Namespace}, hostPod); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("could not load host object: %w", err)
		}

		logger.V(1).Info("host object not found, skipping event emission",
			"involvedObjectName", event.InvolvedObject.Name,
			"involvedObjectNamespace", event.InvolvedObject.Namespace,
		)

		return reconcile.Result{}, nil
	}

	s.Translator.TranslateFrom(hostPod)

	if hostPod.Name == "" {
		logger.V(1).Info("Host object has no name - skipping event",
			"involvedObjectName", event.InvolvedObject.Name,
			"involvedObjectNamespace", event.InvolvedObject.Namespace,
		)

		return reconcile.Result{}, nil
	}

	// Look up the corresponding object in the virtual cluster.
	virtPod := &corev1.Pod{}
	if err := s.VirtualClient.Get(ctx, client.ObjectKey{Name: hostPod.Name, Namespace: hostPod.Namespace}, virtPod); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, fmt.Errorf("could not load virtual object: %w", err)
		}

		logger.V(1).Info("virtual object not found, skipping event emission",
			"virtualInvolvedObjectName", hostPod.Name,
			"virtualInvolvedObjectNamespace", hostPod.Namespace,
		)

		return reconcile.Result{}, nil
	}

	logger.V(1).Info("Emitting event into virtual cluster",
		"virtPodName", virtPod.GetName(), "virtPodNamespace", virtPod.GetNamespace(),
		"reason", event.Reason, "message", event.Message, "type", event.Type)

	message := translateEventMessage(
		event.Message,
		event.InvolvedObject.Name, event.InvolvedObject.Namespace,
		hostPod.Name, hostPod.Namespace,
	)
	s.virtEventRecorder.Event(virtPod, event.Type, event.Reason, message)

	return reconcile.Result{}, nil
}

// translateEventMessage replaces occurrences of the host-cluster pod name and
// "namespace/name" pattern in the event message with the corresponding
// virtual-cluster values, so the emitted event refers to virtual coordinates.
func translateEventMessage(message, hostName, hostNamespace, virtualName, virtualNamespace string) string {
	// Replace the combined "namespace/name" first to avoid a partial match
	// turning the namespace portion into the virtual namespace before the name
	// is replaced.
	if hostNamespace != "" && virtualNamespace != "" {
		message = strings.ReplaceAll(message, hostNamespace+"/"+hostName, virtualNamespace+"/"+virtualName)
	}
	// Replace any remaining standalone occurrences of the host pod name.
	return strings.ReplaceAll(message, hostName, virtualName)
}
