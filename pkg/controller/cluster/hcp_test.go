package cluster

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
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/server"
)

func Test_findNonLoopbackSAN(t *testing.T) {
	tests := []struct {
		name string
		sans []string
		want string
	}{
		{
			name: "loopback first, external second",
			sans: []string{"127.0.0.1", "10.0.0.100"},
			want: "10.0.0.100",
		},
		{
			name: "external first",
			sans: []string{"10.0.0.100", "127.0.0.1"},
			want: "10.0.0.100",
		},
		{
			name: "only loopback",
			sans: []string{"127.0.0.1", "::1"},
			want: "",
		},
		{
			name: "localhost hostname filtered",
			sans: []string{"localhost", "example.com"},
			want: "example.com",
		},
		{
			name: "external hostname",
			sans: []string{"hcp.example.com"},
			want: "hcp.example.com",
		},
		{
			name: "empty",
			sans: []string{},
			want: "",
		},
		{
			name: "ipv6 loopback filtered",
			sans: []string{"::1", "2001:db8::1"},
			want: "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findNonLoopbackSAN(tt.sans)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_hcpEndpointAddress(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantIP       string
		wantHostname string
		wantErr      bool
	}{
		{
			name:         "ipv4 literal is passed through",
			input:        "10.144.101.195",
			wantIP:       "10.144.101.195",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "unresolvable hostname errors",
			input:   "definitely-not-a-real-host.invalid",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback literal is rejected",
			input:   "127.0.0.1",
			wantErr: true,
		},
		{
			name:    "ipv6 loopback literal is rejected",
			input:   "::1",
			wantErr: true,
		},
		{
			name:    "ipv4 loopback in range is rejected",
			input:   "127.0.0.100",
			wantErr: true,
		},
		{
			name:         "valid ipv6 literal passes through",
			input:        "2001:db8::1",
			wantIP:       "2001:db8::1",
			wantHostname: "",
			wantErr:      false,
		},
		{
			name:    "localhost hostname filters loopbacks",
			input:   "localhost",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hcpEndpointAddress(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, tt.wantIP, got.IP)
			assert.Equal(t, tt.wantHostname, got.Hostname)
		})
	}
}

