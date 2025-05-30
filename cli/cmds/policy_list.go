package cmds

import (
	"context"

	"github.com/rancher/k3k/pkg/apis/k3k.io/v1alpha1"
	"github.com/urfave/cli/v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/util/jsonpath"
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

		printerColumns := makePrinterColumns(crd)
		table := makeTable(printerColumns)

		for _, vcp := range policies.Items {
			table.Rows = append(table.Rows, makeRow(&vcp, printerColumns))
		}

		printer := printers.NewTablePrinter(printers.PrintOptions{})

		return printer.PrintObj(table, clx.App.Writer)
	}
}

func makePrinterColumns(crd *apiextensionsv1.CustomResourceDefinition) []apiextensionsv1.CustomResourceColumnDefinition {
	printerColumns := []apiextensionsv1.CustomResourceColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: "Name of the Resource", JSONPath: ".metadata.name"},
	}

	for _, version := range crd.Spec.Versions {
		if version.Name == "v1alpha1" {
			printerColumns = append(printerColumns, version.AdditionalPrinterColumns...)
			break
		}
	}

	return printerColumns
}

func makeTable(printerColumns []apiextensionsv1.CustomResourceColumnDefinition) *metav1.Table {
	var columnDefinitions []metav1.TableColumnDefinition

	for _, col := range printerColumns {
		columnDefinitions = append(columnDefinitions, metav1.TableColumnDefinition{
			Name:        col.Name,
			Type:        col.Type,
			Format:      col.Format,
			Description: col.Description,
			Priority:    col.Priority,
		})
	}

	return &metav1.Table{
		TypeMeta:          metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "Table"},
		ColumnDefinitions: columnDefinitions,
	}
}

func makeRow(obj runtime.Object, printerColumns []apiextensionsv1.CustomResourceColumnDefinition) metav1.TableRow {
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
	if err != nil {
		return metav1.TableRow{
			Cells: []any{"<error: " + err.Error() + ">"},
		}
	}

	return metav1.TableRow{
		Cells:  makeCells(objMap, printerColumns),
		Object: runtime.RawExtension{Object: obj},
	}
}

func makeCells(obj map[string]any, printerColumns []apiextensionsv1.CustomResourceColumnDefinition) []any {
	var cells []any

	for _, printCol := range printerColumns {
		j := jsonpath.New(printCol.Name)

		err := j.Parse("{" + printCol.JSONPath + "}")
		if err != nil {
			cells = append(cells, "<error>")
			continue
		}

		results, err := j.FindResults(obj)
		if err != nil || len(results) == 0 || len(results[0]) == 0 {
			cells = append(cells, "<none>")
			continue
		}

		cells = append(cells, results[0][0].Interface())
	}

	return cells
}
