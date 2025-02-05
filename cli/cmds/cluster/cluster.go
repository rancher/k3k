package cluster

import (
	"github.com/urfave/cli/v2"
)

func NewCommand() *cli.Command {
	return &cli.Command{
		Name:  "cluster",
		Usage: "cluster command",
		Subcommands: []*cli.Command{
			NewCreateCmd(),
			NewDeleteCmd(),
		},
	}
}
