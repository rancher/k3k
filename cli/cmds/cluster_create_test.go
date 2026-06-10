package cmds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestValidateCreateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  CreateConfig
		wantErr string
	}{
		{
			name: "valid defaults",
			config: CreateConfig{
				servers:         1,
				persistenceType: string(v1beta1.DynamicPersistenceMode),
				mode:            string(v1beta1.SharedClusterMode),
			},
		},
		{
			name: "invalid server count",
			config: CreateConfig{
				servers:         0,
				persistenceType: string(v1beta1.DynamicPersistenceMode),
				mode:            string(v1beta1.SharedClusterMode),
			},
			wantErr: "invalid number of servers",
		},
		{
			name: "invalid persistence type",
			config: CreateConfig{
				servers:         1,
				persistenceType: "invalid",
				mode:            string(v1beta1.SharedClusterMode),
			},
			wantErr: `persistence-type should be one of "dynamic" or "ephemeral"`,
		},
		{
			name: "invalid storage request size",
			config: CreateConfig{
				servers:            1,
				persistenceType:    string(v1beta1.DynamicPersistenceMode),
				storageRequestSize: "bad",
				mode:               string(v1beta1.SharedClusterMode),
			},
			wantErr: `invalid storage size, should be a valid resource quantity e.g "10Gi"`,
		},
		{
			name: "negative storage request size",
			config: CreateConfig{
				servers:            1,
				persistenceType:    string(v1beta1.DynamicPersistenceMode),
				storageRequestSize: "-1G",
				mode:               string(v1beta1.SharedClusterMode),
			},
			wantErr: "invalid storage size, should be greater than zero",
		},
		{
			name: "zero storage request size",
			config: CreateConfig{
				servers:            1,
				persistenceType:    string(v1beta1.DynamicPersistenceMode),
				storageRequestSize: "0",
				mode:               string(v1beta1.SharedClusterMode),
			},
			wantErr: "invalid storage size, should be greater than zero",
		},
		{
			name: "invalid mode",
			config: CreateConfig{
				servers:            1,
				persistenceType:    string(v1beta1.DynamicPersistenceMode),
				storageRequestSize: "10Gi",
				mode:               "invalid",
			},
			wantErr: `mode should be one of "shared" or "virtual"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCreateConfig(&tt.config)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestNewClusterRejectsNonPositiveStorageRequestSize(t *testing.T) {
	config := &CreateConfig{
		servers:            1,
		persistenceType:    string(v1beta1.DynamicPersistenceMode),
		storageRequestSize: "-1G",
		mode:               string(v1beta1.SharedClusterMode),
	}

	_, err := newCluster("test", "default", config)
	require.ErrorContains(t, err, "invalid storage size, should be greater than zero")
}
