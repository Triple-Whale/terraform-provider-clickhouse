package resourceview

import "testing"

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
