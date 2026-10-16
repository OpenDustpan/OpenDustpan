package servertest

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestProxyCloseCutsTheDatabase proves that a closed proxy cuts an open
// database connection and refuses a new one, so a killed server cannot
// reach the database.
func TestProxyCloseCutsTheDatabase(t *testing.T) {
	t.Parallel()

	base := os.Getenv(EnvURL)
	if base == "" {
		t.Skipf("set %s to run the multi-instance tests", EnvURL)
	}

	p := startProxy(t, base)
	t.Cleanup(p.close)

	conn, err := pgx.Connect(t.Context(), p.url)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close(t.Context()) //nolint:errcheck // The proxy already cut the connection.
	})

	if _, execErr := conn.Exec(t.Context(), "SELECT 1"); execErr != nil {
		t.Fatalf("select through the proxy: %v", execErr)
	}

	p.close()

	if _, execErr := conn.Exec(t.Context(), "SELECT 1"); execErr == nil {
		t.Fatal("select on a cut connection answered no error")
	}

	if again, connErr := pgx.Connect(t.Context(), p.url); connErr == nil {
		_ = again.Close(t.Context()) //nolint:errcheck // The test fails on the connect itself.

		t.Fatal("a new connection through a closed proxy succeeded")
	}
}
