package resourcetable

import (
	"reflect"
	"testing"
)

func TestKeepHiddenParams(t *testing.T) {
	prior := []string{"'10.0.0.1:5432'", "'db'", "'tbl'", "'user'", "'s3cr3t'"}

	read := GetEngineParams("PostgreSQL('10.0.0.1:5432', 'db', 'tbl', 'user', '[HIDDEN]')")
	if got := keepHiddenParams(read, prior); !reflect.DeepEqual(got, prior) {
		t.Fatalf("a [HIDDEN] password must keep the declared value, got %v", got)
	}

	changed := GetEngineParams("PostgreSQL('10.0.0.2:5432', 'db', 'tbl', 'user', '[HIDDEN]')")
	want := []string{"'10.0.0.2:5432'", "'db'", "'tbl'", "'user'", "'s3cr3t'"}
	if got := keepHiddenParams(changed, prior); !reflect.DeepEqual(got, want) {
		t.Fatalf("a changed non-secret param must still show, got %v", got)
	}

	extra := GetEngineParams("PostgreSQL('10.0.0.1:5432', 'db', 'tbl', 'user', '[HIDDEN]', 'schema')")
	if got := keepHiddenParams(extra, prior); !reflect.DeepEqual(got, extra) {
		t.Fatalf("a different number of params must not be merged, got %v", got)
	}

	if got := keepHiddenParams(read, nil); !reflect.DeepEqual(got, read) {
		t.Fatalf("with no prior state (import) the read value is kept, got %v", got)
	}
}
