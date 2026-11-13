package resourceuser

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type userRows struct {
	values   [][]interface{}
	position int
	closed   bool
	failure  error
}

func (r *userRows) Next() bool { r.position++; return r.position <= len(r.values) }
func (r *userRows) Scan(dest ...interface{}) error {
	for i, v := range r.values[r.position-1] {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}
func (r *userRows) ScanStruct(interface{}) error     { return errors.New("unused") }
func (r *userRows) ColumnTypes() []driver.ColumnType { return nil }
func (r *userRows) Totals(...interface{}) error      { return nil }
func (r *userRows) Columns() []string                { return nil }
func (r *userRows) Close() error                     { r.closed = true; return nil }
func (r *userRows) Err() error                       { return r.failure }

type userConn struct {
	driver.Conn
	user          *CHUser
	queries       []string
	executed      []string
	failPrefix    string
	rows          []*userRows
	rowFailure    error
	grantRows     [][]interface{}
	grantSQL      []string
	roleCollision bool
}

func (c *userConn) Query(ctx context.Context, q string, args ...interface{}) (driver.Rows, error) {
	c.queries = append(c.queries, q)
	r := &userRows{failure: c.rowFailure}
	if c.user != nil {
		if strings.Contains(q, "FROM system.users") {
			u := c.user
			r.values = [][]interface{}{{u.Name, u.Storage, u.AuthTypes, u.DefaultRolesAll, u.DefaultRoles, u.DefaultRolesExcept}}
		} else if strings.Contains(q, "FROM system.role_grants") {
			for _, role := range c.user.Roles {
				var admin uint8
				if includes(c.user.AdminRoles, role) {
					admin = 1
				}
				r.values = append(r.values, []interface{}{role, admin})
			}
		}
	}
	if strings.Contains(q, "FROM system.roles") && c.roleCollision {
		r.values = [][]interface{}{{"svc-user"}}
	}
	if strings.HasPrefix(q, "SHOW GRANTS FOR ") {
		for _, statement := range c.grantSQL {
			r.values = append(r.values, []interface{}{statement})
		}
	}
	if strings.Contains(q, "FROM system.grants") {
		if !strings.Contains(q, "WHERE user_name = ?") || len(args) != 1 {
			return nil, errors.New("direct grants must be scoped to the user")
		}
		if c.grantRows != nil {
			r.values = c.grantRows
		} else if c.user != nil {
			for _, grant := range c.user.Grants {
				var databaseNull, tableNull uint8
				if grant.Database == "*" {
					databaseNull = 1
				}
				if grant.Table == "*" {
					tableNull = 1
				}
				for _, privilege := range grant.Privileges {
					r.values = append(r.values, []interface{}{privilege, grant.Database, grant.Table, "", uint8(0), uint8(0), databaseNull, tableNull})
				}
			}
		}
	}
	c.rows = append(c.rows, r)
	return r, nil
}
func (c *userConn) Exec(ctx context.Context, q string, args ...interface{}) error {
	c.executed = append(c.executed, q)
	if c.failPrefix != "" && strings.HasPrefix(q, c.failPrefix) {
		return errors.New("driver echoed secret-password and hash " + passwordHash("secret-password"))
	}
	if strings.HasPrefix(q, "CREATE USER") {
		c.user = &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}}
	}
	return nil
}
func testUser() *CHUser {
	return &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}, Roles: []string{"old", "keep"}, DefaultRoles: []string{"old"}, AdminRoles: []string{"keep"}}
}

