package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/firstadmin/firstadmintest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestFirstAdministratorConformance runs the first administrator store suite.
func TestFirstAdministratorConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	firstadmintest.RunFirstAdministrator(t, func(t *testing.T) firstadmintest.Harness {
		t.Helper()

		s := open(t, base)
		pool := postgres.Pool(s)

		return firstadmintest.Harness{
			Store:             s,
			BreakAuditAppends: func(ctx context.Context) error { return breakFirstAdminAudit(ctx, pool) },
			SetSubject: func(ctx context.Context, principalID, subject string) error {
				return setFirstAdminSubject(ctx, pool, principalID, subject)
			},
			InsertCode: func(ctx context.Context, r firstadmintest.CodeRow) error {
				return insertFirstAdminCode(ctx, pool, r)
			},
			PutSession: func(ctx context.Context, r firstadmintest.SessionRow) error {
				return putFirstAdminSession(ctx, pool, r)
			},
			IgnoreCodeDeletes: func(ctx context.Context) error { return ignoreFirstAdminCodeDeletes(ctx, pool) },
			InsertMarker: func(ctx context.Context, r firstadmintest.MarkerRow) error {
				return insertFirstAdminMarker(ctx, pool, r)
			},
		}
	})
}

// breakFirstAdminAudit makes each later insert in the audit log fail.
func breakFirstAdminAudit(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE FUNCTION break_audit_appends() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN RAISE EXCEPTION 'audit append broken by the test'; END $$`)
	if err != nil {
		return fmt.Errorf("create the function: %w", err)
	}

	_, err = pool.Exec(ctx, `CREATE TRIGGER break_audit_appends BEFORE INSERT ON audit_entries
FOR EACH ROW EXECUTE FUNCTION break_audit_appends()`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}

// setFirstAdminSubject changes the subject of a principal with no validation.
func setFirstAdminSubject(ctx context.Context, pool *pgxpool.Pool, principalID, subject string) error {
	if _, err := pool.Exec(ctx, `UPDATE principals SET subject = $1 WHERE id = $2`, subject, principalID); err != nil {
		return fmt.Errorf("set subject: %w", err)
	}

	return nil
}

// insertFirstAdminCode writes a row of the code table with direct SQL.
func insertFirstAdminCode(ctx context.Context, pool *pgxpool.Pool, r firstadmintest.CodeRow) error {
	_, err := pool.Exec(ctx, `INSERT INTO first_administrator_codes
(id, principal_id, digest, created_by, reason, created_at_ms, expires_at_ms) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.ID, r.PrincipalID, r.Digest, r.CreatedBy, r.Reason, r.CreatedAtMs, r.ExpiresAtMs)
	if err != nil {
		return fmt.Errorf("insert code row: %w", err)
	}

	return nil
}

// insertFirstAdminMarker writes a row of the marker table with direct SQL.
func insertFirstAdminMarker(ctx context.Context, pool *pgxpool.Pool, r firstadmintest.MarkerRow) error {
	_, err := pool.Exec(ctx, `INSERT INTO first_administrator
(id, principal_id, grant_id, session_id, created_by, made_at_ms) VALUES ($1, $2, $3, $4, $5, $6)`,
		r.ID, r.PrincipalID, r.GrantID, r.SessionID, r.CreatedBy, r.MadeAtMs)
	if err != nil {
		return fmt.Errorf("insert marker row: %w", err)
	}

	return nil
}

// putFirstAdminSession writes a session row with direct SQL, or replaces the row
// with the same ID.
func putFirstAdminSession(ctx context.Context, pool *pgxpool.Pool, r firstadmintest.SessionRow) error {
	var ended *int64
	if r.EndedAtMs != 0 {
		ended = &r.EndedAtMs
	}

	_, err := pool.Exec(ctx, `INSERT INTO sessions
(id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, ended_at_ms, generation)
VALUES ($1, $2, $3, 'corp', $4, $5, $6, $7, 1)
ON CONFLICT (id) DO UPDATE SET principal_id = excluded.principal_id, created_at_ms = excluded.created_at_ms,
  last_seen_at_ms = excluded.last_seen_at_ms, auth_time_ms = excluded.auth_time_ms, ended_at_ms = excluded.ended_at_ms`,
		r.ID, r.PrincipalID, "digest-of-"+r.ID, r.CreatedAtMs, r.LastSeenAtMs, r.AuthTimeMs, ended)
	if err != nil {
		return fmt.Errorf("put session row: %w", err)
	}

	return nil
}

// ignoreFirstAdminCodeDeletes makes each later delete in the code table match
// no row.
func ignoreFirstAdminCodeDeletes(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE FUNCTION ignore_code_deletes() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN RETURN NULL; END $$`)
	if err != nil {
		return fmt.Errorf("create the function: %w", err)
	}

	_, err = pool.Exec(ctx, `CREATE TRIGGER ignore_code_deletes BEFORE DELETE ON first_administrator_codes
FOR EACH ROW EXECUTE FUNCTION ignore_code_deletes()`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}
