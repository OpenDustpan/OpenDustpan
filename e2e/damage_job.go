//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// damagedBody is the body that DamageJob writes: text that is not JSON.
const damagedBody = "not json"

// DamageJob replaces the stored body of the job id with text that is not JSON,
// with direct SQL in the database of the in-process server. The row then has
// a state, a unit ID and a version, and no body that the server can read: a
// damaged job.
func (e *Env) DamageJob(ctx context.Context, tb testing.TB, id string) {
	tb.Helper()

	switch e.store {
	case StoreSQLite:
		query := url.Values{}
		query.Add("_pragma", "busy_timeout(10000)")

		dsn := (&url.URL{
			Scheme: "file", Path: strings.TrimPrefix(e.dbURL, "sqlite:"), RawQuery: query.Encode(),
		}).String()

		conn, err := sql.Open("sqlite", dsn)
		if err != nil {
			tb.Fatalf("open the database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				tb.Errorf("close the database: %v", closeErr)
			}
		}()

		_, err = conn.ExecContext(ctx, `UPDATE jobs SET body = ? WHERE id = ?`, damagedBody, id)
		if err != nil {
			tb.Fatalf("damage job %s: %v", id, err)
		}
	case StorePostgres:
		conn, err := pgx.Connect(ctx, e.dbURL)
		if err != nil {
			tb.Fatalf("connect to the database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				tb.Errorf("close the database: %v", closeErr)
			}
		}()

		_, err = conn.Exec(ctx, `UPDATE jobs SET body = $1 WHERE id = $2`, damagedBody, id)
		if err != nil {
			tb.Fatalf("damage job %s: %v", id, err)
		}
	default:
		tb.Fatalf("unknown store: %s", e.store)
	}
}
