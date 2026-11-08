package common

import (
	"context"
	"errors"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var ErrUserPasswordMismatch = errors.New("ClickHouse user password does not match")

type ApiClient struct {
	CheckUserPassword    func(context.Context, string, string, string) error
	ClickhouseConnection *driver.Conn
	DefaultCluster       string
	// optional connections to every replica, used to verify replica consistency on read
	ReplicaConnections []*driver.Conn
	VerifyReplicas     bool
}
