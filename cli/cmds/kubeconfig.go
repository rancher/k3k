package cmds

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/controller/certs"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
)

type GetKubeconfigConfig struct {
	name                 string
	configName           string
	kubeconfigServerHost string
}

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
		NewKubeconfigGetCmd(appCtx),
		NewKubeconfigGenerateCmd(appCtx),
	)

	return cmd
}

func NewKubeconfigGenerateCmd(appCtx *AppContext) *cobra.Command {
	cfg := &GenerateKubeconfigConfig{}

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate kubeconfig with custom client certificates",
		Long: "Generates a fresh kubeconfig with custom client certificates. " +
			"Allows customization of CN, ORG, altNames, and certificate expiration. " +
			"For most use cases, 'kubeconfig get' is simpler and recommended.",
		RunE: generate(appCtx, cfg),
		Args: cobra.NoArgs,
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

		url, err := url.Parse(appCtx.RestConfig.Host)
		if err != nil {
			return err
		}

		host := strings.Split(url.Host, ":")[0]
		if cfg.kubeconfigServerHost != "" {
			host = cfg.kubeconfigServerHost
		}

		// Build kubeconfig with custom certificates
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

		return writeKubeconfigFile(&cluster, kubeconfig, cfg.configName)
	}
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

func NewKubeconfigGetCmd(appCtx *AppContext) *cobra.Command {
	cfg := &GetKubeconfigConfig{}

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch and export kubeconfig for a cluster",
		Long: "Fetches the kubeconfig from the cluster's secret and writes it to a file. " +
			"Optionally override the server host with --kubeconfig-server for external access.",
		RunE: getKubeconfig(appCtx, cfg),
		Args: cobra.NoArgs,
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	cmd.Flags().StringVar(&cfg.name, "name", "", "cluster name")
	cmd.Flags().StringVar(&cfg.configName, "config-name", "", "the name of the generated kubeconfig file")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "override the kubeconfig server host")

	return cmd
}

func getKubeconfig(appCtx *AppContext, cfg *GetKubeconfigConfig) func(cmd *cobra.Command, args []string) error {
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

		logrus.Infof("waiting for cluster to be available..")

		var (
			kubeconfig *clientcmdapi.Config
			err        error
		)

		if retryErr := retry.OnError(controller.Backoff, apierrors.IsNotFound, func() error {
			kubeconfigSecretKey := types.NamespacedName{
				Name:      controller.SafeConcatNameWithPrefix(cluster.Name, "kubeconfig"),
				Namespace: cluster.Namespace,
			}

			var kubeconfigSecret corev1.Secret
			if err := client.Get(ctx, kubeconfigSecretKey, &kubeconfigSecret); err != nil {
				return err
			}

			kubeconfig, err = clientcmd.Load(kubeconfigSecret.Data["kubeconfig.yaml"])

			return err
		}); retryErr != nil {
			return retryErr
		}

		// Only override server URL if explicitly specified
		if cfg.kubeconfigServerHost != "" {
			origURL, err := url.Parse(kubeconfig.Clusters["default"].Server)
			if err != nil {
				return err
			}

			port := origURL.Port()
			if port == "" || port == "443" {
				// Default HTTPS port, omit from URL
				kubeconfig.Clusters["default"].Server = "https://" + cfg.kubeconfigServerHost
			} else {
				// Non-standard port, include it
				kubeconfig.Clusters["default"].Server = "https://" + cfg.kubeconfigServerHost + ":" + port
			}
		}
		// else: use the server URL from the secret as-is

		return writeKubeconfigFile(&cluster, kubeconfig, cfg.configName)
	}
}
