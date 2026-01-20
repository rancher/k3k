package cmds

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
)

type UpdateConfig struct {
	servers              int32
	agents               int32
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

func updateFlags(cmd *cobra.Command, cfg *UpdateConfig) {
	cmd.Flags().Int32Var(&cfg.servers, "servers", 0, "number of servers")
	cmd.Flags().Int32Var(&cfg.agents, "agents", 0, "number of agents")
	cmd.Flags().StringArrayVar(&cfg.labels, "labels", []string{}, "Labels to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringArrayVar(&cfg.annotations, "annotations", []string{}, "Annotations to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringVar(&cfg.version, "version", "", "k3s version")
	cmd.Flags().DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h).")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "Override the kubeconfig server host")
	cmd.Flags().BoolVarP(&cfg.noConfirm, "no-confirm", "y", false, "Skip interactive approval before applying update")
}

func updateAction(appCtx *AppContext, config *UpdateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

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
				return fmt.Errorf("cluster %s not found in namespace %s", name, appCtx.namespace)
			}

			return fmt.Errorf("failed to fetch cluster: %w", err)
		}

		var changes []change

		if cmd.Flags().Changed("version") && config.version != virtualCluster.Spec.Version {
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
			var oldServers int32
			if virtualCluster.Spec.Agents != nil {
				oldServers = *virtualCluster.Spec.Servers
			}

			if oldServers != config.servers {
				changes = append(changes, change{"Servers", fmt.Sprintf("%d", oldServers), fmt.Sprintf("%d", config.servers)})
				virtualCluster.Spec.Servers = ptr.To(config.servers)
			}
		}

		if cmd.Flags().Changed("agents") {
			var oldAgents int32
			if virtualCluster.Spec.Agents != nil {
				oldAgents = *virtualCluster.Spec.Agents
			}

			if oldAgents != config.agents {
				changes = append(changes, change{"Agents", fmt.Sprintf("%d", oldAgents), fmt.Sprintf("%d", config.agents)})
				virtualCluster.Spec.Agents = ptr.To(config.agents)
			}
		}

		var labelChanges []change

		if cmd.Flags().Changed("labels") {
			oldLabels := labels.Merge(nil, virtualCluster.Labels)
			virtualCluster.Labels = labels.Merge(virtualCluster.Labels, parseKeyValuePairs(config.labels, "label"))
			labelChanges = diffMaps(oldLabels, virtualCluster.Labels)
		}

		var annotationChanges []change

		if cmd.Flags().Changed("annotations") {
			oldAnnotations := labels.Merge(nil, virtualCluster.Annotations)
			virtualCluster.Annotations = labels.Merge(virtualCluster.Annotations, parseKeyValuePairs(config.annotations, "annotation"))
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
	clusterDetails, err := getClusterDetails(cluster)
	if err != nil {
		logrus.Fatalf("unable to get cluster details: %v", err)
	}

	fmt.Printf("\n New %s\n", clusterDetails)

	fmt.Printf("\nDo you want to update the cluster? [y/N]: ")

	scanner := bufio.NewScanner(os.Stdin)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			logrus.Errorf("Error reading input: %v", err)
		}

		return false
	}

	fmt.Printf("\n")

	return strings.ToLower(strings.TrimSpace(scanner.Text())) == "y"
}
