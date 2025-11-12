package cmds

import (
	"errors"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

func createFlags(cmd *cobra.Command, cfg *CreateConfig) {
	cmd.Flags().IntVar(&cfg.servers, "servers", 1, "number of servers")
	cmd.Flags().IntVar(&cfg.agents, "agents", 0, "number of agents")
	cmd.Flags().StringVar(&cfg.token, "token", "", "token of the cluster")
	cmd.Flags().StringVar(&cfg.clusterCIDR, "cluster-cidr", "", "cluster CIDR")
	cmd.Flags().StringVar(&cfg.serviceCIDR, "service-cidr", "", "service CIDR")
	cmd.Flags().BoolVar(&cfg.mirrorHostNodes, "mirror-host-nodes", false, "Mirror Host Cluster Nodes")
	cmd.Flags().StringVar(&cfg.persistenceType, "persistence-type", string(v1beta1.DynamicPersistenceMode), "persistence mode for the nodes (dynamic, ephemeral)")
	cmd.Flags().StringVar(&cfg.storageClassName, "storage-class-name", "", "storage class name for dynamic persistence type")
	cmd.Flags().StringVar(&cfg.storageRequestSize, "storage-request-size", "", "storage size for dynamic persistence type")
	cmd.Flags().StringSliceVar(&cfg.serverArgs, "server-args", []string{}, "servers extra arguments")
	cmd.Flags().StringSliceVar(&cfg.agentArgs, "agent-args", []string{}, "agents extra arguments")
	cmd.Flags().StringSliceVar(&cfg.serverEnvs, "server-envs", []string{}, "servers extra Envs")
	cmd.Flags().StringSliceVar(&cfg.agentEnvs, "agent-envs", []string{}, "agents extra Envs")
	cmd.Flags().StringVar(&cfg.version, "version", "", "k3s version")
	cmd.Flags().StringVar(&cfg.mode, "mode", "shared", "k3k mode type (shared, virtual)")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "override the kubeconfig server host")
	cmd.Flags().StringVar(&cfg.policy, "policy", "", "The policy to create the cluster in")
	cmd.Flags().StringVar(&cfg.customCertsPath, "custom-certs", "", "The path for custom certificate directory")
	cmd.Flags().DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h).")
}

func validateCreateConfig(cfg *CreateConfig) error {
	if cfg.servers <= 0 {
		return errors.New("invalid number of servers")
	}

	if cfg.persistenceType != "" {
		switch v1beta1.PersistenceMode(cfg.persistenceType) {
		case v1beta1.EphemeralPersistenceMode, v1beta1.DynamicPersistenceMode:
			return nil
		default:
			return errors.New(`persistence-type should be one of "dynamic" or "ephemeral"`)
		}
	}

	if _, err := resource.ParseQuantity(cfg.storageRequestSize); err != nil {
		return errors.New(`invalid storage size, should be a valid resource quantity e.g "10Gi"`)
	}

	if cfg.mode != "" {
		switch cfg.mode {
		case string(v1beta1.VirtualClusterMode), string(v1beta1.SharedClusterMode):
			return nil
		default:
			return errors.New(`mode should be one of "shared" or "virtual"`)
		}
	}

	return nil
}
