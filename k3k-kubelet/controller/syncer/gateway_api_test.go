package syncer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/rancher/k3k/k3k-kubelet/translate"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func newGatewayAPIReconciler(clusterName, clusterNamespace string) *GatewayAPIReconciler {
	return &GatewayAPIReconciler{
		Context: &Context{
			ClusterName:      clusterName,
			ClusterNamespace: clusterNamespace,
			Translator: translate.ToHostTranslator{
				ClusterName:      clusterName,
				ClusterNamespace: clusterNamespace,
			},
		},
	}
}

func gatewayNamespace(ns string) *gatewayv1.Namespace {
	v := gatewayv1.Namespace(ns)
	return &v
}

func TestHTTPRouteTranslation(t *testing.T) {
	r := newGatewayAPIReconciler("mycluster", "host-ns")

	tests := []struct {
		name       string
		route      *gatewayv1.HTTPRoute
		syncConfig v1beta1.GatewayAPISyncConfig
		verify     func(t *testing.T, result *gatewayv1.HTTPRoute)
	}{
		{
			name: "override replaces all parentRefs with configured gateway",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Name: "gateway-a"},
							{Name: "gateway-b"},
						},
					},
				},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{
				OverrideParentGateway: &v1beta1.GatewayParentRef{
					Name:      "host-gateway",
					Namespace: "infra-ns",
				},
			},
			verify: func(t *testing.T, result *gatewayv1.HTTPRoute) {
				require.Len(t, result.Spec.ParentRefs, 1)
				assert.Equal(t, gatewayv1.ObjectName("host-gateway"), result.Spec.ParentRefs[0].Name)
				assert.Equal(t, gatewayNamespace("infra-ns"), result.Spec.ParentRefs[0].Namespace)
			},
		},
		{
			name: "without override, parentRef name is translated and namespace set to clusterNamespace",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Name: "my-gateway"},
						},
					},
				},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{},
			verify: func(t *testing.T, result *gatewayv1.HTTPRoute) {
				require.Len(t, result.Spec.ParentRefs, 1)
				ref := result.Spec.ParentRefs[0]
				assert.Equal(t, gatewayv1.ObjectName(r.Translator.TranslateName("virt-ns", "my-gateway")), ref.Name)
				assert.Equal(t, gatewayNamespace("host-ns"), ref.Namespace)
			},
		},
		{
			name: "without override, parentRef with explicit namespace uses that namespace for translation",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Name: "cross-ns-gateway", Namespace: gatewayNamespace("other-ns")},
						},
					},
				},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{},
			verify: func(t *testing.T, result *gatewayv1.HTTPRoute) {
				require.Len(t, result.Spec.ParentRefs, 1)
				ref := result.Spec.ParentRefs[0]
				assert.Equal(t, gatewayv1.ObjectName(r.Translator.TranslateName("other-ns", "cross-ns-gateway")), ref.Name)
				assert.Equal(t, gatewayNamespace("host-ns"), ref.Namespace)
			},
		},
		{
			name: "backendRef service names are translated",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "my-svc"}}},
								{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "other-svc"}}},
							},
						},
					},
				},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{},
			verify: func(t *testing.T, result *gatewayv1.HTTPRoute) {
				require.Len(t, result.Spec.Rules[0].BackendRefs, 2)
				assert.Equal(t, gatewayv1.ObjectName(r.Translator.TranslateName("virt-ns", "my-svc")), result.Spec.Rules[0].BackendRefs[0].Name)
				assert.Equal(t, gatewayv1.ObjectName(r.Translator.TranslateName("virt-ns", "other-svc")), result.Spec.Rules[0].BackendRefs[1].Name)
			},
		},
		{
			name: "original object is not mutated",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{Name: "my-gateway"},
						},
					},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "my-svc"}}},
							},
						},
					},
				},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{},
			verify: func(t *testing.T, _ *gatewayv1.HTTPRoute) {},
		},
		{
			name: "result is placed in host namespace",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "my-route", Namespace: "virt-ns"},
			},
			syncConfig: v1beta1.GatewayAPISyncConfig{},
			verify: func(t *testing.T, result *gatewayv1.HTTPRoute) {
				assert.Equal(t, "host-ns", result.Namespace)
				assert.NotEqual(t, "my-route", result.Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalName := tt.route.Spec.ParentRefs
			_ = originalName

			result := r.httproute(tt.route, tt.syncConfig)

			// verify original is not mutated
			if len(tt.route.Spec.ParentRefs) > 0 {
				assert.NotSame(t, &tt.route.Spec.ParentRefs[0], &result.Spec.ParentRefs[0])
			}

			tt.verify(t, result)
		})
	}
}
