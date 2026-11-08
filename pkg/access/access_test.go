package access

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func TestQuotingAndIdentifiers(t *testing.T) {
	if got := QuoteIdentifier("a`b\\c"); got != "`a\\`b\\\\c`" {
		t.Fatalf("identifier: %s", got)
	}
	if got := QuoteString("a'b\\c\n"); got != "'a\\'b\\\\c\\n'" {
		t.Fatalf("string: %s", got)
	}
	if ClusterClause("") != "" || ClusterClause("main") != " ON CLUSTER `main`" {
		t.Fatal("cluster clause failed")
	}
	for _, name := range []string{"", "bad\x00name", "bad\nname", string([]byte{0xff})} {
		if ValidateIdentifier(name) == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
}

func TestImportIDs(t *testing.T) {
	id := ResourceID("main", `name:with/slash"and\\backslash`)
	cluster, name, err := ParseResourceID(id)
	if err != nil || cluster != "main" || name != `name:with/slash"and\\backslash` {
		t.Fatalf("bad round-trip %q %q %v", cluster, name, err)
	}
	for _, id := range []string{"plain-name", `[]`, `["main"]`, `["","name","extra"]`, `["",""]`, `[null,"name"]`, `["main",1]`, `["main","name"] trailing`} {
		if _, _, err := ParseResourceID(id); err == nil {
			t.Fatalf("accepted malformed ID %s", id)
		}
	}
}

type mockRows struct {
	driver.Rows
	values [][]interface{}
	index  int
}

func (rows *mockRows) Next() bool { return rows.index < len(rows.values) }
func (rows *mockRows) Scan(values ...interface{}) error {
	if len(values) != len(rows.values[rows.index]) {
		return fmt.Errorf("scan arity mismatch")
	}
	for i, value := range values {
		reflect.ValueOf(value).Elem().Set(reflect.ValueOf(rows.values[rows.index][i]))
	}
	rows.index++
	return nil
}
func (rows *mockRows) Err() error   { return nil }
func (rows *mockRows) Close() error { return nil }

type mockConnection struct {
	driver.Conn
	members []string
	local   []string
}

func (conn *mockConnection) Query(_ context.Context, query string, _ ...interface{}) (driver.Rows, error) {
	hosts := conn.members
	if strings.Contains(query, "is_local") {
		hosts = conn.local
	}
	values := [][]interface{}{}
	for _, host := range hosts {
		values = append(values, []interface{}{host, uint16(9000)})
	}
	return &mockRows{values: values}, nil
}

func TestClusterCoverage(t *testing.T) {
	a := &mockConnection{members: []string{"10.0.0.1", "10.0.0.2"}, local: []string{"10.0.0.1"}}
	b := &mockConnection{members: a.members, local: []string{"10.0.0.2"}}
	if err := VerifyCluster(context.Background(), []Connection{a, a, b}, "main"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCluster(context.Background(), []Connection{a, a}, "main"); err == nil {
		t.Fatal("duplicate connections must not count as complete coverage")
	}
	if err := VerifyCluster(context.Background(), []Connection{a, &mockConnection{local: []string{"10.9.9.9"}}}, "main"); err == nil {
		t.Fatal("foreign member accepted")
	}
	if err := VerifyCluster(context.Background(), nil, ""); err != nil {
		t.Fatal("local resources should not inspect clusters")
	}
}

func TestConnectionScope(t *testing.T) {
	var primary driver.Conn = &mockConnection{}
	var replica driver.Conn = &mockConnection{}
	client := &common.ApiClient{ClickhouseConnection: &primary, ReplicaConnections: []*driver.Conn{&replica}, DefaultCluster: "ignored"}
	local, err := Connections(client, "")
	if err != nil || len(local) != 1 {
		t.Fatalf("local target used replica/default cluster: %v %v", local, err)
	}
	if _, err := Connections(client, "main"); err == nil {
		t.Fatal("cluster target accepted without replica verification")
	}
	client.VerifyReplicas = true
	cluster, err := Connections(client, "main")
	if err != nil || len(cluster) != 2 {
		t.Fatalf("cluster connections: %v %v", cluster, err)
	}
}

func TestWaitIsBounded(t *testing.T) {
	if err := Wait(context.Background(), time.Second, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx, time.Second, func(context.Context) (bool, error) { t.Fatal("called after cancellation"); return true, nil }); err == nil {
		t.Fatal("cancellation was ignored")
	}
}
