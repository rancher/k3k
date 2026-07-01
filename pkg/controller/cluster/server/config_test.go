package server

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	defaultVirtualClusterCIDR = "10.52.0.0/16"
	defaultVirtualServiceCIDR = "10.53.0.0/16"
	defaultSharedClusterCIDR  = "10.42.0.0/16"
	defaultSharedServiceCIDR  = "10.43.0.0/16"
	testClusterDNS            = "10.42.0.10"
	testToken                 = "123456"
	testServiceIP             = "1.1.1.1"
	testClusterName           = "test-cluster"
	testClusterNamespace      = "test-ns"
)

var defaultTLSSANs = []string{
	testServiceIP,
	ServiceName(testClusterName),
	fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace),
}

func Test_buildServerConfig(t *testing.T) {
	tests := []struct {
		name       string
		cluster    *v1beta1.Cluster
		initServer bool
		expected   serverConfig
	}{
		{
			name: "init server with default (shared) cluster spec",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultSharedClusterCIDR,
					ServiceCIDR: defaultSharedServiceCIDR,
				},
			},
			initServer: true,
			expected: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             defaultTLSSANs,
			},
		},
		{
			name: "non-init server with default (shared) cluster spec",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultSharedClusterCIDR,
					ServiceCIDR: defaultSharedServiceCIDR,
				},
			},
			initServer: false,
			expected: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             defaultTLSSANs,
				Server:             "https://" + testServiceIP,
			},
		},
		{
			name: "explicit shared mode cluster",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.SharedClusterMode,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultSharedClusterCIDR,
					ServiceCIDR: defaultSharedServiceCIDR,
				},
			},
			initServer: true,
			expected: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             defaultTLSSANs,
			},
		},
		{
			name: "init server with virtual mode cluster",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.VirtualClusterMode,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultVirtualClusterCIDR,
					ServiceCIDR: defaultVirtualServiceCIDR,
				},
			},
			initServer: true,
			expected: serverConfig{
				ClusterInit: true,
				ClusterCIDR: defaultVirtualClusterCIDR,
				ServiceCIDR: defaultVirtualServiceCIDR,
				Token:       testToken,
				TLSSAN:      defaultTLSSANs,
			},
		},
		{
			name: "non-init server with virtual mode cluster",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Spec: v1beta1.ClusterSpec{
					Mode: v1beta1.VirtualClusterMode,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultVirtualClusterCIDR,
					ServiceCIDR: defaultVirtualServiceCIDR,
				},
			},
			initServer: false,
			expected: serverConfig{
				ClusterInit: true,
				ClusterCIDR: defaultVirtualClusterCIDR,
				ServiceCIDR: defaultVirtualServiceCIDR,
				Token:       testToken,
				TLSSAN:      defaultTLSSANs,
				Server:      "https://" + testServiceIP,
			},
		},
		{
			name: "init server with clusterDNS cluster spec",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Spec: v1beta1.ClusterSpec{
					ClusterDNS: testClusterDNS,
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultSharedClusterCIDR,
					ServiceCIDR: defaultSharedServiceCIDR,
				},
			},
			initServer: true,
			expected: serverConfig{
				ClusterInit:        true,
				ClusterDNS:         testClusterDNS,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             defaultTLSSANs,
			},
		},
		{
			name: "init server with custom TLSSANs merged and sorted",
			cluster: &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testClusterName,
					Namespace: testClusterNamespace,
				},
				Spec: v1beta1.ClusterSpec{
					// intentionally unsorted and with a duplicate of serviceIP
					TLSSANs: []string{"custom.example.com", testServiceIP},
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultSharedClusterCIDR,
					ServiceCIDR: defaultSharedServiceCIDR,
				},
			},
			initServer: true,
			expected: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				// sets.List() returns a sorted, de-duplicated slice
				TLSSAN: []string{
					testServiceIP,
					"custom.example.com",
					ServiceName(testClusterName),
					fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := buildServerConfig(tt.cluster, tt.initServer, testServiceIP, testToken)
			assert.Equal(t, tt.expected, config)
		})
	}
}

func Test_setupStartCommand_MultipleServerArgs(t *testing.T) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: testClusterNamespace},
		Spec: v1beta1.ClusterSpec{
			Servers:    new(int32(1)),
			ServerArgs: []string{"--tls-san=foo.example.com", "--kubelet-arg=cgroups-per-qos=false", "--kubelet-arg=enforce-node-allocatable="},
		},
	}
	s := &Server{cluster: cluster}

	script, err := s.setupStartCommand()
	require.NoError(t, err)

	expected := `EXTRA_ARGS="--tls-san=foo.example.com --kubelet-arg=cgroups-per-qos=false --kubelet-arg=enforce-node-allocatable="`
	assert.Contains(t, script, expected)
}
