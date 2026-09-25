package syncer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	testClusterName      = "my-cluster"
	testClusterNamespace = "host-ns"
	virtualNamespace     = "virtual-ns"
)

func TestPriorityClassSyncerTranslatePriorityClass(t *testing.T) {
	syncer := &PriorityClassSyncer{
		Context: &Context{
			Translator: translate.ToHostTranslator{
				ClusterName:      testClusterName,
				ClusterNamespace: testClusterNamespace,
			},
		},
	}
	virtualPriorityClass := newTestPriorityClass("high", 1000, map[string]string{"environment": "production"})

	hostPriorityClass := syncer.translatePriorityClass(*virtualPriorityClass)

	assert.Equal(t, syncer.Translator.TranslateName("", "high"), hostPriorityClass.Name)
	assert.Equal(t, int32(1000), hostPriorityClass.Value)
	assert.Equal(t, "high", hostPriorityClass.Annotations[translate.ResourceNameAnnotation])

	assert.Contains(t, hostPriorityClass.Annotations, translate.ResourceNameAnnotation)
	assert.Equal(t, testClusterName, hostPriorityClass.Labels[translate.ClusterNameLabel])
	assert.Equal(t, "high", virtualPriorityClass.Name)
	assert.Empty(t, virtualPriorityClass.Namespace)
}

func TestPriorityClassSyncerFilterResources(t *testing.T) {
	priorityClass := newTestPriorityClass("high", 1000, map[string]string{"environment": "production"})

	tests := []struct {
		name       string
		syncConfig v1beta1.PriorityClassSyncConfig
		object     *schedulingv1.PriorityClass
		filtered   bool
	}{
		{
			name: "enabled with no selector",
			syncConfig: v1beta1.PriorityClassSyncConfig{
				Enabled: true,
			},
			object:   priorityClass,
			filtered: true,
		},
		{
			name: "enabled matching selector",
			syncConfig: v1beta1.PriorityClassSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "production"},
			},
			object:   priorityClass,
			filtered: true,
		},
		{
			name: "enabled non-matching selector",
			syncConfig: v1beta1.PriorityClassSyncConfig{
				Enabled:  true,
				Selector: map[string]string{"environment": "staging"},
			},
			object:   priorityClass,
			filtered: false,
		},
		{
			name:       "disabled non-deletion",
			syncConfig: v1beta1.PriorityClassSyncConfig{},
			object:     priorityClass,
			filtered:   false,
		},
		{
			name:       "disabled deletion",
			syncConfig: v1beta1.PriorityClassSyncConfig{},
			object: func() *schedulingv1.PriorityClass {
				deleted := priorityClass.DeepCopy()
				deletionTime := metav1.NewTime(time.Now())
				deleted.DeletionTimestamp = &deletionTime

				return deleted
			}(),
			filtered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := newPriorityClassSyncer(t, newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Sync.PriorityClasses = tt.syncConfig
			}), nil)
			assert.Equal(t, tt.filtered, syncer.filterResources(tt.object))
		})
	}
}

func TestPriorityClassSyncerReconcile(t *testing.T) {
	virtualObject := newTestPriorityClass("high-priority", 1000, map[string]string{"environment": "production"})
	cluster := newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.PriorityClasses = v1beta1.PriorityClassSyncConfig{Enabled: true}
	})
	syncer := newPriorityClassSyncer(t, cluster, []client.Object{virtualObject})
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(virtualObject)}

	result, err := syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	var gotVirtual schedulingv1.PriorityClass
	require.NoError(t, syncer.VirtualClient.Get(t.Context(), request.NamespacedName, &gotVirtual))

	hostKey := syncer.Translator.NamespacedName(virtualObject)

	var gotHost schedulingv1.PriorityClass
	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Equal(t, virtualObject.Value, gotHost.Value)
	assert.Equal(t, cluster.UID, gotHost.OwnerReferences[0].UID)

	require.NoError(t, syncer.VirtualClient.Get(t.Context(), request.NamespacedName, &gotVirtual))
	gotVirtual.Value = 2000
	require.NoError(t, syncer.VirtualClient.Update(t.Context(), &gotVirtual))

	_, err = syncer.Reconcile(t.Context(), request)
	require.NoError(t, err)

	require.NoError(t, syncer.HostClient.Get(t.Context(), hostKey, &gotHost))
	assert.Equal(t, int32(2000), gotHost.Value)
}

func TestPriorityClassSyncerReconcileNotFound(t *testing.T) {
	syncer := newPriorityClassSyncer(t, newTestCluster(func(c *v1beta1.Cluster) {
		c.Spec.Sync.PriorityClasses = v1beta1.PriorityClassSyncConfig{Enabled: true}
	}), nil)

	_, err := syncer.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "missing", Namespace: virtualNamespace}})
	require.NoError(t, err)
}

func newPriorityClassSyncer(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *PriorityClassSyncer {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, schedulingv1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &PriorityClassSyncer{
		Context: &Context{
			VirtualClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(virtualObjects...).Build(),
			HostClient:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostObjects...).Build(),
			Translator: translate.ToHostTranslator{
				ClusterName:      testClusterName,
				ClusterNamespace: testClusterNamespace,
			},
			ClusterName:      testClusterName,
			ClusterNamespace: testClusterNamespace,
		},
	}
}

func newTestPriorityClass(name string, value int32, labels map[string]string) *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Value: value,
	}
}

func newTestCluster(opts ...func(*v1beta1.Cluster)) *v1beta1.Cluster {
	c := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testClusterNamespace,
			UID:       types.UID("cluster-uid"),
		},
		Spec: v1beta1.ClusterSpec{
			Sync: &v1beta1.SyncConfig{},
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}
