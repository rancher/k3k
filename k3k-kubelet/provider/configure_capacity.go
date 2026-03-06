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

		m, err := distributeQuotas(ctx, logger, hostClient, virtualClient, mergedResourceLists)
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

// distributeQuotas divides the total resource quotas among all active virtual nodes,
// capped by each node's actual host capacity. This ensures that each virtual node
// reports a fair share of the available resources without exceeding what its
// underlying host node can provide.
//
// For each resource type the algorithm uses a multi-pass redistribution loop:
//  1. Divide the remaining quota evenly among eligible nodes (sorted by name for
//     determinism), assigning any integer remainder to the first nodes alphabetically.
//  2. Cap each node's share at its host allocatable capacity.
//  3. Remove nodes that have reached their host capacity.
//  4. If there is still unallocated quota (because some nodes were capped below their
//     even share), repeat from step 1 with the remaining quota and remaining nodes.
//
// The loop terminates when the quota is fully distributed or no eligible nodes remain.
func distributeQuotas(ctx context.Context, logger logr.Logger, hostClient, virtualClient client.Client, quotas corev1.ResourceList) (map[string]corev1.ResourceList, error) {
	var virtualNodeList, hostNodeList corev1.NodeList

	if err := virtualClient.List(ctx, &virtualNodeList); err != nil {
		logger.Error(err, "error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, err
	}

	if len(virtualNodeList.Items) == 0 {
		logger.Info("error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, nil
	}

	resourceMap := make(map[string]corev1.ResourceList)
	for _, vn := range virtualNodeList.Items {
		resourceMap[vn.Name] = corev1.ResourceList{}
	}

	if err := hostClient.List(ctx, &hostNodeList); err != nil {
		logger.Error(err, "error listing host nodes for stable capacity distribution, falling back to full quota")
		return nil, err
	}

	hostNodeMap := make(map[string]*corev1.Node, len(hostNodeList.Items))
	for i := range hostNodeList.Items {
		name := hostNodeList.Items[i].Name
		if _, ok := resourceMap[name]; ok {
			hostNodeMap[name] = &hostNodeList.Items[i]
		}
	}

	// Distribute each resource type from the policy's hard quota
	for resourceName, totalQuantity := range quotas {
		useMilli := false
		if _, ok := milliScaleResources[resourceName]; ok {
			useMilli = true
		}

		var eligibleNodes []string

		hostCap := make(map[string]int64)

		// Populate the host nodes capacity map and the initial effective nodes
		for _, vn := range virtualNodeList.Items {
			hostNode := hostNodeMap[vn.Name]
			if hostNode == nil {
				continue
			}
			resourceQuantity := hostNode.Status.Allocatable[resourceName]

			hostCap[vn.Name] = resourceQuantity.Value()
			if useMilli {
				hostCap[vn.Name] = resourceQuantity.MilliValue()
			}
			eligibleNodes = append(eligibleNodes, vn.Name)
		}

		sort.Strings(eligibleNodes)

		totalValue := totalQuantity.Value()
		if useMilli {
			totalValue = totalQuantity.MilliValue()
		}

		// Start of the distribution cycle, each cycle will distribute the quota resource
		// evenly between nodes, each node can not exceed the corresponding host node capacity
		for totalValue > 0 && len(eligibleNodes) > 0 {
			nodeNum := int64(len(eligibleNodes))
			quantityPerNode := totalValue / nodeNum
			remainder := totalValue % nodeNum

			remainingNodes := []string{}

			for _, virtualNodeName := range eligibleNodes {
				nodeQuantity := quantityPerNode
				if remainder > 0 {
					nodeQuantity++
					remainder--
				}
				// We cap the quantity to the hostNode capacity
				nodeQuantity = min(nodeQuantity, hostCap[virtualNodeName])

				if nodeQuantity > 0 {
					existing := resourceMap[virtualNodeName][resourceName]
					if useMilli {
						resourceMap[virtualNodeName][resourceName] = *resource.NewMilliQuantity(existing.MilliValue()+nodeQuantity, totalQuantity.Format)
					} else {
						resourceMap[virtualNodeName][resourceName] = *resource.NewQuantity(existing.Value()+nodeQuantity, totalQuantity.Format)
					}
				}

				totalValue -= nodeQuantity
				hostCap[virtualNodeName] -= nodeQuantity

				if hostCap[virtualNodeName] > 0 {
					remainingNodes = append(remainingNodes, virtualNodeName)
				}
			}

			eligibleNodes = remainingNodes
		}
	}

	return resourceMap, nil
}
