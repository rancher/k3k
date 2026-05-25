package server_test

// This file pins the behavior of ServerURL() across the different
// service types (ClusterIP, NodePort, LoadBalancer), Ingress exposure, and the
// TLS SAN fallback logic.
//
// The behaviors asserted here are:
//
// 1. ClusterIP port handling:
//    Uses Spec.ClusterIP with the service's declared port (Spec.Ports[0].Port).
//    The port suffix is omitted only when it is the default 443.
//
// 2. NodePort access:
//    Internal/external aware. When hostServerIP == ClusterIP the connection is
//    treated as internal and uses ClusterIP:Port; otherwise it uses
//    hostServerIP:NodePort.
//
// 3. LoadBalancer hostname support:
//    Reads Status.LoadBalancer.Ingress[0], preferring IP and falling back to
//    Hostname, so a hostname-only ingress produces a valid URL.

import (
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
		expectedURL  string
	}{
		{
			name:         "ClusterIP with default port 443",
			hostServerIP: "10.0.0.1",
			servicePort:  443,
			expectedURL:  "https://10.43.0.100",
		},
		{
			// the service's declared port is used, so it appears in the URL
			name:         "ClusterIP uses custom service port",
			hostServerIP: "10.0.0.1",
			servicePort:  8443,
			expectedURL:  "https://10.43.0.100:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster, svc := createClusterIPService("test-cluster", "default", tt.servicePort)
			fakeClient := createFakeClient(t, cluster, svc)

			url, _, err := server.ServerURL(t.Context(), fakeClient, cluster, tt.hostServerIP)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedURL, url.String())
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
			// internal access: hostServerIP == ClusterIP, so ClusterIP:Port is used
			// instead of the NodePort (port 443 is the default, so it is omitted)
			name:         "NodePort internal access",
			hostServerIP: "10.43.0.100", // same as ClusterIP
			nodePort:     30443,
			servicePort:  443,
			expectedURL:  "https://10.43.0.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster, svc := createNodePortService("test-cluster", "default", tt.servicePort, tt.nodePort)
			fakeClient := createFakeClient(t, cluster, svc)

			url, _, err := server.ServerURL(t.Context(), fakeClient, cluster, tt.hostServerIP)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedURL, url.String())
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
			// the Hostname field is used as a fallback when no IP is present
			name:         "LoadBalancer with hostname ingress",
			hostServerIP: "10.0.0.1",
			lbIP:         "",
			lbHostname:   "cluster.example.com",
			servicePort:  443,
			expectedURL:  "https://cluster.example.com",
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
			cluster, svc := createLoadBalancerService("test-cluster", "default", tt.servicePort, tt.lbIP, tt.lbHostname)
			fakeClient := createFakeClient(t, cluster, svc)

			url, _, err := server.ServerURL(t.Context(), fakeClient, cluster, tt.hostServerIP)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedURL, url.String())
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
			cluster, svc, ingress := createIngressService("test-cluster", "default", tt.ingressHost)
			fakeClient := createFakeClient(t, cluster, svc, ingress)

			url, _, err := server.ServerURL(t.Context(), fakeClient, cluster, "10.0.0.1")
			require.NoError(t, err)

			assert.Equal(t, tt.expectedURL, url.String())
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

			fakeClient := createFakeClient(t, cluster, svc)

			url, _, err := server.ServerURL(t.Context(), fakeClient, cluster, tt.hostServerIP)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedURL, url.String())
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

func createFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()

	schemeBuilder := runtime.NewSchemeBuilder(
		corev1.AddToScheme,
		networkingv1.AddToScheme,
		v1beta1.AddToScheme,
	)

	err := schemeBuilder.AddToScheme(scheme)
	require.NoError(t, err)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}
