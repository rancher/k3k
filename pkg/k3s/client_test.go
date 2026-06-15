package k3s

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
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
			if len(tt.expectedHeaders) != len(client.staticHeaders) {
				t.Fatal("expected headers are not equal actual headers")
			}

			for expectedHeader, expectedValue := range tt.expectedHeaders {
				value, ok := client.staticHeaders[http.CanonicalHeaderKey(expectedHeader)]
				if !ok {
					t.Fatalf("expected header %s is not found", expectedHeader)
				}

				if !reflect.DeepEqual(expectedValue, value) {
					t.Fatalf("expected %v for header %s, got %v instead", expectedValue, expectedHeader, value)
				}
			}
		})
	}
}

func getServerAddress(serverURL string) string {
	serverAddress := strings.Split(serverURL, "//")
	if len(serverAddress) == 2 {
		return serverAddress[1]
	}

	return ""
}
