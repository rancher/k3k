package cmds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_printClusterDetails(t *testing.T) {
	tests := []struct {
		name    string
		cluster *v1beta1.Cluster
		want    string
		wantErr bool
	}{
		{
			name: "simple cluster",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode:    v1beta1.SharedClusterMode,
					Version: "123",
					Persistence: v1beta1.PersistenceConfig{
						Type: v1beta1.DynamicPersistenceMode,
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Version: 123 (Host: 456)
  Persistence:
    Type: dynamic`,
		},
		{
			name: "simple cluster with no version",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.SharedClusterMode,
					Persistence: v1beta1.PersistenceConfig{
						Type: v1beta1.DynamicPersistenceMode,
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Version: 456 (Host: 456)
  Persistence:
    Type: dynamic`,
		},
		{
			name: "cluster with agents",
			cluster: &v1beta1.Cluster{
				Spec: v1beta1.ClusterSpec{
					Mode:   v1beta1.SharedClusterMode,
					Agents: ptr.To[int32](3),
					Persistence: v1beta1.PersistenceConfig{
						Type:               v1beta1.DynamicPersistenceMode,
						StorageClassName:   ptr.To("local-path"),
						StorageRequestSize: ptr.To(resource.MustParse("3G")),
					},
				},
				Status: v1beta1.ClusterStatus{
					HostVersion: "456",
				},
			},
			want: `Cluster details:
  Mode: shared
  Servers: 0
  Agents: 3
  Version: 456 (Host: 456)
  Persistence:
    Type: dynamic
    StorageClass: local-path
    Size: 3G`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterDetails, err := getClusterDetails(tt.cluster)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, clusterDetails)
		})
	}
}

func Test_extractHost(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		override  string
		want      string
	}{
		{
			name:      "plain host",
			serverURL: "https://example.com",
			want:      "example.com",
		},
		{
			name:      "host with port",
			serverURL: "https://example.com:6443",
			want:      "example.com",
		},
		{
			name:      "ipv4 with port",
			serverURL: "https://192.0.2.1:6443",
			want:      "192.0.2.1",
		},
		{
			name:      "ipv6 without port",
			serverURL: "https://[2001:db8::1]",
			want:      "2001:db8::1",
		},
		{
			name:      "ipv6 with port",
			serverURL: "https://[2001:db8::1]:6443",
			want:      "2001:db8::1",
		},
		{
			name:      "override wins",
			serverURL: "https://example.com:6443",
			override:  "custom.host",
			want:      "custom.host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := extractHost(tt.serverURL, tt.override)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, host)
		})
	}
}
