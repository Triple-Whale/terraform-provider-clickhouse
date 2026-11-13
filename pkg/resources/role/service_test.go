package resourcerole

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	grantpolicy "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type testRows struct {
	driver.Rows
	values [][]interface{}
	index  int
	closed bool
}

func (rows *testRows) Next() bool { return rows.index < len(rows.values) }

func (rows *testRows) Scan(values ...interface{}) error {
	if len(values) != len(rows.values[rows.index]) {
		return fmt.Errorf("scan arity mismatch")
	}
	for i, value := range values {
		reflect.ValueOf(value).Elem().Set(reflect.ValueOf(rows.values[rows.index][i]))
	}
	rows.index++
	return nil
}

func (rows *testRows) Err() error { return nil }

func (rows *testRows) Close() error { rows.closed = true; return nil }

type testConnection struct {
	driver.Conn
	query func(string, ...interface{}) (driver.Rows, error)
	execs []string
}

func (conn *testConnection) Query(_ context.Context, query string, args ...interface{}) (driver.Rows, error) {
	return conn.query(query, args...)
}

func (conn *testConnection) Exec(_ context.Context, query string, _ ...interface{}) error {
	conn.execs = append(conn.execs, query)
	return nil
}

func TestRoleReadMissing(t *testing.T) {
	rows := &testRows{}
	conn := &testConnection{query: func(_ string, args ...interface{}) (driver.Rows, error) {
		if len(args) != 1 || args[0] != "missing" {
			t.Fatal("role name must be bound as a query argument")
		}
		return rows, nil
	}}
	role, err := (&CHRoleService{Connection: conn}).GetRole(context.Background(), "missing")
	if err != nil || role != nil || !rows.closed {
		t.Fatalf("missing role: role=%v err=%v closed=%v", role, err, rows.closed)
	}
}

func TestRoleReadRejectsUnsupportedOwnership(t *testing.T) {
	cases := []struct {
		name      string
		storage   string
		inherited bool
		grant     []interface{}
	}{
		{name: "literal star", storage: "local_directory", grant: []interface{}{"SELECT", "*", "table", "", uint8(0), uint8(0), uint8(0), uint8(0)}},
		{name: "xml", storage: "users.xml"},
		{name: "inherited", storage: "local_directory", inherited: true},
		{name: "column", storage: "local_directory", grant: []interface{}{"SELECT", "db", "table", "secret", uint8(0), uint8(0), uint8(0), uint8(0)}},
		{name: "grant option", storage: "local_directory", grant: []interface{}{"SELECT", "db", "table", "", uint8(0), uint8(1), uint8(0), uint8(0)}},
		{name: "partial revoke", storage: "local_directory", grant: []interface{}{"SELECT", "db", "table", "", uint8(1), uint8(0), uint8(0), uint8(0)}},
		{name: "external", storage: "local_directory", grant: []interface{}{"S3", "*", "*", "", uint8(0), uint8(0), uint8(1), uint8(1)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			conn := &testConnection{query: func(query string, _ ...interface{}) (driver.Rows, error) {
				switch {
				case strings.Contains(query, "system.roles"):
					return &testRows{values: [][]interface{}{{"role", test.storage}}}, nil
				case strings.Contains(query, "system.users"), strings.HasPrefix(query, "SHOW GRANTS FOR "):
					return &testRows{}, nil
				case strings.Contains(query, "system.privileges"):
					return &testRows{}, nil
				case strings.Contains(query, "system.role_grants"):
					if test.inherited {
						return &testRows{values: [][]interface{}{{"parent"}}}, nil
					}
					return &testRows{}, nil
				default:
					return &testRows{values: [][]interface{}{test.grant}}, nil
				}
			}}
			if _, err := (&CHRoleService{Connection: conn}).GetRole(context.Background(), "role"); err == nil {
				t.Fatal("unsupported ownership was accepted")
			}
		})
	}
}

func TestRoleReconcileExactMultiScope(t *testing.T) {
	conn := &testConnection{}
	service := &CHRoleService{Connection: conn}
	role := RoleResource{Name: "a`b", Cluster: "main", Grants: []Grant{
		{Database: "target", Table: "orders", Privileges: []string{"SELECT"}},
		{Database: "other", Table: "*", Privileges: []string{"INSERT"}},
	}}
	actual := []*CHRole{{Name: role.Name, Grants: []Grant{{Database: "old", Table: "*", Privileges: []string{"SELECT"}}}}, nil}
	if err := service.Reconcile(context.Background(), role, actual); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE ROLE IF NOT EXISTS `a\\`b` ON CLUSTER `main`",
		"REVOKE ON CLUSTER `main` SELECT ON `old`.* FROM `a\\`b`",
		"GRANT ON CLUSTER `main` INSERT ON `other`.* TO `a\\`b`",
		"GRANT ON CLUSTER `main` SELECT ON `target`.`orders` TO `a\\`b`",
	}
	if !reflect.DeepEqual(conn.execs, want) {
		t.Fatalf("got %#v want %#v", conn.execs, want)
	}
}

