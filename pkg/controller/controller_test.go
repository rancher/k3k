package controller

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_FilterDNSNames(t *testing.T) {
	tests := map[string]struct {
		names    []string
		expected []string
	}{
		"no names": {
			names:    nil,
			expected: nil,
		},
		"only IPs": {
			names:    []string{"10.0.0.5", "192.168.1.1", "::1"},
			expected: []string{},
		},
		"only DNS names": {
			names:    []string{"my-cluster.example.com", "other.example.com"},
			expected: []string{"my-cluster.example.com", "other.example.com"},
		},
		"mixed IPs and DNS names keeps the order": {
			names:    []string{"10.0.0.5", "my-cluster.example.com", "fd00::1", "other.example.com"},
			expected: []string{"my-cluster.example.com", "other.example.com"},
		},
		"wildcards are kept": {
			names:    []string{"*.example.com"},
			expected: []string{"*.example.com"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			original := slices.Clone(tt.names)

			filtered := FilterDNSNames(tt.names)
			assert.Equal(t, tt.expected, filtered)

			// the given slice should be left untouched
			assert.Equal(t, original, tt.names)
		})
	}
}

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
					ObjectMeta: metav1.ObjectMeta{
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
					ObjectMeta: metav1.ObjectMeta{
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
					ObjectMeta: metav1.ObjectMeta{
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
