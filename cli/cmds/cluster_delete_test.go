package cmds

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestDeleteClusterReturnsNotFoundWhenDefaultNamespaceMisses(t *testing.T) {
	appCtx := &AppContext{Client: fake.NewClientBuilder().WithScheme(testDeleteScheme(t)).Build()}

	err := delete(appCtx)(&cobra.Command{}, []string{"demo"})

	require.ErrorContains(t, err, `cluster "demo" not found in namespace "k3k-demo"`)
	require.ErrorContains(t, err, "--namespace")
}

func TestDeleteClusterRemovesExistingCluster(t *testing.T) {
	scheme := testDeleteScheme(t)
	cluster := &v1beta1.Cluster{}
	cluster.Name = "demo"
	cluster.Namespace = "k3k-system"
	appCtx := &AppContext{
		Client:    fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build(),
		namespace: "k3k-system",
	}

	err := delete(appCtx)(&cobra.Command{}, []string{"demo"})

	require.NoError(t, err)
	err = appCtx.Client.Get(t.Context(), types.NamespacedName{Name: "demo", Namespace: "k3k-system"}, &v1beta1.Cluster{})
	require.True(t, apierrors.IsNotFound(err), "expected cluster to be deleted, got %v", err)
}

func testDeleteScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	return scheme
}
