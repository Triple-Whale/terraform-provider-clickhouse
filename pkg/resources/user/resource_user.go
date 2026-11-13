package resourceuser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func ResourceUser() *schema.Resource {
	identifier := func(v interface{}, k string) ([]string, []error) {
		if err := access.ValidateIdentifier(v.(string)); err != nil {
			return nil, []error{fmt.Errorf("%s: %w", k, err)}
		}
		return nil, nil
	}
	roleSet := func(description string) *schema.Schema {
		return &schema.Schema{Description: description, Type: schema.TypeSet, Optional: true, Elem: &schema.Schema{Type: schema.TypeString, ValidateFunc: identifier}}
	}
	return &schema.Resource{
		Description:   "Manage a SQL user, its password, direct grants, exact assigned roles, and explicit default roles.",
		CreateContext: resourceUserCreate, ReadContext: resourceUserRead, UpdateContext: resourceUserUpdate, DeleteContext: resourceUserDelete,
		Importer: &schema.ResourceImporter{StateContext: access.ImportState},
		Timeouts: &schema.ResourceTimeout{Create: schema.DefaultTimeout(2 * time.Minute), Update: schema.DefaultTimeout(2 * time.Minute), Delete: schema.DefaultTimeout(2 * time.Minute), Read: schema.DefaultTimeout(2 * time.Minute)},
		CustomizeDiff: func(ctx context.Context, d *schema.ResourceDiff, meta interface{}) error {
			if d.NewValueKnown("grants") {
				if _, err := grants.Decode(d.Get("grants").(*schema.Set)); err != nil {
					return err
				}
			}
			if d.NewValueKnown("roles") && d.NewValueKnown("default_roles") {
				return validatePlan(UserResource{Roles: setStrings(d.Get("roles").(*schema.Set)), DefaultRoles: setStrings(d.Get("default_roles").(*schema.Set))})
			}
			return nil
		},
		Schema: map[string]*schema.Schema{
			"name": {Description: "SQL user name. Changing it replaces the resource.", Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: identifier},
			"cluster": {Description: "Explicit SQL ON CLUSTER target. Empty manages the connected server only and ignores provider.default_cluster. Changing it replaces the resource.", Type: schema.TypeString, Optional: true, Default: "", ForceNew: true, ValidateFunc: func(v interface{}, k string) ([]string, []error) {
				if v.(string) == "" {
					return nil, nil
				}
				return identifier(v, k)
			}},
			"password": {Description: "User password; retained as sensitive state and never sent as plaintext SQL.", Type: schema.TypeString, Required: true, Sensitive: true, ValidateFunc: func(v interface{}, k string) ([]string, []error) {
				if v.(string) == "" {
					return nil, []error{errors.New("password must not be empty")}
				}
				return nil, nil
			}},
			"roles":         roleSet("Exact assigned roles without ADMIN OPTION; independent from defaults."),
			"grants":        grants.Schema(false),
			"default_roles": roleSet("Explicit default roles, each also assigned in roles. Empty means DEFAULT ROLE NONE."),
			"replicas_in_sync": {Description: "Desired true. Refresh sets false on metadata or password divergence; apply repairs it. Must not be configured false.", Type: schema.TypeBool, Optional: true, Default: true, ValidateFunc: func(v interface{}, k string) ([]string, []error) {
				if !v.(bool) {
					return nil, []error{errors.New("replicas_in_sync must be true")}
				}
				return nil, nil
			}},
		},
	}
}

type dataGetter interface{ Get(string) interface{} }

