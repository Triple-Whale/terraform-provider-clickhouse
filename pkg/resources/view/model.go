package resourceview

import (
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

type CHView struct {
	Database string     `ch:"database"`
	Name     string     `ch:"name"`
	Query    string     `ch:"as_select"`
	Engine   string     `ch:"engine"`
	Comment  string     `ch:"comment"`
	Columns  []CHColumn
}

type ColumnDefinition struct {
	Name              string
	Type              string
	Comment           string
	DefaultKind       string
	DefaultExpression string
}

type CHColumn struct {
	Database          string `ch:"database"`
	Table             string `ch:"table"`
	Name              string `ch:"name"`
	Type              string `ch:"type"`
	Comment           string `ch:"comment"`
	DefaultKind       string `ch:"default_kind"`
	DefaultExpression string `ch:"default_expression"`
}

type ViewResource struct {
	Database     string
	Name         string
	Query        string
	Cluster      string
	Materialized bool
	ToTable      string
	Refresh      string
	Comment      string
	InlineEngine string
	OrderBy      []string
	Columns      []ColumnDefinition
}

func (t *CHView) ToResource() (*ViewResource, error) {
	viewResource := ViewResource{
		Database: t.Database,
		Name:     t.Name,
		Query:    t.Query,
	}

	comment, cluster, toTable, refresh, err := common.UnmarshalComment(t.Comment)
	if err != nil {
		return nil, err
	}

	viewResource.Cluster = cluster
	viewResource.Comment = comment
	viewResource.ToTable = toTable
	viewResource.Refresh = refresh
	viewResource.Materialized = t.Engine == "MaterializedView"
	viewResource.Columns = columnsToResource(t.Columns)

	return &viewResource, nil
}

func (t *ViewResource) setColumns(columns []interface{}) {
	for _, column := range columns {
		col := column.(map[string]interface{})
		columnDefinition := ColumnDefinition{
			Name:              col["name"].(string),
			Type:              col["type"].(string),
			Comment:           col["comment"].(string),
			DefaultKind:       col["default_kind"].(string),
			DefaultExpression: col["default_expression"].(string),
		}
		t.Columns = append(t.Columns, columnDefinition)
	}
}

func getColumnsForSchema(columns []ColumnDefinition) []map[string]interface{} {
	var ret []map[string]interface{}
	for _, column := range columns {
		ret = append(ret, map[string]interface{}{
			"name":               column.Name,
			"type":               column.Type,
			"comment":            column.Comment,
			"default_kind":       column.DefaultKind,
			"default_expression": column.DefaultExpression,
		})
	}
	return ret
}

func columnsToResource(chColumns []CHColumn) []ColumnDefinition {
	var cols []ColumnDefinition
	for _, c := range chColumns {
		cols = append(cols, ColumnDefinition{
			Name:              c.Name,
			Type:              c.Type,
			Comment:           c.Comment,
			DefaultKind:       c.DefaultKind,
			DefaultExpression: c.DefaultExpression,
		})
	}
	return cols
}

func (t *ViewResource) Validate() diag.Diagnostics {
	var diags diag.Diagnostics

	return diags
}
