package cmds

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/jsonpath"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// createTable creates a table to print from the printerColumn defined in the CRD spec, plus the name at the beginning
func createTable[T runtime.Object](crd *apiextensionsv1.CustomResourceDefinition, objs []T) *metav1.Table {
	printerColumns := getPrinterColumnsFromCRD(crd)

	return &metav1.Table{
		TypeMeta:          metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "Table"},
		ColumnDefinitions: convertToTableColumns(printerColumns),
		Rows:              createTableRows(objs, printerColumns),
	}
}

func getPrinterColumnsFromCRD(crd *apiextensionsv1.CustomResourceDefinition) []apiextensionsv1.CustomResourceColumnDefinition {
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

func convertToTableColumns(printerColumns []apiextensionsv1.CustomResourceColumnDefinition) []metav1.TableColumnDefinition {
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

	return columnDefinitions
}

func createTableRows[T runtime.Object](objs []T, printerColumns []apiextensionsv1.CustomResourceColumnDefinition) []metav1.TableRow {
	var rows []metav1.TableRow

	for _, obj := range objs {
		objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
		if err != nil {
			rows = append(rows, metav1.TableRow{Cells: []any{"<error: " + err.Error() + ">"}})
			continue
		}

		rows = append(rows, metav1.TableRow{
			Cells:  buildRowCells(objMap, printerColumns),
			Object: runtime.RawExtension{Object: obj},
		})
	}

	return rows
}

func buildRowCells(objMap map[string]any, printerColumns []apiextensionsv1.CustomResourceColumnDefinition) []any {
	var cells []any

	for _, printCol := range printerColumns {
		j := jsonpath.New(printCol.Name)

		err := j.Parse("{" + printCol.JSONPath + "}")
		if err != nil {
			cells = append(cells, "<error>")
			continue
		}

		results, err := j.FindResults(objMap)
		if err != nil || len(results) == 0 || len(results[0]) == 0 {
			cells = append(cells, "<none>")
			continue
		}

		cells = append(cells, results[0][0].Interface())
	}

	return cells
}

func toPointerSlice[T any](v []T) []*T {
	vPtr := make([]*T, len(v))

	for i := range v {
		vPtr[i] = &v[i]
	}

	return vPtr
}
