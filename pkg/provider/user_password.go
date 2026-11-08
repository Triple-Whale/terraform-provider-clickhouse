package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func userPasswordVerifier(host string, replicas []string, port int, tlsConfig *tls.Config) func(context.Context, string, string, string) error {
	return userPasswordVerifierWithCheck(host, replicas, port, tlsConfig, checkUserLogin)
}

func userPasswordVerifierWithCheck(host string, replicas []string, port int, tlsConfig *tls.Config, check func(context.Context, string, int, *tls.Config, string, string) error) func(context.Context, string, string, string) error {
	return func(ctx context.Context, cluster, name, password string) error {
		hosts := []string{host}
		if cluster != "" {
			hosts = append(hosts, replicas...)
		}
		seen := map[string]bool{}
		mismatch := false
		for _, target := range hosts {
			if seen[target] {
				continue
			}
			seen[target] = true
			err := check(ctx, target, port, tlsConfig, name, password)
			if errors.Is(err, common.ErrUserPasswordMismatch) {
				mismatch = true
				continue
			}
			if err != nil {
				return err
			}
		}
		if mismatch {
			return common.ErrUserPasswordMismatch
		}
		return nil
	}
}

func classifyUserLoginError(err error) error {
	if err == nil {
		return nil
	}
	var exception *clickhouse.Exception
	if errors.As(err, &exception) && exception.Code == 516 {
		return common.ErrUserPasswordMismatch
	}
	return errors.New("ClickHouse user login verification failed; database diagnostics are suppressed")
}

func checkUserLogin(ctx context.Context, host string, port int, tlsConfig *tls.Config, name, password string) error {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", host, port)},
		Auth: clickhouse.Auth{Username: name, Password: password},
		TLS:  tlsConfig, DialTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		return classifyUserLoginError(err)
	}
	defer conn.Close()
	return classifyUserLoginError(conn.Ping(ctx))
}
