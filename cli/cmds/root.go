// Package cmds implements the k3kcli command tree for creating and managing
// virtual clusters and the policies that constrain them.
package cmds

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/buildinfo"
)

const (
	defaultRequestTimeout    = 30 * time.Second       // normal commands
	completionRequestTimeout = 100 * time.Millisecond // shell completion, must be fast
)

// AppContext carries the Kubernetes clients and the global flags shared by every command.
type AppContext struct {
	RestConfig *rest.Config
	Client     client.Client

	// Global flags
	Debug      bool
	Kubeconfig string
	namespace  string
}

// NewRootCmd returns the root k3kcli command with all its subcommands attached.
func NewRootCmd() *cobra.Command {
	appCtx := &AppContext{}

	rootCmd := &cobra.Command{
		SilenceUsage: true,
		Use:          "k3kcli",
		Short:        "CLI for K3K.",
		Version:      buildinfo.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			InitializeConfig(cmd)

			ctrl.SetLogger(logr.Discard())

			if appCtx.Debug {
				logrus.SetLevel(logrus.DebugLevel)
			}

			restConfig, err := loadRESTConfig(appCtx.Kubeconfig, defaultRequestTimeout)
			if err != nil {
				return err
			}

			ctrlClient, err := buildClient(restConfig)
			if err != nil {
				return err
			}

			appCtx.RestConfig = restConfig
			appCtx.Client = ctrlClient

			return nil
		},
		DisableAutoGenTag: true,
	}

	rootCmd.PersistentFlags().StringVar(&appCtx.Kubeconfig, "kubeconfig", "", "kubeconfig path ($HOME/.kube/config or $KUBECONFIG if set)")
	rootCmd.PersistentFlags().BoolVar(&appCtx.Debug, "debug", false, "Turn on debug logs")

	if err := rootCmd.MarkPersistentFlagFilename("kubeconfig"); err != nil {
		logrus.Fatal(err)
	}

	rootCmd.AddCommand(
		NewClusterCmd(appCtx),
		NewPolicyCmd(appCtx),
		NewKubeconfigCmd(appCtx),
	)

	disableFileCompletion(rootCmd)

	return rootCmd
}

// Namespace returns the namespace given with -n, or the default namespace of the cluster
// with the given name.
func (ctx *AppContext) Namespace(name string) string {
	if ctx.namespace != "" {
		return ctx.namespace
	}

	return "k3k-" + name
}

// CobraFlagNamespace adds the shared -n/--namespace flag to a command.
func CobraFlagNamespace(appCtx *AppContext, cmd *cobra.Command, completeFn cobra.CompletionFunc) {
	cmd.Flags().StringVarP(&appCtx.namespace, "namespace", "n", "", "namespace of the k3k cluster")

	if completeFn != nil {
		mustRegisterFlagCompletion(cmd, "namespace", completeFn)
	}
}

// InitializeConfig binds a command's flags to viper, so they can also be set through
// K3K_ prefixed environment variables.
func InitializeConfig(cmd *cobra.Command) {
	viper.SetEnvPrefix("K3K")
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

func loadRESTConfig(kubeconfig string, timeout time.Duration) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	restConfig.Timeout = timeout

	return restConfig, nil
}

func buildClient(restConfig *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()

	schemeBuilder := runtime.NewSchemeBuilder(
		clientgoscheme.AddToScheme,
		v1beta1.AddToScheme,
		apiextensionsv1.AddToScheme,
	)

	if err := schemeBuilder.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return client.New(restConfig, client.Options{Scheme: scheme})
}
