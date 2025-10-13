//go:generate ./scripts/generate
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/buildinfo"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/policy"
	"github.com/rancher/k3k/pkg/log"
)

var (
	scheme                  = runtime.NewScheme()
	config                  cluster.Config
	kubeconfig              string
	kubeletPortRange        string
	webhookPortRange        string
	maxConcurrentReconciles int
	debug                   bool
	logFormat               string
	logger                  logr.Logger
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
}

func main() {
	rootCmd := &cobra.Command{
		Use:     "k3k",
		Short:   "k3k controller",
		Version: buildinfo.Version,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validate()
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmds.InitializeConfig(cmd)
			logger = zapr.NewLogger(log.New(debug, logFormat))
		},
		RunE: run,
	}

	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Debug level logging")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "json", "Log format (json or console)")
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path")
	rootCmd.PersistentFlags().StringVar(&config.ClusterCIDR, "cluster-cidr", "", "Cluster CIDR to be added to the networkpolicy")
	rootCmd.PersistentFlags().StringVar(&config.SharedAgentImage, "agent-shared-image", "rancher/k3k-kubelet", "K3K Virtual Kubelet image")
	rootCmd.PersistentFlags().StringVar(&config.SharedAgentImagePullPolicy, "agent-shared-image-pull-policy", "", "K3K Virtual Kubelet image pull policy must be one of Always, IfNotPresent or Never")
	rootCmd.PersistentFlags().StringVar(&config.VirtualAgentImage, "agent-virtual-image", "rancher/k3s", "K3S Virtual Agent image")
	rootCmd.PersistentFlags().StringVar(&config.VirtualAgentImagePullPolicy, "agent-virtual-image-pull-policy", "", "K3S Virtual Agent image pull policy must be one of Always, IfNotPresent or Never")
	rootCmd.PersistentFlags().StringVar(&kubeletPortRange, "kubelet-port-range", "50000-51000", "Port Range for k3k kubelet in shared mode")
	rootCmd.PersistentFlags().StringVar(&webhookPortRange, "webhook-port-range", "51001-52000", "Port Range for k3k kubelet webhook in shared mode")
	rootCmd.PersistentFlags().StringVar(&config.K3SServerImage, "k3s-server-image", "rancher/k3s", "K3K server image")
	rootCmd.PersistentFlags().StringVar(&config.K3SServerImagePullPolicy, "k3s-server-image-pull-policy", "", "K3K server image pull policy")
	rootCmd.PersistentFlags().StringSliceVar(&config.ServerImagePullSecrets, "server-image-pull-secret", nil, "Image pull secret used for for servers")
	rootCmd.PersistentFlags().StringSliceVar(&config.AgentImagePullSecrets, "agent-image-pull-secret", nil, "Image pull secret used for for agents")
	rootCmd.PersistentFlags().IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 50, "maximum number of concurrent reconciles")

	if err := rootCmd.Execute(); err != nil {
		logger.Error(err, "failed to run k3k controller")
	}
}

func run(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("Starting k3k - Version: " + buildinfo.Version)
	ctrlruntimelog.SetLogger(logger)

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create config from kubeconfig file: %v", err)
	}

	mgr, err := ctrl.NewManager(restConfig, manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create new controller runtime manager: %v", err)
	}

	logger.Info("adding cluster controller")

	portAllocator, err := agent.NewPortAllocator(ctx, mgr.GetClient())
	if err != nil {
		return err
	}

	runnable := portAllocator.InitPortAllocatorConfig(ctx, mgr.GetClient(), kubeletPortRange, webhookPortRange)
	if err := mgr.Add(runnable); err != nil {
		return err
	}

	if err := cluster.Add(ctx, mgr, &config, maxConcurrentReconciles, portAllocator, nil); err != nil {
		return fmt.Errorf("failed to add cluster controller: %v", err)
	}

	logger.Info("adding statefulset controller")

	if err := cluster.AddStatefulSetController(ctx, mgr, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add statefulset controller: %v", err)
	}

	logger.Info("adding service controller")

	if err := cluster.AddServiceController(ctx, mgr, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add service controller: %v", err)
	}

	logger.Info("adding pod controller")

	if err := cluster.AddPodController(ctx, mgr, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add pod controller: %v", err)
	}

	logger.Info("adding clusterpolicy controller")

	if err := policy.Add(mgr, config.ClusterCIDR, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add clusterpolicy controller: %v", err)
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start manager: %v", err)
	}

	logger.Info("controller manager stopped")

	return nil
}

func validate() error {
	if config.SharedAgentImagePullPolicy != "" {
		if config.SharedAgentImagePullPolicy != string(v1.PullAlways) &&
			config.SharedAgentImagePullPolicy != string(v1.PullIfNotPresent) &&
			config.SharedAgentImagePullPolicy != string(v1.PullNever) {
			return errors.New("invalid value for shared agent image policy")
		}
	}

	return nil
}
