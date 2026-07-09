package k3s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_SaveSnapshot(t *testing.T) {
	snapshot := &v1beta1.ETCDSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-snapshot",
		},
		Spec: v1beta1.ETCDSnapshotSpec{
			ClusterName: "test-cluster",
			Dir:         "/var/lib/rancher/k3s/server/db/snapshots",
			Compress:    true,
		},
	}

	tests := []struct {
		name            string
		snapshot        *v1beta1.ETCDSnapshot
		s3Config        *EtcdS3
		isServerRunning bool
		serverStatus    int
		serverResponse  string
		expectedResult  *SnapshotResult
		expectedErr     error
	}{
		{
			name:            "server not ready",
			snapshot:        snapshot,
			isServerRunning: false,
			expectedErr:     ErrServerNotReady,
		},
		{
			name:            "snapshot saved",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `{"created":["on-demand-test-snapshot"]}`,
			expectedResult:  &SnapshotResult{Created: []string{"on-demand-test-snapshot"}},
		},
		{
			name:            "snapshot saved to s3",
			snapshot:        snapshot,
			s3Config:        DefaultEtcdS3,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `{"created":["on-demand-test-snapshot"]}`,
			expectedResult:  &SnapshotResult{Created: []string{"on-demand-test-snapshot"}},
		},
		{
			name:            "server error",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusInternalServerError,
			expectedErr:     ErrSnapshotRequest,
		},
		{
			name:            "invalid server response",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `not-a-json`,
			expectedErr:     ErrSnapshotRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *SnapshotRequest

			token := "token"

			mux := http.NewServeMux()
			mux.Handle(ETCDSnapshotEndpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)

				user, password, ok := r.BasicAuth()
				assert.True(t, ok)
				assert.Equal(t, "server", user)
				assert.Equal(t, token, password)

				req = &SnapshotRequest{}
				require.NoError(t, json.NewDecoder(r.Body).Decode(req))

				w.WriteHeader(tt.serverStatus)
				_, err := w.Write([]byte(tt.serverResponse))
				assert.NoError(t, err)
			}))

			clientConfig := ClientConfig{Token: token}

			mockServer := httptest.NewUnstartedServer(mux)

			clientConfig.ServerIP = "127.0.0.1:0"

			if tt.isServerRunning {
				mockServer.StartTLS()
				defer mockServer.Close()

				u, err := url.Parse(mockServer.URL)
				require.NoError(t, err)

				clientConfig.ServerIP = u.Host
			}

			k3sClient := New(clientConfig)

			result, err := k3sClient.SaveSnapshot(tt.snapshot, tt.s3Config)
			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedResult, result)

			if tt.isServerRunning && tt.expectedErr == nil {
				require.NotNil(t, req)
				assert.Equal(t, SnapshotOperationSave, req.Operation)
				assert.Equal(t, []string{tt.snapshot.Name}, req.Name)

				require.NotNil(t, req.Dir)
				assert.Equal(t, tt.snapshot.Spec.Dir, *req.Dir)

				require.NotNil(t, req.Compress)
				assert.Equal(t, tt.snapshot.Spec.Compress, *req.Compress)

				assert.Equal(t, tt.s3Config, req.S3)
			}
		})
	}
}
