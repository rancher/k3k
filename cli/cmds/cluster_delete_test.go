package cmds

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestDeleteMissingCluster(t *testing.T) {
	scheme := newTestScheme(t)

	appCtx := &AppContext{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	err := clusterDelete(appCtx)(&cobra.Command{}, []string{"missing"})

	require.EqualError(t, err, `cluster "missing" not found in namespace "k3k-missing"`)
}

func Test_resolveClusterArg(t *testing.T) {
	tests := []struct {
		name          string
		flagNamespace string
		arg           string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCtx := &AppContext{namespace: tt.flagNamespace}

			namespace, name, err := resolveClusterArg(appCtx, tt.arg)
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

// TestDeletePrunesTheKubeconfigContext checks that deleting a cluster removes its context
// from the kubeconfig of the user, without touching the other entries.
func TestDeletePrunesTheKubeconfigContext(t *testing.T) {
	scheme := newTestScheme(t)

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"},
	}

	existing := newTestConfig("prod", "https://prod:6443")
	existing.CurrentContext = "prod"
	existing.Clusters["k3k-foo/foo"] = clientcmdapi.NewCluster()
	existing.AuthInfos["k3k-foo/foo"] = clientcmdapi.NewAuthInfo()
	existing.Contexts["k3k-foo/foo"] = clientcmdapi.NewContext()

	path := writeTestConfig(t, existing)

	appCtx := &AppContext{
		Kubeconfig: path,
		Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build(),
	}

	require.NoError(t, clusterDelete(appCtx)(&cobra.Command{}, []string{"k3k-foo/foo"}))

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.NotContains(t, config.Contexts, "k3k-foo/foo")
	assert.Contains(t, config.Contexts, "prod")
	assert.Equal(t, "prod", config.CurrentContext)
}

func TestDeleteAllPrunesTheKubeconfigContexts(t *testing.T) {
	scheme := newTestScheme(t)

	existing := newTestConfig("prod", "https://prod:6443")
	objs := []client.Object{}

	for _, name := range []string{"foo", "bar"} {
		objs = append(objs, &v1beta1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myns"},
		})

		existing.Clusters["myns/"+name] = clientcmdapi.NewCluster()
		existing.AuthInfos["myns/"+name] = clientcmdapi.NewAuthInfo()
		existing.Contexts["myns/"+name] = clientcmdapi.NewContext()
	}

	path := writeTestConfig(t, existing)

	appCtx := &AppContext{
		Kubeconfig: path,
		namespace:  "myns",
		Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
	}

	deleteAll = true
	defer func() { deleteAll = false }()

	require.NoError(t, clusterDelete(appCtx)(&cobra.Command{}, nil))

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.Len(t, config.Contexts, 1)
	assert.Contains(t, config.Contexts, "prod")
}

// TestDeleteWithoutKubeconfig checks that a missing kubeconfig does not fail the deletion.
func TestDeleteWithoutKubeconfig(t *testing.T) {
	scheme := newTestScheme(t)

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "k3k-foo"},
	}

	path := filepath.Join(t.TempDir(), "missing")

	appCtx := &AppContext{
		Kubeconfig: path,
		Client:     fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build(),
	}

	require.NoError(t, clusterDelete(appCtx)(&cobra.Command{}, []string{"k3k-foo/foo"}))

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

// newTestConfig returns a single cluster kubeconfig keyed by name.
func newTestConfig(name, server string) *clientcmdapi.Config {
	cluster := clientcmdapi.NewCluster()
	cluster.Server = server

	context := clientcmdapi.NewContext()
	context.Cluster = name
	context.AuthInfo = name

	config := clientcmdapi.NewConfig()
	config.Clusters[name] = cluster
	config.AuthInfos[name] = clientcmdapi.NewAuthInfo()
	config.Contexts[name] = context
	config.CurrentContext = name

	return config
}

// writeTestConfig writes config to a temporary file, returning its path.
func writeTestConfig(t *testing.T, config *clientcmdapi.Config) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, clientcmd.WriteToFile(*config, path))

	return path
}

// newTestScheme builds the scheme used by the delete action, which also removes the
// PersistentVolumeClaims of the cluster.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, v1beta1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	return scheme
}
