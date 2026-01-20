package cmds

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
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
		RunE:    updateAction(appCtx, updateConfig),
		Args:    cobra.ExactArgs(1),
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

		var changes []change

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

			changes = append(changes, change{"Version", currentVersion, config.version})
			virtualCluster.Spec.Version = config.version
		}

		if cmd.Flags().Changed("servers") {
			oldServers := int32(0)
			if virtualCluster.Spec.Servers != nil {
				oldServers = *virtualCluster.Spec.Servers
			}

			changes = append(changes, change{"Servers", fmt.Sprintf("%d", oldServers), fmt.Sprintf("%d", config.servers)})
			virtualCluster.Spec.Servers = ptr.To(int32(config.servers))
		}

		if cmd.Flags().Changed("agents") {
			oldAgents := int32(0)
			if virtualCluster.Spec.Agents != nil {
				oldAgents = *virtualCluster.Spec.Agents
			}

			changes = append(changes, change{"Agents", fmt.Sprintf("%d", oldAgents), fmt.Sprintf("%d", config.agents)})
			virtualCluster.Spec.Agents = ptr.To(int32(config.agents))
		}

		var labelChanges []change

		if cmd.Flags().Changed("labels") {
			oldLabels := mapCopyWithDefault(nil, virtualCluster.Labels)
			virtualCluster.Labels = mapCopyWithDefault(virtualCluster.Labels, parseKeyValuePairs(config.labels, "label"))
			labelChanges = diffMaps(oldLabels, virtualCluster.Labels)
		}

		var annotationChanges []change

		if cmd.Flags().Changed("annotations") {
			oldAnnotations := mapCopyWithDefault(nil, virtualCluster.Annotations)
			virtualCluster.Annotations = mapCopyWithDefault(virtualCluster.Annotations, parseKeyValuePairs(config.annotations, "annotation"))
			annotationChanges = diffMaps(oldAnnotations, virtualCluster.Annotations)
		}

		if len(changes) == 0 && len(labelChanges) == 0 && len(annotationChanges) == 0 {
			logrus.Info("No changes detected, skipping update")
			return nil
		}

		logrus.Infof("Updating cluster '%s' in namespace '%s'", name, namespace)

		printDiff(changes)
		printMapDiff("Labels", labelChanges)
		printMapDiff("Annotations", annotationChanges)

		if !config.noConfirm {
			if !confirmClusterUpdate(&virtualCluster) {
				return nil
			}
		}

		if err := client.Update(ctx, &virtualCluster); err != nil {
			return err
		}

		logrus.Info("Cluster updated successfully")

		return nil
	}
}

func confirmClusterUpdate(cluster *v1beta1.Cluster) bool {
	r := bufio.NewReader(os.Stdin)

	clusterDetails, err := getClusterDetails(cluster)
	if err != nil {
		logrus.Fatalf("unable to get cluster details: %v", err)
	}

	fmt.Printf("\n New %s\n", clusterDetails)

	fmt.Printf("\nDo you want to update the cluster? [y/N]: ")

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
