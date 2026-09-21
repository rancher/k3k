package syncer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func newPDBTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	return scheme
}

func newPDBSyncer(t *testing.T, syncEnabled bool, hostObjs, virtObjs []runtime.Object) *PDBReconciler {
	scheme := newPDBTestScheme(t)

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mycluster",
			Namespace: "ns-1",
		},
		Spec: v1beta1.ClusterSpec{
			Sync: &v1beta1.SyncConfig{
				PodDisruptionBudgets: v1beta1.PodDisruptionBudgetSyncConfig{
					Enabled: syncEnabled,
				},
			},
		},
	}

	hostObjs = append(hostObjs, cluster)

	return &PDBReconciler{
		SyncerContext: &SyncerContext{
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

func TestPDBSyncerReconcile(t *testing.T) {
	minAvailable := intstr.FromInt32(1)

	virtPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-pdb",
			Namespace: "team-a",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "web"},
			},
		},
	}

	t.Run("creates scoped host pdb", func(t *testing.T) {
		syncer := newPDBSyncer(t, true, nil, []runtime.Object{virtPDB.DeepCopy()})

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "web-pdb", Namespace: "team-a"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		hostName := syncer.Translator.TranslateName("team-a", "web-pdb")

		var hostPDB policyv1.PodDisruptionBudget
		require.NoError(t, syncer.HostClient.Get(context.Background(), types.NamespacedName{Name: hostName, Namespace: "ns-1"}, &hostPDB))

		// the original selector is preserved and scoped to the virtual cluster and namespace
		assert.Equal(t, map[string]string{
			"app":                        "web",
			translate.ClusterNameLabel:   "mycluster",
			translate.NamespaceNameLabel: "team-a",
		}, hostPDB.Spec.Selector.MatchLabels)

		assert.Equal(t, &minAvailable, hostPDB.Spec.MinAvailable)
		assert.Equal(t, "web-pdb", hostPDB.Annotations[translate.ResourceNameAnnotation])
		assert.Equal(t, "team-a", hostPDB.Annotations[translate.ResourceNamespaceAnnotation])

		// the virtual pdb got the cleanup finalizer
		var synced policyv1.PodDisruptionBudget
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced))
		assert.Contains(t, synced.Finalizers, pdbFinalizerName)
	})

	t.Run("empty selector is scoped to the virtual namespace only", func(t *testing.T) {
		emptySelectorPDB := virtPDB.DeepCopy()
		emptySelectorPDB.Spec.Selector = nil

		syncer := newPDBSyncer(t, true, nil, []runtime.Object{emptySelectorPDB})

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "web-pdb", Namespace: "team-a"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var hostPDB policyv1.PodDisruptionBudget
		hostName := syncer.Translator.TranslateName("team-a", "web-pdb")
		require.NoError(t, syncer.HostClient.Get(context.Background(), types.NamespacedName{Name: hostName, Namespace: "ns-1"}, &hostPDB))

		assert.Equal(t, map[string]string{
			translate.ClusterNameLabel:   "mycluster",
			translate.NamespaceNameLabel: "team-a",
		}, hostPDB.Spec.Selector.MatchLabels)
	})

	t.Run("updates existing host pdb", func(t *testing.T) {
		syncer := newPDBSyncer(t, true, nil, []runtime.Object{virtPDB.DeepCopy()})

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "web-pdb", Namespace: "team-a"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		// bump minAvailable on the virtual pdb
		var synced policyv1.PodDisruptionBudget
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced))

		newMinAvailable := intstr.FromInt32(2)
		synced.Spec.MinAvailable = &newMinAvailable
		require.NoError(t, syncer.VirtualClient.Update(context.Background(), &synced))

		_, err = syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		var hostPDB policyv1.PodDisruptionBudget
		hostName := syncer.Translator.TranslateName("team-a", "web-pdb")
		require.NoError(t, syncer.HostClient.Get(context.Background(), types.NamespacedName{Name: hostName, Namespace: "ns-1"}, &hostPDB))

		assert.Equal(t, &newMinAvailable, hostPDB.Spec.MinAvailable)
	})

	t.Run("deletion cleans up host pdb and finalizer", func(t *testing.T) {
		syncer := newPDBSyncer(t, true, nil, []runtime.Object{virtPDB.DeepCopy()})

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "web-pdb", Namespace: "team-a"}}
		_, err := syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		// delete the virtual pdb; the finalizer keeps it around with a deletion timestamp
		var synced policyv1.PodDisruptionBudget
		require.NoError(t, syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced))
		require.NoError(t, syncer.VirtualClient.Delete(context.Background(), &synced))

		_, err = syncer.Reconcile(context.Background(), req)
		require.NoError(t, err)

		// host pdb is gone
		var hostPDB policyv1.PodDisruptionBudget
		hostName := syncer.Translator.TranslateName("team-a", "web-pdb")
		err = syncer.HostClient.Get(context.Background(), types.NamespacedName{Name: hostName, Namespace: "ns-1"}, &hostPDB)
		assert.True(t, apierrors.IsNotFound(err))

		// virtual pdb is fully gone after finalizer removal
		err = syncer.VirtualClient.Get(context.Background(), req.NamespacedName, &synced)
		assert.True(t, apierrors.IsNotFound(err))
	})
}

func TestPDBSyncerFilterResources(t *testing.T) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-pdb",
			Namespace: "team-a",
			Labels:    map[string]string{"app": "web"},
		},
	}

	t.Run("enabled without selector syncs everything", func(t *testing.T) {
		syncer := newPDBSyncer(t, true, nil, nil)
		assert.True(t, syncer.filterResources(pdb))
	})

	t.Run("disabled only processes deletions", func(t *testing.T) {
		syncer := newPDBSyncer(t, false, nil, nil)
		assert.False(t, syncer.filterResources(pdb))

		deleted := pdb.DeepCopy()
		now := metav1.Now()
		deleted.DeletionTimestamp = &now
		assert.True(t, syncer.filterResources(deleted))
	})

	t.Run("selector filters non-matching resources", func(t *testing.T) {
		syncer := newPDBSyncer(t, true, nil, nil)

		var cluster v1beta1.Cluster
		require.NoError(t, syncer.HostClient.Get(context.Background(), types.NamespacedName{Name: "mycluster", Namespace: "ns-1"}, &cluster))

		cluster.Spec.Sync.PodDisruptionBudgets.Selector = map[string]string{"sync": "true"}
		require.NoError(t, syncer.HostClient.Update(context.Background(), &cluster))

		assert.False(t, syncer.filterResources(pdb))

		matching := pdb.DeepCopy()
		matching.Labels["sync"] = "true"
		assert.True(t, syncer.filterResources(matching))
	})
}
