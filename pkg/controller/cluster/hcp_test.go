package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

func Test_hcpRegistrationCommand(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		serverURL string
		token     string
		want      string
	}{
		{
			name:      "with version",
			version:   "v1.33.1-k3s1",
			serverURL: "https://1.2.3.4:30443",
			token:     "abcd1234",
			want:      "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.33.1-k3s1 K3S_URL=https://1.2.3.4:30443 K3S_TOKEN=abcd1234 sh -",
		},
		{
			name:      "without version",
			version:   "",
			serverURL: "https://hcp.example.com",
			token:     "tok",
			want:      "curl -sfL https://get.k3s.io | K3S_URL=https://hcp.example.com K3S_TOKEN=tok sh -",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hcpRegistrationCommand(tt.version, tt.serverURL, tt.token)
			assert.Equal(t, tt.want, got)
		})
	}
}

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

	t.Run("nodeport service produces ready-to-copy command", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      server.ServiceName(cluster.Name),
				Namespace: cluster.Namespace,
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeNodePort,
				ClusterIP: "10.43.0.50",
				Ports: []corev1.ServicePort{
					{Name: "k3s-server-port", Port: 443, NodePort: 31001},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		r := &ClusterReconciler{Client: fakeClient}

		c := cluster.DeepCopy()
		require.NoError(t, r.ensureHCPRegistration(context.Background(), c, "join-token-xyz"))

		assert.Contains(t, c.Status.HCPRegistration, "K3S_URL=https://hcp.example.com:31001")
		assert.Contains(t, c.Status.HCPRegistration, "K3S_TOKEN=join-token-xyz")
		assert.Contains(t, c.Status.HCPRegistration, "INSTALL_K3S_VERSION=v1.33.1-k3s1")
		assert.True(t, strings.HasPrefix(c.Status.HCPRegistration, "curl -sfL https://get.k3s.io"))
	})

	t.Run("clusterip-only service sets degraded condition and clears registration", func(t *testing.T) {
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
		c.Status.HCPRegistration = "stale-value"
		require.NoError(t, r.ensureHCPRegistration(context.Background(), c, "ignored"))

		assert.Empty(t, c.Status.HCPRegistration)

		cond := meta.FindStatusCondition(c.Status.Conditions, ConditionReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, ReasonHCPNoExternalEndpoint, cond.Reason)
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

func Test_hcpEndpointAddress(t *testing.T) {
	t.Run("ipv4 literal is passed through", func(t *testing.T) {
		got, err := hcpEndpointAddress("10.144.101.195")
		require.NoError(t, err)
		assert.Equal(t, "10.144.101.195", got.IP)
		assert.Empty(t, got.Hostname)
	})

	t.Run("unresolvable hostname errors", func(t *testing.T) {
		_, err := hcpEndpointAddress("definitely-not-a-real-host.invalid")
		require.Error(t, err)
	})
}

// Compile-time assertion: every reused exported name from the controller
// package below this test file must remain stable. If `controller.K3SImage`
// disappears (refactor), this guards the dependency.
var _ = controller.K3SImage
