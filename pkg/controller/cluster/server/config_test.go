package server

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"

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
		lbIngress  []corev1.LoadBalancerIngress
		expected   serverConfig
	}{
		{
			name: "init server with default (shared) cluster spec",
			cluster: &v1beta1.Cluster{
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
				Name:      testClusterName,
				Namespace: testClusterNamespace,
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
		{
			name: "init server with expose SANs merged and sorted",
			cluster: &v1beta1.Cluster{
				Name:      testClusterName,
				Namespace: testClusterNamespace,
				Spec: v1beta1.ClusterSpec{
					Mode:    v1beta1.VirtualClusterMode,
					TLSSANs: []string{"10.0.0.1"},
				},
				Status: v1beta1.ClusterStatus{
					ClusterCIDR: defaultVirtualClusterCIDR,
					ServiceCIDR: defaultVirtualServiceCIDR,
				},
			},
			initServer: true,
			lbIngress: []corev1.LoadBalancerIngress{
				{IP: "172.18.0.100"},
				{Hostname: "lb.example.com"},
				{IP: "10.0.0.1"},
			},
			expected: serverConfig{
				ClusterInit: true,
				ClusterCIDR: defaultVirtualClusterCIDR,
				ServiceCIDR: defaultVirtualServiceCIDR,
				Token:       testToken,
				TLSSAN: []string{
					testServiceIP,
					"10.0.0.1",
					"172.18.0.100",
					ServiceName(testClusterName),
					fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace),
					"lb.example.com",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: testServiceIP,
				},
			}

			if len(tt.lbIngress) > 0 {
				service.Spec.Type = corev1.ServiceTypeLoadBalancer
				service.Status.LoadBalancer.Ingress = tt.lbIngress
			}

			config := buildServerConfig(tt.cluster, tt.initServer, service, testToken)
			assert.Equal(t, tt.expected, config)
		})
	}
}

func Test_buildServerConfig_TLSSANs(t *testing.T) {
	cluster := &v1beta1.Cluster{
		Name:      testClusterName,
		Namespace: testClusterNamespace,
		Spec: v1beta1.ClusterSpec{
			Mode:    v1beta1.HCPClusterMode,
			TLSSANs: []string{"custom.example.com"},
		},
		Status: v1beta1.ClusterStatus{
			// a node IP added by a previous reconcile, of a node that is gone
			TLSSANs: []string{"10.0.0.9"},
		},
	}

	service := &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: testServiceIP}}

	config := buildServerConfig(cluster, true, service, testToken, "10.0.0.2", "10.0.0.1")

	expected := []string{
		testServiceIP,
		"10.0.0.1",
		"10.0.0.2",
		"custom.example.com",
		ServiceName(testClusterName),
		fmt.Sprintf("%s.%s", ServiceName(testClusterName), testClusterNamespace),
	}

	assert.Equal(t, expected, config.TLSSAN)
	assert.Equal(t, expected, cluster.Status.TLSSANs, "the status should be rebuilt and not keep the stale SANs")
}

func Test_setupStartCommand_MultipleServerArgs(t *testing.T) {
	cluster := &v1beta1.Cluster{
		Name: testClusterName, Namespace: testClusterNamespace,
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

func Test_setupStartCommand_LogsToLivenessProbeFile(t *testing.T) {
	teeTarget := regexp.MustCompile(`\| tee (\S+)`)

	for _, servers := range []int32{1, 3} {
		t.Run(fmt.Sprintf("%d servers", servers), func(t *testing.T) {
			cluster := &v1beta1.Cluster{
				Name: testClusterName, Namespace: testClusterNamespace,
				Spec: v1beta1.ClusterSpec{Servers: new(servers)},
			}
			s := &Server{cluster: cluster}

			script, err := s.setupStartCommand()
			require.NoError(t, err)

			matches := teeTarget.FindAllStringSubmatch(script, -1)
			require.NotEmpty(t, matches)

			for _, m := range matches {
				assert.Equal(t, k3sLogFile, m[1], "k3s must log to the file read by the liveness probe")
			}

			assert.Contains(t, livenessProbeCommand, k3sLogFile)

			// The log file has to be truncated before k3s (or the safe mode / cluster reset) is started,
			// so a message logged by the previous container cannot fail the probe.
			truncate := strings.Index(script, ": > "+k3sLogFile)
			require.NotEqual(t, -1, truncate)

			start := strings.Index(script, "\ncase ")
			require.NotEqual(t, -1, start)
			assert.Less(t, truncate, start)
		})
	}
}

func Test_livenessProbeCommand(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}

	tests := map[string]struct {
		log      *string
		wantFail bool
	}{
		"no log file": {},
		"empty log":   {log: new("")},
		"node removed from the cluster": {
			log:      new("level=info msg=\"This node has been removed from the cluster - please restart k3s to rejoin the cluster\"\n"),
			wantFail: true,
		},
		"rejoin after tombstone": {
			log: new("level=info msg=\"tombstone file has been detected, removing ${datadir}/server/db to rejoin the cluster\"\n"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			logFile := filepath.Join(t.TempDir(), "k3s.log")
			if tt.log != nil {
				require.NoError(t, os.WriteFile(logFile, []byte(*tt.log), 0o600))
			}

			cmd := strings.ReplaceAll(livenessProbeCommand, k3sLogFile, logFile)
			err := exec.Command(sh, "-c", cmd).Run()

			if !tt.wantFail {
				assert.NoError(t, err)
				return
			}

			var exitErr *exec.ExitError
			require.True(t, errors.As(err, &exitErr), "expected the probe to fail, got %v", err)
			assert.Equal(t, 1, exitErr.ExitCode())
		})
	}
}
