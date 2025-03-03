package cmds

import (
	"fmt"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/buildinfo"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

const (
	defaultNamespace = "default"
)

var (
	Scheme = runtime.NewScheme()

	debug      bool
	Kubeconfig string
	namespace  string

	CommonFlags = []cli.Flag{
		&cli.StringFlag{
			Name:        "kubeconfig",
			EnvVars:     []string{"KUBECONFIG"},
			Usage:       "kubeconfig path",
			Destination: &Kubeconfig,
			Value:       "$HOME/.kube/config",
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
		NewClusterCommand(),
		NewKubeconfigCommand(),
	}

	return app
}

func Namespace() string {
	if namespace == "" {
		return defaultNamespace
	}

	return namespace
}