func TestUserReconcileSeparatesMembershipDefaultsAndDelegation(t *testing.T) {
	conn := &userConn{user: testUser()}
	svc := CHUserService{Connections: []access.Connection{conn}}
	err := svc.Reconcile(context.Background(), UserResource{Name: "svc-user", Password: "secret-password", Roles: []string{"keep", "new-role"}, DefaultRoles: []string{"new-role"}})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(conn.executed, "\n")
	for _, want := range []string{"REVOKE `old` FROM `svc-user`", "REVOKE ADMIN OPTION FOR `keep` FROM `svc-user`", "GRANT `keep`, `new-role` TO `svc-user`", "DEFAULT ROLE `new-role`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing expected operation %s", want)
		}
	}
	if strings.Contains(got, "secret-password") || strings.Contains(got, "OR REPLACE") {
		t.Fatal("unsafe password or replace SQL")
	}
	if !strings.Contains(got, "sha256_hash BY '"+passwordHash("secret-password")+"'") {
		t.Fatal("password was not client hashed")
	}
	for _, row := range conn.rows {
		if !row.closed {
			t.Fatal("read response not closed")
		}
	}
}
func TestEmptyRolesUseNone(t *testing.T) {
	conn := &userConn{user: testUser()}
	svc := CHUserService{Connections: []access.Connection{conn}}
	if err := svc.Reconcile(context.Background(), UserResource{Name: "svc-user", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	last := conn.executed[len(conn.executed)-1]
	if last != "ALTER USER `svc-user` DEFAULT ROLE NONE" {
		t.Fatal("empty defaults must be NONE")
	}
	for _, q := range conn.executed {
		if strings.HasPrefix(q, "GRANT") {
			t.Fatal("empty roles must not grant")
		}
	}
}
func TestCreateCollisionAndPartialGrantFailure(t *testing.T) {
	for _, collision := range []bool{true, false} {
		conn := &userConn{failPrefix: "GRANT"}
		if collision {
			conn.user = testUser()
		}
		svc := CHUserService{Connections: []access.Connection{conn}}
		created := false
		err := svc.Create(context.Background(), UserResource{Name: "svc-user", Password: "secret-password", Roles: []string{"reader"}}, func() { created = true })
		if err == nil {
			t.Fatal("expected failure")
		}
		if created == collision {
			t.Fatal("collision must preserve no ID; partial grant failure must preserve ID")
		}
		if collision && len(conn.executed) != 0 {
			t.Fatal("collision mutated user")
		}
		if strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), passwordHash("secret-password")) {
			t.Fatal("database diagnostic leaked credentials")
		}
	}
}
func TestClusterCreateTimeoutRetainsOwnership(t *testing.T) {
	conn := &userConn{failPrefix: "CREATE"}
	svc := CHUserService{Connections: []access.Connection{conn}}
	created := false
	if err := svc.Create(context.Background(), UserResource{Name: "svc-user", Cluster: "main", Password: "p"}, func() { created = true }); err == nil || !created {
		t.Fatal("uncertain cluster create must retain ID")
	}
}
func TestRejectXMLAndRowErrors(t *testing.T) {
	conn := &userConn{user: testUser()}
	conn.user.Storage = "users_xml"
	svc := CHUserService{Connections: []access.Connection{conn}}
	if _, err := svc.ReadAll(context.Background(), "svc-user"); err == nil {
		t.Fatal("XML user must be rejected")
	}
	conn.user = nil
	conn.rowFailure = errors.New("database response failed")
	if _, err := svc.ReadAll(context.Background(), "svc-user"); err == nil {
		t.Fatal("row failure must not become absence")
	}
}
func TestAllExceptAndAdminOptionsAreDrift(t *testing.T) {
	user := testUser()
	user.DefaultRolesAll = 1
	user.DefaultRolesExcept = []string{"old"}
	if !equalStrings(user.effectiveDefaults(), []string{"keep"}) {
		t.Fatal("ALL EXCEPT effective defaults incorrect")
	}
	if user.canonical() {
		t.Fatal("unsupported default/admin semantics must require normalization")
	}
	if err := validatePlan(UserResource{Roles: []string{"a"}, DefaultRoles: []string{"b"}}); err == nil {
		t.Fatal("default roles must be assigned")
	}
}
func TestUserReadPasswordDriftAndMissingState(t *testing.T) {
	conn := &userConn{user: testUser()}
	conn.user.AdminRoles = nil
	var driverConn driver.Conn = conn
	client := &common.ApiClient{ClickhouseConnection: &driverConn, CheckUserPassword: func(context.Context, string, string, string) error { return common.ErrUserPasswordMismatch }}
	data := schema.TestResourceDataRaw(t, ResourceUser().Schema, map[string]interface{}{"name": "svc-user", "password": "p"})
	data.SetId(access.ResourceID("", "svc-user"))
	if diags := resourceUserRead(context.Background(), data, client); diags.HasError() {
		t.Fatal(diags)
	}
	if data.Get("replicas_in_sync").(bool) {
		t.Fatal("password mismatch must produce drift")
	}
	if len(conn.executed) > 0 {
		t.Fatal("refresh must not mutate")
	}
	client.CheckUserPassword = func(context.Context, string, string, string) error { return errors.New("network unavailable") }
	if diags := resourceUserRead(context.Background(), data, client); !diags.HasError() || data.Id() == "" {
		t.Fatal("unreachable password check must retain resource and fail")
	}
	conn.user = nil
	if diags := resourceUserRead(context.Background(), data, client); diags.HasError() || data.Id() != "" {
		t.Fatal("all replicas absent must clear ID")
	}
}
func TestUserSchema(t *testing.T) {
	resource := ResourceUser()
	if !resource.Schema["password"].Sensitive || !resource.Schema["name"].ForceNew || !resource.Schema["cluster"].ForceNew {
		t.Fatal("sensitive password and immutable identity required")
	}
	if _, errs := resource.Schema["password"].ValidateFunc("", "password"); len(errs) != 1 {
		t.Fatal("empty password accepted")
	}
	if _, errs := resource.Schema["password"].ValidateFunc("secret-password", "password"); len(errs) != 0 {
		t.Fatal("valid password rejected")
	}
}

