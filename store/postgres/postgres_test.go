package postgres_test

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/store/postgres"
	"opendustpan.org/opendustpan/store/storetest"
)

// envURL names the variable holding a PostgreSQL URL for integration tests.
// Each subtest gets its own schema, so the database can be shared.
const envURL = "DUSTPAN_TEST_POSTGRES_URL"

func TestConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		s, err := postgres.Open(t.Context(), isolatedURL(t, base))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return s
	})
}

// isolatedURL creates a fresh schema and returns base with search_path set to it.
func isolatedURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	schema := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	_, err = conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", envURL, err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}
