package resourceuser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
)

type CHUserService struct {
	Connections []access.Connection
}

func (us *CHUserService) GetUser(ctx context.Context, conn access.Connection, name string) (*CHUser, error) {
	if err := rejectRoleCollision(ctx, conn, name); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, "SELECT name, storage, arrayMap(x -> toString(x), auth_type), default_roles_all, default_roles_list, default_roles_except FROM system.users WHERE name = "+access.QuoteString(name))
	if err != nil {
		return nil, errors.New("could not read ClickHouse user metadata")
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return nil, errors.New("could not read ClickHouse user metadata")
		}
		return nil, nil
	}
	user := &CHUser{}
	if err := rows.Scan(&user.Name, &user.Storage, &user.AuthTypes, &user.DefaultRolesAll, &user.DefaultRoles, &user.DefaultRolesExcept); err != nil {
		return nil, errors.New("could not decode ClickHouse user metadata")
	}
	if rows.Next() {
		return nil, errors.New("user name exists in multiple access storages; resolve ownership before managing it")
	}
	if rows.Err() != nil {
		return nil, errors.New("could not read ClickHouse user metadata")
	}
	if err := rows.Close(); err != nil {
		return nil, errors.New("could not close ClickHouse user metadata response")
	}
	if user.Storage != "local_directory" {
		return nil, errors.New("only SQL users in local_directory storage can be managed; XML and replicated access users require a separate migration")
	}
	roleGrants, err := conn.Query(ctx, "SELECT granted_role_name, with_admin_option FROM system.role_grants WHERE user_name = "+access.QuoteString(name))
	if err != nil {
		return nil, errors.New("could not read ClickHouse user role assignments")
	}
	defer roleGrants.Close()
	for roleGrants.Next() {
		var role string
		var admin uint8
		if err := roleGrants.Scan(&role, &admin); err != nil {
			return nil, errors.New("could not decode ClickHouse user role assignments")
		}
		user.Roles = append(user.Roles, role)
		if admin != 0 {
			user.AdminRoles = append(user.AdminRoles, role)
		}
	}
	if roleGrants.Err() != nil {
		return nil, errors.New("could not read ClickHouse user role assignments")
	}
	if err := roleGrants.Close(); err != nil {
		return nil, errors.New("could not close ClickHouse user role assignments")
	}
	user.Grants, err = grants.Read(ctx, conn, "user", name)
	if err != nil {
		return nil, fmt.Errorf("could not read supported direct user privileges: %w", err)
	}
	return user, nil
}

func rejectRoleCollision(ctx context.Context, conn access.Connection, name string) error {
	rows, err := conn.Query(ctx, "SELECT name FROM system.roles WHERE name = ?", name)
	if err != nil {
		return errors.New("could not inspect user/role name ownership")
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("user shares its name with a role; grant ownership is ambiguous")
	}
	if rows.Err() != nil {
		return errors.New("could not inspect user/role name ownership")
	}
	return nil
}

func (us *CHUserService) ReadAll(ctx context.Context, name string) ([]*CHUser, error) {
	users := make([]*CHUser, len(us.Connections))
	for i, conn := range us.Connections {
		user, err := us.GetUser(ctx, conn, name)
		if err != nil {
			return nil, fmt.Errorf("replica %d: %w", i, err)
		}
		users[i] = user
	}
	return users, nil
}

func (us *CHUserService) exec(ctx context.Context, query, operation string) error {
	if err := us.Connections[0].Exec(ctx, query); err != nil {
		return fmt.Errorf("ClickHouse %s failed; credential-bearing database diagnostics are suppressed", operation)
	}
	return nil
}

func userSQL(plan UserResource, verb string) string {
	prefix := verb + " USER "
	if verb == "CREATE IF NOT EXISTS" {
		prefix = "CREATE USER IF NOT EXISTS "
	}
	return prefix + access.QuoteIdentifier(plan.Name) + access.ClusterClause(plan.Cluster) + " IDENTIFIED WITH sha256_hash BY " + access.QuoteString(passwordHash(plan.Password))
}

