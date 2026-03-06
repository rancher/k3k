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

	// Helper to create a host node with given allocatable resources.
	hostNode := func(name string, allocatable corev1.ResourceList) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status:     corev1.NodeStatus{Allocatable: allocatable},
		}
	}

	// Large allocatable so capping doesn't interfere with basic distribution tests.
	largeAllocatable := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100"),
		corev1.ResourceMemory: resource.MustParse("100Gi"),
		corev1.ResourcePods:   resource.MustParse("1000"),
	}

	node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}}
	node3 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-3"}}

	tests := []struct {
		name         string
		virtualNodes []client.Object
		hostNodes    []client.Object
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
			hostNodes: []client.Object{
				hostNode("node-1", largeAllocatable),
				hostNode("node-2", largeAllocatable),
			},
			quotas: corev1.ResourceList{},
			want: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			wantErr: false,
		},
		{
			name:         "even distribution of cpu and memory",
			virtualNodes: []client.Object{node1, node2},
			hostNodes: []client.Object{
				hostNode("node-1", largeAllocatable),
				hostNode("node-2", largeAllocatable),
			},
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
			hostNodes: []client.Object{
				hostNode("node-1", largeAllocatable),
				hostNode("node-2", largeAllocatable),
				hostNode("node-3", largeAllocatable),
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
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
			hostNodes: []client.Object{
				hostNode("node-1", largeAllocatable),
				hostNode("node-2", largeAllocatable),
				hostNode("node-3", largeAllocatable),
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU:  resource.MustParse("2"),
				corev1.ResourcePods: resource.MustParse("11"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU:  resource.MustParse("667m"),
					corev1.ResourcePods: resource.MustParse("4"),
				},
				"node-2": {
					corev1.ResourceCPU:  resource.MustParse("667m"),
					corev1.ResourcePods: resource.MustParse("4"),
				},
				"node-3": {
					corev1.ResourceCPU:  resource.MustParse("666m"),
					corev1.ResourcePods: resource.MustParse("3"),
				},
			},
			wantErr: false,
		},
		{
			name:         "extended resource distributed only to nodes that have it",
			virtualNodes: []client.Object{node1, node2, node3},
			hostNodes: []client.Object{
				hostNode("node-1", corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100"),
					"nvidia.com/gpu":   resource.MustParse("2"),
				}),
				hostNode("node-2", corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100"),
				}),
				hostNode("node-3", corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100"),
					"nvidia.com/gpu":   resource.MustParse("4"),
				}),
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("3"),
				"nvidia.com/gpu":   resource.MustParse("4"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU: resource.MustParse("1"),
					"nvidia.com/gpu":   resource.MustParse("2"),
				},
				"node-2": {
					corev1.ResourceCPU: resource.MustParse("1"),
				},
				"node-3": {
					corev1.ResourceCPU: resource.MustParse("1"),
					"nvidia.com/gpu":   resource.MustParse("2"),
				},
			},
			wantErr: false,
		},
		{
			name:         "capping at host capacity with redistribution",
			virtualNodes: []client.Object{node1, node2},
			hostNodes: []client.Object{
				hostNode("node-1", corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("8"),
				}),
				hostNode("node-2", corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("2"),
				}),
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("6"),
			},
			// Even split would be 3 each, but node-2 only has 2 CPU.
			// node-2 gets capped at 2, the remaining 1 goes to node-1.
			want: map[string]corev1.ResourceList{
				"node-1": {corev1.ResourceCPU: resource.MustParse("4")},
				"node-2": {corev1.ResourceCPU: resource.MustParse("2")},
			},
			wantErr: false,
		},
		{
			name:         "gpu capping with uneven host capacity",
			virtualNodes: []client.Object{node1, node2},
			hostNodes: []client.Object{
				hostNode("node-1", corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("6"),
				}),
				hostNode("node-2", corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			quotas: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("4"),
			},
			// Even split would be 2 each, but node-2 only has 1 GPU.
			// node-2 gets capped at 1, the remaining 1 goes to node-1.
			want: map[string]corev1.ResourceList{
				"node-1": {"nvidia.com/gpu": resource.MustParse("3")},
				"node-2": {"nvidia.com/gpu": resource.MustParse("1")},
			},
			wantErr: false,
		},
		{
			name:         "quota exceeds total host capacity",
			virtualNodes: []client.Object{node1, node2, node3},
			hostNodes: []client.Object{
				hostNode("node-1", corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("2"),
				}),
				hostNode("node-2", corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
				hostNode("node-3", corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			quotas: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("10"),
			},
			// Total host capacity is 4, quota is 10. Each node gets its full capacity.
			want: map[string]corev1.ResourceList{
				"node-1": {"nvidia.com/gpu": resource.MustParse("2")},
				"node-2": {"nvidia.com/gpu": resource.MustParse("1")},
				"node-3": {"nvidia.com/gpu": resource.MustParse("1")},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			virtualClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.virtualNodes...).Build()

			hostClientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tt.hostNodes) > 0 {
				hostClientBuilder = hostClientBuilder.WithObjects(tt.hostNodes...)
			}
			hostClient := hostClientBuilder.Build()
			logger := zapr.NewLogger(zap.NewNop())

			got, gotErr := distributeQuotas(context.Background(), logger, hostClient, virtualClient, tt.quotas)
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
