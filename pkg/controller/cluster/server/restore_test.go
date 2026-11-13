package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_baseRestorePodSpec_PriorityClassName(t *testing.T) {
	tests := map[string]struct {
		spec   string
		policy *v1beta1.AppliedPolicy
		want   string
	}{
		"priority class from the spec": {
			spec: "from-spec",
			want: "from-spec",
		},
		"priority class from the policy takes precedence": {
			spec:   "from-spec",
			policy: &v1beta1.AppliedPolicy{PriorityClass: new("from-policy")},
			want:   "from-policy",
		},
		"policy with an empty priority class falls back to the spec": {
			spec:   "from-spec",
			policy: &v1beta1.AppliedPolicy{PriorityClass: new("")},
			want:   "from-spec",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(func(c *v1beta1.Cluster) {
				c.Spec.PriorityClass = tt.spec
				c.Status.Policy = tt.policy
			})

			s := &Server{cluster: cluster}
			podSpec := s.baseRestorePodSpec(context.Background(), "rancher/k3s:latest", "test", 0)

			assert.Equal(t, tt.want, podSpec.PriorityClassName)
		})
	}
}
