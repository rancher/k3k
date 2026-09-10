package cmds

import (
	"github.com/spf13/cobra"
)

// NewClusterCmd returns the "cluster" command and its subcommands.
func NewClusterCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "K3k cluster command.",
	}

	cmd.AddCommand(
		NewClusterCreateCmd(appCtx),
		NewClusterUpdateCmd(appCtx),
		NewClusterDeleteCmd(appCtx),
		NewClusterListCmd(appCtx),
	)

	return cmd
}
