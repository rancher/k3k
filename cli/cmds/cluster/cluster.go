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
		Action:          create,
		Flags:           append(cmds.CommonFlags, clusterCreateFlags...),
	},
	{
		Name:            "delete",
		Usage:           "Delete an existing cluster",
		SkipFlagParsing: false,
		SkipArgReorder:  true,
		Action:          delete,
		Flags:           append(cmds.CommonFlags, clusterDeleteFlags...),
	},
}

func NewCommand() cli.Command {
	return cli.Command{
		Name:        "cluster",
		Usage:       "cluster command",
		Subcommands: subcommands,
	}
}
