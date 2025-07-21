package cmds

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
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
		Use:     "k3kcli",
		Short:   "CLI for K3K",
		Version: buildinfo.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			InitializeConfig(cmd)

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
		NewClusterCmd(appCtx),
		NewPolicyCmd(appCtx),
		NewKubeconfigCmd(appCtx),
	)

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

func CobraFlagNamespace(appCtx *AppContext, flag *pflag.FlagSet) {
	flag.StringVarP(&appCtx.namespace, "namespace", "n", "", "namespace of the k3k cluster")
}

func FlagNamespace(appCtx *AppContext) *cli.StringFlag {
	return &cli.StringFlag{
		Name:        "namespace",
		Usage:       "namespace of the k3k cluster",
		Aliases:     []string{"n"},
		Destination: &appCtx.namespace,
	}
}

func InitializeConfig(cmd *cobra.Command) {
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	// Bind the current command's flags to viper
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		// Apply the viper config value to the flag when the flag is not set and viper has a value
		if !f.Changed && viper.IsSet(f.Name) {
			val := viper.Get(f.Name)
			_ = cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
		}
	})
}
