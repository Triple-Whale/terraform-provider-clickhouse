package common

import (
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type ApiClient struct {
	ClickhouseConnection *driver.Conn
	DefaultCluster       string
	// optional connections to every replica, used to verify replica consistency on read
	ReplicaConnections []*driver.Conn
	VerifyReplicas     bool
}
