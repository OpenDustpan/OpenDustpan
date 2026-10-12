package schema_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
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

func TestParseMigrationVersion(t *testing.T) {
	t.Parallel()

	if got, err := schema.ParseMigrationVersion("0014_checkpoint_resource_count.sql"); err != nil || got != 14 {
		t.Fatalf("parse = %d, %v, want 14, nil", got, err)
	}

	for _, name := range []string{"", "init.sql", "0000_zero.sql", "-1_neg.sql", "xx_bad.sql"} {
		if _, err := schema.ParseMigrationVersion(name); !errors.Is(err, schema.ErrBadMigrationName) {
			t.Fatalf("parse %q: err = %v, want ErrBadMigrationName", name, err)
		}
	}
}

func TestEmbeddedMigrationNames(t *testing.T) {
	t.Parallel()

	names, newest := schema.EmbeddedMigrationNames()

	if len(names) == 0 || newest <= 0 {
		t.Fatalf("embedded list holds %d names with newest %d, want a non-empty list", len(names), newest)
	}

	for _, name := range names {
		version, err := schema.ParseMigrationVersion(name)
		if err != nil {
			t.Fatalf("embedded name %q: %v", name, err)
		}

		if version > newest {
			t.Fatalf("embedded name %q holds version %d above newest %d", name, version, newest)
		}
	}

	second, secondNewest := schema.EmbeddedMigrationNames()
	if !slices.Equal(names, second) || newest != secondNewest {
		t.Fatalf("second call = %v/%d, want %v/%d", second, secondNewest, names, newest)
	}

	names[0] = "changed.sql"

	third, _ := schema.EmbeddedMigrationNames()
	if third[0] == "changed.sql" {
		t.Fatal("embedded names share backing storage, want a copy per call")
	}
}
