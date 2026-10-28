package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
)

func Test_ensureNetworkPolicy_podCIDRExclusion(t *testing.T) {
	nodeWithPodCIDR := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{PodCIDR: "10.42.0.0/24"},
	}

	tests := []struct {
		name        string
		clusterCIDR string
		objs        []client.Object
		wantExcept  []string
	}{
		{
			name:        "the controller cluster CIDR is used as-is",
			clusterCIDR: "10.0.0.0/16",
			objs:        []client.Object{nodeWithPodCIDR},
			wantExcept:  []string{"10.0.0.0/16"},
		},
		{
			name:       "without a cluster CIDR it's inferred from the nodes",
			objs:       []client.Object{nodeWithPodCIDR},
			wantExcept: []string{"10.42.0.0/24"},
		},
		{
			name: "nothing to exclude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			r := &Reconciler{
				Client: newTestClient(t, tt.objs...),
				Config: Config{ClusterCIDR: tt.clusterCIDR},
			}

			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "ns"},
			}

			require.NoError(t, r.ensureNetworkPolicy(ctx, cluster))

			var networkPolicy networkingv1.NetworkPolicy

			key := client.ObjectKey{Name: controller.SafeConcatNameWithPrefix(cluster.Name), Namespace: cluster.Namespace}
			require.NoError(t, r.Client.Get(ctx, key, &networkPolicy))

			require.NotEmpty(t, networkPolicy.Spec.Egress)
			require.NotEmpty(t, networkPolicy.Spec.Egress[0].To)

			ipBlock := networkPolicy.Spec.Egress[0].To[0].IPBlock
			require.NotNil(t, ipBlock)
			assert.Equal(t, "0.0.0.0/0", ipBlock.CIDR)
			assert.Equal(t, tt.wantExcept, ipBlock.Except)
		})
	}
}
