package cmds

import (
	"github.com/urfave/cli/v2"
)

func NewClusterSetCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:  "clusterset",
		Usage: "clusterset command",
		Subcommands: []*cli.Command{
			NewClusterSetCreateCmd(appCtx),
			NewClusterSetDeleteCmd(appCtx),
		},
	}
}
