package cmds

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1beta1"
)

var completeClusterMode = cobra.FixedCompletions(
	[]string{
		string(v1beta1.SharedClusterMode),
		string(v1beta1.VirtualClusterMode),
	},
	cobra.ShellCompDirectiveNoFileComp,
)

var completePersistenceMode = cobra.FixedCompletions(
	[]string{
		string(v1beta1.DynamicPersistenceMode),
		string(v1beta1.EphemeralPersistenceMode),
	},
	cobra.ShellCompDirectiveNoFileComp,
)

// mustRegisterFlagCompletion registers a completion function for a flag and
// aborts if the flag does not exist. This only fails on programmer error, so
// there is no reason to bubble it up to the caller.
func mustRegisterFlagCompletion(cmd *cobra.Command, flagName string, f cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(flagName, f); err != nil {
		logrus.Fatal(err)
	}
}

// disableFileCompletion walks the command tree and turns off cobra's default
// filename completion for both positional arguments and flag values, leaving
// anything that is explicitly configured (enum completers, MarkFlagFilename,
// MarkFlagDirname, ValidArgs, bool flags) untouched.
func disableFileCompletion(cmd *cobra.Command) {
	if cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Value.Type() == "bool" {
			return
		}

		if _, ok := cmd.GetFlagCompletionFunc(f.Name); ok {
			return
		}

		if _, ok := f.Annotations[cobra.BashCompFilenameExt]; ok {
			return
		}

		if _, ok := f.Annotations[cobra.BashCompSubdirsInDir]; ok {
			return
		}

		_ = cmd.RegisterFlagCompletionFunc(f.Name, cobra.NoFileCompletions)
	})

	for _, sub := range cmd.Commands() {
		disableFileCompletion(sub)
	}
}
