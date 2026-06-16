package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_ServerURL_LoadBalancer(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	cluster := &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "team-a"},
		Status:     v1beta1.ClusterStatus{TLSSANs: []string{"203.0.113.10", "lb.example.com"}},
	}

	tests := []struct {
		name         string
		ingress      []corev1.LoadBalancerIngress
		wantURL      string
		wantExternal bool
	}{
		{
			name:         "no ingress yet (LB still provisioning)",
			ingress:      nil,
			wantExternal: false,
		},
		{
			name:         "ingress with IP",
			ingress:      []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}},
			wantURL:      "https://203.0.113.10",
			wantExternal: true,
		},
		{
			name:         "ingress with hostname only",
			ingress:      []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
			wantURL:      "https://lb.example.com",
			wantExternal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ServiceName(cluster.Name),
					Namespace: cluster.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeLoadBalancer,
					ClusterIP: "10.43.0.50",
					Ports:     []corev1.ServicePort{{Name: "k3s-server-port", Port: 443}},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{Ingress: tt.ingress},
				},
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
			url, external, err := ServerURL(context.Background(), c, cluster, "", 0)
			require.NoError(t, err)
			assert.Equal(t, tt.wantExternal, external)

			if tt.wantExternal {
				assert.Equal(t, tt.wantURL, url)
			}
		})
	}
}
