package resourceuser_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/provider"
	resourceuser "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/resources/user"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func fixtureClient(t *testing.T, username, password string, clustered bool) *common.ApiClient {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC to run against the isolated access fixture")
	}
	host := os.Getenv("TF_CLICKHOUSE_HOST")
	if host != "ch1" && host != "127.0.0.1" && host != "localhost" {
		t.Fatal("acceptance tests require the isolated ch1 or loopback fixture")
	}
	port, err := strconv.Atoi(os.Getenv("TF_CLICKHOUSE_PORT"))
	if err != nil {
		t.Fatal("TF_CLICKHOUSE_PORT must be set")
	}
	replicas := []interface{}{}
	for _, host := range strings.Split(os.Getenv("TF_CLICKHOUSE_REPLICA_HOSTS"), ",") {
		if host != "" && clustered {
			replicas = append(replicas, host)
		}
	}
	p := provider.New("test")()
	d := schema.TestResourceDataRaw(t, p.Schema, map[string]interface{}{"host": host, "port": port, "username": username, "password": password, "replica_hosts": replicas, "verify_replicas": len(replicas) > 0})
	meta, diags := p.ConfigureContextFunc(context.Background(), d)
	if diags.HasError() {
		t.Fatal("fixture provider configuration failed")
	}
	client := meta.(*common.ApiClient)
	t.Cleanup(func() {
		(*client.ClickhouseConnection).Close()
		for _, conn := range client.ReplicaConnections {
			(*conn).Close()
		}
	})
	return client
}
func execute(t *testing.T, client *common.ApiClient, q string) {
	t.Helper()
	if err := (*client.ClickhouseConnection).Exec(context.Background(), q); err != nil {
		t.Fatal("fixture setup SQL failed")
	}
}
func checkLogin(t *testing.T, name, password string, want bool) {
	t.Helper()
	port, _ := strconv.Atoi(os.Getenv("TF_CLICKHOUSE_PORT"))
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{fmt.Sprintf("%s:%d", os.Getenv("TF_CLICKHOUSE_HOST"), port)}, Auth: clickhouse.Auth{Username: name, Password: password}})
	if err != nil {
		t.Fatal("could not construct fixture login")
	}
	defer conn.Close()
	err = conn.Ping(context.Background())
	if (err == nil) != want {
		t.Fatal("password login result differs from expected")
	}
}
func rawStrings(values []string) []interface{} {
	result := make([]interface{}, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func userData(t *testing.T, name, cluster, password string, roles, defaults []string) *schema.ResourceData {
	return schema.TestResourceDataRaw(t, resourceuser.ResourceUser().Schema, map[string]interface{}{"name": name, "cluster": cluster, "password": password, "roles": rawStrings(roles), "default_roles": rawStrings(defaults)})
}

func TestAccResourceUser(t *testing.T) {
	client := fixtureClient(t, "default", "", true)
	ctx := context.Background()
	r := resourceuser.ResourceUser()
	cluster := os.Getenv("TF_CLICKHOUSE_CLUSTER")
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "tf-user-`quoted-" + suffix
	reader := "tf-reader-" + suffix
	writer := "tf-writer-" + suffix
	clause := access.ClusterClause(cluster)
	execute(t, client, "CREATE ROLE "+access.QuoteIdentifier(reader)+clause)
	execute(t, client, "CREATE ROLE "+access.QuoteIdentifier(writer)+clause)
	t.Cleanup(func() {
		for _, role := range []string{reader, writer} {
			(*client.ClickhouseConnection).Exec(ctx, "DROP ROLE IF EXISTS "+access.QuoteIdentifier(role)+clause)
		}
	})
	t.Cleanup(func() {
		(*client.ClickhouseConnection).Exec(ctx, "DROP USER IF EXISTS "+access.QuoteIdentifier(name)+clause)
	})
	initial := userData(t, name, cluster, "Fixture-Initial-42!", []string{reader, writer}, []string{reader})
	if diags := r.CreateContext(ctx, initial, client); diags.HasError() {
		t.Fatal(diags)
	}
	checkLogin(t, name, "Fixture-Initial-42!", true)
	if initial.Id() != access.ResourceID(cluster, name) {
		t.Fatal("incorrect JSON resource identity")
	}
	if initial.Get("roles").(*schema.Set).Len() != 2 || initial.Get("default_roles").(*schema.Set).Len() != 1 {
		t.Fatal("assigned roles and defaults were conflated")
	}
	rotated := userData(t, name, cluster, "Fixture-Rotated-43!", []string{writer}, nil)
	rotated.SetId(initial.Id())
	if diags := r.UpdateContext(ctx, rotated, client); diags.HasError() {
		t.Fatal(diags)
	}
	checkLogin(t, name, "Fixture-Rotated-43!", true)
	checkLogin(t, name, "Fixture-Initial-42!", false)
	if rotated.Get("default_roles").(*schema.Set).Len() != 0 {
		t.Fatal("empty defaults were not preserved")
	}
	imported := schema.TestResourceDataRaw(t, r.Schema, nil)
	imported.SetId(rotated.Id())
	if _, err := r.Importer.StateContext(ctx, imported, client); err != nil {
		t.Fatal(err)
	}
	if diags := r.ReadContext(ctx, imported, client); diags.HasError() {
		t.Fatal(diags)
	}
	if imported.Get("name") != name || imported.Get("cluster") != cluster {
		t.Fatal("import did not restore identity")
	}
	if len(client.ReplicaConnections) > 0 && cluster != "" {
		replica := *client.ReplicaConnections[len(client.ReplicaConnections)-1]
		if err := replica.Exec(ctx, "REVOKE "+access.QuoteIdentifier(writer)+" FROM "+access.QuoteIdentifier(name)); err != nil {
			t.Fatal("could not introduce fixture replica drift")
		}
		if diags := r.ReadContext(ctx, rotated, client); diags.HasError() {
			t.Fatal(diags)
		}
		if rotated.Get("replicas_in_sync").(bool) {
			t.Fatal("replica drift was missed")
		}
		repair := userData(t, name, cluster, "Fixture-Rotated-43!", []string{writer}, nil)
		repair.SetId(initial.Id())
		if diags := r.UpdateContext(ctx, repair, client); diags.HasError() {
			t.Fatal(diags)
		}
		if !repair.Get("replicas_in_sync").(bool) {
			t.Fatal("replica repair did not converge")
		}
		if err := replica.Exec(ctx, "ALTER USER "+access.QuoteIdentifier(name)+" IDENTIFIED WITH sha256_password BY 'Fixture-Drift-44!'"); err != nil {
			t.Fatal("could not introduce fixture password drift")
		}
		if diags := r.ReadContext(ctx, repair, client); diags.HasError() {
			t.Fatal(diags)
		}
		if repair.Get("replicas_in_sync").(bool) {
			t.Fatal("replica password drift was missed")
		}
		if diags := r.UpdateContext(ctx, repair, client); diags.HasError() {
			t.Fatal(diags)
		}
	}
	empty := userData(t, name, cluster, "Fixture-Rotated-43!", nil, nil)
	empty.SetId(initial.Id())
	if diags := r.UpdateContext(ctx, empty, client); diags.HasError() {
		t.Fatal(diags)
	}
	if empty.Get("roles").(*schema.Set).Len() != 0 {
		t.Fatal("roles were not revoked")
	}
	if diags := r.DeleteContext(ctx, empty, client); diags.HasError() {
		t.Fatal(diags)
	}
	empty.SetId(initial.Id())
	if diags := r.ReadContext(ctx, empty, client); diags.HasError() || empty.Id() != "" {
		t.Fatal("missing user did not clear state")
	}
	empty.SetId(initial.Id())
	if diags := r.DeleteContext(ctx, empty, client); diags.HasError() {
		t.Fatal("deletion must be idempotent")
	}
}

func TestAccUserGrantDenialPreservesState(t *testing.T) {
	client := fixtureClient(t, "default", "", true)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	provisioner := "tf-provisioner-" + suffix
	name := "tf-partial-user-" + suffix
	role := "tf-denied-role-" + suffix
	execute(t, client, "CREATE USER "+access.QuoteIdentifier(provisioner)+" IDENTIFIED WITH sha256_password BY 'Fixture-Provisioner-41!'")
	execute(t, client, "GRANT SHOW ACCESS, CREATE USER, ALTER USER, DROP USER ON *.* TO "+access.QuoteIdentifier(provisioner))
	execute(t, client, "CREATE ROLE "+access.QuoteIdentifier(role))
	t.Cleanup(func() {
		(*client.ClickhouseConnection).Exec(ctx, "DROP USER IF EXISTS "+access.QuoteIdentifier(name))
		(*client.ClickhouseConnection).Exec(ctx, "DROP USER IF EXISTS "+access.QuoteIdentifier(provisioner))
		(*client.ClickhouseConnection).Exec(ctx, "DROP ROLE IF EXISTS "+access.QuoteIdentifier(role))
	})
	restricted := fixtureClient(t, provisioner, "Fixture-Provisioner-41!", false)
	d := userData(t, name, "", "Fixture-Partial-42!", []string{role}, nil)
	diags := resourceuser.ResourceUser().CreateContext(ctx, d, restricted)
	if !diags.HasError() || d.Id() == "" {
		t.Fatal("grant failure after create must retain partial resource state")
	}
	for _, diag := range diags {
		if strings.Contains(diag.Summary, "Fixture-") || strings.Contains(diag.Detail, "Fixture-") {
			t.Fatal("credential leaked in diagnostic")
		}
	}
}

func TestAccUserDirectGrants(t *testing.T) {
	client := fixtureClient(t, "default", "", true)
	ctx := context.Background()
	cluster := os.Getenv("TF_CLICKHOUSE_CLUSTER")
	suffix := fmt.Sprint(time.Now().UnixNano())
	name, role := "tf-direct-"+suffix, "tf-inherited-"+suffix
	clause := access.ClusterClause(cluster)
	execute(t, client, "CREATE ROLE "+access.QuoteIdentifier(role)+clause)
	execute(t, client, "GRANT"+clause+" SELECT ON system.one TO "+access.QuoteIdentifier(role))
	t.Cleanup(func() {
		(*client.ClickhouseConnection).Exec(ctx, "DROP USER IF EXISTS "+access.QuoteIdentifier(name)+clause)
		(*client.ClickhouseConnection).Exec(ctx, "DROP ROLE IF EXISTS "+access.QuoteIdentifier(role)+clause)
	})
	makeData := func(password, table string) *schema.ResourceData {
		d := userData(t, name, cluster, password, []string{role}, []string{role})
		if table != "" {
			if err := d.Set("grants", []interface{}{map[string]interface{}{"database": "system", "table": table, "privileges": []interface{}{"SELECT"}}}); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	r := resourceuser.ResourceUser()
	initial := makeData("Fixture-Direct-42!", "numbers")
	if diags := r.CreateContext(ctx, initial, client); diags.HasError() {
		t.Fatal(diags)
	}
	if initial.Get("grants").(*schema.Set).Len() != 1 {
		t.Fatal("direct grant was not persisted")
	}
	updated := makeData("Fixture-Direct-43!", "one")
	updated.SetId(initial.Id())
	if diags := r.UpdateContext(ctx, updated, client); diags.HasError() {
		t.Fatal(diags)
	}
	checkLogin(t, name, "Fixture-Direct-43!", true)
	checkLogin(t, name, "Fixture-Direct-42!", false)
	imported := schema.TestResourceDataRaw(t, r.Schema, nil)
	imported.SetId(initial.Id())
	if _, err := r.Importer.StateContext(ctx, imported, client); err != nil {
		t.Fatal(err)
	}
	if diags := r.ReadContext(ctx, imported, client); diags.HasError() {
		t.Fatal(diags)
	}
	if !imported.Get("grants").(*schema.Set).Equal(updated.Get("grants").(*schema.Set)) {
		t.Fatal("import did not restore direct privileges")
	}
	if len(client.ReplicaConnections) > 0 && cluster != "" {
		replica := *client.ReplicaConnections[len(client.ReplicaConnections)-1]
		if err := replica.Exec(ctx, "REVOKE SELECT ON system.one FROM "+access.QuoteIdentifier(name)); err != nil {
			t.Fatal(err)
		}
		if err := replica.Exec(ctx, "GRANT SELECT ON system.numbers TO "+access.QuoteIdentifier(name)); err != nil {
			t.Fatal(err)
		}
		if diags := r.ReadContext(ctx, updated, client); diags.HasError() {
			t.Fatal(diags)
		}
		if updated.Get("replicas_in_sync").(bool) {
			t.Fatal("direct grant replica drift was missed")
		}
		repair := makeData("Fixture-Direct-43!", "one")
		repair.SetId(initial.Id())
		if diags := r.UpdateContext(ctx, repair, client); diags.HasError() {
			t.Fatal(diags)
		}
		if !repair.Get("replicas_in_sync").(bool) {
			t.Fatal("direct grant replica repair did not converge")
		}
	}
	empty := makeData("Fixture-Direct-43!", "")
	empty.SetId(initial.Id())
	if diags := r.UpdateContext(ctx, empty, client); diags.HasError() {
		t.Fatal(diags)
	}
	if empty.Get("grants").(*schema.Set).Len() != 0 || empty.Get("roles").(*schema.Set).Len() != 1 || empty.Get("default_roles").(*schema.Set).Len() != 1 {
		t.Fatal("revoking direct privileges changed inherited role membership")
	}
}
