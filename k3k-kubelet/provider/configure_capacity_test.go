package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"

	corev1 "k8s.io/api/core/v1"
)

func Test_distributeQuotas(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Large allocatable so capping doesn't interfere with basic distribution tests.
	largeAllocatable := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100"),
		corev1.ResourceMemory: resource.MustParse("100Gi"),
		corev1.ResourcePods:   resource.MustParse("1000"),
	}

	tests := []struct {
		name            string
		virtResourceMap map[string]corev1.ResourceList
		hostResourceMap map[string]corev1.ResourceList
		quotas          corev1.ResourceList
		want            map[string]corev1.ResourceList
	}{
		{
			name:            "no virtual nodes",
			virtResourceMap: map[string]corev1.ResourceList{},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			want: map[string]corev1.ResourceList{},
		},
		{
			name: "no quotas",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
			},
			quotas: corev1.ResourceList{},
			want: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
		},
		{
			name: "even distribution of cpu and memory",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
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
		},
		{
			name: "uneven distribution with remainder",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
				"node-3": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
				"node-3": largeAllocatable,
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {corev1.ResourceCPU: resource.MustParse("667m")},
				"node-2": {corev1.ResourceCPU: resource.MustParse("667m")},
				"node-3": {corev1.ResourceCPU: resource.MustParse("666m")},
			},
		},
		{
			name: "distribution of number resources",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
				"node-3": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
				"node-3": largeAllocatable,
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
		},
		{
			name: "extended resource distributed only to nodes that have it",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
				"node-3": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU: resource.MustParse("100"),
					"nvidia.com/gpu":   resource.MustParse("2"),
				},
				"node-2": {
					corev1.ResourceCPU: resource.MustParse("100"),
				},
				"node-3": {
					corev1.ResourceCPU: resource.MustParse("100"),
					"nvidia.com/gpu":   resource.MustParse("4"),
				},
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
		},
		{
			name: "capping at host capacity with redistribution",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU: resource.MustParse("8"),
				},
				"node-2": {
					corev1.ResourceCPU: resource.MustParse("2"),
				},
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
		},
		{
			name: "gpu capping with uneven host capacity",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": {
					"nvidia.com/gpu": resource.MustParse("6"),
				},
				"node-2": {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
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
		},
		{
			name: "quota exceeds total host capacity",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
				"node-3": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": {
					"nvidia.com/gpu": resource.MustParse("2"),
				},
				"node-2": {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
				"node-3": {
					"nvidia.com/gpu": resource.MustParse("1"),
				},
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := distributeQuotas(tt.hostResourceMap, tt.virtResourceMap, tt.quotas)

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
