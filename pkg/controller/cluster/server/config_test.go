package server

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

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

func Test_BuildServerConfig(t *testing.T) {
	type args struct {
		cluster    *v1beta1.Cluster
		initServer bool
		token      string
		serviceIP  string
	}

	tests := []struct {
		name         string
		args         args
		expectedData serverConfig
	}{
		{
			name: "Init server config with default cluster spec",
			args: args{
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
				token:      testToken,
				serviceIP:  testServiceIP,
			},
			expectedData: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             []string{testServiceIP, ServiceName(testClusterName), fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace)},
			},
		},
		{
			name: "server config with default cluster spec",
			args: args{
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
				token:      testToken,
				serviceIP:  testServiceIP,
			},
			expectedData: serverConfig{
				ClusterInit:        true,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             []string{testServiceIP, ServiceName(testClusterName), fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace)},
				Server:             "https://" + testServiceIP,
			},
		},
		{
			name: "Init server config with virtual mode cluster",
			args: args{
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
				token:      testToken,
				serviceIP:  testServiceIP,
			},
			expectedData: serverConfig{
				ClusterInit: true,
				ClusterCIDR: defaultVirtualClusterCIDR,
				ServiceCIDR: defaultVirtualServiceCIDR,
				Token:       testToken,
				TLSSAN:      []string{testServiceIP, ServiceName(testClusterName), fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace)},
			},
		},
		{
			name: "server config with virtual mode cluster",
			args: args{
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
				token:      testToken,
				serviceIP:  testServiceIP,
			},
			expectedData: serverConfig{
				ClusterInit: true,
				ClusterCIDR: defaultVirtualClusterCIDR,
				ServiceCIDR: defaultVirtualServiceCIDR,
				Token:       testToken,
				TLSSAN:      []string{testServiceIP, ServiceName(testClusterName), fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace)},
				Server:      "https://" + testServiceIP,
			},
		},
		{
			name: "Init server config with clusterDNS cluster spec",
			args: args{
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
				token:      testToken,
				serviceIP:  testServiceIP,
			},
			expectedData: serverConfig{
				ClusterInit:        true,
				ClusterDNS:         testClusterDNS,
				ClusterCIDR:        defaultSharedClusterCIDR,
				ServiceCIDR:        defaultSharedServiceCIDR,
				DisableAgent:       true,
				EgressSelectorMode: "disabled",
				Token:              testToken,
				Disable:            []string{"servicelb", "traefik", "metrics-server", "local-storage"},
				TLSSAN:             []string{testServiceIP, ServiceName(testClusterName), fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverConfig := buildServerConfig(tt.args.cluster, tt.args.initServer, tt.args.serviceIP, tt.args.token)
			if !reflect.DeepEqual(tt.expectedData, serverConfig) {
				t.Errorf("found %v, expected %v", serverConfig, tt.expectedData)
			}
		})
	}
}

func Test_SetupStartCommand_MultipleServerArgs(t *testing.T) {
	servers := int32(1)
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: testClusterNamespace},
		Spec: v1beta1.ClusterSpec{
			Servers:    &servers,
			ServerArgs: []string{"--tls-san=foo.example.com", "--kubelet-arg=cgroups-per-qos=false", "--kubelet-arg=enforce-node-allocatable="},
		},
	}
	s := &Server{cluster: cluster}

	script, err := s.setupStartCommand()
	if err != nil {
		t.Fatalf("setupStartCommand failed: %v", err)
	}

	expected := `EXTRA_ARGS="--tls-san=foo.example.com --kubelet-arg=cgroups-per-qos=false --kubelet-arg=enforce-node-allocatable="`
	if !strings.Contains(script, expected) {
		t.Errorf("script missing quoted EXTRA_ARGS assignment.\nwant: %s\ngot script:\n%s", expected, script)
	}
}
