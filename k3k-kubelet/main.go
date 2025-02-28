package main

import (
	"context"
	"os"

	"github.com/go-logr/zapr"
	"github.com/rancher/k3k/pkg/log"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	configFile string
	cfg        config
	logger     *log.Logger
	debug      bool
)

func main() {
	app := cli.NewApp()
	app.Name = "k3k-kubelet"
	app.Usage = "virtual kubelet implementation k3k"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:        "cluster-name",
			Usage:       "Name of the k3k cluster",
			Destination: &cfg.ClusterName,
			EnvVars:     []string{"CLUSTER_NAME"},
		},
		&cli.StringFlag{
			Name:        "cluster-namespace",
			Usage:       "Namespace of the k3k cluster",
			Destination: &cfg.ClusterNamespace,
			EnvVars:     []string{"CLUSTER_NAMESPACE"},
		},
		&cli.StringFlag{
			Name:        "cluster-token",
			Usage:       "K3S token of the k3k cluster",
			Destination: &cfg.Token,
			EnvVars:     []string{"CLUSTER_TOKEN"},
		},
		&cli.StringFlag{
			Name:        "host-config-path",
			Usage:       "Path to the host kubeconfig, if empty then virtual-kubelet will use incluster config",
			Destination: &cfg.HostConfigPath,
			EnvVars:     []string{"HOST_KUBECONFIG"},
		},
		&cli.StringFlag{
			Name:        "virtual-config-path",
			Usage:       "Path to the k3k cluster kubeconfig, if empty then virtual-kubelet will create its own config from k3k cluster",
			Destination: &cfg.VirtualConfigPath,
			EnvVars:     []string{"CLUSTER_NAME"},
		},
		&cli.StringFlag{
			Name:        "kubelet-port",
			Usage:       "kubelet API port number",
			Destination: &cfg.KubeletPort,
			EnvVars:     []string{"SERVER_PORT"},
			Value:       "10250",
		},
		&cli.StringFlag{
			Name:        "service-name",
			Usage:       "The service name deployed by the k3k controller",
			Destination: &cfg.ServiceName,
			EnvVars:     []string{"SERVICE_NAME"},
		},
		&cli.StringFlag{
			Name:        "agent-hostname",
			Usage:       "Agent Hostname used for TLS SAN for the kubelet server",
			Destination: &cfg.AgentHostname,
			EnvVars:     []string{"AGENT_HOSTNAME"},
		},
		&cli.StringFlag{
			Name:        "server-ip",
			Usage:       "Server IP used for registering the virtual kubelet to the cluster",
			Destination: &cfg.ServerIP,
			EnvVars:     []string{"SERVER_IP"},
		},
		&cli.StringFlag{
			Name:        "version",
			Usage:       "Version of kubernetes server",
			Destination: &cfg.Version,
			EnvVars:     []string{"VERSION"},
		},
		&cli.StringFlag{
			Name:        "config",
			Usage:       "Path to k3k-kubelet config file",
			Destination: &configFile,
			EnvVars:     []string{"CONFIG_FILE"},
			Value:       "/etc/rancher/k3k/config.yaml",
		},
		&cli.BoolFlag{
			Name:        "debug",
			Usage:       "Enable debug logging",
			Destination: &debug,
			EnvVars:     []string{"DEBUG"},
		},
	}
	app.Before = func(clx *cli.Context) error {
		logger = log.New(debug)
		ctrlruntimelog.SetLogger(zapr.NewLogger(logger.Desugar().WithOptions(zap.AddCallerSkip(1))))

		return nil
	}
	app.Action = run

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}

func run(clx *cli.Context) error {
	ctx := context.Background()

	if err := cfg.parse(configFile); err != nil {
		logger.Fatalw("failed to parse config file", "path", configFile, zap.Error(err))
	}

	if err := cfg.validate(); err != nil {
		logger.Fatalw("failed to validate config", zap.Error(err))
	}

	k, err := newKubelet(ctx, &cfg, logger)
	if err != nil {
		logger.Fatalw("failed to create new virtual kubelet instance", zap.Error(err))
	}

	if err := k.registerNode(ctx, k.agentIP, cfg.KubeletPort, cfg.ClusterNamespace, cfg.ClusterName, cfg.AgentHostname, cfg.ServerIP, k.dnsIP, cfg.Version); err != nil {
		logger.Fatalw("failed to register new node", zap.Error(err))
	}

	k.start(ctx)

	return nil
}
