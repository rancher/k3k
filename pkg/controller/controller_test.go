package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_K3S_Image(t *testing.T) {
	type args struct {
		cluster  *v1beta1.Cluster
		k3sImage string
	}

	tests := []struct {
		name         string
		args         args
		expectedData string
	}{
		{
			name: "cluster with assigned version spec",
			args: args{
				k3sImage: "rancher/k3s",
				cluster: &v1beta1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Spec: v1beta1.ClusterSpec{
						Version: "v1.2.3",
					},
				},
			},
			expectedData: "rancher/k3s:v1.2.3",
		},
		{
			name: "cluster with empty version spec and assigned hostVersion status",
			args: args{
				k3sImage: "rancher/k3s",
				cluster: &v1beta1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
					Status: v1beta1.ClusterStatus{
						HostVersion: "v4.5.6",
					},
				},
			},
			expectedData: "rancher/k3s:v4.5.6-k3s1",
		},
		{
			name: "cluster with empty version spec and empty hostVersion status",
			args: args{
				k3sImage: "rancher/k3s",
				cluster: &v1beta1.Cluster{
					ObjectMeta: v1.ObjectMeta{
						Name:      "mycluster",
						Namespace: "ns-1",
					},
				},
			},
			expectedData: "rancher/k3s:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullImage := K3SImage(tt.args.cluster, tt.args.k3sImage)
			assert.Equal(t, tt.expectedData, fullImage)
		})
	}
}
