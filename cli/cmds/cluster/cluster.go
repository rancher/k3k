package cluster

import (
	"github.com/rancher/k3k/cli/cmds"
	"github.com/urfave/cli/v2"
)

var subcommands = []*cli.Command{
	{
		Name:   "create",
		Usage:  "Create new cluster",
		Action: create,
		Flags:  append(cmds.CommonFlags, clusterCreateFlags...),
	},
	{
		Name:   "delete",
		Usage:  "Delete an existing cluster",
		Action: delete,
		Flags:  append(cmds.CommonFlags, clusterDeleteFlags...),
	},
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:        "cluster",
		Usage:       "cluster command",
		Subcommands: subcommands,
	}
}
