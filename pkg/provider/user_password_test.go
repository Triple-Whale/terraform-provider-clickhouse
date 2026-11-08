package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func TestUserLoginErrorsAreClassifiedAndSanitized(t *testing.T) {
	if err := classifyUserLoginError(nil); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(classifyUserLoginError(&clickhouse.Exception{Code: 516, Message: "secret-password"}), common.ErrUserPasswordMismatch) {
		t.Fatal("authentication denial must produce password drift")
	}
	for _, err := range []error{errors.New("secret-password"), &clickhouse.Exception{Code: 497, Message: "secret-password"}} {
		safe := classifyUserLoginError(err)
		if errors.Is(safe, common.ErrUserPasswordMismatch) || strings.Contains(safe.Error(), "secret-password") {
			t.Fatal("non-authentication error was misclassified or leaked")
		}
	}
}

func TestUserLoginChecksOnlyIntendedHosts(t *testing.T) {
	for _, cluster := range []string{"", "main"} {
		calls := []string{}
		tlsConfig := &tls.Config{}
		check := func(_ context.Context, host string, port int, tlsArg *tls.Config, name, password string) error {
			calls = append(calls, host)
			if port != 9440 || tlsArg != tlsConfig || name != "service" || password != "secret" {
				t.Fatal("login options were not preserved")
			}
			return nil
		}
		verify := userPasswordVerifierWithCheck("primary", []string{"primary", "replica"}, 9440, tlsConfig, check)
		if err := verify(context.Background(), cluster, "service", "secret"); err != nil {
			t.Fatal(err)
		}
		expected := []string{"primary"}
		if cluster != "" {
			expected = append(expected, "replica")
		}
		if !reflect.DeepEqual(calls, expected) {
			t.Fatal("login verification included the wrong target hosts")
		}
	}
}
func TestUserLoginNetworkFailureOverridesPasswordMismatch(t *testing.T) {
	calls := 0
	verify := userPasswordVerifierWithCheck("primary", []string{"replica"}, 9000, nil, func(context.Context, string, int, *tls.Config, string, string) error {
		calls++
		if calls == 1 {
			return common.ErrUserPasswordMismatch
		}
		return errors.New("unreachable")
	})
	err := verify(context.Background(), "main", "user", "password")
	if err == nil || errors.Is(err, common.ErrUserPasswordMismatch) || calls != 2 {
		t.Fatal("unreachable replicas must not be treated as password drift")
	}
}
