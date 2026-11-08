package access

import (
	"errors"

	"github.com/ClickHouse/clickhouse-go/v2"
)

func IsCreateCollision(err error) bool {
	var exception *clickhouse.Exception
	if !errors.As(err, &exception) {
		return false
	}
	return hasCollision(exception)
}

func hasCollision(exception *clickhouse.Exception) bool {
	if exception.Code == 493 { // ACCESS_ENTITY_ALREADY_EXISTS
		return true
	}
	for index := range exception.Nested {
		if hasCollision(&exception.Nested[index]) {
			return true
		}
	}
	return false
}

func CreateMayHaveSucceeded(err error, cluster string) bool {
	if IsCreateCollision(err) {
		return false
	}
	var exception *clickhouse.Exception
	if cluster != "" || !errors.As(err, &exception) {
		return true
	}
	const (
		badArguments         = 36
		syntaxError          = 62
		accessDenied         = 497
		authenticationFailed = 516
	)
	switch exception.Code {
	case badArguments, syntaxError, accessDenied, authenticationFailed:
		return false
	default:
		return true
	}
}
