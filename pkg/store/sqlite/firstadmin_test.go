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
			PutSession: func(ctx context.Context, r firstadmintest.SessionRow) error {
				return putFirstAdminSession(ctx, conn, r)
			},
			IgnoreCodeDeletes:        func(ctx context.Context) error { return ignoreFirstAdminCodeDeletes(ctx, conn) },
			InsertMarkerBeforeRedeem: func(ctx context.Context) error { return insertFirstAdminMarkerOnGrant(ctx, conn) },
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

// putFirstAdminSession writes a session row with direct SQL, or replaces the row
// with the same ID.
func putFirstAdminSession(ctx context.Context, conn *sql.DB, r firstadmintest.SessionRow) error {
	var ended any
	if r.EndedAtMs != 0 {
		ended = r.EndedAtMs
	}

	_, err := conn.ExecContext(ctx, `INSERT INTO sessions
(id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, ended_at_ms, generation)
VALUES (?, ?, ?, 'corp', ?, ?, ?, ?, 1)
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
func ignoreFirstAdminCodeDeletes(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `CREATE TRIGGER ignore_code_deletes BEFORE DELETE ON first_administrator_codes
BEGIN SELECT RAISE(IGNORE); END`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}

// insertFirstAdminMarkerOnGrant makes each later insert in the grant table store
// the marker row too.
func insertFirstAdminMarkerOnGrant(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `CREATE TRIGGER insert_marker_on_grant AFTER INSERT ON grants
BEGIN
INSERT INTO first_administrator (id, principal_id, grant_id, session_id, created_by, made_at_ms)
VALUES (1, 'prn-race', 'grt-race', 'ses-race', 'host', 0);
END`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}
