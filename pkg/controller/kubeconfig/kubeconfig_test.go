package kubeconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewConfig guards the shape of the kubeconfig stored in the secret written by the
// cluster controller: both the kubelet and the CLI resolve it through its current context.
func TestNewConfig(t *testing.T) {
	config := NewConfig("https://localhost:6443", []byte("ca"), []byte("cert"), []byte("key"))

	require.Contains(t, config.Clusters, DefaultName)
	require.Contains(t, config.AuthInfos, DefaultName)
	require.Contains(t, config.Contexts, DefaultName)

	assert.Equal(t, DefaultName, config.CurrentContext)
	assert.Equal(t, DefaultName, config.Contexts[DefaultName].Cluster)
	assert.Equal(t, DefaultName, config.Contexts[DefaultName].AuthInfo)
	assert.Equal(t, "https://localhost:6443", config.Clusters[DefaultName].Server)
	assert.Equal(t, []byte("ca"), config.Clusters[DefaultName].CertificateAuthorityData)
	assert.Equal(t, []byte("cert"), config.AuthInfos[DefaultName].ClientCertificateData)
	assert.Equal(t, []byte("key"), config.AuthInfos[DefaultName].ClientKeyData)
}
