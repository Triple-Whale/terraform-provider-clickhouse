package resourcerole

import (
	"context"
	"fmt"
)

func (service *CHRoleService) checkRoleUserCollision(ctx context.Context, name string) error {
	rows, err := service.Connection.Query(ctx, "SELECT name FROM system.users WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("checking role name ownership: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("role %q shares its name with a user; grant ownership is ambiguous", name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("checking role name ownership: %w", err)
	}
	return nil
}
