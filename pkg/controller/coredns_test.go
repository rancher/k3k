package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestGenerateCustomConfigMap(t *testing.T) {
	tests := []struct {
		name             string
		cluster          *v1beta1.Cluster
		expectData       bool
		expectedCorefile string
	}{
		{
			name: "no custom DNS",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			expectData: false,
		},
		{
			name: "single forwarder",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: v1beta1.ClusterSpec{
					CustomDNS: &v1beta1.CustomDNS{
						Forwarders: []v1beta1.CustomForwarder{{IPs: []string{"8.8.8.8"}}},
					},
				},
			},
			expectData:       true,
			expectedCorefile: "    forward . 8.8.8.8\n",
		},
		{
			name: "multiple upstream IPs",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: v1beta1.ClusterSpec{
					CustomDNS: &v1beta1.CustomDNS{
						Forwarders: []v1beta1.CustomForwarder{{IPs: []string{"8.8.8.8", "1.1.1.1"}}},
					},
				},
			},
			expectData:       true,
			expectedCorefile: "    forward . 8.8.8.8 1.1.1.1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := GenerateCustomConfigMap(tt.cluster)

			if cm == nil {
				t.Fatal("expected non-nil ConfigMap")
			}

			if cm.Namespace != tt.cluster.Namespace {
				t.Errorf("namespace: got %q, want %q", cm.Namespace, tt.cluster.Namespace)
			}

			if !tt.expectData {
				if len(cm.Data) != 0 {
					t.Errorf("expected empty data, got %v", cm.Data)
				}
				return
			}

			corefile, ok := cm.Data["custom.override"]
			if !ok {
				t.Fatalf("missing key %q in ConfigMap data", "custom.override")
			}

			if corefile != tt.expectedCorefile {
				t.Errorf("corefile:\ngot:  %q\nwant: %q", corefile, tt.expectedCorefile)
			}
		})
	}
}
