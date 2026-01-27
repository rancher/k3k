package provider

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
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

		startNodeCapacityUpdater(ctx, logger, coreClient, hostClient, virtualClient, virtualCluster, node.Name)
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
