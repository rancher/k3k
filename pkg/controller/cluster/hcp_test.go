package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

func Test_ensureHCPRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "team-a"},
		Spec: v1beta1.ClusterSpec{
			Mode:    v1beta1.HCPClusterMode,
			Version: "v1.33.1-k3s1",
			TLSSANs: []string{"hcp.example.com"},
		},
		Status: v1beta1.ClusterStatus{
			TLSSANs: []string{"hcp.example.com"},
		},
	}

	t.Run("clusterip-only service returns ErrHCPNoExternalEndpoint", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      server.ServiceName(cluster.Name),
				Namespace: cluster.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.43.0.50",
				Ports: []corev1.ServicePort{
					{Name: "k3s-server-port", Port: 443},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		r := &ClusterReconciler{Client: fakeClient}

		c := cluster.DeepCopy()
		err := r.ensureHCPRegistration(context.Background(), c)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrHCPNoExternalEndpoint))
	})

	t.Run("nodeport service returns no error", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      server.ServiceName(cluster.Name),
				Namespace: cluster.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeNodePort,
				ClusterIP: "10.43.0.50",
				Ports: []corev1.ServicePort{
					{Name: "k3s-server-port", Port: 443, NodePort: 30443},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		r := &ClusterReconciler{Client: fakeClient}

		c := cluster.DeepCopy()
		assert.NoError(t, r.ensureHCPRegistration(context.Background(), c))
	})
}

func Test_parseHCPHostPort(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantHost string
		wantPort int32
		wantErr  bool
	}{
		{
			name:     "ip with explicit port",
			url:      "https://10.144.101.195:30337",
			wantHost: "10.144.101.195",
			wantPort: 30337,
		},
		{
			name:     "hostname without port defaults to 443",
			url:      "https://hcp.example.com",
			wantHost: "hcp.example.com",
			wantPort: 443,
		},
		{
			name:     "hostname with explicit port",
			url:      "https://hcp.example.com:6443",
			wantHost: "hcp.example.com",
			wantPort: 6443,
		},
		{
			name:    "missing host",
			url:     "https://",
			wantErr: true,
		},
		{
			name:    "non-numeric port",
			url:     "https://host:abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseHCPHostPort(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

func Test_selectNonLoopbackSAN(t *testing.T) {
	tests := []struct {
		name       string
		specSANs   []string
		statusSANs []string
		want       string
	}{
		{
			name:       "spec with loopback first, external second",
			specSANs:   []string{"127.0.0.1", "10.0.0.100"},
			statusSANs: []string{},
			want:       "10.0.0.100",
		},
		{
			name:       "spec with external first",
			specSANs:   []string{"10.0.0.100", "127.0.0.1"},
			statusSANs: []string{},
			want:       "10.0.0.100",
		},
		{
			name:       "spec empty, status with loopback first, external second",
			specSANs:   []string{},
			statusSANs: []string{"127.0.0.1", "10.43.0.50"},
			want:       "10.43.0.50",
		},
		{
			name:       "spec with only loopback",
			specSANs:   []string{"127.0.0.1", "::1"},
			statusSANs: []string{},
			want:       "",
		},
		{
			name:       "spec with localhost hostname",
			specSANs:   []string{"localhost", "example.com"},
			statusSANs: []string{},
			want:       "example.com",
		},
		{
			name:       "spec with external hostname",
			specSANs:   []string{"hcp.example.com"},
			statusSANs: []string{},
			want:       "hcp.example.com",
		},
		{
			name:       "empty spec and status",
			specSANs:   []string{},
			statusSANs: []string{},
			want:       "",
		},
		{
			name:       "ipv6 loopback filtered",
			specSANs:   []string{"::1", "2001:db8::1"},
			statusSANs: []string{},
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					TLSSANs: tt.specSANs,
				},
				Status: v1beta1.ClusterStatus{
					TLSSANs: tt.statusSANs,
				},
			}
			got := selectNonLoopbackSAN(cluster)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_hcpEndpointAddress(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantIP       string
		wantHostname string
		wantErr      bool
	}{
		{
			name:         "ipv4 literal is passed through",
			input:        "10.144.101.195",
			wantIP:       "10.144.101.195",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "unresolvable hostname errors",
			input:   "definitely-not-a-real-host.invalid",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback literal is rejected",
			input:   "127.0.0.1",
			wantErr: true,
		},
		{
			name:    "ipv6 loopback literal is rejected",
			input:   "::1",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback in range is rejected",
			input:   "127.0.0.100",
			wantErr: true,
		},
		{
			name:         "valid ipv6 literal passes through",
			input:        "2001:db8::1",
			wantIP:       "2001:db8::1",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "localhost hostname filters loopbacks",
			input:   "localhost",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hcpEndpointAddress(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tt.wantIP, got.IP)
			assert.Equal(t, tt.wantHostname, got.Hostname)
		})
	}
}

// Compile-time assertion: every reused exported name from the controller
// package below this test file must remain stable. If `controller.K3SImage`
// disappears (refactor), this guards the dependency.
var _ = controller.K3SImage
