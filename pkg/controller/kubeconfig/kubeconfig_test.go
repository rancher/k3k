package kubeconfig

// This file pins the current behavior of getURLFromService() across the different
// service types (ClusterIP, NodePort, LoadBalancer), Ingress exposure, and the
// TLS SAN fallback logic.
//
// Several test cases intentionally assert known quirks of the current implementation.
// They are documented here (and inline) so a future refactor knows exactly which
// behaviors it changes:
//
// 1. ClusterIP port handling:
//    Always uses Spec.ClusterIP and defaults to port 443; the service's declared
//    port is ignored. Only the serverPort override changes the port.
//
// 2. NodePort access:
//    Always uses hostServerIP:NodePort, with no internal/external detection.
//
// 3. LoadBalancer hostname support:
//    Reads only Status.LoadBalancer.Ingress[0].IP. The Hostname field is not
//    consulted, so a hostname-only ingress produces the invalid URL "https://".
//
// 4. Port override:
//    A non-zero serverPort parameter overrides the computed port.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

// TestURLGeneration_ClusterIP tests URL generation for ClusterIP service type
func TestURLGeneration_ClusterIP(t *testing.T) {
	tests := []struct {
		name         string
		hostServerIP string
		servicePort  int32
		serverPort   int
		expectedURL  string
	}{
		{
			name:         "ClusterIP with default port 443",
			hostServerIP: "10.0.0.1",
			servicePort:  443,
			serverPort:   0,
			expectedURL:  "https://10.43.0.100",
		},
		{
			// the service port is ignored for ClusterIP, so the URL has no port suffix
			name:         "ClusterIP ignores custom service port",
			hostServerIP: "10.0.0.1",
			servicePort:  8443,
			serverPort:   0,
			expectedURL:  "https://10.43.0.100",
		},
		{
			name:         "ClusterIP with serverPort override",
			hostServerIP: "10.0.0.1",
			servicePort:  443,
			serverPort:   9443,
			expectedURL:  "https://10.43.0.100:9443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cluster, svc := createClusterIPService("test-cluster", "default", tt.servicePort)
			fakeClient := createFakeClient(cluster, svc)

			oldURL, err := getURLFromService(ctx, fakeClient, cluster, tt.hostServerIP, tt.serverPort)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, oldURL)
		})
	}
}

// TestURLGeneration_NodePort tests URL generation for NodePort service type
func TestURLGeneration_NodePort(t *testing.T) {
	tests := []struct {
		name         string
		hostServerIP string
		nodePort     int32
		servicePort  int32
		expectedURL  string
	}{
		{
			name:         "NodePort external access",
			hostServerIP: "192.168.1.100",
			nodePort:     30443,
			servicePort:  443,
			expectedURL:  "https://192.168.1.100:30443",
		},
		{
			name:         "NodePort internal access",
			hostServerIP: "10.43.0.100", // same as ClusterIP
			nodePort:     30443,
			servicePort:  443,
			expectedURL:  "https://10.43.0.100:30443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cluster, svc := createNodePortService("test-cluster", "default", tt.servicePort, tt.nodePort)
			fakeClient := createFakeClient(cluster, svc)

			url, err := getURLFromService(ctx, fakeClient, cluster, tt.hostServerIP, 0)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, url)
		})
	}
}

// TestURLGeneration_LoadBalancer tests URL generation for LoadBalancer service type
func TestURLGeneration_LoadBalancer(t *testing.T) {
	tests := []struct {
		name         string
		hostServerIP string
		lbIP         string
		lbHostname   string
		servicePort  int32
		expectedURL  string
	}{
		{
			name:         "LoadBalancer with IP ingress",
			hostServerIP: "10.0.0.1",
			lbIP:         "203.0.113.10",
			lbHostname:   "",
			servicePort:  443,
			expectedURL:  "https://203.0.113.10",
		},
		{
			// quirk: the Hostname field is not read, so a hostname-only ingress
			// produces the invalid URL "https://" (empty host)
			name:         "LoadBalancer with hostname ingress",
			hostServerIP: "10.0.0.1",
			lbIP:         "",
			lbHostname:   "cluster.example.com",
			servicePort:  443,
			expectedURL:  "https://",
		},
		{
			name:         "LoadBalancer with custom port",
			hostServerIP: "10.0.0.1",
			lbIP:         "203.0.113.10",
			lbHostname:   "",
			servicePort:  8443,
			expectedURL:  "https://203.0.113.10:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cluster, svc := createLoadBalancerService("test-cluster", "default", tt.servicePort, tt.lbIP, tt.lbHostname)
			fakeClient := createFakeClient(cluster, svc)

			url, err := getURLFromService(ctx, fakeClient, cluster, tt.hostServerIP, 0)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, url)
		})
	}
}

