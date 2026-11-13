package grants

import (
	"context"
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

func Read(ctx context.Context, conn access.Connection, principal, name string) ([]Grant, error) {
	if principal != "user" && principal != "role" {
		return nil, fmt.Errorf("unsupported grant principal %q", principal)
	}
	if err := checkPrefixGrants(ctx, conn, principal, name); err != nil {
		return nil, err
	}
	query := "SELECT access_type, ifNull(database, '*'), ifNull(table, '*'), ifNull(column, ''), is_partial_revoke, grant_option, isNull(database), isNull(table) FROM system.grants WHERE " + principal + "_name = ?"
	rows, err := conn.Query(ctx, query, name)
	if err != nil {
		return nil, fmt.Errorf("reading %s grants: %w", principal, err)
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
			return nil, fmt.Errorf("%s %q has a literal '*' identifier; only wildcard scopes are supported", principal, name)
		}
		if column != "" || partialRevoke != 0 || grantOption != 0 {
			return nil, fmt.Errorf("%s %q has unsupported column grants, partial revokes, or grant options", principal, name)
		}
		if err := validateGrantScope(database, table); err != nil {
			return nil, fmt.Errorf("%s %q has unsupported scope: %w", principal, name, err)
		}
		if _, errors := ValidatePrivilege(privilege, "privilege"); len(errors) != 0 && hierarchy == nil {
			hierarchy, err = privilegeHierarchy(ctx, conn)
			if err != nil {
				return nil, err
			}
		}
		atomic, err := ExpandPrivilege(privilege, hierarchy, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", principal, name, err)
		}
		grants = append(grants, Grant{Database: database, Table: table, Privileges: atomic})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading grants: %w", err)
	}
	return grantsFromPermissions(permissions(grants)), nil
}

func privilegeHierarchy(ctx context.Context, conn access.Connection) (map[string][]string, error) {
	rows, err := conn.Query(ctx, "SELECT toString(privilege), toString(parent_group) FROM system.privileges WHERE parent_group IS NOT NULL")
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

func ExpandPrivilege(privilege string, hierarchy map[string][]string, visiting map[string]bool) ([]string, error) {
	if _, errors := ValidatePrivilege(privilege, "privilege"); len(errors) == 0 {
		return []string{NormalizePrivilege(privilege)}, nil
	}
	if visiting[privilege] || len(hierarchy[privilege]) == 0 {
		return nil, fmt.Errorf("unsupported privilege %q", privilege)
	}
	visiting[privilege] = true
	defer delete(visiting, privilege)
	atomic := []string{}
	for _, child := range hierarchy[privilege] {
		leaves, err := ExpandPrivilege(child, hierarchy, visiting)
		if err != nil {
			return nil, err
		}
		atomic = append(atomic, leaves...)
	}
	return atomic, nil
}

func Reconcile(ctx context.Context, conn access.Connection, name, cluster string, desired, actual []Grant) error {
	desiredPermissions := permissions(desired)
	wanted := map[Permission]bool{}
	for _, permission := range desiredPermissions {
		wanted[permission] = true
	}
	for _, permission := range permissions(actual) {
		if !wanted[permission] {
			if err := changeGrant(ctx, conn, "REVOKE", "FROM", name, cluster, permission); err != nil {
				return err
			}
		}
	}
	for _, permission := range desiredPermissions {
		if err := changeGrant(ctx, conn, "GRANT", "TO", name, cluster, permission); err != nil {
			return err
		}
	}
	return nil
}

func changeGrant(ctx context.Context, conn access.Connection, action, direction, name, cluster string, permission Permission) error {
	query := action + access.ClusterClause(cluster) + " " + permission.Privilege + " ON " +
		quoteScope(permission.Database) + "." + quoteScope(permission.Table) + " " + direction + " " + access.QuoteIdentifier(name)
	if err := conn.Exec(ctx, query); err != nil {
		return fmt.Errorf("%s privilege: %w", strings.ToLower(action), err)
	}
	return nil
}

func quoteScope(value string) string {
	if value == "*" {
		return "*"
	}
	return access.QuoteIdentifier(value)
}
