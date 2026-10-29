package syncer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestServiceReconcilerDeletion(t *testing.T) {
	tests := []struct {
		name       string
		hostExists bool
	}{
		{name: "host service exists", hostExists: true},
		// A previous reconcile may have deleted the host copy and then failed
		// to remove the finalizer, e.g. because of a conflict.
		{name: "host service already deleted", hostExists: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			virtService := newDeletingTestService("web")
			key := types.NamespacedName{Name: virtService.Name, Namespace: virtService.Namespace}

			reconciler := newServiceReconciler(t, newTestCluster(), []client.Object{virtService})
			hostService := reconciler.service(virtService)
			hostKey := types.NamespacedName{Name: hostService.Name, Namespace: hostService.Namespace}

			if tt.hostExists {
				require.NoError(t, reconciler.HostClient.Create(t.Context(), hostService))
			}

			result, err := reconciler.Reconcile(t.Context(), reconcile.Request{NamespacedName: key})
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			err = reconciler.HostClient.Get(t.Context(), hostKey, &corev1.Service{})
			assert.True(t, apierrors.IsNotFound(err), "expected host service to be deleted, got: %v", err)

			// The fake client removes the object once the last finalizer is gone.
			err = reconciler.VirtualClient.Get(t.Context(), key, &corev1.Service{})
			assert.True(t, apierrors.IsNotFound(err), "expected virtual service finalizer to be removed, got: %v", err)
		})
	}
}

func newServiceReconciler(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *ServiceReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &ServiceReconciler{
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

func newDeletingTestService(name string) *corev1.Service {
	now := metav1.Now()

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         virtualNamespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{serviceFinalizerName},
		},
	}
}
