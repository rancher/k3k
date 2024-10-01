//go:generate ./hack/update-codegen.sh
package main

import (
	"context"
	"flag"
	"os"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/clusterset"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	clusterCIDRFlagName = "cluster-cidr"
	clusterCIDREnvVar   = "CLUSTER_CIDR"
	KubeconfigFlagName  = "kubeconfig"
)

var (
	Scheme      = runtime.NewScheme()
	clusterCIDR string
	kubeconfig  string
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

func main() {
	fs := addFlags()
	fs.Parse(os.Args[1:])
	ctx := context.Background()

	if clusterCIDR == "" {
		clusterCIDR = os.Getenv(clusterCIDREnvVar)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Fatalf("Failed to create config from kubeconfig file: %v", err)
	}

	mgr, err := ctrl.NewManager(restConfig, manager.Options{
		Scheme: Scheme,
	})

	if err != nil {
		klog.Fatalf("Failed to create new controller runtime manager: %v", err)
	}

	if err := cluster.Add(ctx, mgr); err != nil {
		klog.Fatalf("Failed to add the new cluster controller: %v", err)
	}

	if err := cluster.AddPodController(ctx, mgr); err != nil {
		klog.Fatalf("Failed to add the new cluster controller: %v", err)
	}
	klog.Info("adding clusterset controller")
	if err := clusterset.Add(ctx, mgr, clusterCIDR); err != nil {
		klog.Fatalf("Failed to add the clusterset controller: %v", err)
	}

	klog.Info("adding networkpolicy node controller")
	if err := clusterset.AddNodeController(ctx, mgr, clusterCIDR); err != nil {
		klog.Fatalf("Failed to add the clusterset controller: %v", err)
	}

	if err := cluster.AddPodController(ctx, mgr); err != nil {
		klog.Fatalf("Failed to add the new cluster controller: %v", err)
	}

	if err := mgr.Start(ctx); err != nil {
		klog.Fatalf("Failed to start the manager: %v", err)
	}
}

func addFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("k3k", flag.ExitOnError)
	fs.StringVar(&clusterCIDR, clusterCIDRFlagName, "", "The host's cluster CIDR")
	fs.StringVar(&kubeconfig, KubeconfigFlagName, "", "Paths to a kubeconfig. Only required if out-of-cluster.")
	return fs
}
