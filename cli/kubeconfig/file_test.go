package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestAddCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config")

	src := newTestConfig("default", "https://virtual:6443")
	require.NoError(t, New(path).Add("k3k-foo/foo", src))

	// the file holds credentials, so it must not be world readable
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	require.Contains(t, config.Contexts, "k3k-foo/foo")
	assert.Equal(t, "k3k-foo/foo", config.Contexts["k3k-foo/foo"].Cluster)
	assert.Equal(t, "k3k-foo/foo", config.Contexts["k3k-foo/foo"].AuthInfo)
	assert.Equal(t, "https://virtual:6443", config.Clusters["k3k-foo/foo"].Server)
	assert.Equal(t, []byte("key-default"), config.AuthInfos["k3k-foo/foo"].ClientKeyData)

	// a new file has no current context: adding a cluster never selects it
	assert.Empty(t, config.CurrentContext)
}

// TestAddKeepsTheOtherEntries is the safety test of the package: the file we write to is the
// kubeconfig of the user, holding the credentials of every other cluster they talk to.
func TestAddKeepsTheOtherEntries(t *testing.T) {
	existing := newTestConfig("prod", "https://prod:6443")
	existing.Clusters["staging"] = clientcmdapi.NewCluster()
	existing.AuthInfos["staging"] = clientcmdapi.NewAuthInfo()
	existing.Contexts["staging"] = clientcmdapi.NewContext()
	existing.Preferences.Colors = true

	path := writeTestConfig(t, existing)

	require.NoError(t, New(path).Add("k3k-foo/foo", newTestConfig("default", "https://virtual:6443")))

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.Contains(t, config.Clusters, "prod")
	assert.Contains(t, config.Clusters, "staging")
	assert.Contains(t, config.AuthInfos, "prod")
	assert.Contains(t, config.Contexts, "prod")
	assert.Contains(t, config.Contexts, "staging")
	assert.Contains(t, config.Contexts, "k3k-foo/foo")

	assert.Equal(t, "https://prod:6443", config.Clusters["prod"].Server)
	assert.True(t, config.Preferences.Colors)

	// the current context is never switched
	assert.Equal(t, "prod", config.CurrentContext)
}

// TestAddOverwrites checks that re-generating the kubeconfig of a cluster refreshes its
// entries, which is how the certificates are rotated.
func TestAddOverwrites(t *testing.T) {
	path := writeTestConfig(t, newTestConfig("prod", "https://prod:6443"))
	file := New(path)

	require.NoError(t, file.Add("k3k-foo/foo", newTestConfig("default", "https://old:6443")))
	require.NoError(t, file.Add("k3k-foo/foo", newTestConfig("default", "https://new:6443")))

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.Len(t, config.Clusters, 2)
	assert.Equal(t, "https://new:6443", config.Clusters["k3k-foo/foo"].Server)
}

// TestAddWithoutCurrentContext checks that a source config we cannot resolve is rejected,
// instead of being silently merged as an empty entry.
func TestAddWithoutCurrentContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")

	tests := []struct {
		name    string
		src     *clientcmdapi.Config
		wantErr string
	}{
		{
			name:    "no current context",
			src:     clientcmdapi.NewConfig(),
			wantErr: "the kubeconfig has no '' context",
		},
		{
			name: "the context points to a missing cluster",
			src: func() *clientcmdapi.Config {
				config := newTestConfig("default", "https://virtual:6443")
				delete(config.Clusters, "default")

				return config
			}(),
			wantErr: "the kubeconfig has no 'default' cluster",
		},
		{
			name: "the context points to a missing user",
			src: func() *clientcmdapi.Config {
				config := newTestConfig("default", "https://virtual:6443")
				delete(config.AuthInfos, "default")

				return config
			}(),
			wantErr: "the kubeconfig has no 'default' user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, New(path).Add("k3k-foo/foo", tt.src), tt.wantErr)

			_, err := os.Stat(path)
			assert.True(t, os.IsNotExist(err), "the file must not be created")
		})
	}
}

