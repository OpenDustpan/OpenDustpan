package schema_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/store/schema"
)

func TestSchemaNewerError(t *testing.T) {
	t.Parallel()

	err := schema.NewSchemaNewerError([]int64{1, 2, 11}, []int64{1, 2, 10})

	if !errors.Is(err, schema.ErrSchemaNewer) {
		t.Fatalf("errors.Is = false, want true for %v", err)
	}

	if err.DatabaseVersion != 11 || err.BinaryVersion != 10 {
		t.Fatalf("versions = %d/%d, want 11/10", err.DatabaseVersion, err.BinaryVersion)
	}

	want := "database schema version 11 is newer than binary schema version 10: " +
		"start a newer binary or restore a backup"
	if err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
}

func TestVersions(t *testing.T) {
	t.Parallel()

	if got := schema.BinaryVersion([]int64{1, 3, 2}); got != 3 {
		t.Fatalf("binary version = %d, want 3", got)
	}

	if got := schema.DatabaseVersion([]int64{1, 11, 4}); got != 11 {
		t.Fatalf("database version = %d, want 11", got)
	}

	if got := schema.BinaryVersion(nil); got != 0 {
		t.Fatalf("empty binary version = %d, want 0", got)
	}

	if got := schema.DatabaseVersion(nil); got != 0 {
		t.Fatalf("empty database version = %d, want 0", got)
	}
}