func TestRoleReconcileEmptyGrants(t *testing.T) {
	conn := &testConnection{}
	service := &CHRoleService{Connection: conn}
	if err := service.Reconcile(context.Background(), RoleResource{Name: "empty"}, []*CHRole{{Grants: []Grant{{Database: "db", Table: "*", Privileges: []string{"SELECT"}}}}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(conn.execs, []string{"REVOKE SELECT ON `db`.* FROM `empty`"}) {
		t.Fatalf("unexpected SQL: %#v", conn.execs)
	}
}

func TestRoleSchemaAndGrantValidation(t *testing.T) {
	resource := ResourceRole()
	if err := resource.InternalValidate(nil, true); err != nil {
		t.Fatal(err)
	}
	if !resource.Schema["name"].ForceNew || !resource.Schema["cluster"].ForceNew {
		t.Fatal("role name and scope must be immutable")
	}
	if _, errors := grantpolicy.ValidatePrivilege("ALTER", "privilege"); len(errors) == 0 {
		t.Fatal("broad aliases must be rejected")
	}
	if grantpolicy.NormalizePrivilege(" select ") != "SELECT" {
		t.Fatal("privilege normalization failed")
	}
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"name": "role", "grants": []interface{}{
			map[string]interface{}{"database": "db", "table": "orders", "privileges": []interface{}{"SELECT"}},
			map[string]interface{}{"database": "db", "table": "*", "privileges": []interface{}{"SELECT"}},
		},
	})
	if _, err := desiredRole(data); err == nil {
		t.Fatal("redundant overlapping grant scopes were accepted")
	}
}

func TestRoleReplicaComparison(t *testing.T) {
	grants := []Grant{{Database: "db", Table: "*", Privileges: []string{"SELECT", "INSERT"}}}
	role := &CHRole{Name: "role", Grants: grants}
	if !rolesMatch([]*CHRole{role, role}, grants) {
		t.Fatal("identical replicas did not match")
	}
	if rolesMatch([]*CHRole{role, nil}, grants) || firstRole([]*CHRole{nil, role}) != role {
		t.Fatal("missing replicas were not represented as drift")
	}
	if rolesMatch([]*CHRole{role, {Name: "role"}}, grants) {
		t.Fatal("grant divergence was not detected")
	}
}

func TestResourceRoleMissingClearsState(t *testing.T) {
	conn := &testConnection{query: func(string, ...interface{}) (driver.Rows, error) { return &testRows{}, nil }}
	var connection driver.Conn = conn
	data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "missing", "grants": []interface{}{}})
	data.SetId(access.ResourceID("", "missing"))
	if diagnostics := resourceRoleRead(context.Background(), data, &common.ApiClient{ClickhouseConnection: &connection}); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if data.Id() != "" || len(conn.execs) != 0 {
		t.Fatal("refresh did not clear missing resource without writes")
	}
}

func TestResourceRoleCreateRejectsCollision(t *testing.T) {
	conn := &testConnection{query: func(query string, _ ...interface{}) (driver.Rows, error) {
		if strings.Contains(query, "system.roles") {
			return &testRows{values: [][]interface{}{{"existing", "local_directory"}}}, nil
		}
		return &testRows{}, nil
	}}
	var connection driver.Conn = conn
	data := schema.TestResourceDataRaw(t, ResourceRole().Schema, map[string]interface{}{"name": "existing", "grants": []interface{}{}})
	if diagnostics := resourceRoleCreate(context.Background(), data, &common.ApiClient{ClickhouseConnection: &connection}); !diagnostics.HasError() {
		t.Fatal("existing role was silently adopted")
	}
	if data.Id() != "" || len(conn.execs) != 0 {
		t.Fatal("collision registered ownership or executed SQL")
	}
}

func TestRoleReadRejectsAmbiguousIdentity(t *testing.T) {
	conn := &testConnection{query: func(query string, _ ...interface{}) (driver.Rows, error) {
		if strings.Contains(query, "system.users") {
			return &testRows{}, nil
		}
		if !strings.Contains(query, "system.roles") {
			t.Fatal("ambiguous identity must fail before inspecting privileges")
		}
		return &testRows{values: [][]interface{}{{"role", "local_directory"}, {"role", "users.xml"}}}, nil
	}}
	if _, err := (&CHRoleService{Connection: conn}).GetRole(context.Background(), "role"); err == nil {
		t.Fatal("ambiguous storage accepted")
	}
}

func TestExpandServerPrivilegeGroups(t *testing.T) {
	hierarchy := map[string][]string{"SHOW": {"SHOW DATABASES", "SHOW TABLES", "SHOW COLUMNS", "SHOW DICTIONARIES"}}
	leaves, err := grantpolicy.ExpandPrivilege("SHOW", hierarchy, map[string]bool{})
	if err != nil || len(leaves) != 4 {
		t.Fatalf("group did not expand: %v %v", leaves, err)
	}
	hierarchy["SHOW"] = append(hierarchy["SHOW"], "READ")
	if _, err := grantpolicy.ExpandPrivilege("SHOW", hierarchy, map[string]bool{}); err == nil {
		t.Fatal("group containing unsupported rights accepted")
	}
}
