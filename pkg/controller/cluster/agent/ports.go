package agent

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/registry/core/service/portallocator"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	kubeletPortRangeConfigMapName = "k3k-kubelet-port-range"
	webhookPortRangeConfigMapName = "k3k-webhook-port-range"

	rangeKey          = "range"
	allocatedPortsKey = "allocatedPorts"
	snapshotDataKey   = "snapshotData"
)

type PortAllocator struct {
	ctrlruntimeclient.Client

	KubeletCM *v1.ConfigMap
	WebhookCM *v1.ConfigMap
}

func NewPortAllocator(ctx context.Context, client ctrlruntimeclient.Client) (*PortAllocator, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("starting port allocator")

	portRangeConfigMapNamespace := os.Getenv("CONTROLLER_NAMESPACE")
	if portRangeConfigMapNamespace == "" {
		return nil, fmt.Errorf("failed to find k3k controller namespace")
	}

	var kubeletPortRangeCM, webhookPortRangeCM v1.ConfigMap

	kubeletPortRangeCM.Name = kubeletPortRangeConfigMapName
	kubeletPortRangeCM.Namespace = portRangeConfigMapNamespace

	webhookPortRangeCM.Name = webhookPortRangeConfigMapName
	webhookPortRangeCM.Namespace = portRangeConfigMapNamespace

	return &PortAllocator{
		Client:    client,
		KubeletCM: &kubeletPortRangeCM,
		WebhookCM: &webhookPortRangeCM,
	}, nil
}

func (a *PortAllocator) InitPortAllocatorConfig(ctx context.Context, client ctrlruntimeclient.Client, kubeletPortRange, webhookPortRange string) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		if err := a.getOrCreate(ctx, a.KubeletCM, kubeletPortRange); err != nil {
			return err
		}

		if err := a.getOrCreate(ctx, a.WebhookCM, webhookPortRange); err != nil {
			return err
		}

		return nil
	})
}

func (a *PortAllocator) getOrCreate(ctx context.Context, configmap *v1.ConfigMap, portRange string) error {
	nn := types.NamespacedName{
		Name:      configmap.Name,
		Namespace: configmap.Namespace,
	}

	if err := a.Get(ctx, nn, configmap); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		// creating the configMap for the first time
		configmap.Data = map[string]string{
			rangeKey:          portRange,
			allocatedPortsKey: "",
		}
		configmap.BinaryData = map[string][]byte{
			snapshotDataKey: []byte(""),
		}

		if err := a.Create(ctx, configmap); err != nil {
			return fmt.Errorf("failed to create port range configmap: %w", err)
		}
	}

	return nil
}

func (a *PortAllocator) AllocateWebhookPort(ctx context.Context, clusterName, clusterNamespace string) (int, error) {
	return a.allocatePort(ctx, clusterName, clusterNamespace, a.WebhookCM)
}

func (a *PortAllocator) DeallocateWebhookPort(ctx context.Context, clusterName, clusterNamespace string, webhookPort int) error {
	return a.deallocatePort(ctx, clusterName, clusterNamespace, a.WebhookCM, webhookPort)
}

func (a *PortAllocator) AllocateKubeletPort(ctx context.Context, clusterName, clusterNamespace string) (int, error) {
	return a.allocatePort(ctx, clusterName, clusterNamespace, a.KubeletCM)
}

func (a *PortAllocator) DeallocateKubeletPort(ctx context.Context, clusterName, clusterNamespace string, kubeletPort int) error {
	return a.deallocatePort(ctx, clusterName, clusterNamespace, a.KubeletCM, kubeletPort)
}

