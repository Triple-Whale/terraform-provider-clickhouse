package resourcerole

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type lostRoleCreateReply struct {
	testConnection
	created   bool
	execError error
}

func (conn *lostRoleCreateReply) Exec(_ context.Context, query string, _ ...interface{}) error {
	conn.execs = append(conn.execs, query)
	conn.created = true
	if conn.execError != nil {
		return conn.execError
	}
	return errors.New("response lost")
}

func TestLocalCreateLostReplyRetainsRole(t *testing.T) {
	conn := &lostRoleCreateReply{testConnection: testConnection{query: func(string, ...interface{}) (driver.Rows, error) { return &testRows{}, nil }}}
	var connection driver.Conn = conn
	data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "role", "grants": []interface{}{}})
	diagnostics := resourceRoleCreate(context.Background(), data, &common.ApiClient{ClickhouseConnection: &connection})
	if !diagnostics.HasError() || data.Id() != access.ResourceID("", "role") || !conn.created {
		t.Fatalf("uncertain committed create must retain ownership: diagnostics=%v id=%q created=%v", diagnostics, data.Id(), conn.created)
	}
	if len(conn.execs) != 1 {
		t.Fatal("failed create performed followup mutation")
	}
}

func TestRoleCreateFailureOwnership(t *testing.T) {
	cases := []struct {
		name     string
		cluster  string
		failure  error
		retained bool
	}{
		{name: "local transport", failure: errors.New("response lost"), retained: true},
		{name: "cluster transport", cluster: "main", failure: errors.New("response lost"), retained: true},
		{name: "local deadline", failure: context.DeadlineExceeded, retained: true},
		{name: "local server timeout", failure: &clickhouse.Exception{Code: 159}, retained: true},
		{name: "local denied", failure: &clickhouse.Exception{Code: 497}},
		{name: "cluster partial denial", cluster: "main", failure: &clickhouse.Exception{Code: 497}, retained: true},
		{name: "local race collision", failure: &clickhouse.Exception{Code: 493}},
		{name: "cluster race collision", cluster: "main", failure: fmt.Errorf("wrapped: %w", &clickhouse.Exception{Code: 493})},
		{name: "nested cluster collision", cluster: "main", failure: &clickhouse.Exception{Code: 341, Nested: []clickhouse.Exception{{Code: 493}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			conn := &lostRoleCreateReply{execError: test.failure, testConnection: testConnection{query: func(query string, _ ...interface{}) (driver.Rows, error) {
				if strings.Contains(query, "system.clusters") {
					return &testRows{values: [][]interface{}{{"127.0.0.1", uint16(9000)}}}, nil
				}
				return &testRows{}, nil
			}}}
			var connection driver.Conn = conn
			client := &common.ApiClient{ClickhouseConnection: &connection, VerifyReplicas: true, ReplicaConnections: []*driver.Conn{&connection}}
			data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "role", "cluster": test.cluster, "grants": []interface{}{}})
			diagnostics := resourceRoleCreate(context.Background(), data, client)
			retained := data.Id() != ""
			if !diagnostics.HasError() || retained != test.retained || len(conn.execs) != 1 {
				t.Fatalf("wrong failed-create ownership: diagnostics=%v retained=%v statements=%d", diagnostics, retained, len(conn.execs))
			}
			if access.IsCreateCollision(test.failure) && !strings.Contains(diagnostics[0].Summary, "import explicitly") {
				t.Fatal("collision must require explicit ownership recovery")
			}
		})
	}
}