func TestUserReadDirectPrivileges(t *testing.T) {
	conn := &userConn{user: testUser()}
	conn.user.Grants = []grants.Grant{{Database: "db", Table: "*", Privileges: []string{"SELECT"}}}
	service := CHUserService{Connections: []access.Connection{conn}}
	users, err := service.ReadAll(context.Background(), "svc-user")
	if err != nil || !grants.Equal(users[0].Grants, conn.user.Grants) {
		t.Fatalf("direct privileges were not read: %v", err)
	}
	if len(conn.executed) != 0 {
		t.Fatal("reading direct grants must not mutate")
	}
}

func TestUserDirectGrantReconciliation(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint("empty=", empty), func(t *testing.T) {
			first := &userConn{user: &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}, Grants: []grants.Grant{{Database: "old", Table: "*", Privileges: []string{"SELECT"}}}}}
			second := &userConn{user: &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}, Grants: []grants.Grant{{Database: "extra", Table: "orders", Privileges: []string{"INSERT"}}}}}
			plan := UserResource{Name: "svc-user", Cluster: "main", Password: "secret-password", Roles: []string{"reader"}, DefaultRoles: []string{"reader"}}
			if !empty {
				plan.Grants = []grants.Grant{{Database: "target", Table: "orders", Privileges: []string{"SELECT"}}}
			}
			service := CHUserService{Connections: []access.Connection{first, second}}
			if err := service.Reconcile(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(first.executed, "\n")
			for _, want := range []string{"REVOKE ON CLUSTER `main` SELECT ON `old`.* FROM `svc-user`", "REVOKE ON CLUSTER `main` INSERT ON `extra`.`orders` FROM `svc-user`", "GRANT ON CLUSTER `main` `reader` TO `svc-user`", "DEFAULT ROLE `reader`"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing operation %s in %s", want, got)
				}
			}
			if strings.Contains(got, "GRANT ON CLUSTER `main` SELECT ON `target`.`orders` TO `svc-user`") == empty {
				t.Fatal("desired direct grants were not reconciled")
			}
			if len(second.executed) != 0 || strings.Contains(got, "CREATE ROLE") {
				t.Fatal("must use distributed grants without creating roles")
			}
		})
	}
}

