package postgres_test

// Black-box staging tests: they stage through the public backup ports and
// edit the export stream and the staging schema only through the test-only
// helpers in export_test.go. No port writes an orphan, so the foreign-key
// tests need that surgery.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// stagingTestURL names the variable holding a PostgreSQL URL for the staging
// tests. Each test gets its own schema, so the database can be shared.
const stagingTestURL = "DUSTPAN_TEST_POSTGRES_URL"

// openStagingTestStore opens a store in a fresh schema of the database at the
// test URL.
func openStagingTestStore(t *testing.T) *postgres.Store {
	t.Helper()

	base := os.Getenv(stagingTestURL)
	if base == "" {
		t.Skipf("set %s to run the staging tests", stagingTestURL)
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "stage_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	u, parseErr := url.Parse(base)
	if parseErr != nil {
		t.Fatalf("parse %s: %v", stagingTestURL, parseErr)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	s, err := postgres.Open(t.Context(), u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// TestSummarizeStagingReadsStagedVersion builds a staging schema, removes its
// newest migration record, and summarizes it: the summary must fail with
// ErrCorrupt, proving the check reads the schema version of the staged data
// from the staging schema itself, not from the live schema.
func TestSummarizeStagingReadsStagedVersion(t *testing.T) {
	t.Parallel()

	s := openStagingTestStore(t)
	ctx := t.Context()

	live, liveErr := s.SchemaVersionForTest(ctx)
	if liveErr != nil {
		t.Fatalf("read live schema version: %v", liveErr)
	}

	staging, nameErr := postgres.NewStagingSchemaForTest()
	if nameErr != nil {
		t.Fatalf("name staging schema: %v", nameErr)
	}

	if createErr := s.CreateStagingSchemaForTest(ctx, staging); createErr != nil {
		t.Fatalf("create staging schema: %v", createErr)
	}

	defer func() {
		if dropErr := s.DropStagingSchemaForTest(context.WithoutCancel(ctx), staging); dropErr != nil {
			t.Errorf("drop staging schema: %v", dropErr)
		}
	}()

	// The version check needs only the manifest version.
	want := backup.Summary{SchemaVersion: live} //nolint:exhaustruct_v5 // Test sets only the schema version.

	got, summaryErr := s.SummarizeStagingForTest(ctx, staging, want)
	if summaryErr != nil {
		t.Fatalf("summarize staging area: %v", summaryErr)
	}

	if got.SchemaVersion != live {
		t.Fatalf("staged schema version = %d, want the live version %d", got.SchemaVersion, live)
	}

	if deleteErr := s.DeleteStagedMigrationForTest(ctx, staging, live); deleteErr != nil {
		t.Fatalf("remove staged migration record: %v", deleteErr)
	}

	if _, tamperedErr := s.SummarizeStagingForTest(ctx, staging, want); !errors.Is(tamperedErr, backup.ErrCorrupt) {
		t.Fatalf("summarize a staging area of another schema version: err = %v, want ErrCorrupt", tamperedErr)
	}

	again, againErr := s.SchemaVersionForTest(ctx)
	if againErr != nil || again != live {
		t.Fatalf("live schema version after a refused summary = %d, %v; want %d", again, againErr, live)
	}
}

// TestCloseDropsStagingSchema stages a backup, closes the store without
// discarding, and proves no staging schema stays behind.
func TestCloseDropsStagingSchema(t *testing.T) {
	t.Parallel()

	s := openStagingTestStore(t)
	ctx := t.Context()

	files, sum, descs := storetest.PortableFiles(t, s)

	_, stageErr := s.StagePortable(ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, s))
	if stageErr != nil {
		t.Fatalf("stage backup: %v", stageErr)
	}

	staging := s.StagingSchemaForTest()
	if staging == "" {
		t.Fatal("stage kept no staging schema, want the staging area")
	}

	if !strings.HasPrefix(staging, postgres.StagingSchemaPrefixForTest) {
		t.Fatalf("staging schema = %q, want the staging prefix", staging)
	}

	// The cleanup of openStagingTestStore closes again: the second Close is a
	// no-op once the first drops the staging schema.
	s.Close()

	conn, connErr := pgx.Connect(ctx, os.Getenv(stagingTestURL))
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var count int

	query := `SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = $1`

	if countErr := conn.QueryRow(ctx, query, staging).Scan(&count); countErr != nil {
		t.Fatalf("count staging schemas: %v", countErr)
	}

	if count != 0 {
		t.Fatalf("staging schemas after Close = %d, want none", count)
	}
}

// TestCloseContextDropsStagingSchemaWithCanceledContext stages a backup,
// closes the store with a context that is already canceled, and proves that
// the staging schema is gone: the cleanup runs detached from the context.
func TestCloseContextDropsStagingSchemaWithCanceledContext(t *testing.T) {
	t.Parallel()

	s := openStagingTestStore(t)
	ctx := t.Context()

	files, sum, descs := storetest.PortableFiles(t, s)

	_, stageErr := s.StagePortable(ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, s))
	if stageErr != nil {
		t.Fatalf("stage backup: %v", stageErr)
	}

	staging := s.StagingSchemaForTest()
	if staging == "" {
		t.Fatal("stage kept no staging schema, want the staging area")
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	// The cleanup of openStagingTestStore closes again: the second Close is a
	// no-op once the first drops the staging schema.
	s.CloseContext(canceled)

	conn, connErr := pgx.Connect(ctx, os.Getenv(stagingTestURL))
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var count int

	query := `SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = $1`

	if countErr := conn.QueryRow(ctx, query, staging).Scan(&count); countErr != nil {
		t.Fatalf("count staging schemas: %v", countErr)
	}

	if count != 0 {
		t.Fatalf("staging schemas after CloseContext with a canceled context = %d, want none", count)
	}
}
