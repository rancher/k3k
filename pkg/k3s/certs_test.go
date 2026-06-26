package k3s

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeCertPEM = `-----BEGIN CERTIFICATE-----
MIIBgTCCASegAwIBAgIUDZWXjBG6lXVaJ7cv3NAaaCX4QxkwCgYIKoZIzj0EAwIw
FjEUMBIGA1UEAwwLazNrLWt1YmVsZXQwHhcNMjYwNjExMTEwMzQ3WhcNMzYwNjA4
MTEwMzQ3WjAWMRQwEgYDVQQDDAtrM2sta3ViZWxldDBZMBMGByqGSM49AgEGCCqG
SM49AwEHA0IABFrEAV6qpR7m8VUXL1mL9/bmuLa1QvXkiUXhWvuJ+dg7G3p1kSNC
35d4w3IAN626oyMpMD1FL9kw5U6Gd17bcoSjUzBRMB0GA1UdDgQWBBS9KJuhT7sK
3nwpq1W6KkJ34PRsrDAfBgNVHSMEGDAWgBS9KJuhT7sK3nwpq1W6KkJ34PRsrDAP
BgNVHRMBAf8EBTADAQH/MAoGCCqGSM49BAMCA0gAMEUCIQCEg8kpyTuhsbvj9+p+
B+pbwZb+fgGO3iYuawrvVYZwHAIgIu6PYkP0ZGsUjhMkZUUNfQgZ42Lwq1CmKnqv
rWa+ZUI=
-----END CERTIFICATE-----
`
	fakeKeyPEM = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg/030b66JWbB6xK5m
1UuPpRbu2OLo7NyZQ1f/u1mxfq+hRANCAARaxAFeqqUe5vFVFy9Zi/f25ri2tUL1
5IlF4Vr7ifnYOxt6dZEjQt+XeMNyADetuqMjKTA9RS/ZMOVOhnde23KE
-----END PRIVATE KEY-----
`
)

func Test_GetServingKubeletCert(t *testing.T) {
	mux := http.NewServeMux()

	mockServer := httptest.NewTLSServer(mux)
	defer mockServer.Close()

	mux.Handle("/v1-k3s/serving-kubelet.crt", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(fakeCertPEM + fakeKeyPEM))
		require.NoError(t, err)
	}))

	u, err := url.Parse(mockServer.URL)
	require.NoError(t, err)

	k3sClient := New(ClientConfig{ServerIP: u.Host})

	expectedCert, err := tls.X509KeyPair([]byte(fakeCertPEM), []byte(fakeKeyPEM))
	require.NoError(t, err)

	cert, err := k3sClient.GetServingKubeletCrt()
	require.NoError(t, err)

	assert.Equal(t, expectedCert, *cert)
}
