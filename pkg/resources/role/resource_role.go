package resourcerole

import (
	"context"
	"fmt"
	"time"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	grantpolicy "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
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
			_, err := grantpolicy.Decode(diff.Get("grants").(*schema.Set))
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
			"grants": grantpolicy.Schema(true),
		},
	}
}

func desiredRole(data *schema.ResourceData) (RoleResource, error) {
	grants, err := grantpolicy.Decode(data.Get("grants").(*schema.Set))
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
	if err := data.Set("grants", grantpolicy.State(role.Grants)); err != nil {
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
	if err := data.Set("grants", grantpolicy.State(role.Grants)); err != nil {
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
