//go:generate ./hack/update-codegen.sh
package main

import (
	"context"
	"flag"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/controller/cluster"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var (
	Scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

func main() {
	ctrlconfig.RegisterFlags(nil)
	flag.Parse()

	ctx := context.Background()

	kubeconfig := flag.Lookup("kubeconfig").Value.String()
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
		klog.Fatalf("Failed to add the new controller: %v", err)
	}

	if err := mgr.Start(ctx); err != nil {
		klog.Fatalf("Failed to start the manager: %v", err)
	}
}
