package agent

import (
	"testing"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_sharedAgentData(t *testing.T) {
	type args struct {
		cluster     *v1alpha1.Cluster
		serviceName string
		ip          string
		kubeletPort int
		webhookPort int
		token       string
	}

	tests := []struct {
		name         string
		args         args
		expectedData map[string]string
	}{
		{
			name: "simple config",
			args: args{
				cluster: &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Spec: v1alpha1.ClusterSpec{
						Version: "v1.2.3",
					},
				},
				kubeletPort: 10250,
				webhookPort: 9443,
				ip:          "10.0.0.21",
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.2.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
				"webhookPort":      "9443",
			},
		},
		{
			name: "version in status",
			args: args{
				cluster: &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Spec: v1alpha1.ClusterSpec{
						Version: "v1.2.3",
					},
					Status: v1alpha1.ClusterStatus{
						HostVersion: "v1.3.3",
					},
				},
				ip:          "10.0.0.21",
				kubeletPort: 10250,
				webhookPort: 9443,
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.2.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
				"webhookPort":      "9443",
			},
		},
		{
			name: "missing version in spec",
			args: args{
				cluster: &v1alpha1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Status: v1alpha1.ClusterStatus{
						HostVersion: "v1.3.3",
					},
				},
				kubeletPort: 10250,
				webhookPort: 9443,
				ip:          "10.0.0.21",
				serviceName: "service-name",
				token:       "dnjklsdjnksd892389238",
			},
			expectedData: map[string]string{
				"clusterName":      "mycluster",
				"clusterNamespace": "ns-1",
				"serverIP":         "10.0.0.21",
				"serviceName":      "service-name",
				"token":            "dnjklsdjnksd892389238",
				"version":          "v1.3.3",
				"mirrorHostNodes":  "false",
				"kubeletPort":      "10250",
				"webhookPort":      "9443",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := sharedAgentData(tt.args.cluster, tt.args.serviceName, tt.args.token, tt.args.ip, tt.args.kubeletPort, tt.args.webhookPort)

			data := make(map[string]string)
			err := yaml.Unmarshal([]byte(config), data)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedData, data)
		})
	}
}
