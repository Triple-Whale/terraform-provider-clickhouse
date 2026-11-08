package resourcerole

import (
	"context"
	"fmt"
	"time"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func ResourceRole() *schema.Resource {
	return &schema.Resource{
		Description:   "Manage a SQL role and its complete set of explicit database/table grants.",
		CreateContext: resourceRoleCreate,
		ReadContext:   resourceRoleRead,
		UpdateContext: resourceRoleUpdate,
		DeleteContext: resourceRoleDelete,
		Importer:      &schema.ResourceImporter{StateContext: access.ImportState},
		CustomizeDiff: func(_ context.Context, diff *schema.ResourceDiff, _ interface{}) error {
			if !diff.NewValueKnown("grants") {
				return nil
			}
			_, err := decodeGrants(diff.Get("grants").(*schema.Set))
			return err
		},
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(3 * time.Minute),
			Read:   schema.DefaultTimeout(time.Minute),
			Update: schema.DefaultTimeout(3 * time.Minute),
			Delete: schema.DefaultTimeout(3 * time.Minute),
		},
		Schema: map[string]*schema.Schema{
			"name": {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validateName, Description: "Immutable SQL role name."},
			"cluster": {Type: schema.TypeString, Optional: true, Default: "", ForceNew: true, ValidateFunc: validateCluster,
				Description: "Explicit immutable ON CLUSTER target. Empty means local; provider default_cluster is not inherited."},
			"replicas_in_sync": {Type: schema.TypeBool, Optional: true, Default: true, ValidateFunc: validateTrue,
				Description: "Must remain true in configuration. Refresh records false when replicas diverge, scheduling reconciliation."},
			"grants": {Type: schema.TypeSet, Required: true, ConfigMode: schema.SchemaConfigModeAttr, Description: "Complete set of direct grants. Use an empty set for a role with no privileges.", Elem: &schema.Resource{Schema: map[string]*schema.Schema{
				"database": {Type: schema.TypeString, Required: true, ValidateFunc: validateScope, Description: "Database name, or * for every database."},
				"table":    {Type: schema.TypeString, Required: true, ValidateFunc: validateScope, Description: "Explicit table name, or * for every table."},
				"privileges": {Type: schema.TypeSet, Required: true, MinItems: 1, Description: "Atomic privileges, such as SELECT or INSERT; aliases and groups such as ALL and ALTER are unsupported.",
					Elem: &schema.Schema{Type: schema.TypeString, StateFunc: normalizePrivilege, ValidateFunc: validatePrivilege}},
			}}},
		},
	}
}

func decodeGrants(set *schema.Set) ([]Grant, error) {
	grants := []Grant{}
	seen := map[[2]string]bool{}
	count := 0
	for _, value := range set.List() {
		entry := value.(map[string]interface{})
		database, table := entry["database"].(string), entry["table"].(string)
		if err := validateGrantScope(database, table); err != nil {
			return nil, err
		}
		key := [2]string{database, table}
		if seen[key] {
			return nil, fmt.Errorf("combine privileges for database %q table %q into one grants entry", database, table)
		}
		seen[key] = true
		grant := Grant{Database: database, Table: table}
		for _, privilege := range entry["privileges"].(*schema.Set).List() {
			if _, errors := validatePrivilege(privilege, "privileges"); len(errors) != 0 {
				return nil, errors[0]
			}
			grant.Privileges = append(grant.Privileges, normalizePrivilege(privilege))
			count++
		}
		grants = append(grants, grant)
	}
	canonical := permissions(grants)
	if len(canonical) != count {
		return nil, fmt.Errorf("grants contain redundant overlapping scopes for the same privilege")
	}
	return grantsFromPermissions(canonical), nil
}

func validateGrantScope(database, table string) error {
	for key, value := range map[string]string{"database": database, "table": table} {
		if _, errors := validateScope(value, key); len(errors) != 0 {
			return errors[0]
		}
	}
	if database == "*" && table != "*" {
		return fmt.Errorf("a wildcard database requires a wildcard table")
	}
	return nil
}

func desiredRole(data *schema.ResourceData) (RoleResource, error) {
	grants, err := decodeGrants(data.Get("grants").(*schema.Set))
	return RoleResource{Name: data.Get("name").(string), Cluster: data.Get("cluster").(string), Grants: grants}, err
}

