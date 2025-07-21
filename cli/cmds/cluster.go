package cmds

import (
	"github.com/spf13/cobra"
)

func NewClusterCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "cluster command",
	}

	cmd.AddCommand(
		NewClusterCreateCmd(appCtx),
		NewClusterDeleteCmd(appCtx),
		NewClusterListCmd(appCtx),
	)

	return cmd
}
