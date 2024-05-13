package resourcetable

import (
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func buildColumnsSentence(cols []ColumnDefinition) []string {
	outColumn := make([]string, 0)
	for _, col := range cols {
		outColumn = append(outColumn, fmt.Sprintf("\t `%s` %s %s", col.Name, col.Type, getComment(col.Comment)))
	}
	return outColumn
}

func buildIndexesSentence(indexes []IndexDefinition) []string {
	outIndexes := make([]string, 0)
	for _, index := range indexes {
		indexStatement := fmt.Sprintf("INDEX %s %s TYPE %s", index.Name, index.Expression, index.Type)
		if index.Granularity > 0 {
			indexStatement += fmt.Sprintf(" GRANULARITY %d", index.Granularity)
		}
		outIndexes = append(outIndexes, fmt.Sprintf("\t%s", indexStatement))
	}
	return outIndexes
}

func getComment(comment string) string {
	if comment != "" {
		return fmt.Sprintf("COMMENT '%s'", comment)
	}
	return ""
}

func buildPartitionBySentence(partitionBy []string) string {
	if len(partitionBy) > 0 {
		return fmt.Sprintf("PARTITION BY (%v)", strings.Join(partitionBy, ", "))
	}
	return ""
}

func buildOrderBySentence(orderBy []string) string {
	if len(orderBy) > 0 {
		return fmt.Sprintf("ORDER BY (%v)", strings.Join(orderBy, ", "))
	}
	return ""
}

func buildSettingsSentence(settings map[string]string) string {
	if len(settings) > 0 {
		settingsList := make([]string, 0)
		for key, value := range settings {
			settingsList = append(settingsList, fmt.Sprintf("%s = '%s'", key, value))
		}
		ret := fmt.Sprintf("SETTINGS %s", strings.Join(settingsList, ", "))
		return ret
	}
	return ""
}

func buildCreateOnClusterSentence(resource TableResource) (query string) {
	columnsStatement := ""
	if len(resource.Columns) > 0 {
		columnsStatement = "("
		columnsList := buildColumnsSentence(resource.GetColumnsResourceList())
		columnsStatement += strings.Join(columnsList, ",\n")
		columnsStatement += ",\n"

		if len(resource.Indexes) > 0 {
			indexesList := buildIndexesSentence(resource.Indexes)
			columnsStatement += strings.Join(indexesList, ",\n")
		}
		columnsStatement += ")\n"
	}

	clusterStatement := common.GetClusterStatement(resource.Cluster)

	ret := fmt.Sprintf(
		"CREATE TABLE %v.%v %v %v ENGINE = %v(%v) %s %s %s COMMENT '%s'",
		resource.Database,
		resource.Name,
		clusterStatement,
		columnsStatement,
		resource.Engine,
		strings.Join(resource.EngineParams, ", "),
		buildOrderBySentence(resource.OrderBy),
		buildPartitionBySentence(resource.PartitionBy),
		buildSettingsSentence(resource.Settings),
		resource.Comment,
	)
	return ret
}