func TestUserDirectGrantDrift(t *testing.T) {
	user := &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}, Grants: []grants.Grant{{Database: "db", Table: "*", Privileges: []string{"SELECT"}}}}
	plan := UserResource{Name: user.Name, Grants: user.Grants}
	if !matchesPlan(user, plan) || !sameUser(user, user) {
		t.Fatal("equal grants should converge")
	}
	other := *user
	other.Grants = nil
	if matchesPlan(&other, plan) || sameUser(user, &other) {
		t.Fatal("direct grant drift was missed")
	}
	conn := &userConn{user: user}
	var driverConn driver.Conn = conn
	client := &common.ApiClient{ClickhouseConnection: &driverConn, CheckUserPassword: func(context.Context, string, string, string) error { return nil }}
	data := schema.TestResourceDataRaw(t, ResourceUser().Schema, map[string]interface{}{"name": user.Name, "password": "p"})
	data.SetId(access.ResourceID("", user.Name))
	if diags := resourceUserRead(context.Background(), data, client); diags.HasError() {
		t.Fatal(diags)
	}
	observed, err := planFromData(data)
	if err != nil || !grants.Equal(observed.Grants, user.Grants) || len(conn.executed) != 0 {
		t.Fatal("refresh must record direct grants without writing")
	}
}

func TestUserRejectsUnsupportedDirectGrantsAndCollisions(t *testing.T) {
	for _, column := range []string{"column", "grant option", "partial revoke", "prefix", "literal star", "collision"} {
		t.Run(column, func(t *testing.T) {
			conn := &userConn{user: testUser()}
			row := []interface{}{"SELECT", "db", "orders", "", uint8(0), uint8(0), uint8(0), uint8(0)}
			switch column {
			case "column":
				row[3] = "secret"
			case "grant option":
				row[5] = uint8(1)
			case "partial revoke":
				row[4] = uint8(1)
			case "prefix":
				conn.grantSQL = []string{"GRANT SELECT ON db.orders* TO `svc-user`"}
			case "literal star":
				row[1] = "*"
			case "collision":
				conn.roleCollision = true
			}
			conn.grantRows = [][]interface{}{row}
			service := CHUserService{Connections: []access.Connection{conn}}
			if _, err := service.ReadAll(context.Background(), "svc-user"); err == nil || len(conn.executed) != 0 {
				t.Fatal("unsupported grants/ownership must fail without mutations")
			}
		})
	}
}

func TestUserDirectGrantFailureRetainsState(t *testing.T) {
	conn := &userConn{failPrefix: "GRANT SELECT ON"}
	service := CHUserService{Connections: []access.Connection{conn}}
	retained := false
	err := service.Create(context.Background(), UserResource{Name: "svc-user", Password: "secret-password", Grants: []grants.Grant{{Database: "db", Table: "*", Privileges: []string{"SELECT"}}}}, func() { retained = true })
	if err == nil || !retained {
		t.Fatal("direct grant failure must retain created user identity")
	}
	if strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), passwordHash("secret-password")) {
		t.Fatal("direct grant diagnostics leaked credentials")
	}
}

func TestUserGrantSchemaValidation(t *testing.T) {
	resource := ResourceUser()
	if err := resource.InternalValidate(nil, true); err != nil {
		t.Fatal(err)
	}
	if !resource.Schema["grants"].Optional || resource.Schema["grants"].Computed {
		t.Fatal("user direct grants must be optional and authoritative")
	}
	data := schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{"name": "svc-user", "password": "p", "grants": []interface{}{
		map[string]interface{}{"database": "db", "table": "orders", "privileges": []interface{}{"SELECT"}},
		map[string]interface{}{"database": "db", "table": "*", "privileges": []interface{}{"SELECT"}},
	}})
	if _, err := planFromData(data); err == nil {
		t.Fatal("overlapping direct scopes were accepted")
	}
}
