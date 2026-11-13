package grants

import (
	"context"
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

func checkPrefixGrants(ctx context.Context, conn access.Connection, principal, name string) error {
	rows, err := conn.Query(ctx, "SHOW GRANTS FOR "+access.QuoteIdentifier(name))
	if err != nil {
		return fmt.Errorf("reading %s grant scopes: %w", principal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return fmt.Errorf("reading %s grant scope: %w", principal, err)
		}
		prefix, err := HasPrefixWildcard(statement)
		if err != nil {
			return fmt.Errorf("reading %s %q grant scope: %w", principal, name, err)
		}
		if prefix {
			return fmt.Errorf("%s %q has unsupported prefix wildcard grants", principal, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading %s grant scopes: %w", principal, err)
	}
	return nil
}

func HasPrefixWildcard(statement string) (bool, error) {
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
