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

func TestContextName(t *testing.T) {
	assert.Equal(t, "k3k-foo/foo", ContextName("k3k-foo", "foo"))
}

func TestServerURL(t *testing.T) {
	tests := []struct {
		name   string
		config *clientcmdapi.Config
		want   string
	}{
		{
			name:   "the server of the current context",
			config: newTestConfig("default", "https://localhost:6443"),
			want:   "https://localhost:6443",
		},
		{
			name:   "an empty config has no server",
			config: clientcmdapi.NewConfig(),
		},
		{
			name: "a dangling current context has no server",
			config: func() *clientcmdapi.Config {
				config := newTestConfig("default", "https://localhost:6443")
				config.CurrentContext = "missing"

				return config
			}(),
		},
		{
			name: "a context pointing to a missing cluster has no server",
			config: func() *clientcmdapi.Config {
				config := newTestConfig("default", "https://localhost:6443")
				delete(config.Clusters, "default")

				return config
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ServerURL(tt.config))
		})
	}
}

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config.yaml")

	absPath, err := Write(newTestConfig("default", "https://localhost:6443"), path)
	require.NoError(t, err)
	assert.Equal(t, path, absPath)

	// the file holds credentials, so it must not be world readable
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	config, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	assert.Equal(t, "default", config.CurrentContext)
	assert.Equal(t, "https://localhost:6443", config.Clusters["default"].Server)
}

// TestNewPath checks that an empty path is resolved the way kubectl resolves the file it
// writes to.
func TestNewPath(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, clientcmd.WriteToFile(*clientcmdapi.NewConfig(), existing))

	other := filepath.Join(t.TempDir(), "other")
	require.NoError(t, clientcmd.WriteToFile(*clientcmdapi.NewConfig(), other))

	tests := []struct {
		name       string
		path       string
		kubeconfig string
		want       string
	}{
		{
			name: "no flag and no env falls back to the home config",
			want: clientcmd.RecommendedHomeFile,
		},
		{
			name:       "the flag wins over the env",
			path:       "/tmp/flag",
			kubeconfig: existing,
			want:       "/tmp/flag",
		},
		{
			name:       "a single env entry is used",
			kubeconfig: existing,
			want:       existing,
		},
		{
			name:       "the first existing env entry is used",
			kubeconfig: existing + string(os.PathListSeparator) + other,
			want:       existing,
		},
		{
			name:       "a missing env entry is skipped",
			kubeconfig: filepath.Join(t.TempDir(), "missing") + string(os.PathListSeparator) + other,
			want:       other,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KUBECONFIG", tt.kubeconfig)

			assert.Equal(t, tt.want, New(tt.path).Path())
		})
	}
}

// newTestConfig returns a single cluster kubeconfig keyed by name, in the shape of the
// kubeconfig generated for a virtual cluster.
func newTestConfig(name, server string) *clientcmdapi.Config {
	cluster := clientcmdapi.NewCluster()
	cluster.Server = server
	cluster.CertificateAuthorityData = []byte("ca-" + name)

	authInfo := clientcmdapi.NewAuthInfo()
	authInfo.ClientCertificateData = []byte("cert-" + name)
	authInfo.ClientKeyData = []byte("key-" + name)

	context := clientcmdapi.NewContext()
	context.Cluster = name
	context.AuthInfo = name

	config := clientcmdapi.NewConfig()
	config.Clusters[name] = cluster
	config.AuthInfos[name] = authInfo
	config.Contexts[name] = context
	config.CurrentContext = name

	return config
}
