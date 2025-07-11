//go:generate ./scripts/generate
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-logr/zapr"
	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/buildinfo"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/cluster/agent"
	"github.com/rancher/k3k/pkg/controller/policy"
	"github.com/rancher/k3k/pkg/log"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
	flags                      = []cli.Flag{
		&cli.StringFlag{
			Name:        "kubeconfig",
			EnvVars:     []string{"KUBECONFIG"},
			Usage:       "Kubeconfig path",
			Destination: &kubeconfig,
		},
		&cli.StringFlag{
			Name:        "cluster-cidr",
			EnvVars:     []string{"CLUSTER_CIDR"},
			Usage:       "Cluster CIDR to be added to the networkpolicy",
			Destination: &clusterCIDR,
		},
		&cli.StringFlag{
			Name:        "shared-agent-image",
			EnvVars:     []string{"SHARED_AGENT_IMAGE"},
			Usage:       "K3K Virtual Kubelet image",
			Value:       "rancher/k3k:latest",
			Destination: &sharedAgentImage,
		},
		&cli.StringFlag{
			Name:        "shared-agent-pull-policy",
			EnvVars:     []string{"SHARED_AGENT_PULL_POLICY"},
			Usage:       "K3K Virtual Kubelet image pull policy must be one of Always, IfNotPresent or Never",
			Destination: &sharedAgentImagePullPolicy,
		},
		&cli.StringFlag{
			Name:        "kubelet-port-range",
			EnvVars:     []string{"KUBELET_PORT_RANGE"},
			Usage:       "Port Range for k3k kubelet in shared mode",
			Destination: &kubeletPortRange,
			Value:       "50000-51000",
		},
		&cli.StringFlag{
			Name:        "webhook-port-range",
			EnvVars:     []string{"WEBHOOK_PORT_RANGE"},
			Usage:       "Port Range for k3k kubelet webhook in shared mode",
			Destination: &webhookPortRange,
			Value:       "51001-52000",
		},
		&cli.BoolFlag{
			Name:        "debug",
			EnvVars:     []string{"DEBUG"},
			Usage:       "Debug level logging",
			Destination: &debug,
		},
		&cli.StringFlag{
			Name:        "k3s-image",
			EnvVars:     []string{"K3S_IMAGE"},
			Usage:       "K3K server image",
			Value:       "rancher/k3k",
			Destination: &k3SImage,
		},
		&cli.StringFlag{
			Name:        "k3s-image-pull-policy",
			EnvVars:     []string{"K3S_IMAGE_PULL_POLICY"},
			Usage:       "K3K server image pull policy",
			Destination: &k3SImagePullPolicy,
		},
		&cli.IntFlag{
			Name:        "max-concurrent-reconciles",
			EnvVars:     []string{"MAX_CONCURRENT_RECONCILES"},
			Usage:       "maximum number of concurrent reconciles",
			Destination: &maxConcurrentReconciles,
			Value:       50,
		},
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
}

func main() {
	app := cmds.NewApp()
	app.Flags = flags
	app.Action = run
	app.Version = buildinfo.Version
	app.Before = func(clx *cli.Context) error {
		if err := validate(); err != nil {
			return err
		}

		logger = log.New(debug)

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		logger.Fatalw("failed to run k3k controller", zap.Error(err))
	}
}

func run(clx *cli.Context) error {
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
