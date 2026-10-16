package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// installationSchema creates a schema with an installation table on conn
// and holds the given states in it.
func installationSchema(t *testing.T, conn *pgx.Conn, states ...string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)
	ident := pgx.Identifier{name}.Sanitize()

	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+ident); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	create := "CREATE TABLE " + ident + ".installation (id INTEGER PRIMARY KEY, state TEXT NOT NULL)"
	if _, err := conn.Exec(t.Context(), create); err != nil {
		t.Fatalf("create installation table: %v", err)
	}

	for i, state := range states {
		insert := "INSERT INTO " + ident + ".installation (id, state) VALUES ($1, $2)"
		if _, err := conn.Exec(t.Context(), insert, i+1, state); err != nil {
			t.Fatalf("insert installation row: %v", err)
		}
	}

	return name
}

// connectKeepInstallation connects to the test database and closes the
// connection after the test.
func connectKeepInstallation(t *testing.T) *pgx.Conn {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, testPostgresURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return conn
}

// TestKeepInstallationStateCopiesTheRow proves that the swap of a restore
// copies the installation row of the previous schema into the restored
// schema.
func TestKeepInstallationStateCopiesTheRow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	conn := connectKeepInstallation(t)
	live := installationSchema(t, conn, "normal")
	previous := installationSchema(t, conn, "maintenance")

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if keepErr := keepInstallationState(ctx, tx, live, previous); keepErr != nil {
		_ = tx.Rollback(ctx) //nolint:errcheck // The test fails on the keep error.
		t.Fatalf("keep installation state: %v", keepErr)
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}

	var state string

	read := "SELECT state FROM " + pgx.Identifier{live}.Sanitize() + ".installation WHERE id = 1"
	if scanErr := conn.QueryRow(ctx, read).Scan(&state); scanErr != nil {
		t.Fatalf("read installation state: %v", scanErr)
	}

	if state != "maintenance" {
		t.Fatalf("installation state = %q, want maintenance", state)
	}
}

// TestKeepInstallationStateRefusesAMissingRow proves that the swap of a
// restore refuses when the previous schema holds no installation row, so a
// restore never leaves the live schema without the row.
func TestKeepInstallationStateRefusesAMissingRow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	conn := connectKeepInstallation(t)
	live := installationSchema(t, conn, "normal")
	previous := installationSchema(t, conn)

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	keepErr := keepInstallationState(ctx, tx, live, previous)

	if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
		t.Fatalf("rollback: %v", rollbackErr)
	}

	if !errors.Is(keepErr, maintenance.ErrUnavailable) {
		t.Fatalf("keep installation state without a row: err = %v, want ErrUnavailable", keepErr)
	}
}
