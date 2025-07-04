package cmds

import (
	"errors"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/urfave/cli/v2"
	"k8s.io/apimachinery/pkg/api/resource"
)

func newCreateFlags(config *CreateConfig) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:        "servers",
			Usage:       "number of servers",
			Destination: &config.servers,
			Value:       1,
			Action: func(ctx *cli.Context, value int) error {
				if value <= 0 {
					return errors.New("invalid number of servers")
				}
				return nil
			},
		},
		&cli.IntFlag{
			Name:        "agents",
			Usage:       "number of agents",
			Destination: &config.agents,
		},
		&cli.StringFlag{
			Name:        "token",
			Usage:       "token of the cluster",
			Destination: &config.token,
		},
		&cli.StringFlag{
			Name:        "cluster-cidr",
			Usage:       "cluster CIDR",
			Destination: &config.clusterCIDR,
		},
		&cli.StringFlag{
			Name:        "service-cidr",
			Usage:       "service CIDR",
			Destination: &config.serviceCIDR,
		},
		&cli.BoolFlag{
			Name:        "mirror-host-nodes",
			Usage:       "Mirror Host Cluster Nodes",
			Destination: &config.mirrorHostNodes,
		},
		&cli.StringFlag{
			Name:        "persistence-type",
			Usage:       "persistence mode for the nodes (dynamic, ephemeral, static)",
			Value:       string(v1alpha1.DynamicPersistenceMode),
			Destination: &config.persistenceType,
			Action: func(ctx *cli.Context, value string) error {
				switch v1alpha1.PersistenceMode(value) {
				case v1alpha1.EphemeralPersistenceMode, v1alpha1.DynamicPersistenceMode:
					return nil
				default:
					return errors.New(`persistence-type should be one of "dynamic", "ephemeral" or "static"`)
				}
			},
		},
		&cli.StringFlag{
			Name:        "storage-class-name",
			Usage:       "storage class name for dynamic persistence type",
			Destination: &config.storageClassName,
		},
		&cli.StringFlag{
			Name:        "storage-request-size",
			Usage:       "storage size for dynamic persistence type",
			Destination: &config.storageRequestSize,
			Action: func(ctx *cli.Context, value string) error {
				if _, err := resource.ParseQuantity(value); err != nil {
					return errors.New(`invalid storage size, should be a valid resource quantity e.g "10Gi"`)
				}
				return nil
			},
		},
		&cli.StringSliceFlag{
			Name:        "server-args",
			Usage:       "servers extra arguments",
			Destination: &config.serverArgs,
		},
		&cli.StringSliceFlag{
			Name:        "agent-args",
			Usage:       "agents extra arguments",
			Destination: &config.agentArgs,
		},
		&cli.StringSliceFlag{
			Name:        "server-envs",
			Usage:       "servers extra Envs",
			Destination: &config.serverEnvs,
		},
		&cli.StringSliceFlag{
			Name:        "agent-envs",
			Usage:       "agents extra Envs",
			Destination: &config.agentEnvs,
		},
		&cli.StringFlag{
			Name:        "version",
			Usage:       "k3s version",
			Destination: &config.version,
		},
		&cli.StringFlag{
			Name:        "mode",
			Usage:       "k3k mode type (shared, virtual)",
			Destination: &config.mode,
			Value:       "shared",
			Action: func(ctx *cli.Context, value string) error {
				switch value {
				case string(v1alpha1.VirtualClusterMode), string(v1alpha1.SharedClusterMode):
					return nil
				default:
					return errors.New(`mode should be one of "shared" or "virtual"`)
				}
			},
		},
		&cli.StringFlag{
			Name:        "kubeconfig-server",
			Usage:       "override the kubeconfig server host",
			Destination: &config.kubeconfigServerHost,
		},
		&cli.StringFlag{
			Name:        "policy",
			Usage:       "The policy to create the cluster in",
			Destination: &config.policy,
		},
	}
}
