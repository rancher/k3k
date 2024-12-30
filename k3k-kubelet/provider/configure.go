package provider

import (
	"context"
	"time"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	k3klog "github.com/rancher/k3k/pkg/log"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConfigureNode(logger *k3klog.Logger, node *v1.Node, hostname string, servicePort int, ip string, coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualCluster v1alpha1.Cluster) {
	node.Status.Conditions = nodeConditions()
	node.Status.DaemonEndpoints.KubeletEndpoint.Port = int32(servicePort)
	node.Status.Addresses = []v1.NodeAddress{
		{
			Type:    v1.NodeHostName,
			Address: hostname,
		},
		{
			Type:    v1.NodeInternalIP,
			Address: ip,
		},
	}

	node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
	node.Labels["kubernetes.io/os"] = "linux"

	updateNodeCapacityInterval := 10 * time.Second
	ticker := time.NewTicker(updateNodeCapacityInterval)

	go func() {
		for range ticker.C {
			if err := updateNodeCapacity(coreClient, virtualClient, node.Name, virtualCluster.Spec.NodeSelector); err != nil {
				logger.Error("error updating node capacity", err)
			}
		}
	}()
}

// nodeConditions returns the basic conditions which mark the node as ready
func nodeConditions() []v1.NodeCondition {
	return []v1.NodeCondition{
		{
			Type:               "Ready",
			Status:             v1.ConditionTrue,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletReady",
			Message:            "kubelet is ready.",
		},
		{
			Type:               "OutOfDisk",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientDisk",
			Message:            "kubelet has sufficient disk space available",
		},
		{
			Type:               "MemoryPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasSufficientMemory",
			Message:            "kubelet has sufficient memory available",
		},
		{
			Type:               "DiskPressure",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "KubeletHasNoDiskPressure",
			Message:            "kubelet has no disk pressure",
		},
		{
			Type:               "NetworkUnavailable",
			Status:             v1.ConditionFalse,
			LastHeartbeatTime:  metav1.Now(),
			LastTransitionTime: metav1.Now(),
			Reason:             "RouteCreated",
			Message:            "RouteController created a route",
		},
	}
}

// updateNodeCapacity will update the virtual node capacity (and the allocatable field) with the sum of all the available nodes.
// If the nodeLabels are specified only the matching nodes will be considered.
func updateNodeCapacity(coreClient typedv1.CoreV1Interface, virtualClient client.Client, virtualNodeName string, nodeLabels map[string]string) error {
	ctx := context.Background()

	capacity, err := getCapacityFromNodes(ctx, coreClient, nodeLabels)
	if err != nil {
		return err
	}

	var virtualNode corev1.Node
	if err := virtualClient.Get(ctx, types.NamespacedName{Name: virtualNodeName}, &virtualNode); err != nil {
		return err
	}

	virtualNode.Status.Capacity = capacity
	virtualNode.Status.Allocatable = capacity

	return virtualClient.Status().Update(ctx, &virtualNode)
}

// getCapacityFromNodes will return a sum of all the resource capacities of the host nodes.
// If some node labels are specified only the matching nodes will be considered.
func getCapacityFromNodes(ctx context.Context, coreClient typedv1.CoreV1Interface, nodeLabels map[string]string) (v1.ResourceList, error) {
	listOpts := metav1.ListOptions{}
	if nodeLabels != nil {
		labelSelector := metav1.LabelSelector{MatchLabels: nodeLabels}
		listOpts.LabelSelector = labels.Set(labelSelector.MatchLabels).String()
	}

	nodeList, err := coreClient.Nodes().List(ctx, listOpts)
	if err != nil {
		return nil, err
	}

	// sum all
	allVirtualResources := corev1.ResourceList{}

	for _, node := range nodeList.Items {

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
		for resourceName, resourceQuantity := range node.Status.Capacity {
			virtualResource := allVirtualResources[resourceName]

			(&virtualResource).Add(resourceQuantity)
			allVirtualResources[resourceName] = virtualResource
		}
	}

	return allVirtualResources, nil
}
