package cmds

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/urfave/cli/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func NewClusterListCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:            "list",
		Usage:           "List all the existing cluster",
		UsageText:       "k3kcli cluster list [command options]",
		Action:          list(appCtx),
		Flags:           WithCommonFlags(appCtx),
		HideHelpCommand: true,
	}
}

func list(appCtx *AppContext) cli.ActionFunc {
	return func(clx *cli.Context) error {
		ctx := context.Background()
		client := appCtx.Client

		if clx.NArg() > 0 {
			return cli.ShowSubcommandHelp(clx)
		}

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

		return printer.PrintObj(table, clx.App.Writer)
	}
}
