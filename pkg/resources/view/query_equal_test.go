package resourceview

import (
	"os/exec"
	"testing"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/common"
)

func TestQueriesEqualHiddenPassword(t *testing.T) {
	stored := "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', '[HIDDEN]') WHERE x = 'keep'"
	declared := "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', 's3cr3t') WHERE x = 'keep'"
	if !queriesEqual(stored, declared) {
		t.Fatal("a [HIDDEN] password must match the declared literal")
	}
	if queriesEqual(stored, "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', 's3cr3t') WHERE x = 'other'") {
		t.Fatal("a changed non-secret literal must not be suppressed")
	}
	if queriesEqual(stored, "SELECT b FROM postgresql('h:5432', 'db', 't', 'u', 's3cr3t') WHERE x = 'keep'") {
		t.Fatal("a changed column must not be suppressed")
	}
	if queriesEqual(stored, "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', 's3cr3t', 'extra') WHERE x = 'keep'") {
		t.Fatal("a different number of literals must not be suppressed")
	}
	if !queriesEqual("select a from t", "SELECT  a FROM t") {
		t.Fatal("whitespace and case are still normalized")
	}
}

func TestQueriesEqualRawStateVsFormatted(t *testing.T) {
	if _, err := exec.LookPath("clickhouse"); err != nil {
		t.Skip("clickhouse binary not available")
	}
	raw := "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', 'pw') WHERE arrayElement(splitByChar('-', hostName()), -3) = '0'"
	formatted := common.FormatSQL(raw)
	if formatted == raw {
		t.Fatal("expected clickhouse format to normalize the query")
	}
	if queriesEqual(raw, formatted) {
		t.Fatal("sanity: raw vs formatted must differ without formatting both sides")
	}
	if !queriesEqual(common.FormatSQL(raw), common.FormatSQL(formatted)) {
		t.Fatal("formatting both sides must compare equal")
	}
	masked := "SELECT a FROM postgresql('h:5432', 'db', 't', 'u', '[HIDDEN]') WHERE (splitByChar('-', hostName())[-3]) = '0'"
	if !queriesEqual(common.FormatSQL(masked), common.FormatSQL(raw)) {
		t.Fatal("masked as_select vs raw declared must compare equal")
	}
}
