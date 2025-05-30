package cmds

import (
	"github.com/urfave/cli/v2"
)

func NewPolicyCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:  "policy",
		Usage: "policy command",
		Subcommands: []*cli.Command{
			NewPolicyCreateCmd(appCtx),
			NewPolicyDeleteCmd(appCtx),
			NewPolicyListCmd(appCtx),
		},
	}
}
