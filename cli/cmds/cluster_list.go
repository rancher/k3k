package cmds

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
)

func NewClusterListCmd(appCtx *AppContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List all the existing cluster",
		Example: "k3kcli cluster list [command options]",
		RunE:    list(appCtx),
		Args:    cobra.NoArgs,
	}

	CobraFlagNamespace(appCtx, cmd.Flags())

	return cmd
}

func list(appCtx *AppContext) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := appCtx.Client

		var clusters v1alpha1.ClusterList
		if err := client.List(ctx, &clusters, ctrlclient.InNamespace(appCtx.namespace)); err != nil {
			return err
		}

		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := client.Get(ctx, types.NamespacedName{Name: "clusters.k3k.io"}, crd); err != nil {
			return err
		}

		items := toPointerSlice(clusters.Items)
		table := createTable(crd, items)

		printer := printers.NewTablePrinter(printers.PrintOptions{WithNamespace: true})

		return printer.PrintObj(table, cmd.OutOrStdout())
	}
}
