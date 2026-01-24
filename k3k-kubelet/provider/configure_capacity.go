package provider

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func UpdateNodeCapacity(ctx context.Context, logger logr.Logger, interval time.Duration, coreClient typedv1.CoreV1Interface, hostClient client.Client, virtualClient client.Client, virtualCluster v1beta1.Cluster, virtualNodeName string) {
	ticker := time.NewTicker(interval)

	go func() {
		for range ticker.C {
			updateNodeCapacity(ctx, logger, coreClient, hostClient, virtualClient, virtualCluster, virtualNodeName)
		}
	}()
}

// updateNodeCapacity will update the virtual node capacity (and the allocatable field) with the sum of all the resource in the host nodes.
// If the nodeLabels are specified only the matching nodes will be considered.
func updateNodeCapacity(ctx context.Context, logger logr.Logger, coreClient typedv1.CoreV1Interface, hostClient client.Client, virtualClient client.Client, virtualCluster v1beta1.Cluster, virtualNodeName string) {
	// by default we get the resources of the same Node where the kubelet is running
	node, err := coreClient.Nodes().Get(ctx, virtualNodeName, metav1.GetOptions{})
	if err != nil {
		logger.Error(err, "error getting virtual node for updating node capacity")
	}

	allocatable := node.Status.Allocatable.DeepCopy()

	// we need to check if the virtual cluster resources are "limited" through ResourceQuotas
	// If so we will use the minimum resources

	var quotas corev1.ResourceQuotaList
	if err := hostClient.List(ctx, &quotas, &client.ListOptions{Namespace: virtualCluster.Namespace}); err != nil {
		logger.Error(err, "error getting namespace for updating node capacity")
	}

	if len(quotas.Items) > 0 {
		mergedQuotas := GetEffectiveHardLimits(quotas.Items)

		m, err := distributeQuotas(ctx, logger, virtualClient, mergedQuotas)
		if err != nil {
			logger.Error(err, "error distributing policy quota")
		}

		allocatable = m[virtualNodeName]
	}

	var virtualNode corev1.Node
	if err := virtualClient.Get(ctx, types.NamespacedName{Name: virtualNodeName}, &virtualNode); err != nil {
		logger.Error(err, "error getting virtual node for updating node capacity")
	}

	virtualNode.Status.Capacity = allocatable
	virtualNode.Status.Allocatable = allocatable

	if err := virtualClient.Status().Update(ctx, &virtualNode); err != nil {
		logger.Error(err, "error updating node capacity")
	}
}

var rmap = map[corev1.ResourceName]struct{}{
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

// distributeQuotas calculates the capacity and allocatable resources for the virtual nodes
// based on the VirtualClusterPolicy's quota, the host node's resources, and the number of active virtual kubelets.
func distributeQuotas(ctx context.Context, logger logr.Logger, virtualClient client.Client, quotas corev1.ResourceList) (map[string]corev1.ResourceList, error) {
	// List all virtual nodes to distribute the quota stably
	var virtualNodeList corev1.NodeList
	if err := virtualClient.List(ctx, &virtualNodeList); err != nil {
		logger.Error(err, "error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, err
	}

	numNodes := int64(len(virtualNodeList.Items))
	if numNodes == 0 {
		logger.Info("error listing virtual nodes for stable capacity distribution, falling back to full quota")
		return nil, nil
	}

	// Sort nodes by name for stable distribution
	sort.Slice(virtualNodeList.Items, func(i, j int) bool {
		return virtualNodeList.Items[i].Name < virtualNodeList.Items[j].Name
	})

	// init map
	resourceMap := make(map[string]corev1.ResourceList)
	for _, virtualNode := range virtualNodeList.Items {
		resourceMap[virtualNode.Name] = corev1.ResourceList{}
	}

	// Distribute each resource type from the policy's hard quota
	for resourceName, totalQuantity := range quotas {
		// Use MilliValue for precise division, especially for CPU

		var totalValue int64
		if _, found := rmap[resourceName]; found {
			totalValue = totalQuantity.MilliValue()
		} else {
			totalValue = totalQuantity.Value()
		}

		quantityPerNode := totalValue / numNodes
		remainder := totalValue % numNodes

		fmt.Println("totalValue", totalValue, "totalQuantity", totalQuantity.String(), "resourceName", resourceName, "quantityPerNode", quantityPerNode, "remainder", remainder)

		for _, virtualNode := range virtualNodeList.Items {
			nodeQuantity := quantityPerNode
			if remainder > 0 {
				nodeQuantity++
				remainder--
			}

			if _, found := rmap[resourceName]; found {
				resourceMap[virtualNode.Name][resourceName] = *resource.NewMilliQuantity(nodeQuantity, totalQuantity.Format)
			} else {
				resourceMap[virtualNode.Name][resourceName] = *resource.NewQuantity(nodeQuantity, totalQuantity.Format)
			}
		}
	}

	return resourceMap, nil
}
