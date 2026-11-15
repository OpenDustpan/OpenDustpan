package postgres

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// testPostgresURL returns the PostgreSQL URL for the tests of this package,
// or skips when it is unset.
func testPostgresURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL tests", "DUSTPAN_TEST_POSTGRES_URL")
	}

	return base
}

// openKeepLockStore opens a store in a migrated schema of a database of its
// own, cloned from the template database.
func openKeepLockStore(t *testing.T, base string) *Store {
	t.Helper()

	s, err := Open(t.Context(), pgtest.MigratedURL(t, base))
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
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now().UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
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

// TestStagePortableRefusesPlacementRuleOfAnotherNamespace checks that a
// restore refuses a backup whose placement rule names a collection of
// another namespace. The rule row is rewritten in the backup file after the
// export, and the descriptor of the file is rebuilt with the new bytes, so
// only the database constraint of the staging schema can refuse the row. The
// stage reports ErrCorrupt and the live database keeps its rule.
func TestStagePortableRefusesPlacementRuleOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	seed := newPlacementSeed(t)
	s := seed.s

	_, err := s.pool.Exec(t.Context(), placementRuleInsert,
		ident.NewID("plr-"), seed.beta.Name.String(), seed.colID, 1_700_000_000_000,
	)
	if err != nil {
		t.Fatalf("insert a rule of beta that names the collection of beta: %v", err)
	}

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(t.Context(), sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	files = placementRulesOfAnotherNamespace(t, sink, files, seed.alpha.Name.String())

	_, err = s.StagePortable(t.Context(), sink.read, sum, files, storetest.StageAs(t, s))
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("stage the backup with a rule of another namespace = %v, want ErrCorrupt", err)
	}

	var rules int

	err = s.pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM placement_rules").Scan(&rules)
	if err != nil {
		t.Fatalf("count live placement rules: %v", err)
	}

	if rules != 1 {
		t.Fatalf("live placement rules after the refused stage = %d, want 1", rules)
	}
}

// placementRulesOfAnotherNamespace rewrites the placement rules file of the
// backup in sink: every rule row keeps its collection ID but moves to the
// namespace to, which the backup also holds. The descriptor of the file is
// rebuilt with the new bytes, so the rewrite itself stays undetected and only
// the database constraint can refuse the row.
func placementRulesOfAnotherNamespace(
	t *testing.T, sink *keepLockSink, files []backup.PortableFile, to string,
) []backup.PortableFile {
	t.Helper()

	for i, file := range files {
		if file.Table == "placement_rules" {
			files[i] = placementRuleFileOfAnotherNamespace(t, sink, file, to)
		}
	}

	return files
}

// placementRuleFileOfAnotherNamespace rewrites the one placement rules file:
// every rule row keeps its collection ID but moves to the namespace to. The
// descriptor is rebuilt with the new bytes, so the rewrite itself stays
// undetected.
func placementRuleFileOfAnotherNamespace(
	t *testing.T, sink *keepLockSink, file backup.PortableFile, to string,
) backup.PortableFile {
	t.Helper()

	rows := placementDecodeRuleRows(t, sink, file, to)
	raw, sum := placementEncodeRuleRows(t, rows)
	placementWriteRuleFile(t, sink, file.Path, raw.Bytes())

	file.Size = int64(raw.Len())
	file.SHA256 = hex.EncodeToString(sum.Sum(nil))

	return file
}

// placementDecodeRuleRows reads the rule rows of the file and returns them
// with the namespace replaced by to.
func placementDecodeRuleRows(
	t *testing.T, sink *keepLockSink, file backup.PortableFile, to string,
) []map[string]json.RawMessage {
	t.Helper()

	src, err := sink.read(file.Path)
	if err != nil {
		t.Fatalf("read the placement rules file: %v", err)
	}

	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			t.Errorf("close the placement rules file: %v", closeErr)
		}
	}()

	zr, err := gzip.NewReader(src)
	if err != nil {
		t.Fatalf("open the gzip of the placement rules file: %v", err)
	}

	dec := json.NewDecoder(zr)

	rows := make([]map[string]json.RawMessage, 0, file.Rows)

	for {
		var row map[string]json.RawMessage

		err = dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			return rows
		}

		if err != nil {
			t.Fatalf("decode a placement rule row: %v", err)
		}

		row["namespace"] = json.RawMessage(`"` + to + `"`)
		rows = append(rows, row)
	}
}

// placementEncodeRuleRows encodes the rule rows as the one file of the backup,
// and returns its bytes and its digest.
func placementEncodeRuleRows(
	t *testing.T, rows []map[string]json.RawMessage,
) (*bytes.Buffer, hash.Hash) {
	t.Helper()

	sum := sha256.New()
	raw := &bytes.Buffer{}
	gz := gzip.NewWriter(io.MultiWriter(raw, sum))
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)

	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatalf("encode a placement rule row: %v", err)
		}
	}

	if err := gz.Close(); err != nil {
		t.Fatalf("finish the placement rules file: %v", err)
	}

	return raw, sum
}

// placementWriteRuleFile writes the bytes of the rewritten file into the
// sink.
func placementWriteRuleFile(t *testing.T, sink *keepLockSink, path string, raw []byte) {
	t.Helper()

	out, err := sink.open(path)
	if err != nil {
		t.Fatalf("rewrite the placement rules file: %v", err)
	}

	_, err = out.Write(raw)
	if err != nil {
		t.Fatalf("write the placement rules file: %v", err)
	}

	if closeErr := out.Close(); closeErr != nil {
		t.Fatalf("close the placement rules file: %v", closeErr)
	}
}
