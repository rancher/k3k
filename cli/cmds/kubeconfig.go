package cmds

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/util/retry"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/cli/kubeconfig"
	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	ctrlkubeconfig "github.com/rancher/k3k/pkg/controller/kubeconfig"
)

// GenerateKubeconfigConfig holds the flags of the "kubeconfig generate" command.
type GenerateKubeconfigConfig struct {
	kubeconfigOutFlags

	name                 string
	cn                   string
	org                  []string
	altNames             []string
	expirationDays       int64
	kubeconfigServerHost string
}

// NewKubeconfigCmd returns the "kubeconfig" command and its subcommands.
func NewKubeconfigCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Manage kubeconfig for clusters.",
	}

	cmd.AddCommand(
		NewKubeconfigGenerateCmd(appCtx),
	)

	return cmd
}

// NewKubeconfigGenerateCmd returns the "kubeconfig generate" command.
func NewKubeconfigGenerateCmd(appCtx *AppContext) *cobra.Command {
	cfg := &GenerateKubeconfigConfig{}

	cmd := &cobra.Command{
		Use:     "generate",
		Short:   "Generate kubeconfig for clusters.",
		Example: "k3kcli kubeconfig generate [command options] NAME",
		RunE:    generate(appCtx, cfg),
		Args:    cobra.MaximumNArgs(1),
	}

	cmd.ValidArgsFunction = completeClusterNames

	CobraFlagNamespace(appCtx, cmd, completeClusterNamespaces)

	CobraFlagKubeconfigOut(cmd, &cfg.kubeconfigOutFlags)

	generateKubeconfigFlags(cmd, cfg)

	return cmd
}

func generateKubeconfigFlags(cmd *cobra.Command, cfg *GenerateKubeconfigConfig) {
	cmd.Flags().StringVar(&cfg.name, "name", "", "cluster name")

	if err := cmd.Flags().MarkDeprecated("name", "it will be removed in a future release, use the NAME argument instead"); err != nil {
		logrus.Fatal(err)
	}

	cmd.Flags().StringVar(&cfg.configName, "config-name", "", "the name of the generated kubeconfig file")

	if err := cmd.Flags().MarkDeprecated("config-name", "use --out instead"); err != nil {
		logrus.Fatal(err)
	}

	cmd.MarkFlagsMutuallyExclusive("config-name", "out")
	cmd.MarkFlagsMutuallyExclusive("config-name", "no-out")

	cmd.Flags().StringVar(&cfg.cn, "cn", controller.AdminCommonName, "Common name (CN) of the generated certificates for the kubeconfig")
	cmd.Flags().StringSliceVar(&cfg.org, "org", nil, "Organization name (ORG) of the generated certificates for the kubeconfig")
	cmd.Flags().StringSliceVar(&cfg.altNames, "altNames", nil, "altNames of the generated certificates for the kubeconfig")
	cmd.Flags().Int64Var(&cfg.expirationDays, "expiration-days", 365, "Expiration date of the certificates used for the kubeconfig")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "override the kubeconfig server host")
}

