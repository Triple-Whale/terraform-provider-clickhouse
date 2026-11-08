package resourceuser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/access"
)

type lostUserCreateReply struct {
	userConn
	attempted bool
	execError error
}

func (conn *lostUserCreateReply) Exec(_ context.Context, query string, _ ...interface{}) error {
	conn.executed = append(conn.executed, query)
	conn.attempted = true
	conn.user = &CHUser{Name: "svc-user", Storage: "local_directory", AuthTypes: []string{"sha256_password"}}
	if conn.execError != nil {
		return conn.execError
	}
	return errors.New("lost response secret-password")
}

func (conn *lostUserCreateReply) Query(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
	if conn.attempted {
		return nil, errors.New("connection unavailable")
	}
	return conn.userConn.Query(ctx, query, args...)
}

func TestLocalCreateLostReplyRetainsUser(t *testing.T) {
	conn := &lostUserCreateReply{}
	service := CHUserService{Connections: []access.Connection{conn}}
	retained := false
	err := service.Create(context.Background(), UserResource{Name: "svc-user", Password: "secret-password"}, func() { retained = true })
	if err == nil || !retained || conn.user == nil {
		t.Fatalf("uncertain committed create must retain ownership: err=%v retained=%v userExists=%v", err, retained, conn.user != nil)
	}
	if strings.Contains(err.Error(), "secret-password") || len(conn.executed) != 1 {
		t.Fatal("failed create leaked credentials or performed followup mutation")
	}
}

func TestUserCreateFailureOwnership(t *testing.T) {
	cases := []struct {
		name     string
		cluster  string
		failure  error
		retained bool
	}{
		{name: "local transport", failure: errors.New("secret-password"), retained: true},
		{name: "cluster transport", cluster: "main", failure: errors.New("secret-password"), retained: true},
		{name: "local deadline", failure: context.DeadlineExceeded, retained: true},
		{name: "local server timeout", failure: &clickhouse.Exception{Code: 159, Message: "secret-password"}, retained: true},
		{name: "local denied", failure: &clickhouse.Exception{Code: 497, Message: "secret-password"}},
		{name: "cluster partial denial", cluster: "main", failure: &clickhouse.Exception{Code: 497, Message: "secret-password"}, retained: true},
		{name: "local race collision", failure: &clickhouse.Exception{Code: 493, Message: "secret-password"}},
		{name: "cluster race collision", cluster: "main", failure: fmt.Errorf("wrapped: %w", &clickhouse.Exception{Code: 493, Message: "secret-password"})},
		{name: "nested cluster collision", cluster: "main", failure: &clickhouse.Exception{Code: 341, Nested: []clickhouse.Exception{{Code: 493, Message: "secret-password"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			conn := &lostUserCreateReply{execError: test.failure}
			service := CHUserService{Connections: []access.Connection{conn}}
			retained := false
			err := service.Create(context.Background(), UserResource{Name: "svc-user", Cluster: test.cluster, Password: "secret-password"}, func() { retained = true })
			if err == nil || retained != test.retained || len(conn.executed) != 1 {
				t.Fatalf("wrong failed-create ownership: err=%v retained=%v statements=%d", err, retained, len(conn.executed))
			}
			if strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), passwordHash("secret-password")) {
				t.Fatal("failed create leaked credentials")
			}
			if access.IsCreateCollision(test.failure) && !strings.Contains(err.Error(), "import explicitly") {
				t.Fatal("collision must require explicit ownership recovery")
			}
		})
	}
}