func Test_hostNodeIPs(t *testing.T) {
	objs := []client.Object{
		newTestNode("node-a", map[string]string{"pool": "servers"}, "10.0.0.1", "fd00::1"),
		newTestNode("node-b", nil, "10.0.0.2"),
		newTestNode("node-c", map[string]string{"pool": "servers", "zone": "z1"}, "10.0.0.3"),
	}

	tests := map[string]struct {
		clusterOpts func(*v1beta1.Cluster)
		want        []string
	}{
		"no node selector returns all the nodes": {
			want: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "fd00::1"},
		},
		"node selector from the spec": {
			clusterOpts: func(c *v1beta1.Cluster) {
				c.Spec.NodeSelector = map[string]string{"pool": "servers"}
			},
			want: []string{"10.0.0.1", "10.0.0.3", "fd00::1"},
		},
		"node selector from the policy takes precedence": {
			clusterOpts: func(c *v1beta1.Cluster) {
				c.Spec.NodeSelector = map[string]string{"pool": "servers"}
				c.Status.Policy = &v1beta1.AppliedPolicy{NodeSelector: map[string]string{"zone": "z1"}}
			},
			want: []string{"10.0.0.3"},
		},
		"node selector matching no nodes": {
			clusterOpts: func(c *v1beta1.Cluster) {
				c.Spec.NodeSelector = map[string]string{"pool": "none"}
			},
			want: []string{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestHCPCluster("test-cluster")
			if tt.clusterOpts != nil {
				tt.clusterOpts(cluster)
			}

			r := &Reconciler{Client: newTestClient(t, objs...)}

			got, err := r.hostNodeIPs(t.Context(), cluster)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_serverNodeIPs(t *testing.T) {
	objs := []client.Object{
		newTestNode("node-a", nil, "10.0.0.1", "fd00::1"),
		newTestNode("node-b", nil, "10.0.0.2"),
		terminating(newTestNode("node-c", nil, "10.0.0.3")),
		newTestNode("node-d", nil, "10.0.0.4"),
		newTestNode("node-e", nil, "10.0.0.5", "fd00::5"),

		newTestServerPod("test-cluster", "server-0", "node-e"),
		newTestServerPod("test-cluster", "server-1", "node-a"),
		// a terminating server is skipped
		terminating(newTestServerPod("test-cluster", "server-2", "node-b")),
		// a server on a terminating node is skipped
		newTestServerPod("test-cluster", "server-3", "node-c"),
		// a server on a node that cannot be fetched is skipped
		newTestServerPod("test-cluster", "server-4", "node-missing"),
		// a server not scheduled yet is skipped
		newTestServerPod("test-cluster", "server-5", ""),
		// a server of another cluster is skipped
		newTestServerPod("other-cluster", "server-0", "node-d"),
	}

	tests := map[string]struct {
		wantIPv4 bool
		want     []string
	}{
		"ipv4 addresses sorted by node name": {
			wantIPv4: true,
			want:     []string{"10.0.0.1", "10.0.0.5"},
		},
		"ipv6 addresses sorted by node name": {
			wantIPv4: false,
			want:     []string{"fd00::1", "fd00::5"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := &Reconciler{Client: newTestClient(t, objs...)}

			got, err := r.serverNodeIPs(t.Context(), newTestHCPCluster("test-cluster"), tt.wantIPv4)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_hcpEndpointAddresses(t *testing.T) {
	nodesAndServers := []client.Object{
		newTestNode("node-a", nil, "10.0.0.1"),
		newTestNode("node-b", nil, "10.0.0.2"),
		newTestServerPod("test-cluster", "server-0", "node-b"),
		newTestServerPod("test-cluster", "server-1", "node-a"),
	}

	nodePortService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: server.ServiceName("test-cluster"), Namespace: "test-ns"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Name: "k3s-server-port", Port: 443, NodePort: 30443}},
		},
	}

	loadBalancerService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: server.ServiceName("test-cluster"), Namespace: "test-ns"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.43.0.10",
			Ports:     []corev1.ServicePort{{Name: "k3s-server-port", Port: 443, NodePort: 30443}},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{IP: "192.168.1.10"}},
			},
		},
	}

	nodePort := func(c *v1beta1.Cluster) {
		c.Spec.Expose = &v1beta1.ExposeConfig{NodePort: &v1beta1.NodePortConfig{}}
	}

	loadBalancer := func(c *v1beta1.Cluster) {
		c.Spec.Expose = &v1beta1.ExposeConfig{LoadBalancer: &v1beta1.LoadBalancerConfig{}}
	}

	tests := map[string]struct {
		clusterOpts func(*v1beta1.Cluster)
		objs        []client.Object
		wantIPs     []string
		wantPort    int32
	}{
		"node port publishes the node of every server with the node port": {
			clusterOpts: nodePort,
			objs:        append([]client.Object{nodePortService}, nodesAndServers...),
			wantIPs:     []string{"10.0.0.1", "10.0.0.2"},
			wantPort:    30443,
		},
		"node port without scheduled servers falls back to the external address": {
			clusterOpts: nodePort,
			objs:        []client.Object{nodePortService},
			wantIPs:     []string{"10.0.0.100"},
			wantPort:    30443,
		},
		"load balancer publishes only its address": {
			clusterOpts: loadBalancer,
			objs:        append([]client.Object{loadBalancerService}, nodesAndServers...),
			wantIPs:     []string{"192.168.1.10"},
			wantPort:    443,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestHCPCluster("test-cluster")
			cluster.Spec.TLSSANs = []string{"10.0.0.100"}
			cluster.Status.TLSSANs = []string{"10.0.0.100", "192.168.1.10"}
			tt.clusterOpts(cluster)

			r := &Reconciler{Client: newTestClient(t, tt.objs...)}

			ips, port, err := r.hcpEndpointAddresses(t.Context(), cluster)
			require.NoError(t, err)
			assert.Equal(t, tt.wantIPs, ips)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

func newTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, v1beta1.AddToScheme(scheme))

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func newTestHCPCluster(name string) *v1beta1.Cluster {
	return &v1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns"},
		Spec:       v1beta1.ClusterSpec{Mode: v1beta1.HCPClusterMode},
	}
}

func newTestNode(name string, labels map[string]string, internalIPs ...string) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			// the external and hostname addresses are never used
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: name},
				{Type: corev1.NodeExternalIP, Address: "203.0.113.1"},
			},
		},
	}

	for _, ip := range internalIPs {
		node.Status.Addresses = append(node.Status.Addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip})
	}

	return node
}

func newTestServerPod(clusterName, name, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName + "-" + name,
			Namespace: "test-ns",
			Labels: map[string]string{
				"cluster": clusterName,
				"role":    "server",
			},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
	}
}

// terminating marks the object as being deleted. The fake client requires a finalizer
// for objects with a deletion timestamp.
func terminating[T client.Object](obj T) T {
	now := metav1.Now()
	obj.SetDeletionTimestamp(&now)
	obj.SetFinalizers([]string{"test.k3k.io/finalizer"})

	return obj
}

// Compile-time assertion: every reused exported name from the controller
// package below this test file must remain stable. If `controller.K3SImage`
// disappears (refactor), this guards the dependency.
var _ = controller.K3SImage