// allocatePort will assign port to the cluster from a port Range configured for k3k
func (a *PortAllocator) allocatePort(ctx context.Context, clusterName, clusterNamespace string, configMap *v1.ConfigMap) (int, error) {
	portRange, ok := configMap.Data[rangeKey]
	if !ok {
		return 0, fmt.Errorf("port range is not initialized")
	}

	// get configMap first to avoid conflicts
	if err := a.getOrCreate(ctx, configMap, portRange); err != nil {
		return 0, err
	}

	clusterNamespaceName := clusterNamespace + "/" + clusterName

	portsMap, err := parsePortMap(configMap.Data[allocatedPortsKey])
	if err != nil {
		return 0, err
	}

	if _, ok := portsMap[clusterNamespaceName]; ok {
		return portsMap[clusterNamespaceName], nil
	}
	// allocate a new port and save the snapshot
	snapshot := core.RangeAllocation{
		Range: configMap.Data[rangeKey],
		Data:  configMap.BinaryData[snapshotDataKey],
	}

	pa, err := portallocator.NewFromSnapshot(&snapshot)
	if err != nil {
		return 0, err
	}

	next, err := pa.AllocateNext()
	if err != nil {
		return 0, err
	}

	portsMap[clusterNamespaceName] = next

	if err := saveSnapshot(pa, &snapshot, configMap, portsMap); err != nil {
		return 0, err
	}

	if err := a.Update(ctx, configMap); err != nil {
		return 0, err
	}

	return next, nil
}

// deallocatePort will remove the port used by the cluster from the port range
func (a *PortAllocator) deallocatePort(ctx context.Context, clusterName, clusterNamespace string, configMap *v1.ConfigMap, port int) error {
	portRange, ok := configMap.Data[rangeKey]
	if !ok {
		return fmt.Errorf("port range is not initialized")
	}

	if err := a.getOrCreate(ctx, configMap, portRange); err != nil {
		return err
	}

	clusterNamespaceName := clusterNamespace + "/" + clusterName

	portsMap, err := parsePortMap(configMap.Data[allocatedPortsKey])
	if err != nil {
		return err
	}
	// check if the cluster already exists in the configMap
	if usedPort, ok := portsMap[clusterNamespaceName]; ok {
		if usedPort != port {
			return fmt.Errorf("port %d does not match used port %d for the cluster", port, usedPort)
		}

		snapshot := core.RangeAllocation{
			Range: configMap.Data[rangeKey],
			Data:  configMap.BinaryData[snapshotDataKey],
		}

		pa, err := portallocator.NewFromSnapshot(&snapshot)
		if err != nil {
			return err
		}

		if err := pa.Release(port); err != nil {
			return err
		}

		delete(portsMap, clusterNamespaceName)

		if err := saveSnapshot(pa, &snapshot, configMap, portsMap); err != nil {
			return err
		}
	}

	return a.Update(ctx, configMap)
}

// parsePortMap will convert ConfigMap Data to a portMap of string keys and values of ints
func parsePortMap(portMapData string) (map[string]int, error) {
	portMap := make(map[string]int)
	if err := yaml.Unmarshal([]byte(portMapData), &portMap); err != nil {
		return nil, fmt.Errorf("failed to parse allocatedPorts: %w", err)
	}

	return portMap, nil
}

// serializePortMap will convert a portMap of string keys and values of ints to ConfigMap Data
func serializePortMap(m map[string]int) (string, error) {
	out, err := yaml.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("failed to serialize allocatedPorts: %w", err)
	}

	return string(out), nil
}

func saveSnapshot(portAllocator *portallocator.PortAllocator, snapshot *core.RangeAllocation, configMap *v1.ConfigMap, portsMap map[string]int) error {
	// save the new snapshot
	if err := portAllocator.Snapshot(snapshot); err != nil {
		return err
	}
	// update the configmap with the new portsMap and the new snapshot
	configMap.BinaryData[snapshotDataKey] = snapshot.Data
	configMap.Data[rangeKey] = snapshot.Range

	allocatedPortsData, err := serializePortMap(portsMap)
	if err != nil {
		return err
	}

	configMap.Data[allocatedPortsKey] = allocatedPortsData

	return nil
}