func roleConnections(ctx context.Context, data *schema.ResourceData, meta interface{}) ([]access.Connection, error) {
	client, ok := meta.(*common.ApiClient)
	if !ok {
		return nil, fmt.Errorf("ClickHouse provider is not configured")
	}
	cluster := data.Get("cluster").(string)
	connections, err := access.Connections(client, cluster)
	if err != nil {
		return nil, err
	}
	if err := access.VerifyCluster(ctx, connections, cluster); err != nil {
		return nil, err
	}
	return connections, nil
}

func resourceRoleRead(ctx context.Context, data *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, data.Timeout(schema.TimeoutRead))
	defer cancel()
	connections, err := roleConnections(ctx, data, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	roles, err := readRoles(ctx, connections, data.Get("name").(string))
	if err != nil {
		return diag.FromErr(err)
	}
	role := firstRole(roles)
	if role == nil {
		data.SetId("")
		return nil
	}
	if err := data.Set("grants", stateGrants(role.Grants)); err != nil {
		return diag.FromErr(err)
	}
	if err := data.Set("replicas_in_sync", rolesMatch(roles, role.Grants)); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

func resourceRoleCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, data.Timeout(schema.TimeoutCreate))
	defer cancel()
	role, err := desiredRole(data)
	if err != nil {
		return diag.FromErr(err)
	}
	connections, err := roleConnections(ctx, data, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	actual, err := readRoles(ctx, connections, role.Name)
	if err != nil {
		return diag.FromErr(err)
	}
	if firstRole(actual) != nil {
		return diag.Errorf("role %q already exists; import it explicitly before managing it", role.Name)
	}
	service := CHRoleService{Connection: connections[0]}
	if err := service.CreateRole(ctx, role, false); err != nil {
		if access.IsCreateCollision(err) {
			return diag.Errorf("role creation encountered an existing name; ownership was not recorded; inspect all replicas and import explicitly before managing it")
		}
		if access.CreateMayHaveSucceeded(err, role.Cluster) {
			data.SetId(access.ResourceID(role.Cluster, role.Name))
			return diag.Errorf("role creation may have partially completed; state retains ownership for reconciliation: %v", err)
		}
		return diag.FromErr(err)
	}
	data.SetId(access.ResourceID(role.Cluster, role.Name))
	return reconcileRole(ctx, data, connections, role)
}

func resourceRoleUpdate(ctx context.Context, data *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, data.Timeout(schema.TimeoutUpdate))
	defer cancel()
	role, err := desiredRole(data)
	if err != nil {
		return diag.FromErr(err)
	}
	connections, err := roleConnections(ctx, data, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	return reconcileRole(ctx, data, connections, role)
}

func reconcileRole(ctx context.Context, data *schema.ResourceData, connections []access.Connection, role RoleResource) diag.Diagnostics {
	actual, err := readRoles(ctx, connections, role.Name)
	if err != nil {
		return diag.FromErr(err)
	}
	service := CHRoleService{Connection: connections[0]}
	if err := service.Reconcile(ctx, role, actual); err != nil {
		return diag.FromErr(err)
	}
	if err := access.Wait(ctx, time.Minute, func(ctx context.Context) (bool, error) {
		actual, err := readRoles(ctx, connections, role.Name)
		return rolesMatch(actual, role.Grants), err
	}); err != nil {
		return diag.FromErr(err)
	}
	if err := data.Set("grants", stateGrants(role.Grants)); err != nil {
		return diag.FromErr(err)
	}
	if err := data.Set("replicas_in_sync", true); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

func resourceRoleDelete(ctx context.Context, data *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, data.Timeout(schema.TimeoutDelete))
	defer cancel()
	connections, err := roleConnections(ctx, data, meta)
	if err != nil {
		return diag.FromErr(err)
	}
	role := RoleResource{Name: data.Get("name").(string), Cluster: data.Get("cluster").(string)}
	if _, err := readRoles(ctx, connections, role.Name); err != nil {
		return diag.FromErr(err)
	}
	service := CHRoleService{Connection: connections[0]}
	if err := service.DeleteRole(ctx, role); err != nil {
		return diag.FromErr(err)
	}
	if err := access.Wait(ctx, time.Minute, func(ctx context.Context) (bool, error) {
		actual, err := readRoles(ctx, connections, role.Name)
		return firstRole(actual) == nil, err
	}); err != nil {
		return diag.FromErr(err)
	}
	data.SetId("")
	return nil
}

func stateGrants(grants []Grant) []interface{} {
	values := make([]interface{}, 0, len(grants))
	for _, grant := range grants {
		values = append(values, map[string]interface{}{"database": grant.Database, "table": grant.Table, "privileges": grant.Privileges})
	}
	return values
}
