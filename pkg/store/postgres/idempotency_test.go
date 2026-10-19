package postgres_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/secret"
)

func TestIdempotencyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	idempotencytest.Run(t, func(t *testing.T) idempotency.Store {
		t.Helper()

		return open(t, base).Idempotency(idempotencytest.TestWrapper(t, 1))
	})
}

// sealedDB runs the sealed-key cases on one schema.
type sealedDB struct {
	url string
}

// Open implements idempotencytest.SealedDB.
//
//nolint:ireturn // The suite takes the port.
func (d sealedDB) Open(t *testing.T, w secret.Wrapper) idempotency.Store {
	t.Helper()

	return openURL(t, d.url).Idempotency(w)
}

func (d sealedDB) RawServerKey(t *testing.T) []byte {
	t.Helper()

	var raw []byte

	d.scan(t, `SELECT wrapped_key FROM idempotency_server_key`, &raw)

	return raw
}

func (d sealedDB) RawFingerprint(t *testing.T) []byte {
	t.Helper()

	var raw []byte

	d.scan(t, `SELECT fingerprint FROM idempotency_records`, &raw)

	return raw
}

func (d sealedDB) RecordCount(t *testing.T) int {
	t.Helper()

	var n int

	d.scan(t, `SELECT COUNT(*) FROM idempotency_records`, &n)

	return n
}

// scan reads one value of query from the schema.
func (d sealedDB) scan(t *testing.T, query string, dest any) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), d.url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close connection: %v", closeErr)
		}
	}()

	if err = conn.QueryRow(t.Context(), query).Scan(dest); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestIdempotencySealedKey(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	idempotencytest.RunSealed(t, func(t *testing.T) idempotencytest.SealedDB {
		t.Helper()

		return sealedDB{url: isolatedURL(t, base)}
	})
}
