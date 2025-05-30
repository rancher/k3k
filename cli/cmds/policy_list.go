package cmds

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/urfave/cli/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"
)

func NewPolicyListCmd(appCtx *AppContext) *cli.Command {
	return &cli.Command{
		Name:            "list",
		Usage:           "List all the existing policies",
		UsageText:       "k3kcli policy list [command options]",
		Action:          policyList(appCtx),
		Flags:           WithCommonFlags(appCtx),
		HideHelpCommand: true,
	}
}

func policyList(appCtx *AppContext) cli.ActionFunc {
	return func(clx *cli.Context) error {
		ctx := context.Background()
		client := appCtx.Client

		if clx.NArg() > 0 {
			return cli.ShowSubcommandHelp(clx)
		}

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

		return printer.PrintObj(table, clx.App.Writer)
	}
}
