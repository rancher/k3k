package cmds

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/urfave/cli/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/rancher/k3k/pkg/buildinfo"
)

type AppContext struct {
	RestConfig *rest.Config
	Client     client.Client

	// Global flags
	Debug      bool
	Kubeconfig string
	namespace  string
}

func NewApp() *cobra.Command {
	appCtx := &AppContext{}

	rootCmd := &cobra.Command{
		Use:   "k3kcli",
		Short: "CLI for K3K",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
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
			_ = apiextensionsv1.AddToScheme(scheme)

			ctrlClient, err := client.New(restConfig, client.Options{Scheme: scheme})
			if err != nil {
				return err
			}

			appCtx.RestConfig = restConfig
			appCtx.Client = ctrlClient

			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&appCtx.Kubeconfig, "kubeconfig", "", "kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)")
	rootCmd.PersistentFlags().BoolVar(&appCtx.Debug, "debug", false, "Turn on debug logs")

	rootCmd.AddCommand(
		versionCmd,
	)

	// app.Commands = []*cli.Command{
	// 	NewClusterCmd(appCtx),
	// 	NewPolicyCmd(appCtx),
	// 	NewKubeconfigCmd(appCtx),
	// }

	return rootCmd
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

func FlagNamespace(appCtx *AppContext) *cli.StringFlag {
	return &cli.StringFlag{
		Name:        "namespace",
		Usage:       "namespace of the k3k cluster",
		Aliases:     []string{"n"},
		Destination: &appCtx.namespace,
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("k3kcli version " + buildinfo.Version)
	},
}
