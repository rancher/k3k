package cmds

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func newFakeClient(t *testing.T, clusters ...*v1beta1.Cluster) ctrlclient.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, cluster := range clusters {
		builder = builder.WithObjects(cluster)
	}

	return builder.Build()
}

func testCluster(namespace, name string) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func TestDeleteMissingCluster(t *testing.T) {
	appCtx := &AppContext{Client: newFakeClient(t)}
	err := delete(appCtx)(&cobra.Command{}, []string{"missing"})

	require.EqualError(t, err, `cluster "missing" not found in namespace "k3k-missing"`)
}

func TestDeleteAmbiguousCluster(t *testing.T) {
	appCtx := &AppContext{Client: newFakeClient(t, testCluster("default", "foo"), testCluster("k3k-foo", "foo"))}
	err := delete(appCtx)(&cobra.Command{}, []string{"foo"})

	require.EqualError(t, err, `multiple clusters named "foo" found in namespaces [default k3k-foo], specify one with --namespace/-n`)

	// nothing should have been deleted
	var clusters v1beta1.ClusterList
	require.NoError(t, appCtx.Client.List(context.Background(), &clusters))
	assert.Len(t, clusters.Items, 2)
}

func Test_resolveClusterArg(t *testing.T) {
	tests := []struct {
		name          string
		flagNamespace string
		arg           string
		clusters      []*v1beta1.Cluster
		wantNamespace string
		wantName      string
		wantErr       bool
	}{
		{
			name:          "bare name defaults to k3k-<name>",
			arg:           "mycluster",
			wantNamespace: "k3k-mycluster",
			wantName:      "mycluster",
		},
		{
			name:          "bare name respects the namespace flag",
			flagNamespace: "custom",
			arg:           "mycluster",
			wantNamespace: "custom",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name form is split",
			arg:           "k3k-foo/mycluster",
			wantNamespace: "k3k-foo",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name matching the flag is accepted",
			flagNamespace: "k3k-foo",
			arg:           "k3k-foo/mycluster",
			wantNamespace: "k3k-foo",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name conflicting with the flag errors",
			flagNamespace: "bar",
			arg:           "k3k-foo/mycluster",
			wantErr:       true,
		},
		{
			name:          "bare name resolves to the only namespace holding the cluster",
			arg:           "mycluster",
			clusters:      []*v1beta1.Cluster{testCluster("default", "mycluster")},
			wantNamespace: "default",
			wantName:      "mycluster",
		},
		{
			name:     "bare name matching several namespaces errors",
			arg:      "mycluster",
			clusters: []*v1beta1.Cluster{testCluster("default", "mycluster"), testCluster("k3k-mycluster", "mycluster")},
			wantErr:  true,
		},
		{
			name:          "the namespace flag skips the ambiguity lookup",
			flagNamespace: "default",
			arg:           "mycluster",
			clusters:      []*v1beta1.Cluster{testCluster("default", "mycluster"), testCluster("k3k-mycluster", "mycluster")},
			wantNamespace: "default",
			wantName:      "mycluster",
		},
		{
			name:          "namespace/name form skips the ambiguity lookup",
			arg:           "k3k-mycluster/mycluster",
			clusters:      []*v1beta1.Cluster{testCluster("default", "mycluster"), testCluster("k3k-mycluster", "mycluster")},
			wantNamespace: "k3k-mycluster",
			wantName:      "mycluster",
		},
		{
			name:          "clusters with a different name are ignored",
			arg:           "mycluster",
			clusters:      []*v1beta1.Cluster{testCluster("default", "othercluster")},
			wantNamespace: "k3k-mycluster",
			wantName:      "mycluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := &AppContext{namespace: tt.flagNamespace, Client: newFakeClient(t, tt.clusters...)}

			namespace, name, err := resolveClusterArg(context.Background(), appCtx, tt.arg)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantNamespace, namespace)
			assert.Equal(t, tt.wantName, name)
		})
	}
}
