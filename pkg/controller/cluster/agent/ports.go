package agent

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/registry/core/service/portallocator"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	kubeletPortRangeConfigMapName = "k3k-kubelet-port-range"
	webhookPortRangeConfigMapName = "k3k-webhook-port-range"
)

type PortAllocator struct {
	KubeletCM *v1.ConfigMap
	WebhookCM *v1.ConfigMap
}

func NewPortAllocator(ctx context.Context, client ctrlruntimeclient.Client, kubeletPortRange, webhookPortRange string) (*PortAllocator, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("starting port allocator")

	var kubeletPortRangeCM, webhookPortRangeCM v1.ConfigMap

	portRangeConfigMapNamespace := os.Getenv("CONTROLLER_NAMESPACE")
	if portRangeConfigMapNamespace == "" {
		return nil, fmt.Errorf("failed to find k3k controller namespace")
	}

	kubeletPortRangeCM.Name = kubeletPortRangeConfigMapName
	kubeletPortRangeCM.Namespace = portRangeConfigMapNamespace

	webhookPortRangeCM.Name = webhookPortRangeConfigMapName
	webhookPortRangeCM.Namespace = portRangeConfigMapNamespace

	return &PortAllocator{
		KubeletCM: &kubeletPortRangeCM,
		WebhookCM: &webhookPortRangeCM,
	}, nil
}

func (a *PortAllocator) InitPortAllocatorConfig(ctx context.Context, client ctrlruntimeclient.Client, kubeletPortRange, webhookPortRange string) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		if err := a.getOrCreate(ctx, client, a.KubeletCM, kubeletPortRange); err != nil {
			return err
		}

		if err := a.getOrCreate(ctx, client, a.WebhookCM, webhookPortRange); err != nil {
			return err
		}

		return nil
	})
}

func (a *PortAllocator) cm(name, namespace, portRange string) *v1.ConfigMap {
	return &v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			"range":          portRange,
			"allocatedPorts": "",
		},
		BinaryData: map[string][]byte{
			"snapshotData": []byte(""),
		},
	}
}

func (a *PortAllocator) getOrCreate(ctx context.Context, client ctrlruntimeclient.Client, configmap *v1.ConfigMap, portRange string) error {
	nn := types.NamespacedName{
		Name:      configmap.Name,
		Namespace: configmap.Namespace,
	}
	if err := client.Get(ctx, nn, configmap); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		// creating the configMap for the first time
		configmap = a.cm(configmap.Name, configmap.Namespace, portRange)
		if err := client.Create(ctx, configmap); err != nil {
			return fmt.Errorf("failed to create port range configmap: %w", err)
		}
	}

	return nil
}

func (a *PortAllocator) AllocateWebhookPort(ctx context.Context, cfg *Config) (*int, error) {
	return a.allocatePort(ctx, cfg, a.WebhookCM)
}

func (a *PortAllocator) DeallocateWebhookPort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, webhookPort int) error {
	return a.deallocatePort(ctx, client, clusterName, clusterNamespace, a.WebhookCM, webhookPort)
}

func (a *PortAllocator) AllocateKubeletPort(ctx context.Context, cfg *Config) (*int, error) {
	return a.allocatePort(ctx, cfg, a.KubeletCM)
}

func (a *PortAllocator) DeallocateKubeletPort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, kubeletPort int) error {
	return a.deallocatePort(ctx, client, clusterName, clusterNamespace, a.KubeletCM, kubeletPort)
}

// allocatePort will assign port to the cluster from a port Range configured for k3k
func (a *PortAllocator) allocatePort(ctx context.Context, cfg *Config, configMap *v1.ConfigMap) (*int, error) {
	portRange, ok := configMap.Data["range"]
	if !ok {
		return nil, fmt.Errorf("port range is not initialized")
	}
	// get configMap first to avoid conflicts
	if err := a.getOrCreate(ctx, cfg.client, configMap, portRange); err != nil {
		return nil, err
	}

	clusterNameNamespace := cfg.cluster.Name + "-" + cfg.cluster.Namespace

	portsMap, err := parsePortMap(configMap.Data["allocatedPorts"])
	if err != nil {
		return nil, err
	}

	if _, ok := portsMap[clusterNameNamespace]; ok {
		return ptr.To(portsMap[clusterNameNamespace]), nil
	}
	// allocate a new port and save the snapshot
	snapshot := core.RangeAllocation{
		Range: configMap.Data["range"],
		Data:  configMap.BinaryData["snapshotData"],
	}

	pa, err := portallocator.NewFromSnapshot(&snapshot)
	if err != nil {
		return nil, err
	}

	next, err := pa.AllocateNext()
	if err != nil {
		return nil, err
	}

	portsMap[clusterNameNamespace] = next

	if err := saveSnapshot(pa, &snapshot, configMap, portsMap); err != nil {
		return nil, err
	}

	if err := cfg.client.Update(ctx, configMap); err != nil {
		return nil, err
	}

	return ptr.To(next), nil
}

// deallocatePort will remove the port used by the cluster from the port range
func (a *PortAllocator) deallocatePort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, configMap *v1.ConfigMap, port int) error {
	portRange, ok := configMap.Data["range"]
	if !ok {
		return fmt.Errorf("port range is not initialized")
	}
	if err := a.getOrCreate(ctx, client, configMap, portRange); err != nil {
		return err
	}

	clusterNameNamespace := clusterName + "-" + clusterNamespace

	portsMap, err := parsePortMap(configMap.Data["allocatedPorts"])
	if err != nil {
		return err
	}
	// check if the cluster already exists in the configMap
	if usedPort, ok := portsMap[clusterNameNamespace]; ok {
		if usedPort != port {
			return fmt.Errorf("port %d does not match used port %d for the cluster", port, usedPort)
		}

		snapshot := core.RangeAllocation{
			Range: configMap.Data["range"],
			Data:  configMap.BinaryData["snapshotData"],
		}

		pa, err := portallocator.NewFromSnapshot(&snapshot)
		if err != nil {
			return err
		}

		if err := pa.Release(port); err != nil {
			return err
		}

		delete(portsMap, clusterNameNamespace)

		if err := saveSnapshot(pa, &snapshot, configMap, portsMap); err != nil {
			return err
		}
	}

	return client.Update(ctx, configMap)
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
	configMap.BinaryData["snapshotData"] = snapshot.Data
	configMap.Data["range"] = snapshot.Range

	allocatedPortsData, err := serializePortMap(portsMap)
	if err != nil {
		return err
	}

	configMap.Data["allocatedPorts"] = allocatedPortsData

	return nil
}
