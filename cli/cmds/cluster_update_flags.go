package cmds

import (
	"time"

	"github.com/spf13/cobra"
)

func updateFlags(cmd *cobra.Command, cfg *UpdateConfig) {
	cmd.Flags().IntVar(&cfg.servers, "servers", 1, "number of servers")
	cmd.Flags().IntVar(&cfg.agents, "agents", 0, "number of agents")
	cmd.Flags().StringSliceVar(&cfg.serverArgs, "server-args", []string{}, "servers extra arguments")
	cmd.Flags().StringSliceVar(&cfg.agentArgs, "agent-args", []string{}, "agents extra arguments")
	cmd.Flags().StringSliceVar(&cfg.serverEnvs, "server-envs", []string{}, "servers extra Envs")
	cmd.Flags().StringSliceVar(&cfg.agentEnvs, "agent-envs", []string{}, "agents extra Envs")
	cmd.Flags().StringArrayVar(&cfg.labels, "labels", []string{}, "Labels to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringArrayVar(&cfg.annotations, "annotations", []string{}, "Annotations to add to the cluster object (e.g. key=value)")
	cmd.Flags().StringVar(&cfg.version, "version", "", "k3s version")
	cmd.Flags().DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "The timeout for waiting for the cluster to become ready (e.g., 10s, 5m, 1h).")
	cmd.Flags().StringVar(&cfg.kubeconfigServerHost, "kubeconfig-server", "", "override the kubeconfig server host")
}
