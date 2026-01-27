package provider

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

const (
	// UpdateNodeCapacityInterval is the interval at which the node capacity is updated.
	UpdateNodeCapacityInterval = 10 * time.Second
)

// milliScaleResources is a set of resource names that are measured in milli-units (e.g., CPU).
// This is used to determine whether to use MilliValue() for calculations.
var milliScaleResources = map[corev1.ResourceName]struct{}{
	corev1.ResourceCPU:                      {},
	corev1.ResourceMemory:                   {},
	corev1.ResourceStorage:                  {},
	corev1.ResourceEphemeralStorage:         {},
	corev1.ResourceRequestsCPU:              {},
	corev1.ResourceRequestsMemory:           {},
	corev1.ResourceRequestsStorage:          {},
	corev1.ResourceRequestsEphemeralStorage: {},
	corev1.ResourceLimitsCPU:                {},
	corev1.ResourceLimitsMemory:             {},
	corev1.ResourceLimitsEphemeralStorage:   {},
}

// StartNodeCapacityUpdater starts a goroutine that periodically updates the capacity
// of the virtual node based on host node capacity and any applied ResourceQuotas.
func startNodeCapacityUpdater(ctx context.Context, logger logr.Logger, hostClient client.Client, virtualClient client.Client, virtualCluster v1beta1.Cluster, virtualNodeName string) {
	go func() {
		ticker := time.NewTicker(UpdateNodeCapacityInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				updateNodeCapacity(ctx, logger, hostClient, virtualClient, virtualCluster, virtualNodeName)
			case <-ctx.Done():
				logger.Info("Stopping node capacity updates for node", "node", virtualNodeName)
				return
			}
		}
	}()
}

// updateNodeCapacity will update the virtual node capacity (and the allocatable field) with the sum of all the resource in the host nodes.
// If the nodeLabels are specified only the matching nodes will be considered.
func updateNodeCapacity(ctx context.Context, logger logr.Logger, hostClient client.Client, virtualClient client.Client, virtualCluster v1beta1.Cluster, virtualNodeName string) {
	// by default we get the resources of the same Node where the kubelet is running
	var node corev1.Node
	if err := hostClient.Get(ctx, types.NamespacedName{Name: virtualNodeName}, &node); err != nil {
		logger.Error(err, "error getting virtual node for updating node capacity")
		return
	}

	allocatable := node.Status.Allocatable.DeepCopy()

	// we need to check if the virtual cluster resources are "limited" through ResourceQuotas
	// If so we will use the minimum resources

	var quotas corev1.ResourceQuotaList
	if err := hostClient.List(ctx, &quotas, &client.ListOptions{Namespace: virtualCluster.Namespace}); err != nil {
		logger.Error(err, "error getting namespace for updating node capacity")
	}

	if len(quotas.Items) > 0 {
		resourceLists := []corev1.ResourceList{allocatable}
		for _, q := range quotas.Items {
			resourceLists = append(resourceLists, q.Status.Hard)
		}

		mergedResourceLists := mergeResourceLists(resourceLists...)

		m, err := distributeQuotas(ctx, logger, virtualClient, mergedResourceLists)
		if err != nil {
			logger.Error(err, "error distributing policy quota")
		}

		allocatable = m[virtualNodeName]
	}

	var virtualNode corev1.Node
	if err := virtualClient.Get(ctx, types.NamespacedName{Name: virtualNodeName}, &virtualNode); err != nil {
		logger.Error(err, "error getting virtual node for updating node capacity")
		return
	}

	virtualNode.Status.Capacity = allocatable
	virtualNode.Status.Allocatable = allocatable

	if err := virtualClient.Status().Update(ctx, &virtualNode); err != nil {
		logger.Error(err, "error updating node capacity")
	}
}

// mergeResourceLists takes multiple resource lists and returns a single list that represents
// the most restrictive set of resources. For each resource name, it selects the minimum
// quantity found across all the provided lists.
func mergeResourceLists(resourceLists ...corev1.ResourceList) corev1.ResourceList {
	merged := corev1.ResourceList{}

	for _, resourceList := range resourceLists {
		for resName, qty := range resourceList {
			existingQty, found := merged[resName]

			// If it's the first time we see it OR the new one is smaller -> Update
			if !found || qty.Cmp(existingQty) < 0 {
				merged[resName] = qty.DeepCopy()
			}
		}
	}

	return merged
}

// distributeQuotas divides the total resource quotas evenly among all active virtual nodes.
// This ensures that each virtual node reports a fair share of the available resources,
// preventing the scheduler from overloading a single node.
//
// The algorithm iterates over each resource, divides it as evenly as possible among the
// sorted virtual nodes, and distributes any remainder to the first few nodes to ensure
// all resources are allocated. Sorting the nodes by name guarantees a deterministic
// distribution.
func distributeQuotas(ctx context.Context, logger logr.Logger, virtualClient client.Client, quotas corev1.ResourceList) (map[string]corev1.ResourceList, error) {
	// List all virtual nodes to distribute the quota stably.
	var virtualNodeList corev1.NodeList
	if err := virtualClient.List(ctx, &virtualNodeList); err != nil {
		logger.Error(err, "error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, err
	}

	// If there are no virtual nodes, there's nothing to distribute.
	numNodes := int64(len(virtualNodeList.Items))
	if numNodes == 0 {
		logger.Info("error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, nil
	}

	// Sort nodes by name for a deterministic distribution of resources.
	sort.Slice(virtualNodeList.Items, func(i, j int) bool {
		return virtualNodeList.Items[i].Name < virtualNodeList.Items[j].Name
	})

	// Initialize the resource map for each virtual node.
	resourceMap := make(map[string]corev1.ResourceList)
	for _, virtualNode := range virtualNodeList.Items {
		resourceMap[virtualNode.Name] = corev1.ResourceList{}
	}

	// Distribute each resource type from the policy's hard quota
	for resourceName, totalQuantity := range quotas {
		// Use MilliValue for precise division, especially for resources like CPU,
		// which are often expressed in milli-units. Otherwise, use the standard Value().
		var totalValue int64
		if _, found := milliScaleResources[resourceName]; found {
			totalValue = totalQuantity.MilliValue()
		} else {
			totalValue = totalQuantity.Value()
		}

		// Calculate the base quantity of the resource to be allocated per node.
		// and the remainder that needs to be distributed among the nodes.
		//
		// For example, if totalValue is 2000 (e.g., 2 CPU) and there are 3 nodes:
		// - quantityPerNode would be 666 (2000 / 3)
		// - remainder would be 2 (2000 % 3)
		// The first two nodes would get 667 (666 + 1), and the last one would get 666.
		quantityPerNode := totalValue / numNodes
		remainder := totalValue % numNodes

		// Iterate through the sorted virtual nodes to distribute the resource.
		for _, virtualNode := range virtualNodeList.Items {
			nodeQuantity := quantityPerNode
			if remainder > 0 {
				nodeQuantity++
				remainder--
			}

			if _, found := milliScaleResources[resourceName]; found {
				resourceMap[virtualNode.Name][resourceName] = *resource.NewMilliQuantity(nodeQuantity, totalQuantity.Format)
			} else {
				resourceMap[virtualNode.Name][resourceName] = *resource.NewQuantity(nodeQuantity, totalQuantity.Format)
			}
		}
	}

	return resourceMap, nil
}