func planFromData(d dataGetter) (UserResource, error) {
	directGrants, err := grants.Decode(d.Get("grants").(*schema.Set))
	return UserResource{Name: d.Get("name").(string), Cluster: d.Get("cluster").(string), Password: d.Get("password").(string), Roles: setStrings(d.Get("roles").(*schema.Set)), DefaultRoles: setStrings(d.Get("default_roles").(*schema.Set)), Grants: directGrants}, err
}
func setStrings(set *schema.Set) []string {
	result := make([]string, 0, set.Len())
	for _, v := range set.List() {
		result = append(result, v.(string))
	}
	return sorted(result)
}
func validatePlan(plan UserResource) error {
	for _, role := range plan.DefaultRoles {
		if !includes(plan.Roles, role) {
			return errors.New("every default_roles entry must also be assigned in roles")
		}
	}
	return nil
}
func userService(ctx context.Context, meta interface{}, cluster string) (*CHUserService, *common.ApiClient, error) {
	client := meta.(*common.ApiClient)
	connections, err := access.Connections(client, cluster)
	if err != nil {
		return nil, nil, err
	}
	if err := access.VerifyCluster(ctx, connections, cluster); err != nil {
		return nil, nil, err
	}
	if client.CheckUserPassword == nil {
		return nil, nil, errors.New("ClickHouse user password verification is unavailable")
	}
	return &CHUserService{Connections: connections}, client, nil
}
func resourceUserRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout(schema.TimeoutRead))
	defer cancel()
	cluster, name, err := access.ParseResourceID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	service, client, err := userService(ctx, meta, cluster)
	if err != nil {
		return diag.FromErr(err)
	}
	users, err := service.ReadAll(ctx, name)
	if err != nil {
		return diag.FromErr(err)
	}
	var representative *CHUser
	for _, user := range users {
		if user != nil {
			representative = user
			break
		}
	}
	if representative == nil {
		d.SetId("")
		return nil
	}
	inSync := representative.canonical()
	for _, user := range users {
		if !sameUser(representative, user) {
			inSync = false
		}
	}
	if password, ok := d.GetOk("password"); ok {
		err := client.CheckUserPassword(ctx, cluster, name, password.(string))
		if errors.Is(err, common.ErrUserPasswordMismatch) {
			inSync = false
		} else if err != nil {
			return diag.FromErr(errors.New("could not verify ClickHouse user password; resource retained"))
		}
	}
	for key, value := range map[string]interface{}{"name": name, "cluster": cluster, "roles": representative.Roles, "default_roles": representative.effectiveDefaults(), "grants": grants.State(representative.Grants), "replicas_in_sync": inSync} {
		if err := d.Set(key, value); err != nil {
			return diag.FromErr(err)
		}
	}
	return nil
}
func applyUser(ctx context.Context, d *schema.ResourceData, meta interface{}, create bool) diag.Diagnostics {
	timeout := d.Timeout(schema.TimeoutUpdate)
	if create {
		timeout = d.Timeout(schema.TimeoutCreate)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	plan, err := planFromData(d)
	if err != nil {
		return diag.FromErr(err)
	}
	if err := validatePlan(plan); err != nil {
		return diag.FromErr(err)
	}
	service, client, err := userService(ctx, meta, plan.Cluster)
	if err != nil {
		return diag.FromErr(err)
	}
	if create {
		err = service.Create(ctx, plan, func() { d.SetId(access.ResourceID(plan.Cluster, plan.Name)) })
	} else {
		cluster, name, idErr := access.ParseResourceID(d.Id())
		if idErr != nil {
			return diag.FromErr(idErr)
		}
		if cluster != plan.Cluster || name != plan.Name {
			return diag.FromErr(errors.New("user name and cluster are immutable"))
		}
		err = service.Reconcile(ctx, plan)
	}
	if err != nil {
		return diag.FromErr(err)
	}
	if err := service.Verify(ctx, plan, timeout); err != nil {
		return diag.FromErr(err)
	}
	err = access.Wait(ctx, timeout, func(ctx context.Context) (bool, error) {
		err := client.CheckUserPassword(ctx, plan.Cluster, plan.Name, plan.Password)
		if errors.Is(err, common.ErrUserPasswordMismatch) {
			return false, nil
		}
		if err != nil {
			return false, errors.New("could not verify ClickHouse user password")
		}
		return true, nil
	})
	if err != nil {
		return diag.FromErr(err)
	}
	return resourceUserRead(ctx, d, meta)
}
func resourceUserCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return applyUser(ctx, d, meta, true)
}
func resourceUserUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	return applyUser(ctx, d, meta, false)
}
func resourceUserDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout(schema.TimeoutDelete))
	defer cancel()
	cluster, name, err := access.ParseResourceID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	service, _, err := userService(ctx, meta, cluster)
	if err != nil {
		return diag.FromErr(err)
	}
	if err := service.Delete(ctx, name, cluster, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
