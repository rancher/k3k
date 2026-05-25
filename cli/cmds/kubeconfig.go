package cmds

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
)

type GenerateKubeconfigConfig struct {
	name                 string
	configName           string
	cn                   string
	org                  []string
	altNames             []string
	expirationDays       int64
	kubeconfigServerHost string
}

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

func NewKubeconfigGenerateCmd(appCtx *AppContext) *cobra.Command {
	cfg := &GenerateKubeconfigConfig{}

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate kubeconfig for clusters.",
		RunE:  generate(appCtx, cfg),
		Args:  cobra.NoArgs,
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	generateKubeconfigFlags(cmd, cfg)

	return cmd
}

func generateKubeconfigFlags(cmd *cobra.Command, cfg *GenerateKubeconfigConfig) {
	cmd.Flags().StringVar(&cfg.name, "name", "", "cluster name")
	cmd.Flags().StringVar(&cfg.configName, "config-name", "", "the name of the generated kubeconfig file")
	cmd.Flags().StringVar(&cfg.cn, "cn", controller.AdminCommonName, "Common name (CN) of the generated certificates for the kubeconfig")
	cmd.Flags().StringSliceVar(&cfg.org, "org", nil, "Organization name (ORG) of the generated certificates for the kubeconfig")
	cmd.Flags().StringSliceVar(&cfg.altNames, "altNames", nil, "altNames of the generated certificates for the kubeconfig")
	cmd.Flags().Int64Var(&cfg.expirationDays, "expiration-days", 365, "Expiration date of the certificates used for the kubeconfig")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "override the kubeconfig server host")
}

func generate(appCtx *AppContext, cfg *GenerateKubeconfigConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client

		clusterKey := types.NamespacedName{
			Name:      cfg.name,
			Namespace: appCtx.Namespace(cfg.name),
		}

		var cluster v1beta1.Cluster

		if err := client.Get(ctx, clusterKey, &cluster); err != nil {
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

		kubeCfg := kubeconfig.KubeConfig{
			CN:         cfg.cn,
			ORG:        cfg.org,
			ExpiryDate: time.Hour * 24 * time.Duration(cfg.expirationDays),
			AltNames:   certAltNames,
		}

		logrus.Infof("waiting for cluster to be available..")

		var kubeconfig *clientcmdapi.Config

		if err := retry.OnError(controller.Backoff, apierrors.IsNotFound, func() error {
			kubeconfig, err = kubeCfg.Generate(ctx, client, &cluster, host)
			return err
		}); err != nil {
			return err
		}

		if err := writeKubeconfigFile(&cluster, kubeconfig, cfg.configName); err != nil {
			return err
		}

		if cluster.Spec.Mode == v1beta1.HCPClusterMode {
			printHCPJoinInstructions(&cluster, kubeconfig)
		}

		return nil
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

func writeKubeconfigFile(cluster *v1beta1.Cluster, kubeconfig *clientcmdapi.Config, configName string) error {
	if configName == "" {
		configName = cluster.Namespace + "-" + cluster.Name + "-kubeconfig.yaml"
	}

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	logrus.Infof(`You can start using the cluster with:

	export KUBECONFIG=%s
	kubectl cluster-info
	`, filepath.Join(pwd, configName))

	kubeconfigData, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return err
	}

	return os.WriteFile(configName, kubeconfigData, 0o644)
}
