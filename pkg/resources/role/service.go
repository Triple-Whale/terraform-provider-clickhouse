package resourcerole

import (
	"context"
	"fmt"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	grantpolicy "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
)

type CHRoleService struct {
	Connection access.Connection
}

func (service *CHRoleService) GetRole(ctx context.Context, name string) (*CHRole, error) {
	if err := service.checkRoleUserCollision(ctx, name); err != nil {
		return nil, err
	}
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
	grants, err := grantpolicy.Read(ctx, service.Connection, "role", name)
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

func rolesMatch(roles []*CHRole, grants []Grant) bool {
	for _, role := range roles {
		if role == nil || !grantpolicy.Equal(role.Grants, grants) {
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
	return grantpolicy.Reconcile(ctx, service.Connection, role.Name, role.Cluster, role.Grants, allGrants)
}

func (service *CHRoleService) DeleteRole(ctx context.Context, role RoleResource) error {
	query := "DROP ROLE IF EXISTS " + access.QuoteIdentifier(role.Name) + access.ClusterClause(role.Cluster)
	if err := service.Connection.Exec(ctx, query); err != nil {
		return fmt.Errorf("deleting role: %w", err)
	}
	return nil
}