// TestAddKeepsTheSourceUntouched checks that the caller can keep using the config it passed,
// to write it as a standalone kubeconfig.
func TestAddKeepsTheSourceUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	src := newTestConfig("default", "https://virtual:6443")

	require.NoError(t, New(path).Add("k3k-foo/foo", src))

	assert.Equal(t, "default", src.CurrentContext)
	assert.Contains(t, src.Clusters, "default")
	assert.Contains(t, src.AuthInfos, "default")
	assert.Contains(t, src.Contexts, "default")
	assert.NotContains(t, src.Clusters, "k3k-foo/foo")
	assert.NotContains(t, src.Contexts, "k3k-foo/foo")
}

// TestAddMalformedFile checks that we bail out instead of overwriting a file we cannot parse.
func TestAddMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("not a kubeconfig"), 0o600))

	err := New(path).Add("k3k-foo/foo", newTestConfig("default", "https://virtual:6443"))
	require.ErrorContains(t, err, "failed to load the kubeconfig")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "not a kubeconfig", string(data))
}

func TestRemove(t *testing.T) {
	existing := newTestConfig("prod", "https://prod:6443")

	for _, name := range []string{"k3k-foo/foo", "k3k-bar/bar"} {
		existing.Clusters[name] = clientcmdapi.NewCluster()
		existing.AuthInfos[name] = clientcmdapi.NewAuthInfo()
		existing.Contexts[name] = clientcmdapi.NewContext()
	}

	path := writeTestConfig(t, existing)

	removed, err := New(path).Remove("k3k-foo/foo", "k3k-bar/bar", "k3k-baz/baz")
	require.NoError(t, err)
	assert.Equal(t, []string{"k3k-foo/foo", "k3k-bar/bar"}, removed)

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.NotContains(t, config.Contexts, "k3k-foo/foo")
	assert.NotContains(t, config.Clusters, "k3k-bar/bar")
	assert.NotContains(t, config.AuthInfos, "k3k-bar/bar")

	assert.Contains(t, config.Contexts, "prod")
	assert.Equal(t, "prod", config.CurrentContext)
}

// TestRemoveClearsTheCurrentContext checks that we don't leave a dangling reference behind
// when the user was on the context of the deleted cluster.
func TestRemoveClearsTheCurrentContext(t *testing.T) {
	existing := newTestConfig("prod", "https://prod:6443")
	existing.Clusters["k3k-foo/foo"] = clientcmdapi.NewCluster()
	existing.AuthInfos["k3k-foo/foo"] = clientcmdapi.NewAuthInfo()
	existing.Contexts["k3k-foo/foo"] = clientcmdapi.NewContext()
	existing.CurrentContext = "k3k-foo/foo"

	path := writeTestConfig(t, existing)

	removed, err := New(path).Remove("k3k-foo/foo")
	require.NoError(t, err)
	assert.Equal(t, []string{"k3k-foo/foo"}, removed)

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)

	assert.Empty(t, config.CurrentContext)
	assert.Contains(t, config.Contexts, "prod")
}

// TestRemoveMissingFile checks that deleting a cluster does not create a kubeconfig.
func TestRemoveMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")

	removed, err := New(path).Remove("k3k-foo/foo")
	require.NoError(t, err)
	assert.Empty(t, removed)

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

// TestRemoveUnknownName checks that we don't rewrite - and so reformat - the kubeconfig of
// the user when there is nothing to remove.
func TestRemoveUnknownName(t *testing.T) {
	path := writeTestConfig(t, newTestConfig("prod", "https://prod:6443"))

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	removed, err := New(path).Remove("k3k-foo/foo")
	require.NoError(t, err)
	assert.Empty(t, removed)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// writeTestConfig writes config to a temporary file, returning its path.
func writeTestConfig(t *testing.T, config *clientcmdapi.Config) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, clientcmd.WriteToFile(*config, path))

	return path
}