func (us *CHUserService) Create(ctx context.Context, plan UserResource, created func()) error {
	users, err := us.ReadAll(ctx, plan.Name)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user != nil {
			return errors.New("user already exists; import it explicitly instead of creating or replacing it")
		}
	}
	err = us.Connections[0].Exec(ctx, userSQL(plan, "CREATE")+" DEFAULT ROLE NONE")
	if err != nil {
		if access.IsCreateCollision(err) {
			return errors.New("user creation encountered an existing name; ownership was not recorded; inspect all replicas and import explicitly before managing it")
		}
		if access.CreateMayHaveSucceeded(err, plan.Cluster) {
			created()
			return errors.New("user creation may have partially completed; resource state retained for reconciliation; credential-bearing database diagnostics are suppressed")
		}
		return errors.New("ClickHouse rejected user creation; credential-bearing database diagnostics are suppressed")
	}
	created()
	return us.Reconcile(ctx, plan)
}

func quotedRoles(roles []string) string {
	result := make([]string, len(roles))
	for i, role := range sorted(roles) {
		result[i] = access.QuoteIdentifier(role)
	}
	return strings.Join(result, ", ")
}

func (us *CHUserService) Reconcile(ctx context.Context, plan UserResource) error {
	users, err := us.ReadAll(ctx, plan.Name)
	if err != nil {
		return err
	}
	missing := false
	extras := map[string]bool{}
	admins := map[string]bool{}
	actualGrants := []grants.Grant{}
	for _, user := range users {
		if user == nil {
			missing = true
			continue
		}
		for _, role := range user.Roles {
			if !includes(plan.Roles, role) {
				extras[role] = true
			}
		}
		for _, role := range user.AdminRoles {
			admins[role] = true
		}
		actualGrants = append(actualGrants, user.Grants...)
	}
	if missing {
		if err := us.exec(ctx, userSQL(plan, "CREATE IF NOT EXISTS"), "missing user repair"); err != nil {
			return err
		}
	}
	if err := us.exec(ctx, userSQL(plan, "ALTER"), "password update"); err != nil {
		return err
	}
	if err := grants.Reconcile(ctx, us.Connections[0], plan.Name, plan.Cluster, plan.Grants, actualGrants); err != nil {
		return errors.New("ClickHouse direct user privilege reconciliation failed; resource retained; database diagnostics are suppressed")
	}
	target := access.QuoteIdentifier(plan.Name)
	cluster := access.ClusterClause(plan.Cluster)
	if err := us.exec(ctx, "ALTER USER "+target+cluster+" DEFAULT ROLE NONE", "default role reset"); err != nil {
		return err
	}
	for _, role := range mapKeys(extras) {
		if err := us.exec(ctx, "REVOKE"+cluster+" "+access.QuoteIdentifier(role)+" FROM "+target, "role revocation"); err != nil {
			return err
		}
	}
	for _, role := range mapKeys(admins) {
		if !includes(plan.Roles, role) {
			continue
		}
		if err := us.exec(ctx, "REVOKE"+cluster+" ADMIN OPTION FOR "+access.QuoteIdentifier(role)+" FROM "+target, "admin option revocation"); err != nil {
			return err
		}
	}
	if len(plan.Roles) > 0 {
		if err := us.exec(ctx, "GRANT"+cluster+" "+quotedRoles(plan.Roles)+" TO "+target, "role assignment"); err != nil {
			return err
		}
	}
	defaults := "NONE"
	if len(plan.DefaultRoles) > 0 {
		defaults = quotedRoles(plan.DefaultRoles)
	}
	return us.exec(ctx, "ALTER USER "+target+cluster+" DEFAULT ROLE "+defaults, "default role update")
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return sorted(result)
}

func (us *CHUserService) Verify(ctx context.Context, plan UserResource, timeout time.Duration) error {
	return access.Wait(ctx, timeout, func(ctx context.Context) (bool, error) {
		users, err := us.ReadAll(ctx, plan.Name)
		if err != nil {
			return false, err
		}
		for _, user := range users {
			if !matchesPlan(user, plan) {
				return false, nil
			}
		}
		return true, nil
	})
}

func (us *CHUserService) Delete(ctx context.Context, name, cluster string, timeout time.Duration) error {
	if _, err := us.ReadAll(ctx, name); err != nil {
		return err
	}
	if err := us.exec(ctx, "DROP USER IF EXISTS "+access.QuoteIdentifier(name)+access.ClusterClause(cluster), "user deletion"); err != nil {
		return err
	}
	return access.Wait(ctx, timeout, func(ctx context.Context) (bool, error) {
		users, err := us.ReadAll(ctx, name)
		if err != nil {
			return false, err
		}
		for _, user := range users {
			if user != nil {
				return false, nil
			}
		}
		return true, nil
	})
}
