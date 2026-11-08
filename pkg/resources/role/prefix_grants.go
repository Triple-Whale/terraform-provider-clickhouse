package resourcerole

import (
	"context"
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

func (service *CHRoleService) checkPrefixGrants(ctx context.Context, name string) error {
	rows, err := service.Connection.Query(ctx, "SHOW GRANTS FOR "+access.QuoteIdentifier(name))
	if err != nil {
		return fmt.Errorf("reading role grant scopes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return fmt.Errorf("reading role grant scope: %w", err)
		}
		prefix, err := hasPrefixWildcard(statement)
		if err != nil {
			return fmt.Errorf("reading role %q grant scope: %w", name, err)
		}
		if prefix {
			return fmt.Errorf("role %q has unsupported prefix wildcard grants", name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading role grant scopes: %w", err)
	}
	return nil
}

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

func hasPrefixWildcard(statement string) (bool, error) {
	identifierEnd := -1
	for i := 0; i < len(statement); {
		if strings.ContainsRune(" \t\r\n\f\v", rune(statement[i])) {
			i++
			continue
		}
		// SHOW GRANTS preserves prefix '*' adjacent to its identifier, including quoted names.
		if statement[i] == '*' && identifierEnd == i {
			return true, nil
		}
		quote := statement[i]
		if quote == '`' || quote == '"' || quote == '\'' {
			i++
			closed := false
			for i < len(statement) {
				if statement[i] == '\\' {
					i += 2
					continue
				}
				if statement[i] == quote {
					i++
					if i < len(statement) && statement[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return false, fmt.Errorf("unterminated SHOW GRANTS identifier")
			}
			identifierEnd = i
			continue
		}
		if strings.ContainsRune(".,*();", rune(statement[i])) {
			identifierEnd = -1
			i++
			continue
		}
		for i < len(statement) && !strings.ContainsRune(" \t\r\n\f\v.,*();`\"'", rune(statement[i])) {
			i++
		}
		identifierEnd = i
	}
	return false, nil
}
