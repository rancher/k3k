package kubeconfig

import (
	"github.com/rancher/k3k/cli/cmds"
	"github.com/urfave/cli"
)

var kubeconfigSubcommands = []cli.Command{
	{
		Name:            "generate",
		Usage:           "Generate kubeconfig for clusters",
		SkipFlagParsing: false,
		SkipArgReorder:  true,
		Action:          generateKubeconfig,
		Flags:           append(cmds.CommonFlags, generateKubeconfigFlags...),
	},
}

func NewKubeconfigCommand() cli.Command {
	return cli.Command{
		Name:        "kubeconfig",
		Usage:       "Manage kubeconfig for clusters",
		Subcommands: kubeconfigSubcommands,
	}
}
