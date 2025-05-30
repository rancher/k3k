package cmds

import (
	"github.com/urfave/cli/v2"
)

func NewClusterCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:  "cluster",
		Usage: "cluster command",
		Subcommands: []*cli.Command{
			NewClusterCreateCmd(appCtx),
			NewClusterDeleteCmd(appCtx),
			NewClusterListCmd(appCtx),
		},
	}
}
