package cmds

import (
	"github.com/spf13/cobra"
)

func NewPolicyCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "cluster command",
	}

	cmd.AddCommand(
		NewPolicyCreateCmd(appCtx),
		// NewPolicyDeleteCmd(appCtx),
		// NewPolicyListCmd(appCtx),
	)

	return cmd
}
