package policy_test

// This file pins the behavior of FindPodCIDRs(), which computes the CIDR ranges excluded
// from the isolation NetworkPolicy's egress allow-list (so pod-to-pod traffic on the host's real
// pod network is blocked). The behaviors asserted here are:
//
// 1. --cluster-cidr override: when set, it's used as-is and Nodes are never consulted.
// 2. Node-based detection: falls back to collecting Spec.PodCIDRs/Spec.PodCIDR from live Nodes.
// 3. Can't-determine fallback: returns an empty list (fail-open) when neither is available.
//
// It also pins the spec produced by IsolationNetworkPolicySpec(), shared by standalone
// clusters and namespaces bound to a VirtualClusterPolicy.

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

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/controller/policy"
)

func TestFindPodCIDRs_ClusterCIDROverride(t *testing.T) {
	// a Node with a different PodCIDR is present, but the explicit override must win and
	// the Node must not even be consulted
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{PodCIDR: "10.244.0.0/24"},
	}

	cl := createFakeClient(t, node)

	cidrs, err := policy.FindPodCIDRs(t.Context(), cl, "10.42.0.0/16")
	require.NoError(t, err)
	assert.Equal(t, []string{"10.42.0.0/16"}, cidrs)
}

func TestFindPodCIDRs_FromNodePodCIDR(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{PodCIDR: "192.168.77.0/24"},
	}

	cl := createFakeClient(t, node)

	cidrs, err := policy.FindPodCIDRs(t.Context(), cl, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"192.168.77.0/24"}, cidrs)
}

func TestFindPodCIDRs_FromNodePodCIDRs(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{PodCIDRs: []string{"192.168.77.0/24", "fd00:1::/64"}},
	}

	cl := createFakeClient(t, node)

	cidrs, err := policy.FindPodCIDRs(t.Context(), cl, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"192.168.77.0/24", "fd00:1::/64"}, cidrs)
}

func TestFindPodCIDRs_MultipleNodes(t *testing.T) {
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{PodCIDR: "192.168.77.0/24"},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
		Spec:       corev1.NodeSpec{PodCIDR: "192.168.78.0/24"},
	}

	cl := createFakeClient(t, node1, node2)

	cidrs, err := policy.FindPodCIDRs(t.Context(), cl, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"192.168.77.0/24", "192.168.78.0/24"}, cidrs)
}

func TestFindPodCIDRs_CannotDetermine(t *testing.T) {
	// no --cluster-cidr override, and the Node carries no PodCIDR at all (e.g. Cilium with
	// cluster-pool IPAM) -- must fail open (empty list) rather than error
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
	}

	cl := createFakeClient(t, node)

	cidrs, err := policy.FindPodCIDRs(t.Context(), cl, "")
	require.NoError(t, err)
	assert.Empty(t, cidrs)
}

func TestIsolationNetworkPolicySpec(t *testing.T) {
	spec := policy.IsolationNetworkPolicySpec("my-ns", []string{"10.42.0.0/16"})

	// only synced workload pods are selected, leaving the k3k infra pods unrestricted
	assert.Equal(t, []metav1.LabelSelectorRequirement{
		{Key: translate.ClusterNameLabel, Operator: metav1.LabelSelectorOpExists},
	}, spec.PodSelector.MatchExpressions)

	assert.ElementsMatch(t, []networkingv1.PolicyType{
		networkingv1.PolicyTypeIngress,
		networkingv1.PolicyTypeEgress,
	}, spec.PolicyTypes)

	assert.Equal(t, []networkingv1.NetworkPolicyIngressRule{{}}, spec.Ingress)

	require.Len(t, spec.Egress, 1)
	assert.ElementsMatch(t, []networkingv1.NetworkPolicyPeer{
		{
			IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.42.0.0/16"}},
		},
		{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": "my-ns"},
			},
		},
		{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": metav1.NamespaceSystem},
			},
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"k8s-app": "kube-dns"},
			},
		},
	}, spec.Egress[0].To)
}

func createFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()

	schemeBuilder := runtime.NewSchemeBuilder(
		corev1.AddToScheme,
	)

	err := schemeBuilder.AddToScheme(scheme)
	require.NoError(t, err)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}
