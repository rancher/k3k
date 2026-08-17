package cluster

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func Test_validate(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		mode        v1beta1.ClusterMode
		tlsSANs     []string
		expose      *v1beta1.ExposeConfig
		policy      *v1beta1.VirtualClusterPolicy
		wantErr     string
	}{
		{
			name: "valid cluster without a policy",
		},
		{
			name:   "valid cluster with a policy",
			policy: newTestPolicy(v1beta1.SharedClusterMode),
		},
		{
			// the name check does not depend on the policy, so it also runs
			// in namespaces that are not bound to one
			name:        "invalid cluster name without a policy",
			clusterName: ClusterInvalidName,
			wantErr:     "invalid cluster name",
		},
		{
			name:        "invalid cluster name with a policy",
			clusterName: ClusterInvalidName,
			policy:      newTestPolicy(v1beta1.SharedClusterMode),
			wantErr:     "invalid cluster name",
		},
		{
			name:    "mode not allowed by the policy",
			mode:    v1beta1.VirtualClusterMode,
			policy:  newTestPolicy(v1beta1.SharedClusterMode),
			wantErr: "is not allowed by the policy",
		},
		{
			// without a policy there is no allowed mode to check against
			name: "any mode is allowed without a policy",
			mode: v1beta1.VirtualClusterMode,
		},
		{
			name:   "expose without ingress",
			expose: &v1beta1.ExposeConfig{NodePort: &v1beta1.NodePortConfig{}},
		},
		{
			name:    "expose ingress without tlsSANs",
			expose:  &v1beta1.ExposeConfig{Ingress: &v1beta1.IngressConfig{}},
			wantErr: "spec.tlsSANs",
		},
		{
			name:    "expose ingress with only IP tlsSANs",
			tlsSANs: []string{"10.0.0.5", "::1"},
			expose:  &v1beta1.ExposeConfig{Ingress: &v1beta1.IngressConfig{}},
			wantErr: "spec.tlsSANs",
		},
		{
			name:    "expose ingress with a DNS tlsSAN",
			tlsSANs: []string{"10.0.0.5", "my-cluster.example.com"},
			expose:  &v1beta1.ExposeConfig{Ingress: &v1beta1.IngressConfig{}},
		},
		{
			name:    "no ingress, IP-only tlsSANs is fine",
			tlsSANs: []string{"10.0.0.5"},
			expose:  &v1beta1.ExposeConfig{LoadBalancer: &v1beta1.LoadBalancerConfig{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterName := tt.clusterName
			if clusterName == "" {
				clusterName = "test-cluster"
			}

			mode := tt.mode
			if mode == "" {
				mode = v1beta1.SharedClusterMode
			}

			cluster := &v1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: "test-namespace"},
				Spec: v1beta1.ClusterSpec{
					Mode:    mode,
					TLSSANs: tt.tlsSANs,
					Expose:  tt.expose,
				},
			}

			// the Client is only needed to validate the customCAs secrets,
			// which none of these clusters enable
			reconciler := &ClusterReconciler{}

			err := reconciler.validate(cluster, tt.policy)

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}

			assert.Error(t, err)
			// the status controller relies on this to report Pending/ValidationFailed
			// instead of letting the API server reject an invalid resource.
			assert.True(t, errors.Is(err, ErrClusterValidation))
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func newTestPolicy(allowedMode v1beta1.ClusterMode) *v1beta1.VirtualClusterPolicy {
	return &v1beta1.VirtualClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test-policy"},
		Spec:       v1beta1.VirtualClusterPolicySpec{AllowedMode: allowedMode},
	}
}
