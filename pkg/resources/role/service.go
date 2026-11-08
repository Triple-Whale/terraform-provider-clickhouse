package resourcerole

import (
	"context"
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

type CHRoleService struct {
	Connection access.Connection
}

func (service *CHRoleService) GetRole(ctx context.Context, name string) (*CHRole, error) {
	rows, err := service.Connection.Query(ctx, "SELECT name, storage FROM system.roles WHERE name = ?", name)
	if err != nil {
		return nil, fmt.Errorf("reading role: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var roleName, storage string
	if err := rows.Scan(&roleName, &storage); err != nil {
		return nil, fmt.Errorf("reading role identity: %w", err)
	}
	if rows.Next() {
		return nil, fmt.Errorf("role %q exists in more than one access storage; ownership is ambiguous", name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("closing role identity query: %w", err)
	}
	if storage != "local_directory" {
		return nil, fmt.Errorf("role %q is managed by unsupported access storage %q; only SQL local_directory roles are supported", name, storage)
	}
	if err := service.checkInheritedRoles(ctx, name); err != nil {
		return nil, err
	}
	grants, err := service.getGrants(ctx, name)
	if err != nil {
		return nil, err
	}
	return &CHRole{Name: roleName, Grants: grants}, nil
}

func (service *CHRoleService) checkInheritedRoles(ctx context.Context, name string) error {
	rows, err := service.Connection.Query(ctx, "SELECT granted_role_name FROM system.role_grants WHERE role_name = ?", name)
	if err != nil {
		return fmt.Errorf("reading inherited roles: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("role %q inherits another role; inherited roles are not supported", name)
	}
	return rows.Err()
}

func (service *CHRoleService) getGrants(ctx context.Context, name string) ([]Grant, error) {
	query := "SELECT access_type, ifNull(database, '*'), ifNull(table, '*'), ifNull(column, ''), is_partial_revoke, grant_option, isNull(database), isNull(table) FROM system.grants WHERE role_name = ?"
	rows, err := service.Connection.Query(ctx, query, name)
	if err != nil {
		return nil, fmt.Errorf("reading role grants: %w", err)
	}
	defer rows.Close()
	grants := []Grant{}
	var hierarchy map[string][]string
	for rows.Next() {
		var privilege, database, table, column string
		var partialRevoke, grantOption, databaseNull, tableNull uint8
		if err := rows.Scan(&privilege, &database, &table, &column, &partialRevoke, &grantOption, &databaseNull, &tableNull); err != nil {
			return nil, fmt.Errorf("reading grant: %w", err)
		}
		if (database == "*" && databaseNull == 0) || (table == "*" && tableNull == 0) {
			return nil, fmt.Errorf("role %q has a literal '*' identifier; only wildcard scopes are supported", name)
		}
		if column != "" || partialRevoke != 0 || grantOption != 0 {
			return nil, fmt.Errorf("role %q has unsupported column grants, partial revokes, or grant options", name)
		}
		if err := validateGrantScope(database, table); err != nil {
			return nil, fmt.Errorf("role %q has unsupported scope: %w", name, err)
		}
		if _, errors := validatePrivilege(privilege, "privilege"); len(errors) != 0 && hierarchy == nil {
			hierarchy, err = service.privilegeHierarchy(ctx)
			if err != nil {
				return nil, err
			}
		}
		atomic, err := expandPrivilege(privilege, hierarchy, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("role %q: %w", name, err)
		}
		grants = append(grants, Grant{Database: database, Table: table, Privileges: atomic})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading grants: %w", err)
	}
	return grantsFromPermissions(permissions(grants)), nil
}

func readRoles(ctx context.Context, connections []access.Connection, name string) ([]*CHRole, error) {
	roles := make([]*CHRole, 0, len(connections))
	for index, conn := range connections {
		service := CHRoleService{Connection: conn}
		role, err := service.GetRole(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("reading role on connection %d: %w", index, err)
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func firstRole(roles []*CHRole) *CHRole {
	for _, role := range roles {
		if role != nil {
			return role
		}
	}
	return nil
}

func (service *CHRoleService) privilegeHierarchy(ctx context.Context) (map[string][]string, error) {
	rows, err := service.Connection.Query(ctx, "SELECT toString(privilege), toString(parent_group) FROM system.privileges WHERE parent_group IS NOT NULL")
	if err != nil {
		return nil, fmt.Errorf("reading privilege groups: %w", err)
	}
	defer rows.Close()
	hierarchy := map[string][]string{}
	for rows.Next() {
		var privilege, parent string
		if err := rows.Scan(&privilege, &parent); err != nil {
			return nil, fmt.Errorf("reading privilege group: %w", err)
		}
		hierarchy[parent] = append(hierarchy[parent], privilege)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading privilege groups: %w", err)
	}
	return hierarchy, nil
}

func expandPrivilege(privilege string, hierarchy map[string][]string, visiting map[string]bool) ([]string, error) {
	if _, errors := validatePrivilege(privilege, "privilege"); len(errors) == 0 {
		return []string{normalizePrivilege(privilege)}, nil
	}
	if visiting[privilege] || len(hierarchy[privilege]) == 0 {
		return nil, fmt.Errorf("unsupported privilege %q", privilege)
	}
	visiting[privilege] = true
	defer delete(visiting, privilege)
	atomic := []string{}
	for _, child := range hierarchy[privilege] {
		leaves, err := expandPrivilege(child, hierarchy, visiting)
		if err != nil {
			return nil, err
		}
		atomic = append(atomic, leaves...)
	}
	return atomic, nil
}

func rolesMatch(roles []*CHRole, grants []Grant) bool {
	for _, role := range roles {
		if role == nil || !sameGrants(role.Grants, grants) {
			return false
		}
	}
	return len(roles) != 0
}

func (service *CHRoleService) CreateRole(ctx context.Context, role RoleResource, ifMissing bool) error {
	query := "CREATE ROLE "
	if ifMissing {
		query += "IF NOT EXISTS "
	}
	query += access.QuoteIdentifier(role.Name) + access.ClusterClause(role.Cluster)
	if err := service.Connection.Exec(ctx, query); err != nil {
		return fmt.Errorf("creating role: %w", err)
	}
	return nil
}

func (service *CHRoleService) Reconcile(ctx context.Context, role RoleResource, actual []*CHRole) error {
	allGrants := []Grant{}
	missing := false
	for _, observed := range actual {
		if observed == nil {
			missing = true
			continue
		}
		allGrants = append(allGrants, observed.Grants...)
	}
	if missing {
		if err := service.CreateRole(ctx, role, true); err != nil {
			return err
		}
	}
	desired := permissions(role.Grants)
	wanted := map[Permission]bool{}
	for _, permission := range desired {
		wanted[permission] = true
	}
	for _, permission := range permissions(allGrants) {
		if !wanted[permission] {
			if err := service.changeGrant(ctx, "REVOKE", "FROM", role, permission); err != nil {
				return err
			}
		}
	}
	for _, permission := range desired {
		if err := service.changeGrant(ctx, "GRANT", "TO", role, permission); err != nil {
			return err
		}
	}
	return nil
}

func (service *CHRoleService) changeGrant(ctx context.Context, action, direction string, role RoleResource, permission Permission) error {
	query := action + access.ClusterClause(role.Cluster) + " " + permission.Privilege + " ON " +
		quoteScope(permission.Database) + "." + quoteScope(permission.Table) + " " + direction + " " + access.QuoteIdentifier(role.Name)
	if err := service.Connection.Exec(ctx, query); err != nil {
		return fmt.Errorf("%s role privilege: %w", strings.ToLower(action), err)
	}
	return nil
}

func quoteScope(value string) string {
	if value == "*" {
		return "*"
	}
	return access.QuoteIdentifier(value)
}

func (service *CHRoleService) DeleteRole(ctx context.Context, role RoleResource) error {
	query := "DROP ROLE IF EXISTS " + access.QuoteIdentifier(role.Name) + access.ClusterClause(role.Cluster)
	if err := service.Connection.Exec(ctx, query); err != nil {
		return fmt.Errorf("deleting role: %w", err)
	}
	return nil
}
