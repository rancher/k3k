//go:generate ./hack/update-codegen.sh
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
	"github.com/rancher/k3k/pkg/controller/clusterset"
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
			Usage:       "Cluster CIDR to be added to the networkpolicy of the clustersets",
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
		&cli.BoolFlag{
			Name:        "debug",
			EnvVars:     []string{"DEBUG"},
			Usage:       "Debug level logging",
			Destination: &debug,
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
	if err := cluster.Add(ctx, mgr, sharedAgentImage, sharedAgentImagePullPolicy); err != nil {
		return fmt.Errorf("failed to add the new cluster controller: %v", err)
	}

	logger.Info("adding etcd pod controller")
	if err := cluster.AddPodController(ctx, mgr); err != nil {
		return fmt.Errorf("failed to add the new cluster controller: %v", err)
	}

	logger.Info("adding clusterset controller")
	if err := clusterset.Add(ctx, mgr, clusterCIDR); err != nil {
		return fmt.Errorf("failed to add the clusterset controller: %v", err)
	}

	if clusterCIDR == "" {
		logger.Info("adding networkpolicy node controller")
		if err := clusterset.AddNodeController(ctx, mgr); err != nil {
			return fmt.Errorf("failed to add the clusterset node controller: %v", err)
		}
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
