package cmds

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

func NewPolicyListCmd(appCtx *AppContext) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all the existing policies",
		Example: "k3kcli policy list [command options]",
		RunE:    policyList(appCtx),
		Args:    cobra.NoArgs,
	}
}

func policyList(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client

		var policies v1alpha1.VirtualClusterPolicyList
		if err := client.List(ctx, &policies); err != nil {
			return err
		}

		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := client.Get(ctx, types.NamespacedName{Name: "virtualclusterpolicies.k3k.io"}, crd); err != nil {
			return err
		}

		items := toPointerSlice(policies.Items)
		table := createTable(crd, items)

		printer := printers.NewTablePrinter(printers.PrintOptions{})

		return printer.PrintObj(table, cmd.OutOrStdout())
	}
}
