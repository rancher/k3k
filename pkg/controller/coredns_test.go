package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func TestGenerateCustomConfigMap(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *v1beta1.Cluster
		wantData map[string]string
	}{
		{
			name: "no custom DNS",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
		},
		{
			name: "coredns configuration with overrides",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
				Spec: v1beta1.ClusterSpec{
					DNS: &v1beta1.CustomDNS{
						CoreDNS: &v1beta1.CoreDNS{
							CustomConfig: []v1beta1.CustomDNSConfig{
								{
									Name:  "forward.override",
									Value: "   forward . 8.8.8.8 1.1.1.1",
								},
								{
									Name:  "rewrite.override",
									Value: "   rewrite stop name regex (.*)\\.old\\.local {1}.new.local",
								},
							},
						},
					},
				},
			},
			wantData: map[string]string{
				"forward.override": "   forward . 8.8.8.8 1.1.1.1",
				"rewrite.override": "   rewrite stop name regex (.*)\\.old\\.local {1}.new.local",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := GenerateCustomConfigMap(tt.cluster)

			if tt.wantData == nil {
				assert.Nil(t, cm)
				return
			}

			assert.Equal(t, tt.wantData, cm.Data)
			assert.Equal(t, coreDNSCustomConfigMapName, cm.Name)
			assert.Equal(t, kubeSystemNamespace, cm.Namespace)
		})
	}
}
