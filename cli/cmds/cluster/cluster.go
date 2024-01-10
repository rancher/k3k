package cluster

import (
	"github.com/rancher/k3k/cli/cmds"
	"github.com/urfave/cli"
)

var subcommands = []cli.Command{
	{
		Name:            "create",
		Usage:           "Create new cluster",
		SkipFlagParsing: false,
		SkipArgReorder:  true,
		Action:          createCluster,
		Flags:           append(cmds.CommonFlags, clusterCreateFlags...),
	},
}

func NewCommand() cli.Command {
	return cli.Command{
		Name:        "cluster",
		Usage:       "cluster command",
		Subcommands: subcommands,
	}
}
