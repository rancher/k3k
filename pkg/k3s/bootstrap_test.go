package k3s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
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
				if _, err := w.Write([]byte(tt.serverResponse)); err != nil {
					t.Fatalf("failed to write server response: %v", err)
				}
			}))

			if tt.clientConfig.ServerIP == "" {
				tt.clientConfig.ServerIP = getServerAddress(mockServer.URL)
			}

			k3sClient := New(tt.clientConfig)

			k3sConfig, err := k3sClient.GetServerConfig()
			if err != nil {
				if tt.expectedErr != nil {
					if err.Error() != tt.expectedErr.Error() {
						t.Fatalf("expected err %v got %v", tt.expectedErr, err)
					}
				} else {
					t.Fatalf("failed to get server config: %v", err)
				}
			}

			if !reflect.DeepEqual(k3sConfig, tt.expectedConfig) {
				t.Fatalf("got config %v expected %v", k3sConfig, tt.expectedConfig)
			}
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
	if err != nil {
		t.Fatalf("failed to marshal server response: %v", err)
	}

	mux.Handle("/v1-k3s/server-bootstrap", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(serverResponse); err != nil {
			t.Fatalf("failed to write server response: %v", err)
		}
	}))

	k3sClient := New(ClientConfig{ServerIP: getServerAddress(mockServer.URL)})

	bootstrap, err := k3sClient.GetServerBootstrap()
	if err != nil {
		t.Fatalf("failed to get server bootstrap: %v", err)
	}

	if !reflect.DeepEqual(bootstrap, expectedBootstrap) {
		t.Fatalf("got bootstrap %v expected %v", bootstrap, expectedBootstrap)
	}
}
