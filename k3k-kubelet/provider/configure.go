package provider

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func ConfigureNode(logger logr.Logger, node *corev1.Node, hostname string, servicePort int, ip string, coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualCluster v1beta1.Cluster, version string, mirrorHostNodes bool) {
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
				if err := updateNodeCapacity(ctx, coreClient, virtualClient, node.Name); err != nil {
					logger.Error(err, "error updating node capacity")
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
