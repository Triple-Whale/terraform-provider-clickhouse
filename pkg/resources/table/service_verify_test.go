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
	out := normalizeNestedColumns(in, map[string]bool{"object_column": true})
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
	out := normalizeNestedColumns(in, map[string]bool{})
	if len(out) != 2 {
		t.Fatalf("expected passthrough, got %+v", out)
	}
}

func TestNormalizeSkipsUndeclaredDottedArrays(t *testing.T) {
	// crm_contacts-style: dotted Array columns DECLARED flattened must never be folded
	in := []CHColumn{
		{Name: "lifecyclestage_history.value", Type: "Array(String)"},
		{Name: "lifecyclestage_history.timestamp", Type: "Array(DateTime)"},
	}
	out := normalizeNestedColumns(in, map[string]bool{})
	if len(out) != 2 || out[0].Name != "lifecyclestage_history.value" {
		t.Fatalf("undeclared dotted arrays must pass through untouched: %+v", out)
	}
}

func TestFlattenDeclaredColumns(t *testing.T) {
	in := []ColumnDefinition{
		{Name: "id", Type: "String"},
		{Name: "object_column", Type: "Nested(name String, score Nullable(Float64))"},
	}
	out := flattenDeclaredColumns(in)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %+v", out)
	}
	if out[1].Name != "object_column.name" || out[1].Type != "Array(String)" {
		t.Fatalf("bad flatten: %+v", out[1])
	}
	if out[2].Name != "object_column.score" || out[2].Type != "Array(Nullable(Float64))" {
		t.Fatalf("bad flatten: %+v", out[2])
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
