package cmds

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
	"github.com/rancher/k3k/pkg/controller/kubeconfig"
)

type UpdateConfig struct {
	servers              int
	agents               int
	serverArgs           []string
	agentArgs            []string
	serverEnvs           []string
	agentEnvs            []string
	labels               []string
	annotations          []string
	version              string
	kubeconfigServerHost string
	timeout              time.Duration
}

func NewClusterUpdateCmd(appCtx *AppContext) *cobra.Command {
	updateConfig := &UpdateConfig{}

	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update existing cluster",
		Example: "k3kcli cluster update [command options] NAME",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if updateConfig.servers <= 0 {
				return errors.New("invalid number of servers")
			}
			return nil
		},
		RunE: updateAction(appCtx, updateConfig),
		Args: cobra.ExactArgs(1),
	}

	CobraFlagNamespace(appCtx, cmd.Flags())
	updateFlags(cmd, updateConfig)

	return cmd
}

func updateAction(appCtx *AppContext, config *UpdateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client
		name := args[0]

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		namespace := appCtx.Namespace(name)

		var virtualCluster v1beta1.Cluster
		clusterKey := types.NamespacedName{Name: name, Namespace: appCtx.namespace}
		if err := appCtx.Client.Get(ctx, clusterKey, &virtualCluster); err != nil {
			return fmt.Errorf("failed to fetch existing cluster: %w", err)
		}
		if config.version != "" {
			currentVersion := virtualCluster.Spec.Version
			if currentVersion == "" {
				currentVersion = virtualCluster.Status.HostVersion
			}

			currentVersionSemver, err := semver.ParseTolerant(currentVersion)
			if err != nil {
				return fmt.Errorf("failed to parse current cluster version %w", err)
			}

			newVersionSemver, err := semver.ParseTolerant(config.version)
			if err != nil {
				return fmt.Errorf("failed to parse new cluster version %w", err)
			}

			if newVersionSemver.LT(currentVersionSemver) {
				return fmt.Errorf("downgrading cluster version is not supported")
			}
		}

		logrus.Infof("Updating cluster '%s' in namespace '%s'", name, namespace)

		virtualCluster.Spec.Servers = ptr.To(int32(config.servers))
		virtualCluster.Spec.Agents = ptr.To(int32(config.agents))
		virtualCluster.Spec.ServerArgs = config.serverArgs
		virtualCluster.Spec.AgentArgs = config.agentArgs
		virtualCluster.Spec.Version = config.version
		virtualCluster.Labels = parseKeyValuePairs(config.labels, "label")
		virtualCluster.Annotations = parseKeyValuePairs(config.annotations, "annotation")

		if err := client.Update(ctx, &virtualCluster); err != nil {
			return err
		}

		clusterDetails, err := printClusterDetails(&virtualCluster)
		if err != nil {
			return fmt.Errorf("failed to print cluster details: %w", err)
		}

		logrus.Info(clusterDetails)

		logrus.Infof("Waiting for cluster to be available..")

		if err := waitForClusterReady(ctx, client, &virtualCluster, config.timeout); err != nil {
			return fmt.Errorf("failed to wait for cluster to become ready (status: %s): %w", virtualCluster.Status.Phase, err)
		}

		logrus.Infof("Extracting Kubeconfig for '%s' cluster", name)

		// retry every 5s for at most 2m, or 25 times
		availableBackoff := wait.Backoff{
			Duration: 5 * time.Second,
			Cap:      2 * time.Minute,
			Steps:    25,
		}

		// add Host IP address as an extra TLS-SAN to expose the k3k cluster
		url, err := url.Parse(appCtx.RestConfig.Host)
		if err != nil {
			return err
		}

		host := strings.Split(url.Host, ":")
		if config.kubeconfigServerHost != "" {
			host = []string{config.kubeconfigServerHost}
		}

		virtualCluster.Spec.TLSSANs = []string{host[0]}

		cfg := kubeconfig.New()

		var kubeconfig *clientcmdapi.Config

		if err := retry.OnError(availableBackoff, apierrors.IsNotFound, func() error {
			kubeconfig, err = cfg.Generate(ctx, client, &virtualCluster, host[0], 0)
			return err
		}); err != nil {
			return err
		}

		return writeKubeconfigFile(&virtualCluster, kubeconfig, "")
	}
}