func generate(appCtx *AppContext, cfg *GenerateKubeconfigConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client := appCtx.Client

		name, namespace := cfg.name, appCtx.Namespace(cfg.name)

		switch {
		case len(args) == 1:
			if cfg.name != "" {
				logrus.Warnf("the --name flag is deprecated and will be removed in a future release, ignoring it in favor of the '%s' argument", args[0])
			}

			var err error
			if namespace, name, err = resolveClusterArg(appCtx, args[0]); err != nil {
				return err
			}

		case name != "":
			logrus.Warn("the --name flag is deprecated and will be removed in a future release, use the NAME argument instead")

		default:
			return errors.New("expected exactly one cluster name")
		}

		clusterKey := types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}

		var cluster v1beta1.Cluster

		if err := client.Get(ctx, clusterKey, &cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("cluster %q not found in namespace %q", name, namespace)
			}

			return err
		}

		host, err := resolveServerHost(appCtx.RestConfig.Host, cfg.kubeconfigServerHost)
		if err != nil {
			return err
		}

		if cfg.kubeconfigServerHost != "" {
			cfg.altNames = append(cfg.altNames, cfg.kubeconfigServerHost)
		}

		certAltNames := certs.AddSANs(cfg.altNames)

		if len(cfg.org) == 0 {
			cfg.org = []string{user.SystemPrivilegedGroup}
		}

		kubeCfg := ctrlkubeconfig.KubeConfig{
			CN:         cfg.cn,
			ORG:        cfg.org,
			ExpiryDate: time.Hour * 24 * time.Duration(cfg.expirationDays),
			AltNames:   certAltNames,
		}

		logrus.Infof("waiting for cluster to be available..")

		var kubeConfig *clientcmdapi.Config

		if err := retry.OnError(controller.Backoff, apierrors.IsNotFound, func() error {
			kubeConfig, err = kubeCfg.Generate(ctx, client, &cluster, host)
			return err
		}); err != nil {
			return err
		}

		serverURL := kubeconfig.ServerURL(kubeConfig)

		if err := writeKubeconfig(appCtx, &cluster, kubeConfig, cfg.standalonePath(&cluster)); err != nil {
			return err
		}

		if cluster.Spec.Mode == v1beta1.HCPClusterMode {
			printHCPJoinInstructions(&cluster, serverURL)
		}

		return nil
	}
}

// writeKubeconfig writes the kubeconfig of the cluster to the standalone file at path, if
// any, and always adds it as a context to the kubeconfig of the user.
func writeKubeconfig(appCtx *AppContext, cluster *v1beta1.Cluster, config *clientcmdapi.Config, path string) error {
	if path != "" {
		absPath, err := kubeconfig.Write(config, path)
		if err != nil {
			return err
		}

		logrus.Infof("Wrote a standalone kubeconfig to '%s'", absPath)
	}

	return addKubeconfigContext(appCtx, cluster, config)
}

// addKubeconfigContext adds the cluster to the kubeconfig of the user as a "<namespace>/<name>" context,
// leaving the rest of the file, and the current context, untouched.
func addKubeconfigContext(appCtx *AppContext, cluster *v1beta1.Cluster, config *clientcmdapi.Config) error {
	file := kubeconfig.New(appCtx.Kubeconfig)
	name := kubeconfig.ContextName(cluster.Namespace, cluster.Name)

	if err := file.Add(name, config); err != nil {
		return err
	}

	logrus.Infof(`Added the '%s' context in the current kubeconfig (%s)

You can start using the cluster with:

	kubectl config use-context %s
	kubectl cluster-info

`, name, file.Path(), name)

	return nil
}

// removeKubeconfigContexts removes the contexts of the deleted clusters from the kubeconfig of the user.
func removeKubeconfigContexts(appCtx *AppContext, clusters ...v1beta1.Cluster) {
	file := kubeconfig.New(appCtx.Kubeconfig)

	names := make([]string, 0, len(clusters))
	for i := range clusters {
		names = append(names, kubeconfig.ContextName(clusters[i].Namespace, clusters[i].Name))
	}

	removed, err := file.Remove(names...)
	if err != nil {
		logrus.Warnf("Failed to remove the contexts from the current kubeconfig (%s): %v", file.Path(), err)
		return
	}

	for _, name := range removed {
		logrus.Infof("Removed the '%s' context from the current kubeconfig (%s)", name, file.Path())
	}
}

// resolveServerHost returns the host that should be embedded in the kubeconfig
// server URL and used as the TLS-SAN. If override is set it takes precedence;
// otherwise the host is extracted from restConfigHost.
func resolveServerHost(restConfigHost, override string) (string, error) {
	if override != "" {
		return override, nil
	}

	u, err := url.Parse(restConfigHost)
	if err != nil {
		return "", err
	}

	return u.Hostname(), nil
}
