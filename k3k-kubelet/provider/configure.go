package provider

import (
	"context"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/policy"
)

func ConfigureNode(logger logr.Logger, node *corev1.Node, hostname string, servicePort int, ip string, hostClient client.Client, coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualCluster v1beta1.Cluster, version string, mirrorHostNodes bool) {
	ctx := context.Background()
	if mirrorHostNodes {
		hostNode, err := coreClient.Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			logger.Error(err, "error getting host node for mirroring", err)
		}

		node.Spec = *hostNode.Spec.DeepCopy()
		node.Status = *hostNode.Status.DeepCopy()
		node.Labels = hostNode.GetLabels()
		node.Annotations = hostNode.GetAnnotations()
		node.Finalizers = hostNode.GetFinalizers()
		node.Status.DaemonEndpoints.KubeletEndpoint.Port = int32(servicePort)
	} else {
		node.Status.Conditions = nodeConditions()
		node.Status.DaemonEndpoints.KubeletEndpoint.Port = int32(servicePort)
		node.Status.Addresses = []corev1.NodeAddress{
			{
				Type:    corev1.NodeHostName,
				Address: hostname,
			},
			{
				Type:    corev1.NodeInternalIP,
				Address: ip,
			},
		}

		node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
		node.Labels["kubernetes.io/os"] = "linux"

		// configure versions
		node.Status.NodeInfo.KubeletVersion = version

		updateNodeCapacityInterval := 10 * time.Second
		ticker := time.NewTicker(updateNodeCapacityInterval)

		go func() {
			for range ticker.C {
				var virtualNode corev1.Node
				if err := virtualClient.Get(ctx, types.NamespacedName{Name: node.Name}, &virtualNode); err != nil {
					logger.Error(err, "error getting virtual node for updating node capacity")
				}

				var ns corev1.Namespace
				if err := hostClient.Get(ctx, types.NamespacedName{Name: virtualCluster.Namespace}, &ns); err != nil {
					logger.Error(err, "error getting namespace for updating node capacity")
				}

				policyName := ns.Labels[policy.PolicyNameLabelKey]
				if policyName != "" {
					var policy v1beta1.VirtualClusterPolicy
					if err := hostClient.Get(ctx, types.NamespacedName{Name: policyName}, &policy); err != nil {
						logger.Error(err, "error getting policy for updating node capacity")
					}

					// Call the new function to distribute the quota
					if err := distributePolicyQuota(logger, ctx, hostClient, coreClient, virtualClient, &virtualNode, virtualCluster, &policy); err != nil {
						logger.Error(err, "error distributing policy quota")
					}
				} else {
					if err := updateNodeCapacity(ctx, coreClient, virtualClient, node.Name); err != nil {
						logger.Error(err, "error updating node capacity")
					}
				}
			}
		}()
	}
}

