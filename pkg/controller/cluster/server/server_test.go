package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_NodeSelector(t *testing.T) {
	specSelector := map[string]string{"from": "spec"}
	policySelector := map[string]string{"from": "policy"}

	tests := map[string]struct {
		spec   map[string]string
		policy *v1beta1.AppliedPolicy
		want   map[string]string
	}{
		"no node selector": {},
		"node selector from the spec": {
			spec: specSelector,
			want: specSelector,
		},
		"node selector from the policy takes precedence": {
			spec:   specSelector,
			policy: &v1beta1.AppliedPolicy{NodeSelector: policySelector},
			want:   policySelector,
		},
		"policy without a node selector falls back to the spec": {
			spec:   specSelector,
			policy: &v1beta1.AppliedPolicy{},
			want:   specSelector,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.NodeSelector = tt.spec
				c.Status.Policy = tt.policy
			})

			assert.Equal(t, tt.want, NodeSelector(cluster))
		})
	}
}

func Test_defaultServerAffinity(t *testing.T) {
	spreadAffinity := &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: corev1.LabelHostname,
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"cluster": "test-cluster",
							"role":    "server",
						},
					},
				},
			}},
		},
	}

	tests := map[string]struct {
		mode    v1beta1.ClusterMode
		servers *int32
		want    *corev1.Affinity
	}{
		"hcp mode with multiple servers spreads the servers": {
			mode:    v1beta1.HCPClusterMode,
			servers: new(int32(3)),
			want:    spreadAffinity,
		},
		"hcp mode with a single server": {
			mode:    v1beta1.HCPClusterMode,
			servers: new(int32(1)),
		},
		"hcp mode without servers set": {
			mode: v1beta1.HCPClusterMode,
		},
		"shared mode with multiple servers": {
			mode:    v1beta1.SharedClusterMode,
			servers: new(int32(3)),
		},
		"virtual mode with multiple servers": {
			mode:    v1beta1.VirtualClusterMode,
			servers: new(int32(3)),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.Mode = tt.mode
				c.Spec.Servers = tt.servers
			})

			assert.Equal(t, tt.want, defaultServerAffinity(cluster))
		})
	}
}
