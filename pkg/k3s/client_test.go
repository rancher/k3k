package k3s

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_NewClient(t *testing.T) {
	tests := []struct {
		name            string
		clientConfig    ClientConfig
		expectedHeaders http.Header
	}{
		{
			name:            "empty config",
			clientConfig:    ClientConfig{},
			expectedHeaders: nil,
		},
		{
			name: "client config passed",
			clientConfig: ClientConfig{
				Token:    "test_token",
				ServerIP: "0.0.0.0:1234",
				NodeName: "test_node",
				AgentIP:  "1.1.1.1",
				PodIP:    "2.2.2.2",
			},
			expectedHeaders: map[string][]string{
				k3sNodePasswordHeader: {"test_token"},
				k3sNodeNameHeader:     {"test_node"},
				k3sNodeIPHeader:       {"1.1.1.1,2.2.2.2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New(tt.clientConfig)
			assert.Len(t, client.staticHeaders, len(tt.expectedHeaders))

			for expectedHeader, expectedValue := range tt.expectedHeaders {
				value, ok := client.staticHeaders[http.CanonicalHeaderKey(expectedHeader)]
				assert.True(t, ok, "expected header %s is not found", expectedHeader)
				assert.Equal(t, expectedValue, value)
			}
		})
	}
}

func Test_ClientReusesConnectionAfterErrorResponse(t *testing.T) {
	status, err := json.Marshal(metav1.Status{
		Status:  metav1.StatusFailure,
		Message: "boom",
		Code:    http.StatusInternalServerError,
	})
	require.NoError(t, err)

	// trailing data after the JSON document is left unread by the JSON decoder
	errBody := append(status, []byte("\n"+strings.Repeat(" ", 2048))...)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(errBody)
	}))

	var newConns atomic.Int32

	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}

	server.StartTLS()
	defer server.Close()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	client := New(ClientConfig{ServerIP: u.Host})
	t.Cleanup(client.httpClient.CloseIdleConnections)

	for range 5 {
		_, err := client.do("/test", "server", http.MethodGet, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	}

	assert.EqualValues(t, 1, newConns.Load(), "expected the connection to be reused")
}