// nodeConditions returns the basic conditions which mark the node as ready
func nodeConditions() []corev1.NodeCondition {
	return []corev1.NodeCondition{
		{
			Type:               "Ready",
			Status:             corev1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "kubelet is ready.",
		},
		{
			Type:               "OutOfDisk",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             corev1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

// updateNodeCapacity will update the virtual node capacity (and the allocatable field) with the sum of all the resource in the host nodes.
// If the nodeLabels are specified only the matching nodes will be considered.
func updateNodeCapacity(ctx context.Context, coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualNodeName string) error {
	capacity, allocatable, err := getResourcesFromNodes(ctx, coreClient, virtualNodeName)
	if err != nil {
		return err
	}

	var virtualNode corev1.Node
	if err := virtualClient.Get(ctx, types.NamespacedName{Name: virtualNodeName}, &virtualNode); err != nil {
		return err
	}

	virtualNode.Status.Capacity = capacity
	virtualNode.Status.Allocatable = allocatable

	return virtualClient.Status().Update(ctx, &virtualNode)
}

// distributePolicyQuota calculates and sets the capacity and allocatable resources for a virtual node
// based on the VirtualClusterPolicy's quota, the host node's resources, and the number of active virtual kubelets.
func distributePolicyQuota(logger logr.Logger, ctx context.Context, hostClient client.Client, coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualNode *corev1.Node, virtualCluster v1beta1.Cluster, policy *v1beta1.VirtualClusterPolicy) error {
	if policy.Spec.Quota == nil {
		return nil // No quota defined, nothing to distribute
	}

	// Get the DaemonSet for the virtual kubelets to determine the desired number of nodes
	key := types.NamespacedName{
		Name:      controller.SafeConcatNameWithPrefix(virtualCluster.Name, agent.SharedNodeAgentName),
		Namespace: virtualCluster.Namespace,
	}

	var daemonSet apps.DaemonSet
	if err := hostClient.Get(ctx, key, &daemonSet); err != nil {
		logger.Error(err, "error getting DaemonSet for virtual kubelets, cannot distribute policy quota accurately")
		// Fallback: if DaemonSet cannot be retrieved, set node capacity to full policy quota
		virtualNode.Status.Capacity = policy.Spec.Quota.Hard.DeepCopy()
		virtualNode.Status.Allocatable = policy.Spec.Quota.Hard.DeepCopy()
		return virtualClient.Status().Update(ctx, virtualNode)
	}

	numVirtualKubelets := int64(daemonSet.Status.DesiredNumberScheduled)
	if numVirtualKubelets == 0 {
		logger.Info("no virtual kubelets desired by DaemonSet, setting capacity to policy hard limit")
		virtualNode.Status.Capacity = policy.Spec.Quota.Hard.DeepCopy()
		virtualNode.Status.Allocatable = policy.Spec.Quota.Hard.DeepCopy()
		return virtualClient.Status().Update(ctx, virtualNode)
	}

	// List all virtual nodes to distribute the quota stably
	var virtualNodeList corev1.NodeList
	if err := virtualClient.List(ctx, &virtualNodeList); err != nil {
		logger.Error(err, "error listing virtual nodes for stable capacity distribution, falling back to full quota")
		virtualNode.Status.Capacity = policy.Spec.Quota.Hard.DeepCopy()
		virtualNode.Status.Allocatable = policy.Spec.Quota.Hard.DeepCopy()
		return virtualClient.Status().Update(ctx, virtualNode)
	}

	// Sort nodes by name for stable distribution
	sort.Slice(virtualNodeList.Items, func(i, j int) bool {
		return virtualNodeList.Items[i].Name < virtualNodeList.Items[j].Name
	})

	// Find the index of the current virtualNode in the sorted list
	currentNodeIndex := -1
	for i, n := range virtualNodeList.Items {
		if n.Name == virtualNode.Name {
			currentNodeIndex = i
			break
		}
	}

	if currentNodeIndex == -1 {
		logger.Error(nil, "current virtual node not found in the list of virtual nodes, cannot distribute quota stably")
		// Fallback: if current node not found, set node capacity to full policy quota
		virtualNode.Status.Capacity = policy.Spec.Quota.Hard.DeepCopy()
		virtualNode.Status.Allocatable = policy.Spec.Quota.Hard.DeepCopy()
		return virtualClient.Status().Update(ctx, virtualNode)
	}

	// Get the actual host node's capacity and allocatable resources as the baseline
	hostNodeCapacity, hostNodeAllocatable, err := getResourcesFromNodes(ctx, coreClient, virtualNode.Name)
	if err != nil {
		logger.Error(err, "error getting host node resources for baseline capacity, falling back to full policy quota")
		virtualNode.Status.Capacity = policy.Spec.Quota.Hard.DeepCopy()
		virtualNode.Status.Allocatable = policy.Spec.Quota.Hard.DeepCopy()
		return virtualClient.Status().Update(ctx, virtualNode)
	}

	// Start with the host node's capacity and allocatable as the base
	newCapacity := hostNodeCapacity.DeepCopy()
	newAllocatable := hostNodeAllocatable.DeepCopy()

	// Distribute each resource type from the policy's hard quota
	for resourceName, totalQuantity := range policy.Spec.Quota.Hard {
		totalValue := totalQuantity.Value()

		quantityPerNode := totalValue / numVirtualKubelets
		remainder := totalValue % numVirtualKubelets

		currentNodesQuantity := quantityPerNode
		if int64(currentNodeIndex) < remainder {
			currentNodesQuantity++
		}

		// Update the specific resource for the current virtual node
		newCapacity[resourceName] = *resource.NewQuantity(currentNodesQuantity, resource.DecimalSI)
		newAllocatable[resourceName] = *resource.NewQuantity(currentNodesQuantity, resource.DecimalSI)
	}

	// Apply the calculated capacity and allocatable to the virtual node
	virtualNode.Status.Capacity = newCapacity
	virtualNode.Status.Allocatable = newAllocatable
	return virtualClient.Status().Update(ctx, virtualNode)
}

// getResourcesFromNodes will return a sum of all the resource capacity of the host nodes, and the allocatable resources.
// If some node labels are specified only the matching nodes will be considered.
func getResourcesFromNodes(ctx context.Context, coreClient typedv1.CoreV1Interface, virtualNodeName string) (corev1.ResourceList, corev1.ResourceList, error) {
	node, err := coreClient.Nodes().Get(ctx, virtualNodeName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	// sum all
	virtualCapacityResources := corev1.ResourceList{}
	virtualAvailableResources := corev1.ResourceList{}

	// check if the node is Ready
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady {
			continue
		}

		// if the node is not Ready then we can skip it
		if condition.Status != corev1.ConditionTrue {
			break
		}
	}

	// add all the available metrics to the virtual node
	// TODO when using Quotas we should use that to actually limits the virtual node
	for resourceName, resourceQuantity := range node.Status.Capacity {
		virtualResource := virtualCapacityResources[resourceName]

		(&virtualResource).Add(resourceQuantity)
		virtualCapacityResources[resourceName] = virtualResource
	}

	for resourceName, resourceQuantity := range node.Status.Allocatable {
		virtualResource := virtualAvailableResources[resourceName]

		(&virtualResource).Add(resourceQuantity)
		virtualAvailableResources[resourceName] = virtualResource
	}

	return virtualCapacityResources, virtualAvailableResources, nil
}
