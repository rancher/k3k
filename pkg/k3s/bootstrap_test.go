package k3s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GetServerConfig(t *testing.T) {
	tests := []struct {
		name            string
		serverResponse  string
		clientConfig    ClientConfig
		expectedConfig  *K3SConfig
		expectedErr     error
		isServerRunning bool
	}{
		{
			name:            "server not ready",
			isServerRunning: false,
			serverResponse:  "",
			expectedConfig:  nil,
			expectedErr:     ErrServerNotReady,
			clientConfig: ClientConfig{
				ServerIP: "127.0.0.1:33333",
			},
		},
		{
			name:            "cluster init is true",
			isServerRunning: true,
			serverResponse:  `{"ClusterInit": true}`,
			expectedConfig:  &K3SConfig{ClusterInit: true},
			expectedErr:     ErrServerNotReady,
			clientConfig:    ClientConfig{},
		},
		{
			name:            "cluster init is false",
			isServerRunning: true,
			serverResponse:  `{"ClusterInit": false}`,
			expectedConfig:  &K3SConfig{ClusterInit: false},
			expectedErr:     ErrServerNotReady,
			clientConfig:    ClientConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()

			mockServer := httptest.NewUnstartedServer(mux)
			if tt.isServerRunning {
				mockServer.StartTLS()
				defer mockServer.Close()
			}

			mux.Handle("/v1-k3s/config", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(tt.serverResponse))
				require.NoError(t, err)
			}))

			if tt.clientConfig.ServerIP == "" {
				u, err := url.Parse(mockServer.URL)
				require.NoError(t, err)

				tt.clientConfig.ServerIP = u.Host
			}

			k3sClient := New(tt.clientConfig)

			k3sConfig, err := k3sClient.GetServerConfig()
			if err != nil && tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedConfig, k3sConfig)
		})
	}
}

func Test_GetServerBootstrap(t *testing.T) {
	fakeBootstrap := &BootstrapData{
		ClientCA: cert{
			Content: "dGVzdA==",
		},
		ClientCAKey: cert{
			Content: "dGVzdA==",
		},
		ServerCA: cert{
			Content: "dGVzdA==",
		},
		ServerCAKey: cert{
			Content: "dGVzdA==",
		},
		ETCDServerCA: cert{
			Content: "dGVzdA==",
		},
		ETCDServerCAKey: cert{
			Content: "dGVzdA==",
		},
	}

	expectedBootstrap := &BootstrapData{
		ClientCA: cert{
			Content: "test",
		},
		ClientCAKey: cert{
			Content: "test",
		},
		ServerCA: cert{
			Content: "test",
		},
		ServerCAKey: cert{
			Content: "test",
		},
		ETCDServerCA: cert{
			Content: "test",
		},
		ETCDServerCAKey: cert{
			Content: "test",
		},
	}

	mux := http.NewServeMux()

	mockServer := httptest.NewTLSServer(mux)
	defer mockServer.Close()

	serverResponse, err := json.Marshal(fakeBootstrap)
	require.NoError(t, err)

	mux.Handle("/v1-k3s/server-bootstrap", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write(serverResponse)
		require.NoError(t, err)
	}))

	u, err := url.Parse(mockServer.URL)
	require.NoError(t, err)

	k3sClient := New(ClientConfig{ServerIP: u.Host})

	bootstrap, err := k3sClient.GetServerBootstrap()
	require.NoError(t, err)

	assert.Equal(t, expectedBootstrap, bootstrap)
}
