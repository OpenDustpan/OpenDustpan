package schema_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
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

// TestBaselineVersionsAgree pins the baseline version of the backup package,
// which cannot import this package, to the baseline version of the schema.
func TestBaselineVersionsAgree(t *testing.T) {
	t.Parallel()

	if int64(backup.BaselineSchemaVersion) != schema.BaselineVersion {
		t.Fatalf(
			"backup baseline %d, schema baseline %d, want equal",
			backup.BaselineSchemaVersion,
			schema.BaselineVersion,
		)
	}

	version, err := schema.ParseMigrationVersion(schema.BaselineName)
	if err != nil || version != schema.BaselineVersion {
		t.Fatalf(
			"baseline name %q has version %d, %v, want %d",
			schema.BaselineName,
			version,
			err,
			schema.BaselineVersion,
		)
	}
}

func TestCheckBaseline(t *testing.T) {
	t.Parallel()

	for _, recorded := range [][]int64{nil, {schema.BaselineVersion}, {schema.BaselineVersion, schema.BaselineVersion + 1}} {
		if err := schema.CheckBaseline(recorded); err != nil {
			t.Fatalf("check baseline of %v: %v, want nil", recorded, err)
		}
	}

	err := schema.CheckBaseline([]int64{1, 75, 76})

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("check baseline of 1, 75, 76 = %v, want a BeforeBaselineError", err)
	}

	want := "database schema version 76 is older than baseline schema version 100: " +
		"no binary can upgrade this database. Start the server on an empty database"
	if before.Error() != want {
		t.Fatalf("message = %q, want %q", before.Error(), want)
	}
}
