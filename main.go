//go:generate ./hack/update-codegen.sh
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rancher/k3k/cli/cmds"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/clusterset"
	"github.com/urfave/cli"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	program   = "k3k"
	version   = "dev"
	gitCommit = "HEAD"
)

var (
	scheme      = runtime.NewScheme()
	clusterCIDR string
	kubeconfig  string
	flags       = []cli.Flag{
		cli.StringFlag{
			Name:        "kubeconfig",
			EnvVar:      "KUBECONFIG",
			Usage:       "Kubeconfig path",
			Destination: &kubeconfig,
		},
		cli.StringFlag{
			Name:        "cluster-cidr",
			EnvVar:      "CLUSTER_CIDR",
			Usage:       "Cluster CIDR to be added to the networkpolicy of the clustersets",
			Destination: &clusterCIDR,
		}}
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
}

func main() {
	app := cmds.NewApp()
	app.Flags = flags
	app.Action = run
	app.Version = version + " (" + gitCommit + ")"

	if err := app.Run(os.Args); err != nil {
		klog.Fatal(err)
	}

}

func run(clx *cli.Context) error {
	ctx := context.Background()

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("Failed to create config from kubeconfig file: %v", err)
	}

	mgr, err := ctrl.NewManager(restConfig, manager.Options{
		Scheme: scheme,
	})

	if err != nil {
		return fmt.Errorf("Failed to create new controller runtime manager: %v", err)
	}

	if err := cluster.Add(ctx, mgr); err != nil {
		return fmt.Errorf("Failed to add the new cluster controller: %v", err)
	}

	if err := cluster.AddPodController(ctx, mgr); err != nil {
		return fmt.Errorf("Failed to add the new cluster controller: %v", err)
	}
	klog.Info("adding clusterset controller")
	if err := clusterset.Add(ctx, mgr, clusterCIDR); err != nil {
		return fmt.Errorf("Failed to add the clusterset controller: %v", err)
	}

	if clusterCIDR == "" {
		klog.Info("adding networkpolicy node controller")
		if err := clusterset.AddNodeController(ctx, mgr); err != nil {
			return fmt.Errorf("Failed to add the clusterset node controller: %v", err)
		}
	}

	if err := cluster.AddPodController(ctx, mgr); err != nil {
		return fmt.Errorf("Failed to add the new cluster controller: %v", err)
	}

	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("Failed to start the manager: %v", err)
	}

	return nil
}
