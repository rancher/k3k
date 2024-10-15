package provider

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ConfigureNode(node *v1.Node, podIP string, servicePort int) {
	node.Status.Conditions = nodeConditions()
	node.Status.DaemonEndpoints.KubeletEndpoint.Port = int32(servicePort)
	node.Status.Addresses = []v1.NodeAddress{
		{
			Type:    v1.NodeExternalIP,
			Address: podIP,
		},
	}
	node.Status.Capacity = v1.ResourceList{
		// TODO: Make this more dynamic based on the sum of existing nodes
		v1.ResourceCPU:    resource.MustParse("8"),
		v1.ResourceMemory: resource.MustParse("326350752922"),
		v1.ResourcePods:   resource.MustParse("110"),
	}
	node.Status.Allocatable = node.Status.Capacity
	node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"] = "true"
	node.Labels["kubernetes.io/os"] = "linux"
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
