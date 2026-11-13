package grants

import (
	"fmt"
	"strings"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

var AllowedPrivileges = []string{
	"SELECT", "INSERT", "CREATE DATABASE", "CREATE TABLE", "CREATE VIEW", "CREATE DICTIONARY",
	"DROP DATABASE", "DROP TABLE", "DROP VIEW", "DROP DICTIONARY", "SHOW DATABASES", "SHOW TABLES",
	"SHOW COLUMNS", "SHOW DICTIONARIES", "TRUNCATE", "OPTIMIZE", "ALTER UPDATE", "ALTER DELETE",
	"ALTER ADD COLUMN", "ALTER DROP COLUMN", "ALTER MODIFY COLUMN", "ALTER COMMENT COLUMN",
	"ALTER CLEAR COLUMN", "ALTER RENAME COLUMN", "ALTER ORDER BY", "ALTER SAMPLE BY",
	"ALTER ADD INDEX", "ALTER DROP INDEX", "ALTER MATERIALIZE INDEX", "ALTER CLEAR INDEX",
	"ALTER TTL", "ALTER MATERIALIZE TTL", "ALTER FREEZE PARTITION",
	"ALTER MOVE PARTITION", "ALTER FETCH PARTITION",
}

func NormalizePrivilege(value interface{}) string {
	return strings.Join(strings.Fields(strings.ToUpper(value.(string))), " ")
}

func ValidatePrivilege(value interface{}, key string) ([]string, []error) {
	privilege := NormalizePrivilege(value)
	for _, allowed := range AllowedPrivileges {
		if privilege == allowed {
			return nil, nil
		}
	}
	return nil, []error{fmt.Errorf("%s: unsupported privilege %q; use a documented atomic database/table privilege", key, privilege)}
}

func validateScope(value interface{}, key string) ([]string, []error) {
	name := value.(string)
	if name == "*" {
		return nil, nil
	}
	if strings.Contains(name, "*") {
		return nil, []error{fmt.Errorf("%s: only the complete wildcard '*' is supported", key)}
	}
	if err := access.ValidateIdentifier(value.(string)); err != nil {
		return nil, []error{fmt.Errorf("%s: %w", key, err)}
	}
	return nil, nil
}
