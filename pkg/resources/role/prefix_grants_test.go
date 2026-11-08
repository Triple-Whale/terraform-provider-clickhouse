package resourcerole

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestHasPrefixWildcard(t *testing.T) {
	cases := []struct {
		statement string
		prefix    bool
	}{
		{"GRANT SELECT ON db.orders* TO role", true},
		{"GRANT SELECT ON db*.* TO role", true},
		{"GRANT SELECT ON `db name`*.* TO role", true},
		{"GRANT SELECT ON db.`table name`* TO role", true},
		{"GRANT SELECT ON TO.ON* TO role", true},
		{"GRANT SELECT ON ON.TO* TO role", true},
		{"GRANT SELECT ON exact.table, INSERT ON db.prefix* TO role", true},
		{"GRANT SELECT ON db.`a\\`b`* TO role", true},
		{"GRANT SELECT ON db.`a``b`* TO role", true},
		{"GRANT SELECT ON db.`a\\\\`* TO role", true},
		{`GRANT SELECT ON db."a""b"* TO role`, true},
		{`GRANT SELECT ON db.'a\'b'* TO role`, true},
		{"GRANT SELECT ON *.* TO role", false},
		{"GRANT SELECT ON db.* TO role", false},
		{"GRANT SELECT ON db.table TO role", false},
		{"GRANT SELECT ON ON.TO TO role", false},
		{"GRANT SELECT ON db.table TO `role*`", false},
		{"GRANT SELECT ON db.table TO `ON db.prefix* TO other`", false},
		{"GRANT SELECT ON `db*`.`table*` TO `role*`", false},
		{"GRANT SELECT ON db.`a\\`*b` TO role", false},
		{"GRANT SELECT ON db.`a\\\\b` TO role", false},
		{"GRANT SELECT ON db.`a``*b` TO role", false},
		{`GRANT SELECT ON db."a""*b" TO role`, false},
		{`GRANT SELECT ON db.'a\'*b' TO role`, false},
		{"GRANT SELECT ON exact.table, INSERT ON db.* TO `ON*`", false},
	}
	for _, test := range cases {
		t.Run(test.statement, func(t *testing.T) {
			prefix, err := hasPrefixWildcard(test.statement)
			if err != nil || prefix != test.prefix {
				t.Fatalf("prefix=%v err=%v; want %v", prefix, err, test.prefix)
			}
		})
	}
	for _, statement := range []string{"GRANT SELECT ON db.`unterminated", "GRANT SELECT ON db.`trailing\\"} {
		if _, err := hasPrefixWildcard(statement); err == nil {
			t.Fatalf("accepted malformed statement %q", statement)
		}
	}
}

func roleScopeConnection(t *testing.T, host, statement string, collision bool, showError error) *testConnection {
	t.Helper()
	return &testConnection{query: func(query string, args ...interface{}) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "system.clusters"):
			if strings.Contains(query, "is_local") {
				return &testRows{values: [][]interface{}{{host, uint16(9000)}}}, nil
			}
			return &testRows{values: [][]interface{}{{"one", uint16(9000)}, {"two", uint16(9000)}}}, nil
		case strings.Contains(query, "system.users"):
			if len(args) != 1 || args[0] != "role" {
				t.Fatal("user collision lookup must bind the role name")
			}
			if collision {
				return &testRows{values: [][]interface{}{{"role"}}}, nil
			}
			return &testRows{}, nil
		case strings.Contains(query, "system.roles"):
			return &testRows{values: [][]interface{}{{"role", "local_directory"}}}, nil
		case strings.Contains(query, "system.role_grants"):
			return &testRows{}, nil
		case strings.HasPrefix(query, "SHOW GRANTS FOR "):
			if query != "SHOW GRANTS FOR `role`" {
				t.Fatalf("unexpected SHOW query %q", query)
			}
			if showError != nil {
				return nil, showError
			}
			return &testRows{values: [][]interface{}{{statement}}}, nil
		case strings.Contains(query, "system.grants"):
			return &testRows{values: [][]interface{}{{"SELECT", "db", "orders", "", uint8(0), uint8(0), uint8(0), uint8(0)}}}, nil
		default:
			t.Fatalf("unexpected query %q", query)
			return nil, nil
		}
	}}
}

func TestRoleUnsupportedScopesPreserveStateWithoutWrites(t *testing.T) {
	for _, action := range []string{"read", "update", "delete", "import"} {
		for _, issue := range []string{"table prefix", "database prefix", "user collision", "show failure", "replica prefix", "replica collision"} {
			t.Run(action+"/"+issue, func(t *testing.T) {
				statement := "GRANT SELECT ON db.orders* TO role"
				if issue == "database prefix" {
					statement = "GRANT SELECT ON db*.* TO role"
				}
				var showError error
				if issue == "show failure" {
					showError = errors.New("metadata unavailable")
				}
				bad := roleScopeConnection(t, "two", statement, strings.Contains(issue, "collision"), showError)
				var connection driver.Conn = bad
				client := &common.ApiClient{ClickhouseConnection: &connection}
				cluster := ""
				var good *testConnection
				if strings.HasPrefix(issue, "replica") {
					cluster = "main"
					good = roleScopeConnection(t, "one", "GRANT SELECT ON db.orders TO role", false, nil)
					var primary driver.Conn = good
					client = &common.ApiClient{ClickhouseConnection: &primary, VerifyReplicas: true, ReplicaConnections: []*driver.Conn{&connection}}
				}
				data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "role", "cluster": cluster, "grants": []interface{}{}})
				id := access.ResourceID(cluster, "role")
				data.SetId(id)
				callback := resourceRoleRead
				switch action {
				case "update":
					callback = resourceRoleUpdate
				case "delete":
					callback = resourceRoleDelete
				case "import":
					if _, err := access.ImportState(context.Background(), data, client); err != nil {
						t.Fatal(err)
					}
				}
				diagnostics := callback(context.Background(), data, client)
				if !diagnostics.HasError() || data.Id() != id || len(bad.execs) != 0 || (good != nil && len(good.execs) != 0) {
					t.Fatalf("diagnostics=%v id=%q bad writes=%v", diagnostics, data.Id(), bad.execs)
				}
				if strings.Contains(issue, "prefix") && !strings.Contains(diagnostics[0].Summary, "prefix wildcard") {
					t.Fatalf("wrong rejection: %v", diagnostics)
				}
			})
		}
	}
}

func TestRoleCreateRejectsUserNameBeforeLookingUpRole(t *testing.T) {
	conn := &testConnection{query: func(query string, _ ...interface{}) (driver.Rows, error) {
		if !strings.Contains(query, "system.users") {
			t.Fatalf("must reject before reading or creating the role: %s", query)
		}
		return &testRows{values: [][]interface{}{{"role"}}}, nil
	}}
	var connection driver.Conn = conn
	data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "role", "grants": []interface{}{}})
	if diagnostics := resourceRoleCreate(context.Background(), data, &common.ApiClient{ClickhouseConnection: &connection}); !diagnostics.HasError() || data.Id() != "" || len(conn.execs) != 0 {
		t.Fatalf("diagnostics=%v id=%q writes=%v", diagnostics, data.Id(), conn.execs)
	}
}
