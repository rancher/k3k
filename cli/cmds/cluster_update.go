package cmds

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
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
	labels               []string
	annotations          []string
	version              string
	kubeconfigServerHost string
	timeout              time.Duration
	noConfirm            bool
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
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("cluster %s not found in namespace %s. Please verify the cluster name and namespace are correct", name, appCtx.namespace)
			}
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

		oldClusterDetails, err := getClusterDetails(&virtualCluster)
		if err != nil {
			return fmt.Errorf("failed to get cluster details: %w", err)
		}

		logrus.Infof("Updating cluster '%s' in namespace '%s'", name, namespace)

		if config.servers > 0 {
			virtualCluster.Spec.Servers = ptr.To(int32(config.servers))
		}

		if config.agents > 0 {
			virtualCluster.Spec.Agents = ptr.To(int32(config.agents))
		}

		if config.version != "" {
			virtualCluster.Spec.Version = config.version
		}

		virtualCluster.Labels = mapCopyWithDefault(virtualCluster.Labels, parseKeyValuePairs(config.labels, "label"))
		virtualCluster.Annotations = mapCopyWithDefault(virtualCluster.Annotations, parseKeyValuePairs(config.annotations, "annotation"))

		clusterDetails, err := getClusterDetails(&virtualCluster)
		if err != nil {
			return fmt.Errorf("failed to get cluster details: %w", err)
		}

		if !config.noConfirm {
			if !confirmClusterUpdate(oldClusterDetails, clusterDetails) {
				return nil
			}
		}

		if err := client.Update(ctx, &virtualCluster); err != nil {
			return err
		}

		logrus.Info(clusterDetails)

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

func confirmClusterUpdate(oldClusterDetails, clusterDetails string) bool {
	r := bufio.NewReader(os.Stdin)

	logrus.Infof("Current %v\n\nNew %v\n\n", oldClusterDetails, clusterDetails)

	fmt.Printf("Do you want to update the cluster? [y/N]: ")

	res, err := r.ReadString('\n')
	if err != nil {
		logrus.Fatalf("unable to read string: %v", err)
	}

	fmt.Printf("\n")

	if len(res) < 2 {
		return false
	}

	return strings.ToLower(strings.TrimSpace(res))[0] == 'y'
}

func mapCopyWithDefault(dst, src map[string]string) map[string]string {
	if src == nil {
		return dst
	}

	if dst == nil {
		dst = make(map[string]string)
	}

	maps.Copy(dst, src)

	return dst
}
