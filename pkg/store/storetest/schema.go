package storetest

import (
	"context"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// SchemaStore is the schema-reporting port the installation API answers for
// `opendustpan upgrade check`: the applied migrations of the database,
// ordered by version.
type SchemaStore interface {
	// AppliedMigrations returns the rows of schema_migrations ordered by
	// version, or nothing when the database records none.
	AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error)
}

// SchemaFactory returns a fresh, migrated store for one subtest. Opening a
// store applies every embedded migration, so the factory store records them
// all.
type SchemaFactory func(t *testing.T) SchemaStore

// RunSchema executes the conformance suite of the schema-reporting port
// against stores produced by newStore. A new adapter must run it, and a new
// port method must get conformance tests here before any adapter implements
// it.
func RunSchema(t *testing.T, newStore SchemaFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s SchemaStore)
		name string
	}{
		{name: "applied migrations are ordered by version", test: testAppliedOrdered},
		{name: "applied names match their versions", test: testAppliedNamesMatchVersions},
		{name: "applied names are the embedded list", test: testAppliedAreEmbedded},
		{name: "repeat reads match without migrating", test: testAppliedRepeatReads},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// readApplied returns the applied migrations of a migrated store, failing
// the test when the call errors or reports none: the factory store records
// every embedded migration at open.
func readApplied(t *testing.T, s SchemaStore) []schema.AppliedMigration {
	t.Helper()

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}

	if len(applied) == 0 {
		t.Fatal("applied migrations hold nothing, want the embedded list")
	}

	return applied
}

// testAppliedOrdered proves the applied rows come ordered by version with no
// duplicate.
func testAppliedOrdered(t *testing.T, s SchemaStore) {
	t.Helper()

	applied := readApplied(t, s)

	versions := make([]int64, 0, len(applied))

	for _, migration := range applied {
		versions = append(versions, migration.Version)
	}

	if !slices.IsSorted(versions) {
		t.Fatalf("applied versions = %v, want them ordered", versions)
	}

	if len(slices.Compact(slices.Clone(versions))) != len(versions) {
		t.Fatalf("applied versions = %v, want no duplicate", versions)
	}
}

// testAppliedNamesMatchVersions proves every applied name parses to the
// version its row records.
func testAppliedNamesMatchVersions(t *testing.T, s SchemaStore) {
	t.Helper()

	for _, migration := range readApplied(t, s) {
		version, err := schema.ParseMigrationVersion(migration.Name)
		if err != nil {
			t.Fatalf("applied migration %q: %v", migration.Name, err)
		}

		if version != migration.Version {
			t.Fatalf("applied migration %q records version %d, want %d",
				migration.Name, migration.Version, version)
		}
	}
}

// testAppliedAreEmbedded proves the applied names are exactly the embedded
// list the binary shares with the upgrade check client: the verdict never
// depends on which dialect the server runs.
func testAppliedAreEmbedded(t *testing.T, s SchemaStore) {
	t.Helper()

	applied := readApplied(t, s)

	names := make([]string, 0, len(applied))
	versions := make([]int64, 0, len(applied))

	for _, migration := range applied {
		names = append(names, migration.Name)
		versions = append(versions, migration.Version)
	}

	wantNames, wantNewest := schema.EmbeddedMigrationNames()

	if !slices.Equal(names, wantNames) {
		t.Fatalf("applied names = %v, want the embedded list %v", names, wantNames)
	}

	if got := schema.DatabaseVersion(versions); got != wantNewest {
		t.Fatalf("newest applied version = %d, want the embedded newest %d", got, wantNewest)
	}
}

// testAppliedRepeatReads proves a second read matches the first: the report
// is a read-only inspection that migrates nothing.
func testAppliedRepeatReads(t *testing.T, s SchemaStore) {
	t.Helper()

	first := readApplied(t, s)

	second, err := s.AppliedMigrations(t.Context())
	if err != nil {
		t.Fatalf("second applied migrations read: %v", err)
	}

	if !slices.Equal(first, second) {
		t.Fatalf("second read = %v, want the first read %v", second, first)
	}
}