// TestURLGeneration_Ingress tests URL generation when Ingress is configured
func TestURLGeneration_Ingress(t *testing.T) {
	tests := []struct {
		name        string
		ingressHost string
		expectedURL string
	}{
		{
			name:        "Ingress with host",
			ingressHost: "api.example.com",
			expectedURL: "https://api.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cluster, svc, ingress := createIngressService("test-cluster", "default", tt.ingressHost)
			fakeClient := createFakeClient(cluster, svc, ingress)

			url, err := getURLFromService(ctx, fakeClient, cluster, "10.0.0.1", 0)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, url)
		})
	}
}

// TestURLGeneration_TLSSANs tests TLS SAN fallback logic
func TestURLGeneration_TLSSANs(t *testing.T) {
	tests := []struct {
		name          string
		clusterIP     string
		hostServerIP  string
		specTLSSANs   []string
		statusTLSSANs []string
		expectedURL   string
	}{
		{
			name:          "IP in status TLSSANs",
			clusterIP:     "10.43.0.100",
			hostServerIP:  "10.43.0.100",
			specTLSSANs:   nil,
			statusTLSSANs: []string{"10.43.0.100", "cluster.local"},
			expectedURL:   "https://10.43.0.100",
		},
		{
			name:          "IP not in status, fallback to spec",
			clusterIP:     "10.43.0.100",
			hostServerIP:  "10.43.0.100",
			specTLSSANs:   []string{"custom.example.com"},
			statusTLSSANs: []string{"other.example.com"},
			expectedURL:   "https://custom.example.com",
		},
		{
			name:          "IP not in spec, fallback to status",
			clusterIP:     "10.43.0.100",
			hostServerIP:  "10.43.0.100",
			specTLSSANs:   nil,
			statusTLSSANs: []string{"fallback.example.com"},
			expectedURL:   "https://fallback.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: v1beta1.ClusterSpec{
					TLSSANs: tt.specTLSSANs,
				},
				Status: v1beta1.ClusterStatus{
					TLSSANs: tt.statusTLSSANs,
				},
			}

			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      server.ServiceName("test-cluster"),
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: tt.clusterIP,
					Ports: []corev1.ServicePort{
						{Port: 443},
					},
				},
			}

			fakeClient := createFakeClient(cluster, svc)

			url, err := getURLFromService(ctx, fakeClient, cluster, tt.hostServerIP, 0)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedURL, url)
		})
	}
}

// Helper functions to create test objects
func createClusterIPService(clusterName, namespace string, port int32) (*v1beta1.Cluster, *corev1.Service) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Status: v1beta1.ClusterStatus{
			TLSSANs: []string{"10.43.0.100"},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.ServiceName(clusterName),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.43.0.100",
			Ports: []corev1.ServicePort{
				{Port: port},
			},
		},
	}

	return cluster, svc
}

func createNodePortService(clusterName, namespace string, port, nodePort int32) (*v1beta1.Cluster, *corev1.Service) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Status: v1beta1.ClusterStatus{
			TLSSANs: []string{"10.43.0.100", "192.168.1.100"},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.ServiceName(clusterName),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.43.0.100",
			Ports: []corev1.ServicePort{
				{Port: port, NodePort: nodePort},
			},
		},
	}

	return cluster, svc
}

func createLoadBalancerService(clusterName, namespace string, port int32, lbIP, lbHostname string) (*v1beta1.Cluster, *corev1.Service) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Status: v1beta1.ClusterStatus{
			TLSSANs: []string{"10.43.0.100", lbIP, lbHostname},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.ServiceName(clusterName),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.43.0.100",
			Ports: []corev1.ServicePort{
				{Port: port},
			},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{IP: lbIP, Hostname: lbHostname},
				},
			},
		},
	}

	return cluster, svc
}

func createIngressService(clusterName, namespace, ingressHost string) (*v1beta1.Cluster, *corev1.Service, *networkingv1.Ingress) {
	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: v1beta1.ClusterSpec{
			Expose: &v1beta1.ExposeConfig{
				Ingress: &v1beta1.IngressConfig{},
			},
		},
		Status: v1beta1.ClusterStatus{
			TLSSANs: []string{"10.43.0.100", ingressHost},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.ServiceName(clusterName),
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.43.0.100",
			Ports: []corev1.ServicePort{
				{Port: 443},
			},
		},
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.IngressName(clusterName),
			Namespace: namespace,
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{Host: ingressHost},
			},
		},
	}

	return cluster, svc, ingress
}

func createFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}
