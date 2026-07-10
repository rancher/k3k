package k3s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	k3sv1 "github.com/k3s-io/api/k3s.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

type testCase[T any] struct {
	name            string
	snapshot        *v1beta1.ETCDSnapshot
	s3Config        *EtcdS3
	isServerRunning bool
	serverStatus    int
	serverResponse  string
	expectedResult  T
	expectedErr     error
}

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

	tests := []testCase[*SnapshotResult]{
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
			expectedErr:     errSnapshotRequest,
		},
		{
			name:            "invalid server response",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `not-a-json`,
			expectedErr:     errSnapshotRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, snapshotOperationSave, func(c *Client) (*SnapshotResult, error) {
				return c.SaveSnapshot(tt.snapshot, tt.s3Config)
			})
		})
	}
}

func Test_DeleteSnapshot(t *testing.T) {
	snapshot := &v1beta1.ETCDSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-snapshot",
		},
		Spec: v1beta1.ETCDSnapshotSpec{
			ClusterName: "test-cluster",
			Dir:         "/var/lib/rancher/k3s/server/db/snapshots",
			Compress:    true,
		},
		Status: v1beta1.ETCDSnapshotStatus{
			Location: "/var/lib/rancher/k3s/server/db/snapshots/test-snapshot-123",
		},
	}

	tests := []testCase[*SnapshotResult]{
		{
			name:            "server not ready",
			snapshot:        snapshot,
			isServerRunning: false,
			expectedErr:     ErrServerNotReady,
		},
		{
			name:            "snapshot deleted",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `{"deleted":["on-demand-test-snapshot"]}`,
			expectedResult:  &SnapshotResult{Deleted: []string{"on-demand-test-snapshot"}},
		},
		{
			name:            "snapshot deleted from s3",
			snapshot:        snapshot,
			s3Config:        DefaultEtcdS3,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `{"deleted":["on-demand-test-snapshot"]}`,
			expectedResult:  &SnapshotResult{Deleted: []string{"on-demand-test-snapshot"}},
		},
		{
			name:            "server error",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusInternalServerError,
			expectedErr:     errSnapshotRequest,
		},
		{
			name:            "invalid server response",
			snapshot:        snapshot,
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `not-a-json`,
			expectedErr:     errSnapshotRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, snapshotOperationDelete, func(c *Client) (*SnapshotResult, error) {
				return c.DeleteSnapshot(tt.snapshot, tt.s3Config)
			})
		})
	}
}

func Test_ListSnapshots(t *testing.T) {
	tests := []testCase[*k3sv1.ETCDSnapshotFileList]{
		{
			name:            "server not ready",
			isServerRunning: false,
			expectedErr:     ErrServerNotReady,
		},
		{
			name:            "snapshots list",
			isServerRunning: true,
			serverStatus:    http.StatusOK,
			serverResponse:  `{"kind":"list","apiversion":"v1","items":[{"kind":"etcdsnapshotfile","apiversion":"k3s.cattle.io/v1","metadata":{"name":"test-snapshot"},"spec":{"snapshotName":"test-snapshot","location":"file:///var/lib/rancher/k3s/server/db/snapshots/test-snapshot"}}]}`,
			expectedResult: &k3sv1.ETCDSnapshotFileList{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "list",
				},
				Items: []k3sv1.ETCDSnapshotFile{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       "etcdsnapshotfile",
							APIVersion: "k3s.cattle.io/v1",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-snapshot",
						},
						Spec: k3sv1.ETCDSnapshotSpec{
							SnapshotName: "test-snapshot",
							Location:     "file:///var/lib/rancher/k3s/server/db/snapshots/test-snapshot",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t, snapshotOperationList, func(c *Client) (*k3sv1.ETCDSnapshotFileList, error) {
				return c.ListSnapshots(tt.s3Config)
			})
		})
	}
}

func (tt *testCase[T]) testHandler(t *testing.T, req *snapshotRequest, operation snapshotOperation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		user, _, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "server", user)

		req = &snapshotRequest{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(req))
		require.NotNil(t, req)

		assert.Equal(t, operation, req.Operation)
		if tt.snapshot != nil && tt.snapshot.Status.Location != "" {
			assert.Equal(t, []string{filepath.Base(tt.snapshot.Status.Location)}, req.Name)
		}

		if req.Dir != nil {
			assert.Equal(t, tt.snapshot.Spec.Dir, *req.Dir)
		}

		assert.Equal(t, tt.s3Config, req.S3)

		w.WriteHeader(tt.serverStatus)
		_, err := w.Write([]byte(tt.serverResponse))
		assert.NoError(t, err)
	}
}

func (tt *testCase[T]) run(t *testing.T, operation snapshotOperation, k3sRequset func(*Client) (T, error)) {
	var (
		req          = &snapshotRequest{}
		clientConfig = ClientConfig{}
	)

	mux := http.NewServeMux()
	mux.Handle(etcdSnapshotEndpoint, tt.testHandler(t, req, operation))

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

	result, err := k3sRequset(k3sClient)
	if tt.expectedErr != nil {
		require.ErrorIs(t, err, tt.expectedErr)
	} else {
		require.NoError(t, err)
	}

	assert.Equal(t, tt.expectedResult, result)
}
