package sqlite_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func TestIdempotencyConformance(t *testing.T) {
	t.Parallel()

	idempotencytest.Run(t, func(t *testing.T) idempotency.Store {
		t.Helper()

		return open(t).Idempotency(idempotencytest.TestWrapper(t, 1))
	})
}

// sealedDB runs the sealed-key cases on one database file. The file allows
// one open store, so Open closes the store of the call before.
type sealedDB struct {
	path string
	cur  *sqlite.Store
}

// Open implements idempotencytest.SealedDB. It closes the store of the call
// before.
//
//nolint:ireturn // The suite takes the port.
func (d *sealedDB) Open(t *testing.T, w secret.Wrapper) idempotency.Store {
	t.Helper()

	d.closeCurrent(t)

	s, err := sqlite.Open(t.Context(), d.path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	d.cur = s

	return s.Idempotency(w)
}

func (d *sealedDB) RawServerKey(t *testing.T) []byte {
	t.Helper()

	var raw []byte

	d.scan(t, `SELECT wrapped_key FROM idempotency_server_key`, &raw)

	return raw
}

func (d *sealedDB) RawFingerprint(t *testing.T) []byte {
	t.Helper()

	var raw []byte

	d.scan(t, `SELECT fingerprint FROM idempotency_records`, &raw)

	return raw
}

func (d *sealedDB) RecordCount(t *testing.T) int {
	t.Helper()

	var n int

	d.scan(t, `SELECT COUNT(*) FROM idempotency_records`, &n)

	return n
}

// closeCurrent closes the open store, if any.
func (d *sealedDB) closeCurrent(t *testing.T) {
	t.Helper()

	if d.cur == nil {
		return
	}

	if err := d.cur.Close(); err != nil {
		t.Errorf("close store: %v", err)
	}

	d.cur = nil
}

// scan reads one value of query from the raw database.
func (d *sealedDB) scan(t *testing.T, query string, dest any) {
	t.Helper()

	conn, err := openRawDB(d.path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	if err = conn.QueryRowContext(t.Context(), query).Scan(dest); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestIdempotencySealedKey(t *testing.T) {
	t.Parallel()

	idempotencytest.RunSealed(t, func(t *testing.T) idempotencytest.SealedDB {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		d := &sealedDB{path: path, cur: nil}

		t.Cleanup(func() { d.closeCurrent(t) })

		return d
	})
}

// TestIdempotencyKeepsNoRequestBytes proves the database file holds the
// fingerprint of a request and never the request: the file and its log hold
// no text of the request bytes.
func TestIdempotencyKeepsNoRequestBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)
	material := []byte("a-secret-in-the-request-body-0123456789")
	ref := idempotency.Ref{Principal: "alice", Method: "POST", Path: "/api/dustpan/v1/p", Key: "k1"}

	res, err := s.Idempotency(idempotencytest.TestWrapper(t, 1)).Begin(t.Context(), ref, material, time.Now())
	if err != nil || res.Outcome != idempotency.Started {
		t.Fatalf("begin: outcome = %d, err = %v", res.Outcome, err)
	}

	answer := idempotency.Answer{Status: 201, Headers: nil, Body: []byte(`{"id":"p1"}`)}
	records := s.Idempotency(idempotencytest.TestWrapper(t, 1))

	if err = records.Complete(t.Context(), ref, res.Token, answer); err != nil {
		t.Fatalf("complete: %v", err)
	}

	for _, name := range []string{path, path + "-wal"} {
		data, readErr := os.ReadFile(name) //nolint:gosec // The test reads its own temporary database file.
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}

		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if bytes.Contains(data, material) {
			t.Errorf("%s holds the request bytes", name)
		}
	}
}
