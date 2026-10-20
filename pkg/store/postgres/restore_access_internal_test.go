package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// testPostgresURL returns the PostgreSQL URL for the keep-access lock test,
// or skips when it is unset.
func testPostgresURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		t.Skipf("set %s to run the keep-access lock test", "DUSTPAN_TEST_POSTGRES_URL")
	}

	return base
}

// openKeepLockStore opens a store in a fresh schema of the database at base.
// Each test gets its own schema, so the suite can share one database.
func openKeepLockStore(t *testing.T, base string) *Store {
	t.Helper()

	suffix := make([]byte, 8)

	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	createQuery := "CREATE SCHEMA " + pgx.Identifier{name}.Sanitize()

	if _, execErr := conn.Exec(t.Context(), createQuery); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}

	query := parsed.Query()
	query.Set("search_path", name)
	parsed.RawQuery = query.Encode()

	s, err := Open(t.Context(), parsed.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// keepLockSink holds the bytes of each portable file in memory, by path.
type keepLockSink struct {
	files map[string]*bytes.Buffer
}

// keepLockWriter is the writer of one file of keepLockSink.
type keepLockWriter struct {
	*bytes.Buffer
}

// Close does nothing: the buffer stays in the sink.
func (keepLockWriter) Close() error {
	return nil
}

func (s *keepLockSink) open(path string) (io.WriteCloser, error) {
	buf := &bytes.Buffer{}
	s.files[path] = buf

	return keepLockWriter{Buffer: buf}, nil
}

func (s *keepLockSink) read(path string) (io.ReadCloser, error) {
	buf, ok := s.files[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}

	return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}

// TestKeepAccessHoldsShareLocksAcrossSwap proves the keep-access restore
// holds a share lock on every live access table between the live read and
// the schema swap: the hook runs there and counts the share locks this
// transaction holds on the live access tables in pg_locks. Without the lock,
// or with the lock taken only after the swap, the count falls short and the
// test fails, so a revoke between the copy and the swap cannot be lost.
func TestKeepAccessHoldsShareLocksAcrossSwap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))

	seed := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now().UTC(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, seed); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	if _, err := s.StagePortable(ctx, sink.read, sum, files, storetest.StageAs(t, s)); err != nil {
		t.Fatalf("stage portable: %v", err)
	}

	var got, want int64

	s.keepAccessHook = func(ctx context.Context, tx pgx.Tx, live string) {
		t.Helper()

		var count int64

		query := `SELECT count(DISTINCT c.relname)
			FROM pg_locks l
			JOIN pg_class c ON c.oid = l.relation
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE l.pid = pg_backend_pid()
			AND l.locktype = 'relation'
			AND l.mode = 'ShareLock'
			AND n.nspname = $1
			AND c.relname = ANY($2)`

		if err := tx.QueryRow(ctx, query, live, backup.AccessTables).Scan(&count); err != nil {
			t.Errorf("count share locks: %v", err)

			return
		}

		got, want = count, int64(len(backup.AccessTables))
	}

	if _, err := s.ApplyStagingKeepingAccess(ctx); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	if got != want {
		t.Fatalf("share-locked access tables = %d, want %d", got, want)
	}
}
