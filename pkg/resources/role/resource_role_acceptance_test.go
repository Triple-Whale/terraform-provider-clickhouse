package resourcerole_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	resourcerole "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/resources/role"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/testutils"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccResourceRole(t *testing.T) {
	cluster := os.Getenv("TF_CLICKHOUSE_CLUSTER")
	name := "terraform_access_role_test"
	resource.Test(t, resource.TestCase{
		ProviderFactories: roleProviders(),
		CheckDestroy: func(_ *terraform.State) error {
			client := testutils.TestAccProvider.Meta().(*common.ApiClient)
			connections, err := access.Connections(client, cluster)
			if err != nil {
				return err
			}
			for _, conn := range connections {
				service := resourcerole.CHRoleService{Connection: conn}
				role, err := service.GetRole(context.Background(), name)
				if err != nil {
					return err
				}
				if role != nil {
					return fmt.Errorf("role still exists after destruction")
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: roleConfig(cluster, name, `[{database="access_one",table="orders",privileges=["SELECT"]},{database="access_two",table="events",privileges=["INSERT"]}]`),
				Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("clickhouse_role.test", "grants.#", "2"), resource.TestCheckResourceAttr("clickhouse_role.test", "replicas_in_sync", "true"))},
			{ResourceName: "clickhouse_role.test", ImportState: true, ImportStateId: access.ResourceID(cluster, name), ImportStateVerify: true},
			{Config: roleConfig(cluster, name, `[{database="access_two",table="*",privileges=["SELECT","INSERT"]}]`), Check: resource.TestCheckResourceAttr("clickhouse_role.test", "grants.#", "1")},
			{Config: roleConfig(cluster, name, `[{database="access_two",table="orders",privileges=["SELECT"]}]`), Check: resource.TestCheckResourceAttr("clickhouse_role.test", "grants.#", "1")},
			{Config: roleConfig(cluster, name, `[]`), Check: resource.TestCheckResourceAttr("clickhouse_role.test", "grants.#", "0")},
			{Config: roleConfig(cluster, name, `[{database="access_one",table="orders",privileges=["SELECT"]}]`), Check: resource.TestCheckResourceAttr("clickhouse_role.test", "grants.#", "1")},
		},
	})
}

func TestAccRoleReplicaDrift(t *testing.T) {
	if os.Getenv("TF_ACC") == "" || os.Getenv("TF_CLICKHOUSE_CLUSTER") == "" {
		t.Skip("requires the isolated clustered acceptance fixture")
	}
	cluster := os.Getenv("TF_CLICKHOUSE_CLUSTER")
	name := "terraform_access_role_drift_test"
	config := roleConfig(cluster, name, `[{database="access_one",table="orders",privileges=["SELECT"]}]`)
	resource.Test(t, resource.TestCase{
		ProviderFactories: roleProviders(),
		Steps: []resource.TestStep{
			{Config: config},
			{Config: config, PreConfig: func() {
				client := testutils.TestAccProvider.Meta().(*common.ApiClient)
				conn := client.ReplicaConnections[len(client.ReplicaConnections)-1]
				if err := (*conn).Exec(context.Background(), "DROP ROLE "+access.QuoteIdentifier(name)); err != nil {
					t.Fatal(err)
				}
			}, Check: resource.TestCheckResourceAttr("clickhouse_role.test", "replicas_in_sync", "true")},
			{Config: config, PreConfig: func() {
				client := testutils.TestAccProvider.Meta().(*common.ApiClient)
				conn := client.ReplicaConnections[len(client.ReplicaConnections)-1]
				if err := (*conn).Exec(context.Background(), "GRANT INSERT ON access_one.orders TO "+access.QuoteIdentifier(name)); err != nil {
					t.Fatal(err)
				}
			}, Check: resource.TestCheckResourceAttr("clickhouse_role.test", "replicas_in_sync", "true")},
		},
	})
}

func roleConfig(cluster, name, grants string) string {
	providerConfig := ""
	if cluster != "" {
		hosts := strings.Split(os.Getenv("TF_CLICKHOUSE_REPLICA_HOSTS"), ",")
		encoded, _ := json.Marshal(hosts)
		providerConfig = fmt.Sprintf("provider \"clickhouse\" {\n verify_replicas=true\n replica_hosts=%s\n}\n", encoded)
	}
	return providerConfig + fmt.Sprintf("resource \"clickhouse_role\" \"test\" {\n name=%q\n cluster=%q\n grants=%s\n}\n", name, cluster, grants)
}

func roleProviders() map[string]func() (*schema.Provider, error) {
	return map[string]func() (*schema.Provider, error){
		"clickhouse": func() (*schema.Provider, error) { return testutils.TestAccProvider, nil },
	}
}

func TestAccRoleAtomicPrivileges(t *testing.T) {
	cluster := os.Getenv("TF_CLICKHOUSE_CLUSTER")
	encoded, _ := json.Marshal(resourcerole.AllowedPrivileges)
	grants := fmt.Sprintf(`[{database="*",table="*",privileges=%s}]`, encoded)
	resource.Test(t, resource.TestCase{
		ProviderFactories: roleProviders(),
		Steps: []resource.TestStep{{
			Config: roleConfig(cluster, "terraform_access_role_atomic_test", grants),
			Check:  resource.TestCheckResourceAttr("clickhouse_role.test", "replicas_in_sync", "true"),
		}},
	})
}
