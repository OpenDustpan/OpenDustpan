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
