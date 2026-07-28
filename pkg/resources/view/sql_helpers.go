package resourceview

import (
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func buildCreateOnClusterSentence(resource ViewResource) (query string) {
	clusterStatement := common.GetClusterStatement(resource.Cluster)

	createPrefix := "CREATE OR REPLACE"
	if resource.Refresh != "" {
		createPrefix = "CREATE"
	}

	if resource.InlineEngine != "" {
		ret := fmt.Sprintf(
			"%s %s VIEW %v.%v %v %s %s ENGINE = %s %s AS (%s) COMMENT '%s'",
			createPrefix,
			isMaterializedStatement(resource.Materialized),
			resource.Database,
			resource.Name,
			clusterStatement,
			refreshStatement(resource.Refresh),
			buildViewColumnsSentence(resource.Columns),
			resource.InlineEngine,
			buildViewOrderBySentence(resource.OrderBy),
			resource.Query,
			resource.Comment,
		)
		return ret
	}

	ret := fmt.Sprintf(
		"%s %s VIEW %v.%v %v %s %s as (%s) COMMENT '%s'",
		createPrefix,
		isMaterializedStatement(resource.Materialized),
		resource.Database,
		resource.Name,
		clusterStatement,
		refreshStatement(resource.Refresh),
		toTableStatement(resource.ToTable),
		resource.Query,
		resource.Comment,
	)
	return ret
}

func isMaterializedStatement(materialized bool) string {
	if materialized {
		return "MATERIALIZED"
	}
	return ""
}

func refreshStatement(refresh string) string {
	if refresh != "" {
		return "REFRESH " + refresh
	}
	return ""
}

func toTableStatement(toTable string) string {
	if toTable != "" {
		return "TO " + toTable
	}
	return ""
}

func buildViewColumnsSentence(cols []ColumnDefinition) string {
	if len(cols) == 0 {
		return ""
	}
	colDefs := make([]string, 0, len(cols))
	for _, col := range cols {
		def := fmt.Sprintf("\t`%s` %s %s %s %s", col.Name, col.Type, col.DefaultKind, col.DefaultExpression, getViewComment(col.Comment))
		colDefs = append(colDefs, def)
	}
	return "(\n" + strings.Join(colDefs, ",\n") + "\n)"
}

func buildViewOrderBySentence(orderBy []string) string {
	if len(orderBy) > 0 {
		return fmt.Sprintf("ORDER BY (%v)", strings.Join(orderBy, ", "))
	}
	return ""
}

func getViewComment(comment string) string {
	if comment != "" {
		return fmt.Sprintf("COMMENT '%s'", comment)
	}
	return ""
}
