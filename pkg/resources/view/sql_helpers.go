package resourceview

import (
	"fmt"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func buildCreateOnClusterSentence(resource ViewResource) (query string) {
	clusterStatement := common.GetClusterStatement(resource.Cluster)

	ret := fmt.Sprintf(
		"CREATE OR REPLACE %s VIEW %v.%v %v %s as (%s) COMMENT '%s'",
		isMaterializedStatement(resource.Materialized),
		resource.Database,
		resource.Name,
		clusterStatement,
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

func toTableStatement(toTable string) string {
	if toTable != "" {
		return "TO " + toTable
	}
	return ""
}
