package cmds

import (
	"github.com/urfave/cli/v2"
)

func NewClusterCmd() *cli.Command {
	return &cli.Command{
		Name:  "cluster",
		Usage: "cluster command",
		Subcommands: []*cli.Command{
			NewClusterCreateCmd(),
			NewClusterDeleteCmd(),
		},
	}
}
