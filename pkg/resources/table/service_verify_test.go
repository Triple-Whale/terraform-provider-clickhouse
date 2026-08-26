package resourcetable

import "testing"

func TestNormalizeNestedColumns(t *testing.T) {
	in := []CHColumn{
		{Name: "id", Type: "String"},
		{Name: "object_column.name", Type: "Array(String)"},
		{Name: "object_column.score", Type: "Array(Nullable(Float64))"},
		{Name: "plain_array", Type: "Array(String)"},
		{Name: "sonic_version", Type: "UInt64"},
	}
	out := normalizeNestedColumns(in)
	if len(out) != 4 {
		t.Fatalf("expected 4 columns, got %d: %+v", len(out), out)
	}
	if out[1].Name != "object_column" || out[1].Type != "Nested(name String, score Nullable(Float64))" {
		t.Fatalf("bad nested fold: %+v", out[1])
	}
	if out[2].Name != "plain_array" || out[2].Type != "Array(String)" {
		t.Fatalf("plain array must survive untouched: %+v", out[2])
	}
}

func TestNormalizeNestedColumnsNoNested(t *testing.T) {
	in := []CHColumn{{Name: "id", Type: "String"}, {Name: "v", Type: "UInt64"}}
	out := normalizeNestedColumns(in)
	if len(out) != 2 {
		t.Fatalf("expected passthrough, got %+v", out)
	}
}

func TestSameColumns(t *testing.T) {
	a := []CHColumn{{Name: "id", Type: "String"}, {Name: "v", Type: "UInt64"}}
	b := []CHColumn{{Name: "v", Type: "UInt64"}, {Name: "id", Type: "String"}}
	if !sameColumns(a, b) {
		t.Fatal("order must not matter")
	}
	c := []CHColumn{{Name: "id", Type: "String"}, {Name: "hacked", Type: "String"}}
	if sameColumns(a, c) {
		t.Fatal("extra column must diverge")
	}
	d := []CHColumn{{Name: "id", Type: "Int64"}, {Name: "v", Type: "UInt64"}}
	if sameColumns(a, d) {
		t.Fatal("type change must diverge")
	}
}
