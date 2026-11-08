package access

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type Connection interface {
	Exec(context.Context, string, ...interface{}) error
	Query(context.Context, string, ...interface{}) (driver.Rows, error)
}

func ValidateIdentifier(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("identifier must be valid UTF-8")
	}
	if value == "" {
		return fmt.Errorf("identifier must not be empty")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("identifier must not contain control characters")
		}
	}
	return nil
}

func QuoteIdentifier(value string) string {
	return "`" + strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(value) + "`"
}

func QuoteString(value string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'", "\x00", "\\0", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(value) + "'"
}

func ClusterClause(cluster string) string {
	if cluster == "" {
		return ""
	}
	return " ON CLUSTER " + QuoteIdentifier(cluster)
}

func ResourceID(cluster, name string) string {
	value, _ := json.Marshal([]string{cluster, name})
	return string(value)
}

func ParseResourceID(id string) (cluster, name string, err error) {
	var values []interface{}
	if err = json.Unmarshal([]byte(id), &values); err != nil || len(values) != 2 {
		return "", "", fmt.Errorf("resource ID must be a JSON array [cluster, name]; use an empty cluster for local resources")
	}
	var clusterOK, nameOK bool
	cluster, clusterOK = values[0].(string)
	name, nameOK = values[1].(string)
	if !clusterOK || !nameOK {
		return "", "", fmt.Errorf("resource ID cluster and name must both be strings")
	}
	if err = ValidateIdentifier(name); err != nil {
		return "", "", fmt.Errorf("invalid resource name: %w", err)
	}
	if cluster != "" {
		if err = ValidateIdentifier(cluster); err != nil {
			return "", "", fmt.Errorf("invalid cluster: %w", err)
		}
	}
	return cluster, name, nil
}

func ImportState(_ context.Context, data *schema.ResourceData, _ interface{}) ([]*schema.ResourceData, error) {
	cluster, name, err := ParseResourceID(data.Id())
	if err != nil {
		return nil, err
	}
	if err = data.Set("name", name); err != nil {
		return nil, err
	}
	if err = data.Set("cluster", cluster); err != nil {
		return nil, err
	}
	data.SetId(ResourceID(cluster, name))
	return []*schema.ResourceData{data}, nil
}

func Connections(client *common.ApiClient, cluster string) ([]Connection, error) {
	if client == nil || client.ClickhouseConnection == nil || *client.ClickhouseConnection == nil {
		return nil, fmt.Errorf("ClickHouse connection is not configured")
	}
	connections := []Connection{*client.ClickhouseConnection}
	if cluster == "" {
		return connections, nil
	}
	if !client.VerifyReplicas || len(client.ReplicaConnections) == 0 {
		return nil, fmt.Errorf("clustered access resources require verify_replicas=true and replica_hosts containing every cluster member")
	}
	for _, conn := range client.ReplicaConnections {
		if conn == nil || *conn == nil {
			return nil, fmt.Errorf("replica connection is not configured")
		}
		connections = append(connections, *conn)
	}
	return connections, nil
}

func VerifyCluster(ctx context.Context, connections []Connection, cluster string) error {
	if cluster == "" {
		return nil
	}
	if len(connections) == 0 {
		return fmt.Errorf("no cluster connections configured")
	}
	expected, err := clusterMembers(ctx, connections[0], cluster, false)
	if err != nil {
		return err
	}
	if len(expected) == 0 {
		return fmt.Errorf("cluster %q has no configured members", cluster)
	}
	covered := map[string]bool{}
	for index, conn := range connections {
		local, err := clusterMembers(ctx, conn, cluster, true)
		if err != nil {
			return fmt.Errorf("checking cluster membership on connection %d: %w", index, err)
		}
		if len(local) == 0 {
			return fmt.Errorf("connection %d is not a member of cluster %q", index, cluster)
		}
		for member := range local {
			if !expected[member] {
				return fmt.Errorf("connection %d has a different definition of cluster %q", index, cluster)
			}
			covered[member] = true
		}
	}
	if len(covered) != len(expected) {
		return fmt.Errorf("replica_hosts covers %d of %d members of cluster %q", len(covered), len(expected), cluster)
	}
	return nil
}

func clusterMembers(ctx context.Context, conn Connection, cluster string, local bool) (map[string]bool, error) {
	query := "SELECT host_address, port FROM system.clusters WHERE cluster = ?"
	if local {
		query += " AND is_local = 1"
	}
	rows, err := conn.Query(ctx, query, cluster)
	if err != nil {
		return nil, fmt.Errorf("reading cluster membership: %w", err)
	}
	defer rows.Close()
	members := map[string]bool{}
	for rows.Next() {
		var host string
		var port uint16
		if err := rows.Scan(&host, &port); err != nil {
			return nil, fmt.Errorf("reading cluster member: %w", err)
		}
		members[fmt.Sprintf("%s:%d", host, port)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading cluster members: %w", err)
	}
	return members, nil
}

func Wait(ctx context.Context, timeout time.Duration, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for access state convergence: %w", err)
		}
		ready, err := check(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("waiting for access state convergence: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
