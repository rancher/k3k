package cmds

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
	k3kcluster "github.com/rancher/k3k/pkg/controller/cluster"
)

// UpdateConfig holds the flags of the "cluster update" command.
type UpdateConfig struct {
	servers     int32
	agents      int32
	labels      []string
	annotations []string
	version     string
	noConfirm   bool
}

// NewClusterUpdateCmd returns the "cluster update" command.
func NewClusterUpdateCmd(appCtx *AppContext) *cobra.Command {
	updateConfig := &UpdateConfig{}

	cmd := &cobra.Command{
		Use:     "update",
		Short:   "Update existing cluster",
		Example: "k3kcli cluster update [command options] NAME",
		RunE:    updateAction(appCtx, updateConfig),
		Args:    cobra.ExactArgs(1),
	}

	cmd.ValidArgsFunction = completeClusterNames

	CobraFlagNamespace(appCtx, cmd, completeClusterNamespaces)

	updateFlags(cmd, updateConfig)

	return cmd
}

func updateFlags(cmd *cobra.Command, cfg *UpdateConfig) {
	cmd.Flags().Int32Var(&cfg.servers, "servers", 1, "number of servers")
	cmd.Flags().Int32Var(&cfg.agents, "agents", 0, "number of agents")
	cmd.Flags().StringArrayVar(&cfg.labels, "labels", []string{}, "Labels to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringArrayVar(&cfg.annotations, "annotations", []string{}, "Annotations to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringVar(&cfg.version, "version", "", "k3s version")
	cmd.Flags().BoolVarP(&cfg.noConfirm, "no-confirm", "y", false, "Skip interactive approval before applying update")
}

func updateAction(appCtx *AppContext, config *UpdateConfig) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		client := appCtx.Client

		namespace, name, err := resolveClusterArg(appCtx, args[0])
		if err != nil {
			return err
		}

		if name == k3kcluster.ClusterInvalidName {
			return errors.New("invalid cluster name")
		}

		var virtualCluster v1beta1.Cluster

		clusterKey := types.NamespacedName{Name: name, Namespace: namespace}
		if err := client.Get(ctx, clusterKey, &virtualCluster); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("cluster %s not found in namespace %s", name, namespace)
			}

			return fmt.Errorf("failed to fetch cluster: %w", err)
		}

		var changes []change

		if cmd.Flags().Changed("version") && config.version != virtualCluster.Spec.Version {
			currentVersion := virtualCluster.Spec.Version
			if currentVersion == "" {
				currentVersion = virtualCluster.Status.HostVersion
			}

			downgrade, err := isDowngrade(currentVersion, config.version)
			if err != nil {
				return err
			}

			if downgrade {
				return errors.New("downgrading cluster version is not supported")
			}

			changes = append(changes, change{"Version", currentVersion, config.version})
			virtualCluster.Spec.Version = config.version
		}

		if cmd.Flags().Changed("servers") {
			var oldServers int32
			if virtualCluster.Spec.Servers != nil {
				oldServers = *virtualCluster.Spec.Servers
			}

			if oldServers != config.servers {
				changes = append(changes, change{"Servers", fmt.Sprintf("%d", oldServers), fmt.Sprintf("%d", config.servers)})
				virtualCluster.Spec.Servers = new(config.servers)
			}
		}

		if cmd.Flags().Changed("agents") {
			var oldAgents int32
			if virtualCluster.Spec.Agents != nil {
				oldAgents = *virtualCluster.Spec.Agents
			}

			if oldAgents != config.agents {
				changes = append(changes, change{"Agents", fmt.Sprintf("%d", oldAgents), fmt.Sprintf("%d", config.agents)})
				virtualCluster.Spec.Agents = new(config.agents)
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

	fmt.Printf("\nNew %s\n", clusterDetails)

	fmt.Print("\nDo you want to update the cluster? [y/N]: ")

	scanner := bufio.NewScanner(os.Stdin)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			logrus.Errorf("Error reading input: %v", err)
		}

		return false
	}

	fmt.Println()

	return strings.ToLower(strings.TrimSpace(scanner.Text())) == "y"
}

var (
	distSuffixRegex = regexp.MustCompile(`^(.*?)[-+](k3s\d+|rke2r\d+)$`)
	suffixNumRegex  = regexp.MustCompile(`^(.*?)(\d+)$`)
)

func parseClusterVersion(v string) (semver.Version, string, error) {
	var suffix string
	baseStr := v
	if match := distSuffixRegex.FindStringSubmatch(v); len(match) == 3 {
		baseStr = match[1]
		suffix = match[2]
	}

	sv, err := semver.ParseTolerant(baseStr)
	if err != nil {
		return semver.Version{}, "", err
	}

	return sv, suffix, nil
}

func compareSuffix(s1, s2 string) int {
	if s1 == s2 {
		return 0
	}

	m1 := suffixNumRegex.FindStringSubmatch(s1)
	m2 := suffixNumRegex.FindStringSubmatch(s2)
	if len(m1) == 3 && len(m2) == 3 && m1[1] == m2[1] {
		n1, err1 := strconv.ParseUint(m1[2], 10, 64)
		n2, err2 := strconv.ParseUint(m2[2], 10, 64)
		if err1 == nil && err2 == nil {
			if n1 < n2 {
				return -1
			}
			if n1 > n2 {
				return 1
			}
			return 0
		}
	}

	return strings.Compare(s1, s2)
}

func isDowngrade(currentVersion, newVersion string) (bool, error) {
	currentBase, currentSuffix, err := parseClusterVersion(currentVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse current cluster version %w", err)
	}

	newBase, newSuffix, err := parseClusterVersion(newVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse new cluster version %w", err)
	}

	cmp := newBase.Compare(currentBase)
	if cmp < 0 {
		return true, nil
	}
	if cmp > 0 {
		return false, nil
	}

	// Base semver versions are equal; compare distribution suffixes if both are present.
	if currentSuffix != "" && newSuffix != "" {
		if compareSuffix(newSuffix, currentSuffix) < 0 {
			return true, nil
		}
	}

	return false, nil
}
