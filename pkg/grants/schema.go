package grants

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Schema(required bool) *schema.Schema {
	return &schema.Schema{Type: schema.TypeSet, Required: required, Optional: !required, ConfigMode: schema.SchemaConfigModeAttr, Description: "Complete set of direct database/table grants. An empty set grants no direct privileges.", Elem: &schema.Resource{Schema: map[string]*schema.Schema{
		"database": {Type: schema.TypeString, Required: true, ValidateFunc: validateScope, Description: "Database name, or * for every database."},
		"table":    {Type: schema.TypeString, Required: true, ValidateFunc: validateScope, Description: "Explicit table name, or * for every table."},
		"privileges": {Type: schema.TypeSet, Required: true, MinItems: 1, Description: "Atomic privileges, such as SELECT or INSERT; aliases and groups such as ALL and ALTER are unsupported.",
			Elem: &schema.Schema{Type: schema.TypeString, StateFunc: NormalizePrivilege, ValidateFunc: ValidatePrivilege}},
	}}}
}

func Decode(set *schema.Set) ([]Grant, error) {
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
			if _, errors := ValidatePrivilege(privilege, "privileges"); len(errors) != 0 {
				return nil, errors[0]
			}
			grant.Privileges = append(grant.Privileges, NormalizePrivilege(privilege))
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

func State(grants []Grant) []interface{} {
	values := make([]interface{}, 0, len(grants))
	for _, grant := range grants {
		values = append(values, map[string]interface{}{"database": grant.Database, "table": grant.Table, "privileges": grant.Privileges})
	}
	return values
}
