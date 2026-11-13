package resourcerole

import (
	"fmt"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

func validateName(value interface{}, key string) ([]string, []error) {
	if err := access.ValidateIdentifier(value.(string)); err != nil {
		return nil, []error{fmt.Errorf("%s: %w", key, err)}
	}
	return nil, nil
}

func validateCluster(value interface{}, key string) ([]string, []error) {
	if value.(string) == "" {
		return nil, nil
	}
	return validateName(value, key)
}

func validateTrue(value interface{}, key string) ([]string, []error) {
	if !value.(bool) {
		return nil, []error{fmt.Errorf("%s must be true; false is reserved for detected replica drift", key)}
	}
	return nil, nil
}
