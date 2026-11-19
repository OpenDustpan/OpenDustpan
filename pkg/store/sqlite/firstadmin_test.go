package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/firstadmin/firstadmintest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestFirstAdministratorConformance runs the first administrator store suite.
func TestFirstAdministratorConformance(t *testing.T) {
	t.Parallel()

	firstadmintest.RunFirstAdministrator(t, func(t *testing.T) firstadmintest.Harness {
		t.Helper()

		s := open(t)
		conn := sqlite.DB(s)

		return firstadmintest.Harness{
			Store:             s,
			BreakAuditAppends: func(ctx context.Context) error { return breakFirstAdminAudit(ctx, conn) },
			SetSubject: func(ctx context.Context, principalID, subject string) error {
				return setFirstAdminSubject(ctx, conn, principalID, subject)
			},
			InsertCode: func(ctx context.Context, r firstadmintest.CodeRow) error {
				return insertFirstAdminCode(ctx, conn, r)
			},
			InsertMarker: func(ctx context.Context, r firstadmintest.MarkerRow) error {
				return insertFirstAdminMarker(ctx, conn, r)
			},
		}
	})
}

// breakFirstAdminAudit makes each later insert in the audit log fail.
func breakFirstAdminAudit(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `CREATE TRIGGER break_audit_appends BEFORE INSERT ON audit_entries
BEGIN SELECT RAISE(ABORT, 'audit append broken by the test'); END`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}

// setFirstAdminSubject changes the subject of a principal with no validation.
func setFirstAdminSubject(ctx context.Context, conn *sql.DB, principalID, subject string) error {
	_, err := conn.ExecContext(ctx, `UPDATE principals SET subject = ? WHERE id = ?`, subject, principalID)
	if err != nil {
		return fmt.Errorf("set subject: %w", err)
	}

	return nil
}

// insertFirstAdminCode writes a row of the code table with direct SQL.
func insertFirstAdminCode(ctx context.Context, conn *sql.DB, r firstadmintest.CodeRow) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO first_administrator_codes
(id, principal_id, digest, created_by, reason, created_at_ms, expires_at_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.PrincipalID, r.Digest, r.CreatedBy, r.Reason, r.CreatedAtMs, r.ExpiresAtMs)
	if err != nil {
		return fmt.Errorf("insert code row: %w", err)
	}

	return nil
}

// insertFirstAdminMarker writes a row of the marker table with direct SQL.
func insertFirstAdminMarker(ctx context.Context, conn *sql.DB, r firstadmintest.MarkerRow) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO first_administrator
(id, principal_id, grant_id, session_id, created_by, made_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.PrincipalID, r.GrantID, r.SessionID, r.CreatedBy, r.MadeAtMs)
	if err != nil {
		return fmt.Errorf("insert marker row: %w", err)
	}

	return nil
}
