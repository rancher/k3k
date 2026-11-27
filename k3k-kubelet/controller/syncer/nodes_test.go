package syncer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func newNodeSyncer(hostObjs, virtObjs []runtime.Object, scheme *runtime.Scheme) *NodeSyncer {
	return &NodeSyncer{
		Context: &Context{
			HostClient:    fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(hostObjs...).Build(),
			VirtualClient: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(virtObjs...).Build(),
			Translator: translate.ToHostTranslator{
				ClusterName:      "mycluster",
				ClusterNamespace: "ns-1",
			},
			ClusterName:      "mycluster",
			ClusterNamespace: "ns-1",
		},
	}
}

func TestNodeSyncerReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	hostNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node-1",
			Labels:      map[string]string{"node-role.kubernetes.io/control-plane": "true", "topology.kubernetes.io/zone": "z1"},
			Annotations: map[string]string{"some": "annotation"},
		},
		Spec: corev1.NodeSpec{
			Unschedulable: true,
			Taints: []corev1.Taint{
				{Key: "node.kubernetes.io/unschedulable", Effect: corev1.TaintEffectNoSchedule},
			},
		},
	}

	virtNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"node-role.kubernetes.io/worker": "true"},
		},
	}

	t.Run("mirrors labels, annotations, taints and unschedulable", func(t *testing.T) {
		syncer := newNodeSyncer(
			[]runtime.Object{hostNode.DeepCopy()},
			[]runtime.Object{virtNode.DeepCopy()},
			scheme,
		)

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "node-1"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var synced corev1.Node
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced))

		assert.Equal(t, hostNode.Labels, synced.Labels)
		assert.Equal(t, hostNode.Annotations, synced.Annotations)
		assert.Equal(t, hostNode.Spec.Taints, synced.Spec.Taints)
		assert.True(t, synced.Spec.Unschedulable)
	})

	t.Run("preserves virtual-kubelet annotations", func(t *testing.T) {
		virtWithVKAnnotation := virtNode.DeepCopy()
		virtWithVKAnnotation.Annotations = map[string]string{
			"virtual-kubelet.io/last-applied-node-status": "{...}",
		}

		syncer := newNodeSyncer(
			[]runtime.Object{hostNode.DeepCopy()},
			[]runtime.Object{virtWithVKAnnotation},
			scheme,
		)

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "node-1"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var synced corev1.Node
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced))

		assert.Equal(t, "{...}", synced.Annotations["virtual-kubelet.io/last-applied-node-status"])
		assert.Equal(t, "annotation", synced.Annotations["some"])
	})

	t.Run("no virtual counterpart is a no-op", func(t *testing.T) {
		syncer := newNodeSyncer(
			[]runtime.Object{hostNode.DeepCopy()},
			nil,
			scheme,
		)

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "node-1"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)
	})

	t.Run("deleted host node is a no-op", func(t *testing.T) {
		syncer := newNodeSyncer(
			nil,
			[]runtime.Object{virtNode.DeepCopy()},
			scheme,
		)

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "node-1"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var unchanged corev1.Node
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &unchanged))
		assert.Equal(t, virtNode.Labels, unchanged.Labels)
	})
}

func TestMirroredFieldsChangedPredicate(t *testing.T) {
	base := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{"a": "1"},
		},
	}

	t.Run("status-only change is filtered out", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Status.NodeInfo.KubeletVersion = "v1.34.9"

		assert.False(t, mirroredFieldsChanged().Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}))
	})

	t.Run("label change passes", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Labels["b"] = "2"

		assert.True(t, mirroredFieldsChanged().Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}))
	})

	t.Run("cordon passes", func(t *testing.T) {
		updated := base.DeepCopy()
		updated.Spec.Unschedulable = true

		assert.True(t, mirroredFieldsChanged().Update(event.UpdateEvent{ObjectOld: base, ObjectNew: updated}))
	})
}

func TestNodeSyncerNodeSelected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1", Labels: map[string]string{"pool": "a"}}}

	tests := []struct {
		name   string
		spec   map[string]string
		policy map[string]string
		want   bool
	}{
		{name: "no node selector selects every node", want: true},
		{name: "cluster node selector matches", spec: map[string]string{"pool": "a"}, want: true},
		{name: "cluster node selector does not match", spec: map[string]string{"pool": "b"}, want: false},
		{name: "policy node selector wins over the cluster one", spec: map[string]string{"pool": "a"}, policy: map[string]string{"pool": "b"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "mycluster", Namespace: "ns-1"},
				Spec:       v1beta1.ClusterSpec{NodeSelector: tt.spec},
			}
			if tt.policy != nil {
				cluster.Status.Policy = &v1beta1.AppliedPolicy{NodeSelector: tt.policy}
			}

			s := newNodeSyncer([]runtime.Object{cluster}, nil, scheme)
			assert.Equal(t, tt.want, s.nodeSelected(node))
		})
	}
}
