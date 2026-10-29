package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

			err := validate(cluster, tt.policy)

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

func Test_lookupServiceCIDR(t *testing.T) {
	defaultServiceCIDR := func(cidrs ...string) *networkingv1.ServiceCIDR {
		return &networkingv1.ServiceCIDR{
			ObjectMeta: metav1.ObjectMeta{Name: "kubernetes"},
			Spec:       networkingv1.ServiceCIDRSpec{CIDRs: cidrs},
		}
	}

	apiServerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "kube-system",
			Labels:    map[string]string{"component": "kube-apiserver", "tier": "control-plane"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "kube-apiserver",
				Args: []string{"--service-cluster-ip-range=10.96.0.0/12"},
			}},
		},
	}

	tests := []struct {
		name    string
		objs    []client.Object
		want    string
		wantErr bool
	}{
		{
			name: "from the default ServiceCIDR",
			objs: []client.Object{defaultServiceCIDR("172.20.0.0/16")},
			want: "172.20.0.0/16",
		},
		{
			name: "dual-stack ServiceCIDR returns the primary family",
			objs: []client.Object{defaultServiceCIDR("fd00:10:96::/112", "10.96.0.0/16")},
			want: "fd00:10:96::/112",
		},
		{
			name: "ServiceCIDR takes precedence over the kube-apiserver pod",
			objs: []client.Object{defaultServiceCIDR("172.20.0.0/16"), apiServerPod},
			want: "172.20.0.0/16",
		},
		{
			name: "falls back to the kube-apiserver pod without a ServiceCIDR",
			objs: []client.Object{apiServerPod},
			want: "10.96.0.0/12",
		},
		{
			name:    "invalid ServiceCIDR",
			objs:    []client.Object{defaultServiceCIDR("not-a-cidr")},
			wantErr: true,
		},
		{
			name: "nothing found",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{Client: newTestClient(t, tt.objs...)}

			got, err := r.lookupServiceCIDR(context.Background())
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
