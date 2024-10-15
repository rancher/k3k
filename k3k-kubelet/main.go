package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rancher/k3k/k3k-kubelet/config"
	"github.com/rancher/k3k/k3k-kubelet/kubelet"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	configFile string
	c          config.Config
)

func main() {
	app := cli.NewApp()
	app.Name = "k3k-kubelet"
	app.Usage = "virtual kubelet implementation k3k"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "cluster-name",
			Usage:       "Name of the k3k cluster",
			Destination: &c.ClusterName,
			EnvVar:      "CLUSTER_NAME",
		},
		cli.StringFlag{
			Name:        "cluster-namespace",
			Usage:       "Namespace of the k3k cluster",
			Destination: &c.ClusterNamespace,
			EnvVar:      "CLUSTER_NAMESPACE",
		},
		cli.StringFlag{
			Name:        "cluster-token",
			Usage:       "K3S token of the k3k cluster",
			Destination: &c.Token,
			EnvVar:      "CLUSTER_Token",
		},
		cli.StringFlag{
			Name:        "host-config-path",
			Usage:       "Path to the host kubeconfig, if empty then virtual-kubelet will use incluster config",
			Destination: &c.HostConfigPath,
			EnvVar:      "HOST_KUBECONFIG",
		},
		cli.StringFlag{
			Name:        "virtual-config-path",
			Usage:       "Path to the k3k cluster kubeconfig, if empty then virtual-kubelet will create its own config from k3k cluster",
			Destination: &c.VirtualConfigPath,
			EnvVar:      "CLUSTER_NAME",
		},
		cli.StringFlag{
			Name:        "kubelet-port",
			Usage:       "kubelet API port number",
			Destination: &c.KubeletPort,
			EnvVar:      "SERVER_PORT",
			Value:       "9443",
		},
		cli.StringFlag{
			Name:        "agent-pod-ip",
			Usage:       "Agent Pod IP used for TLS SAN for the kubelet server",
			Destination: &c.AgentPodIP,
			EnvVar:      "AGENT_POD_IP",
		},
		cli.StringFlag{
			Name:        "config",
			Usage:       "Path to k3k-kubelet config file",
			Destination: &configFile,
			EnvVar:      "CONFIG_FILE",
			Value:       "/etc/rancher/k3k/config.yaml",
		},
	}
	app.Action = Run
	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}

func Run(clx *cli.Context) {
	if err := c.Parse(configFile); err != nil {
		fmt.Printf("failed to parse config file %s: %v", configFile, err)
		os.Exit(-1)
	}

	if err := c.Validate(); err != nil {
		fmt.Printf("failed to validate config: %v", err)
		os.Exit(-1)
	}
	k, err := kubelet.New(&c)
	if err != nil {
		fmt.Printf("failed to create new virtual kubelet instance: %v", err)
		os.Exit(-1)
	}

	if err := k.RegisterNode(c.KubeletPort, c.ClusterNamespace, c.ClusterName, c.AgentPodIP); err != nil {
		fmt.Printf("failed to register new node: %v", err)
		os.Exit(-1)
	}

	k.Start(context.Background())
}
