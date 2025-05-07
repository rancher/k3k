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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AppContext struct {
	RestConfig *rest.Config
	Client     client.Client

	// Global flags
	Debug      bool
	Kubeconfig string
	namespace  string
}

func NewApp() *cli.App {
	appCtx := &AppContext{}

	app := cli.NewApp()
	app.Name = "k3kcli"
	app.Usage = "CLI for K3K"
	app.Flags = WithCommonFlags(appCtx)

	app.Before = func(clx *cli.Context) error {
		if appCtx.Debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		restConfig, err := loadRESTConfig(appCtx.Kubeconfig)
		if err != nil {
			return err
		}

		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		ctrlClient, err := client.New(restConfig, client.Options{Scheme: scheme})
		if err != nil {
			return err
		}

		appCtx.RestConfig = restConfig
		appCtx.Client = ctrlClient

		return nil
	}

	app.Version = buildinfo.Version
	cli.VersionPrinter = func(cCtx *cli.Context) {
		fmt.Println("k3kcli Version: " + buildinfo.Version)
	}

	app.Commands = []*cli.Command{
		NewClusterCmd(appCtx),
		NewPolicyCmd(appCtx),
		NewKubeconfigCmd(appCtx),
	}

	return app
}

func (ctx *AppContext) Namespace(name string) string {
	if ctx.namespace != "" {
		return ctx.namespace
	}

	return "k3k-" + name
}

func loadRESTConfig(kubeconfig string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	return kubeConfig.ClientConfig()
}

func WithCommonFlags(appCtx *AppContext, flags ...cli.Flag) []cli.Flag {
	commonFlags := []cli.Flag{
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "Turn on debug logs",
			Destination: &appCtx.Debug,
			EnvVars:     []string{"K3K_DEBUG"},
		},
		&cli.StringFlag{
			Name:        "kubeconfig",
			Usage:       "kubeconfig path",
			Destination: &appCtx.Kubeconfig,
			DefaultText: "$HOME/.kube/config or $KUBECONFIG if set",
		},
		&cli.StringFlag{
			Name:        "namespace",
			Usage:       "namespace to create the k3k cluster in",
			Destination: &appCtx.namespace,
		},
	}

	return append(commonFlags, flags...)
}
