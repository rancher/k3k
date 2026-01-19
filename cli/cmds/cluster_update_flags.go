package cmds

import (
	"time"

	"github.com/spf13/cobra"
)

func updateFlags(cmd *cobra.Command, cfg *UpdateConfig) {
	cmd.Flags().IntVar(&cfg.servers, "servers", -1, "number of servers (-1 means no change)")
	cmd.Flags().IntVar(&cfg.agents, "agents", -1, "number of agents (-1 means no change)")
	cmd.Flags().StringArrayVar(&cfg.labels, "labels", []string{}, "Labels to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringArrayVar(&cfg.annotations, "annotations", []string{}, "Annotations to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringVar(&cfg.version, "version", "", "k3s version")
	cmd.Flags().DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h).")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "Override the kubeconfig server host")
	cmd.Flags().BoolVarP(&cfg.noConfirm, "no-confirm", "y", false, "Skip interactive approval before applying update")
}
