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

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestIngressReconcilerDeletion(t *testing.T) {
	tests := []struct {
		name       string
		hostExists bool
	}{
		{name: "host ingress exists", hostExists: true},
		// A previous reconcile may have deleted the host copy and then failed
		// to remove the finalizer, e.g. because of a conflict.
		{name: "host ingress already deleted", hostExists: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			virtIngress := newDeletingTestIngress("web")
			key := types.NamespacedName{Name: virtIngress.Name, Namespace: virtIngress.Namespace}

			reconciler := newIngressReconciler(t, newTestCluster(), []client.Object{virtIngress})
			hostIngress := reconciler.ingress(virtIngress, false)
			hostKey := types.NamespacedName{Name: hostIngress.Name, Namespace: hostIngress.Namespace}

			if tt.hostExists {
				require.NoError(t, reconciler.HostClient.Create(t.Context(), hostIngress))
			}

			result, err := reconciler.Reconcile(t.Context(), reconcile.Request{NamespacedName: key})
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			err = reconciler.HostClient.Get(t.Context(), hostKey, &networkingv1.Ingress{})
			assert.True(t, apierrors.IsNotFound(err), "expected host ingress to be deleted, got: %v", err)

			// The fake client removes the object once the last finalizer is gone.
			err = reconciler.VirtualClient.Get(t.Context(), key, &networkingv1.Ingress{})
			assert.True(t, apierrors.IsNotFound(err), "expected virtual ingress finalizer to be removed, got: %v", err)
		})
	}
}

func newIngressReconciler(t *testing.T, cluster *v1beta1.Cluster, virtualObjects []client.Object, hostObjects ...client.Object) *IngressReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	hostObjects = append(hostObjects, cluster)

	return &IngressReconciler{
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

func newDeletingTestIngress(name string) *networkingv1.Ingress {
	now := metav1.Now()

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         virtualNamespace,
			DeletionTimestamp: &now,
			Finalizers:        []string{ingressFinalizerName},
		},
	}
}
