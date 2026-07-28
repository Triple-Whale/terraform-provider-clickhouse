package resourceview

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

type CHViewService struct {
	CHConnection *driver.Conn
}

func (ts *CHViewService) GetDBViews(ctx context.Context, database string) ([]CHView, error) {
	query := fmt.Sprintf("SELECT database, name FROM system.tables where database = '%s'", database)
	rows, err := (*ts.CHConnection).Query(ctx, query)

	if err != nil {
		return nil, fmt.Errorf("reading views from Clickhouse: %v", err)
	}

	var views []CHView
	for rows.Next() {
		var view CHView
		err := rows.ScanStruct(&view)
		if err != nil {
			return nil, fmt.Errorf("scanning Clickhouse view row: %v", err)
		}
		views = append(views, view)
	}

	return views, nil
}

func (ts *CHViewService) GetView(ctx context.Context, database string, view string) (*CHView, error) {
	query := fmt.Sprintf("SELECT database, name, engine, as_select, comment FROM system.tables where database = '%s' and name = '%s'", database, view)
	row := (*ts.CHConnection).QueryRow(ctx, query)

	if row.Err() != nil {
		return nil, fmt.Errorf("reading view from Clickhouse: %v", row.Err())
	}

	var chView CHView
	err := row.ScanStruct(&chView)
	if err != nil && strings.Contains(err.Error(), "no rows in result set") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning Clickhouse view row: %v", err)
	}

	// the value read from system.tables is not formatted
	formattedQuery := common.FormatSQL(chView.Query)
	chView.Query = formattedQuery

	return &chView, nil
}

func (ts *CHViewService) getViewColumns(ctx context.Context, database string, table string) ([]CHColumn, error) {
	query := fmt.Sprintf(
		"SELECT database, table, name, type, comment, default_kind, default_expression FROM system.columns WHERE database = '%s' AND table = '%s'",
		database,
		table,
	)
	rows, err := (*ts.CHConnection).Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("reading columns from Clickhouse: %v", err)
	}

	var chColumns []CHColumn
	for rows.Next() {
		var column CHColumn
		err := rows.ScanStruct(&column)
		if err != nil {
			return nil, fmt.Errorf("scanning Clickhouse column row: %v", err)
		}
		chColumns = append(chColumns, column)
	}
	return chColumns, nil
}

func (ts *CHViewService) CreateView(ctx context.Context, viewResource ViewResource) error {
	query := buildCreateOnClusterSentence(viewResource)
	err := (*ts.CHConnection).Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("creating Clickhouse view: %v", err)
	}
	return nil
}

func (ts *CHViewService) DeleteView(ctx context.Context, viewResource ViewResource) error {
	query := fmt.Sprintf("DROP VIEW if exists %s.%s %s", viewResource.Database, viewResource.Name, common.GetClusterStatement(viewResource.Cluster))
	err := (*ts.CHConnection).Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("deleting Clickhouse view: %v", err)
	}
	return nil
}
