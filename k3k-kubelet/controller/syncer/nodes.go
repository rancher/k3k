package syncer

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/k3k-kubelet/translate"
)

const (
	nodeControllerName = "node-syncer"
)

// NodeSyncer keeps mirrored virtual nodes in sync with their host node
// counterparts while the cluster runs with MirrorHostNodes enabled.
//
// ConfigureNode copies the host node object once at kubelet registration;
// without this syncer any later host change (cordon, taints, labels) is
// invisible to the virtual cluster until its kubelet restarts. The syncer
// watches host nodes and re-applies the mirrored metadata (labels,
// annotations) and scheduling-relevant spec fields (taints, unschedulable)
// to the matching virtual node. Status is left to the virtual kubelet's
// own update loop.
type NodeSyncer struct {
	*SyncerContext
}

func (s *NodeSyncer) Name() string {
	return nodeControllerName
}

// AddNodeSyncer adds the node syncer controller to the manager of the host
// cluster. It only acts on host nodes that have a mirrored counterpart in
// the virtual cluster, so it is a no-op for nodes the cluster's kubelets
// have not registered.
func AddNodeSyncer(ctx context.Context, virtMgr, hostMgr manager.Manager, clusterName, clusterNamespace string) error {
	reconciler := NodeSyncer{
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

	name := reconciler.Translator.TranslateName(clusterNamespace, nodeControllerName)

	return ctrl.NewControllerManagedBy(hostMgr).
		Named(name).
		For(&corev1.Node{}).
		WithEventFilter(mirroredFieldsChangedPredicate).
		Complete(&reconciler)
}

// mirroredFieldsChangedPredicate skips node updates that do not touch any
// mirrored field — most notably the periodic status/heartbeat updates.
var mirroredFieldsChangedPredicate = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldNode, okOld := e.ObjectOld.(*corev1.Node)
		newNode, okNew := e.ObjectNew.(*corev1.Node)

		if !okOld || !okNew {
			return false
		}

		return !equality.Semantic.DeepEqual(oldNode.Labels, newNode.Labels) ||
			!equality.Semantic.DeepEqual(oldNode.Annotations, newNode.Annotations) ||
			!equality.Semantic.DeepEqual(oldNode.Spec.Taints, newNode.Spec.Taints) ||
			oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable
	},
}

// Reconcile implements reconcile.Reconciler and mirrors label, annotation,
// taint and unschedulable changes of a host node onto the virtual node of
// the same name.
func (s *NodeSyncer) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithValues("cluster", s.ClusterName, "clusterNamespace", s.ClusterNamespace)
	ctx = ctrl.LoggerInto(ctx, logger)

	var hostNode corev1.Node
	if err := s.HostClient.Get(ctx, req.NamespacedName, &hostNode); err != nil {
		// A deleted host node takes its kubelet pod (and thereby the
		// virtual node lifecycle) with it — nothing to mirror.
		return reconcile.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}

	var virtNode corev1.Node
	if err := s.VirtualClient.Get(ctx, types.NamespacedName{Name: req.Name}, &virtNode); err != nil {
		if apierrors.IsNotFound(err) {
			// No mirrored counterpart (yet) — registration will copy the
			// full node object when the kubelet for this node starts.
			return reconcile.Result{}, nil
		}

		return reconcile.Result{}, err
	}

	patch := ctrlruntimeclient.MergeFrom(virtNode.DeepCopy())

	virtNode.Labels = hostNode.GetLabels()
	virtNode.Annotations = hostNode.GetAnnotations()
	virtNode.Spec.Taints = hostNode.Spec.Taints
	virtNode.Spec.Unschedulable = hostNode.Spec.Unschedulable

	logger.Info("mirroring host node change to virtual node", "node", req.Name)

	return reconcile.Result{}, s.VirtualClient.Patch(ctx, &virtNode, patch)
}
