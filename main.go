//go:generate ./scripts/generate
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
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
	scheme                     = runtime.NewScheme()
	clusterCIDR                string
	sharedAgentImage           string
	sharedAgentImagePullPolicy string
	kubeconfig                 string
	k3SImage                   string
	k3SImagePullPolicy         string
	kubeletPortRange           string
	webhookPortRange           string
	maxConcurrentReconciles    int
	debug                      bool
	logger                     *log.Logger
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
			logger = log.New(debug)
		},
		RunE: run,
	}

	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "Debug level logging")
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path")
	rootCmd.PersistentFlags().StringVar(&clusterCIDR, "cluster-cidr", "", "Cluster CIDR to be added to the networkpolicy")
	rootCmd.PersistentFlags().StringVar(&sharedAgentImage, "shared-agent-image", "", "K3K Virtual Kubelet image")
	rootCmd.PersistentFlags().StringVar(&sharedAgentImagePullPolicy, "shared-agent-pull-policy", "", "K3K Virtual Kubelet image pull policy must be one of Always, IfNotPresent or Never")
	rootCmd.PersistentFlags().StringVar(&kubeletPortRange, "kubelet-port-range", "50000-51000", "Port Range for k3k kubelet in shared mode")
	rootCmd.PersistentFlags().StringVar(&webhookPortRange, "webhook-port-range", "51001-52000", "Port Range for k3k kubelet webhook in shared mode")
	rootCmd.PersistentFlags().StringVar(&k3SImage, "k3s-image", "rancher/k3k", "K3K server image")
	rootCmd.PersistentFlags().StringVar(&k3SImagePullPolicy, "k3s-image-pull-policy", "", "K3K server image pull policy")
	rootCmd.PersistentFlags().IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 50, "maximum number of concurrent reconciles")

	if err := rootCmd.Execute(); err != nil {
		logger.Fatalw("failed to run k3k controller", zap.Error(err))
	}
}

func run(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	logger.Info("Starting k3k - Version: " + buildinfo.Version)

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

	ctrlruntimelog.SetLogger(zapr.NewLogger(logger.Desugar().WithOptions(zap.AddCallerSkip(1))))

	logger.Info("adding cluster controller")

	portAllocator, err := agent.NewPortAllocator(ctx, mgr.GetClient())
	if err != nil {
		return err
	}

	runnable := portAllocator.InitPortAllocatorConfig(ctx, mgr.GetClient(), kubeletPortRange, webhookPortRange)
	if err := mgr.Add(runnable); err != nil {
		return err
	}

	if err := cluster.Add(ctx, mgr, sharedAgentImage, sharedAgentImagePullPolicy, k3SImage, k3SImagePullPolicy, maxConcurrentReconciles, portAllocator, nil); err != nil {
		return fmt.Errorf("failed to add the new cluster controller: %v", err)
	}

	logger.Info("adding etcd pod controller")

	if err := cluster.AddPodController(ctx, mgr, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add the new cluster controller: %v", err)
	}

	logger.Info("adding clusterpolicy controller")

	if err := policy.Add(mgr, clusterCIDR, maxConcurrentReconciles); err != nil {
		return fmt.Errorf("failed to add the clusterpolicy controller: %v", err)
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start the manager: %v", err)
	}

	return nil
}

func validate() error {
	if sharedAgentImagePullPolicy != "" {
		if sharedAgentImagePullPolicy != string(v1.PullAlways) &&
			sharedAgentImagePullPolicy != string(v1.PullIfNotPresent) &&
			sharedAgentImagePullPolicy != string(v1.PullNever) {
			return errors.New("invalid value for shared agent image policy")
		}
	}

	return nil
}
