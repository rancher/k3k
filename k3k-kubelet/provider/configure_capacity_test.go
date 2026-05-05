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
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
			},
		},
		{
			name: "fewer virtual nodes than host nodes",
			virtResourceMap: map[string]corev1.ResourceList{
				"node-1": {},
				"node-2": {},
			},
			hostResourceMap: map[string]corev1.ResourceList{
				"node-1": largeAllocatable,
				"node-2": largeAllocatable,
				"node-3": largeAllocatable,
				"node-4": largeAllocatable,
			},
			quotas: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			want: map[string]corev1.ResourceList{
				"node-1": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
				"node-2": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
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
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
				"node-2": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
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
				"node-1": {
					corev1.ResourceCPU:    resource.MustParse("667m"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
				"node-2": {
					corev1.ResourceCPU:    resource.MustParse("667m"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
				"node-3": {
					corev1.ResourceCPU:    resource.MustParse("666m"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
					corev1.ResourcePods:   resource.MustParse("1000"),
				},
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
					corev1.ResourceCPU:    resource.MustParse("667m"),
					corev1.ResourcePods:   resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
				},
				"node-2": {
					corev1.ResourceCPU:    resource.MustParse("667m"),
					corev1.ResourcePods:   resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
				},
				"node-3": {
					corev1.ResourceCPU:    resource.MustParse("666m"),
					corev1.ResourcePods:   resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("100Gi"),
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
			// quotas are assumed to be filtered and merged into one list. there is a dedicated test for filtering quotas
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

func Test_mergeQuotas(t *testing.T) {
	tests := []struct {
		name       string
		quotasList []corev1.ResourceList
		want       corev1.ResourceList
	}{
		{
			name:       "no resource lists",
			quotasList: []corev1.ResourceList{},
			want:       corev1.ResourceList{},
		},
		{
			name: "single resource list",
			quotasList: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
		{
			name: "most restrictive value wins",
			quotasList: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
		{
			name: "resource only in one list is included",
			quotasList: []corev1.ResourceList{
				{
					corev1.ResourceCPU: resource.MustParse("4"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("16Gi"),
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
		{
			name: "three lists pick minimum across all",
			quotasList: []corev1.ResourceList{
				{corev1.ResourceCPU: resource.MustParse("10")},
				{corev1.ResourceCPU: resource.MustParse("6")},
				{corev1.ResourceCPU: resource.MustParse("8")},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("6"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeQuotas(tt.quotasList...)

			assert.Equal(t, len(tt.want), len(got), "Number of resources in merged quota should match")

			for resName, expectedQty := range tt.want {
				actualQty, ok := got[resName]
				assert.True(t, ok, "Resource %s not found in merged quota", resName)
				assert.True(t, expectedQty.Equal(actualQty), "Resource %s did not match. want: %s, got: %s", resName, expectedQty.String(), actualQty.String())
			}
		})
	}
}

func Test_filterQuotas(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	tests := []struct {
		name   string
		quotas corev1.ResourceList
		want   corev1.ResourceList
	}{
		{
			name: "no quotas",
			want: corev1.ResourceList{},
		}, {
			name: "filter core infrastructure request resources with no requests prefix",
			quotas: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("500m"),
				corev1.ResourceMemory:           resource.MustParse("1Gi"),
				corev1.ResourceStorage:          resource.MustParse("5Gi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("5Gi"),
			},
			want: corev1.ResourceList{},
		}, {
			name: "filter core infrastructure request resources with requests prefix",
			quotas: corev1.ResourceList{
				corev1.ResourceRequestsCPU:              resource.MustParse("500m"),
				corev1.ResourceRequestsMemory:           resource.MustParse("1Gi"),
				corev1.ResourceRequestsStorage:          resource.MustParse("5Gi"),
				corev1.ResourceRequestsEphemeralStorage: resource.MustParse("5Gi"),
			},
			want: corev1.ResourceList{},
		}, {
			name: "trim limits prefix in core infrastructure resources",
			quotas: corev1.ResourceList{
				corev1.ResourceLimitsCPU:              resource.MustParse("500m"),
				corev1.ResourceLimitsMemory:           resource.MustParse("1Gi"),
				corev1.ResourceLimitsEphemeralStorage: resource.MustParse("5Gi"),
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("500m"),
				corev1.ResourceMemory:           resource.MustParse("1Gi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("5Gi"),
			},
		}, {
			name: "trim requests prefix in extended resources",
			quotas: corev1.ResourceList{
				"requests.nvidia.com/gpu": resource.MustParse("2"),
			},
			want: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("2"),
			},
		}, {
			name: "will not filter extended resources or object counts",
			quotas: corev1.ResourceList{
				"nvidia.com/gpu":    resource.MustParse("2"),
				corev1.ResourcePods: resource.MustParse("5"),
			},
			want: corev1.ResourceList{
				"nvidia.com/gpu":    resource.MustParse("2"),
				corev1.ResourcePods: resource.MustParse("5"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filteredQuota := filterQuotas(tt.quotas)

			for expectedResourceName, expectedResourceQty := range tt.want {
				actualResourceQty, ok := filteredQuota[expectedResourceName]
				assert.True(t, ok, "%s resource is not found in filtered quotas", expectedResourceName)
				assert.True(t, expectedResourceQty.Equal(actualResourceQty), "Filtered Resource %s did not match. want: %s, got: %s", expectedResourceName, expectedResourceQty.String(), actualResourceQty.String())
			}
		})
	}
}
