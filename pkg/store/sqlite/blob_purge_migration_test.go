package sqlite_test

import (
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// blobPurgeNamespaceIDVersion is the version of the migration that gives a
// blob purge record the ID of its namespace in place of the name.
const blobPurgeNamespaceIDVersion = 144

// TestBlobPurgeNamespaceIDMigration applies the migrations before the record
// held an ID, plants records that name a live namespace and a missing one,
// then opens the store, which applies the rest. A record of a live namespace
// gets its ID. A record of a missing namespace gets the empty ID, which the
// sweep leaves in place.
func TestBlobPurgeNamespaceIDMigration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= blobPurgeNamespaceIDVersion })
	path := filepath.Join(t.TempDir(), "blobpurge.db")

	applyTagMigrations(ctx, t, path, before)

	liveID := namespace.NewID()
	cases := blobPurgeMigrationCases(liveID)

	plantBlobPurges(t, path, liveID, cases)

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	records, err := s.ListBlobPurges(ctx, nil, 100)
	if err != nil || len(records) != len(cases) {
		t.Fatalf("blob purge records = %d (%v), want %d", len(records), err, len(cases))
	}

	checkMigratedBlobPurges(t, records, cases)
}

// blobPurgeMigrationCase is one planted record and the namespace ID that the
// migration must give it.
type blobPurgeMigrationCase struct {
	name   string
	unitID string
	ns     string
	want   namespace.ID
}

// blobPurgeMigrationCases returns a record of the live namespace shop and a
// record of a namespace that does not exist.
func blobPurgeMigrationCases(liveID namespace.ID) []blobPurgeMigrationCase {
	return []blobPurgeMigrationCase{
		{name: "a live namespace gets its ID", unitID: "stk-live", ns: "shop", want: liveID},
		{name: "a missing namespace gets the empty ID", unitID: "stk-gone", ns: "missing", want: ""},
	}
}

// plantBlobPurges inserts the namespace shop and the records of cases in the
// shape before the migration: the namespace name, no ID.
func plantBlobPurges(t *testing.T, path string, liveID namespace.ID, cases []blobPurgeMigrationCase) {
	t.Helper()

	ctx := t.Context()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	_, err = conn.ExecContext(ctx,
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
			VALUES ('shop', 'eu-west', 1, 0, ?)`, liveID.String())
	if err != nil {
		t.Fatalf("plant namespace: %v", err)
	}

	for _, tc := range cases {
		_, err = conn.ExecContext(ctx,
			`INSERT INTO blob_purges (tool, unit_id, namespace, purged_at_ms) VALUES ('pulumi', ?, ?, 1000)`,
			tc.unitID, tc.ns)
		if err != nil {
			t.Fatalf("%s: plant record: %v", tc.name, err)
		}
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}
}

// checkMigratedBlobPurges checks the namespace ID of each record against its case.
func checkMigratedBlobPurges(t *testing.T, records []inventory.BlobPurge, cases []blobPurgeMigrationCase) {
	t.Helper()

	for _, tc := range cases {
		i := slices.IndexFunc(records, func(r inventory.BlobPurge) bool { return r.UnitID == tc.unitID })
		if i < 0 || records[i].NamespaceID != tc.want {
			t.Errorf("%s: records = %+v, want namespace ID %q", tc.name, records, tc.want)
		}
	}
}
