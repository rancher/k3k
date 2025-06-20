package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	KubeletCM        *v1.ConfigMap
	KubeletPortStart int
	KubeletPortEnd   int
	WebhookCM        *v1.ConfigMap
	WebhookPortStart int
	WebhookPortEnd   int
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

	kubeletPortStart, kubeletPortEnd, err := parsePortRange(kubeletPortRange)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubelet port range %v", err)
	}

	webhookPortStart, webhookPortEnd, err := parsePortRange(webhookPortRange)
	if err != nil {
		return nil, fmt.Errorf("failed to parse webhook port range %v", err)
	}

	return &PortAllocator{
		KubeletPortStart: *kubeletPortStart,
		KubeletPortEnd:   *kubeletPortEnd,
		WebhookPortStart: *webhookPortStart,
		WebhookPortEnd:   *webhookPortEnd,
		KubeletCM:        &kubeletPortRangeCM,
		WebhookCM:        &webhookPortRangeCM,
	}, nil
}

func (a *PortAllocator) InitPortAllocatorConfig(ctx context.Context, client ctrlruntimeclient.Client) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		if err := a.getOrCreate(ctx, client, a.KubeletCM); err != nil {
			return err
		}
		if err := a.getOrCreate(ctx, client, a.WebhookCM); err != nil {
			return err
		}
		return nil
	})
}

func (a *PortAllocator) cm(name, namespace string) *v1.ConfigMap {
	return &v1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{},
	}
}

func (a *PortAllocator) getOrCreate(ctx context.Context, client ctrlruntimeclient.Client, configmap *v1.ConfigMap) error {
	nn := types.NamespacedName{
		Name:      configmap.Name,
		Namespace: configmap.Namespace,
	}
	if err := client.Get(ctx, nn, configmap); err != nil {
		if apierrors.IsNotFound(err) {
			// creating the configMap for the first time
			configmap = a.cm(configmap.Name, configmap.Namespace)
			if err := client.Create(ctx, configmap); err != nil {
				return fmt.Errorf("failed to create port range configmap: %v", err)
			}
		} else {
			return err
		}
	}
	return nil
}

func (a *PortAllocator) AllocateWebhookPort(ctx context.Context, cfg *Config) (*int, error) {
	return a.allocatePort(ctx, cfg, a.WebhookCM, a.WebhookPortStart, a.WebhookPortEnd)
}

func (a *PortAllocator) DeallocateWebhookPort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, webhookPort int) error {
	return a.deallocatePort(ctx, client, clusterName, clusterNamespace, a.WebhookCM, webhookPort)
}

func (a *PortAllocator) AllocateKubeletPort(ctx context.Context, cfg *Config) (*int, error) {
	return a.allocatePort(ctx, cfg, a.KubeletCM, a.KubeletPortStart, a.KubeletPortEnd)
}

func (a *PortAllocator) DeallocateKubeletPort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, kubeletPort int) error {
	return a.deallocatePort(ctx, client, clusterName, clusterNamespace, a.KubeletCM, kubeletPort)
}

// allocatePort will assign port to the cluster from a port Range configured for k3k
func (a *PortAllocator) allocatePort(ctx context.Context, cfg *Config, configMap *v1.ConfigMap, portStart, portEnd int) (*int, error) {
	// get configMap first to avoid conflicts
	if err := a.getOrCreate(ctx, cfg.client, configMap); err != nil {
		return nil, err
	}
	clusterNameNamespace := cfg.cluster.Name + "-" + cfg.cluster.Namespace
	portMap, err := parsePortMap(configMap.Data)
	if err != nil {
		return nil, err
	}
	// check if the cluster already exists in the configMap
	if port, ok := portMap[clusterNameNamespace]; ok {
		return ptr.To(port), nil
	}

	// allocating a new port
	used := make(map[int]bool)
	for _, p := range portMap {
		used[p] = true
	}

	var allocatedPort int
	for p := portStart; p <= portEnd; p++ {
		if !used[p] {
			allocatedPort = p
			break
		}
	}
	if allocatedPort == 0 {
		return ptr.To(allocatedPort), fmt.Errorf("no ports available")
	}

	portMap[clusterNameNamespace] = allocatedPort
	configMap.Data = serializePortMap(portMap)
	if err := cfg.client.Update(ctx, configMap); err != nil {
		return nil, err
	}

	return ptr.To(allocatedPort), nil
}

// deallocatePort will remove the port used by the cluster from the port range
func (a *PortAllocator) deallocatePort(ctx context.Context, client ctrlruntimeclient.Client, clusterName, clusterNamespace string, configMap *v1.ConfigMap, port int) error {
	if err := a.getOrCreate(ctx, client, configMap); err != nil {
		return err
	}
	clusterNameNamespace := clusterName + "-" + clusterNamespace
	portMap, err := parsePortMap(configMap.Data)
	if err != nil {
		return err
	}
	// check if the cluster already exists in the configMap
	if usedPort, ok := portMap[clusterNameNamespace]; ok {
		if usedPort != port {
			return fmt.Errorf("port %d does not match used port %d for the cluster", port, usedPort)
		}
		delete(configMap.Data, clusterNameNamespace)
	}

	return client.Update(ctx, configMap)
}

// parsePortMap will convert ConfigMap Data to a portMap of string keys and values of ints
func parsePortMap(portMap map[string]string) (map[string]int, error) {
	result := map[string]int{}
	for cluster, portString := range portMap {
		port, err := strconv.Atoi(portString)
		if err != nil {
			return nil, err
		}
		result[cluster] = port
	}
	return result, nil
}

// serializePortMap will convert a portMap of string keys and values of ints to ConfigMap Data
func serializePortMap(m map[string]int) map[string]string {
	result := map[string]string{}
	for cluster, port := range m {
		portString := strconv.Itoa(port)
		result[cluster] = portString

	}
	return result
}

func parsePortRange(portRange string) (*int, *int, error) {
	// get the start and the end of the portRange
	portRangeSplitted := strings.SplitN(portRange, "-", 2)
	if len(portRangeSplitted) > 2 {
		return nil, nil, fmt.Errorf("incorrect port range")
	}
	portRangeStart, err := strconv.Atoi(portRangeSplitted[0])
	if err != nil {
		return nil, nil, err
	}
	portRangeEnd, err := strconv.Atoi(portRangeSplitted[1])
	if err != nil {
		return nil, nil, err
	}

	if portRangeEnd < portRangeStart {
		return nil, nil, fmt.Errorf("invalid port range %d-%d", portRangeStart, portRangeEnd)
	}

	return ptr.To(portRangeStart), ptr.To(portRangeEnd), nil
}
