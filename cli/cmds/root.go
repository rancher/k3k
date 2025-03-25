package cmds

import (
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/buildinfo"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	Scheme = runtime.NewScheme()

	debug      bool
	Kubeconfig string
	namespace  string

	CommonFlags = []cli.Flag{
		&cli.StringFlag{
			Name:        "kubeconfig",
			Usage:       "kubeconfig path",
			Destination: &Kubeconfig,
			DefaultText: "$HOME/.kube/config or $KUBECONFIG if set",
		},
		&cli.StringFlag{
			Name:        "namespace",
			Usage:       "namespace to create the k3k cluster in",
			Destination: &namespace,
		},
	}
)

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = v1alpha1.AddToScheme(Scheme)
}

func NewApp() *cli.App {
	app := cli.NewApp()
	app.Name = "k3kcli"
	app.Usage = "CLI for K3K"
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "Turn on debug logs",
			Destination: &debug,
			EnvVars:     []string{"K3K_DEBUG"},
		},
	}

	app.Before = func(clx *cli.Context) error {
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		return nil
	}

	app.Version = buildinfo.Version
	cli.VersionPrinter = func(cCtx *cli.Context) {
		fmt.Println("k3kcli Version: " + buildinfo.Version)
	}

	app.Commands = []*cli.Command{
		NewClusterCmd(),
		NewKubeconfigCmd(),
	}

	return app
}

func Namespace(clusterName string) string {
	if namespace != "" {
		return namespace
	}

	return "k3k-" + clusterName
}

func loadRESTConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	if Kubeconfig != "" {
		loadingRules.ExplicitPath = Kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	return kubeConfig.ClientConfig()
}
