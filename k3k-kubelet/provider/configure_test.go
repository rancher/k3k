package provider

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

// fakeManager provides the manager methods ConfigureNode uses.
type fakeManager struct {
	manager.Manager
	client client.Client
}

func (f *fakeManager) GetAPIReader() client.Reader { return f.client }
func (f *fakeManager) GetClient() client.Client    { return f.client }

func Test_ConfigureNode(t *testing.T) {
	hostNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "node-1",
			Labels:      map[string]string{"topology.kubernetes.io/zone": "01"},
			Annotations: map[string]string{"example.com/annotation": "value"},
		},
		Spec:   corev1.NodeSpec{Unschedulable: true},
		Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.32.13+rke2r1"}},
	}

	tests := []struct {
		name            string
		mirrorHostNodes bool
		hostObjects     []client.Object
		wantErr         bool
		check           func(t *testing.T, node *corev1.Node)
	}{
		{
			name:            "mirrored node copies the host node and keeps the virtual cluster version",
			mirrorHostNodes: true,
			hostObjects:     []client.Object{hostNode},
			check: func(t *testing.T, node *corev1.Node) {
				assert.Equal(t, "01", node.Labels["topology.kubernetes.io/zone"])
				assert.Equal(t, "value", node.Annotations["example.com/annotation"])
				assert.True(t, node.Spec.Unschedulable)
				assert.Equal(t, "v1.34.9-k3s1", node.Status.NodeInfo.KubeletVersion)
				assert.Equal(t, int32(10250), node.Status.DaemonEndpoints.KubeletEndpoint.Port)
			},
		},
		{
			name:            "mirrored node without a host node fails",
			mirrorHostNodes: true,
			wantErr:         true,
		},
		{
			name: "virtual node gets its own addresses, labels and version",
			check: func(t *testing.T, node *corev1.Node) {
				assert.Equal(t, []corev1.NodeAddress{
					{Type: corev1.NodeHostName, Address: "agent-1"},
					{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
				}, node.Status.Addresses)
				assert.Equal(t, "linux", node.Labels["kubernetes.io/os"])
				assert.Equal(t, "v1.34.9-k3s1", node.Status.NodeInfo.KubeletVersion)
				assert.Equal(t, int32(10250), node.Status.DaemonEndpoints.KubeletEndpoint.Port)
				assert.NotEmpty(t, node.Status.Conditions)
			},
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.hostObjects...).Build()
			p := &Provider{
				host:    ClusterContext{Manager: &fakeManager{client: hostClient}},
				virtual: ClusterContext{Client: fake.NewClientBuilder().WithScheme(scheme).Build()},
			}

			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1", Labels: map[string]string{}}}

			err := p.ConfigureNode(logr.Discard(), node, "agent-1", 10250, "10.0.0.1", v1beta1.Cluster{}, "v1.34.9-k3s1", tt.mirrorHostNodes)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			tt.check(t, node)
		})
	}
}
