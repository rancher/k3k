package provider

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_distributeQuotas(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}}
	node3 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-3"}}

	tests := []struct {
		name         string
		virtualNodes []client.Object
		quotas       corev1.ResourceList
		want         map[string]corev1.ResourceList
		wantErr      bool
	}{
		{
			name:         "no virtual nodes",
			virtualNodes: []client.Object{},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			want:    map[string]corev1.ResourceList{},
			wantErr: false,
		},
		{
			name:         "no quotas",
			virtualNodes: []client.Object{node1, node2},
			quotas:       corev1.ResourceList{},
			want: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			wantErr: false,
		},
		{
			name:         "even distribution of cpu and memory",
			virtualNodes: []client.Object{node1, node2},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				"node-2": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
			wantErr: false,
		},
		{
			name:         "uneven distribution with remainder",
			virtualNodes: []client.Object{node1, node2, node3},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"), // 2000m / 3 = 666m with 2m remainder
			},
			want: map[string]corev1.ResourceList{
				"node-1": {corev1.ResourceCPU: resource.MustParse("667m")},
				"node-2": {corev1.ResourceCPU: resource.MustParse("667m")},
				"node-3": {corev1.ResourceCPU: resource.MustParse("666m")},
			},
			wantErr: false,
		},
		{
			name:         "distribution of number resources",
			virtualNodes: []client.Object{node1, node2, node3},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU:     resource.MustParse("2"),
				corev1.ResourcePods:    resource.MustParse("11"),
				corev1.ResourceSecrets: resource.MustParse("9"),
				"custom":               resource.MustParse("8"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU:     resource.MustParse("667m"),
					corev1.ResourcePods:    resource.MustParse("4"),
					corev1.ResourceSecrets: resource.MustParse("3"),
					"custom":               resource.MustParse("3"),
				},
				"node-2": {
					corev1.ResourceCPU:     resource.MustParse("667m"),
					corev1.ResourcePods:    resource.MustParse("4"),
					corev1.ResourceSecrets: resource.MustParse("3"),
					"custom":               resource.MustParse("3"),
				},
				"node-3": {
					corev1.ResourceCPU:     resource.MustParse("666m"),
					corev1.ResourcePods:    resource.MustParse("3"),
					corev1.ResourceSecrets: resource.MustParse("3"),
					"custom":               resource.MustParse("2"),
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.virtualNodes...).Build()
			logger := zapr.NewLogger(zap.NewNop())

			got, gotErr := distributeQuotas(context.Background(), logger, fakeClient, tt.quotas)
			if tt.wantErr {
				assert.Error(t, gotErr)
			} else {
				assert.NoError(t, gotErr)
			}

			assert.Equal(t, len(tt.want), len(got), "Number of nodes in result should match")

			for nodeName, expectedResources := range tt.want {
				actualResources, ok := got[nodeName]
				assert.True(t, ok, "Node %s not found in result", nodeName)

				assert.Equal(t, len(expectedResources), len(actualResources), "Number of resources for node %s should match", nodeName)

				for resName, expectedQty := range expectedResources {
					actualQty, ok := actualResources[resName]
					assert.True(t, ok, "Resource %s not found for node %s", resName, nodeName)
					assert.True(t, expectedQty.Equal(actualQty), "Resource %s for node %s did not match. want: %s, got: %s", resName, nodeName, expectedQty.String(), actualQty.String())
				}
			}
		})
	}
}
