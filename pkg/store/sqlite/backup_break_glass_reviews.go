package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// sqliteBreakGlassReviewBackupTables holds the backup columns of the
// break-glass review table, in the order of backup.Tables.
func sqliteBreakGlassReviewBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "break_glass_reviews",
			columns: []string{
				"id", "use_kind", "use_ref", "use_scope", "use_object", "use_subject", "use_people",
				"use_reason", "use_at_ms", "use_until_ms", backupColState, "created_at_ms", "due_at_ms", "calendar",
				"calendar_from_namespace", "calendar_from_group", "step", "next_notice_at_ms", "verdict_reviewer",
				"verdict_note", "verdict_auth", "verdict_auth_at_ms", "verdict_reviewed_at_ms", "verdict_act_as",
				backupColVersion,
			},
		},
	}
}

// checkStagedBreakGlassReviews checks each break-glass review row of a staged
// backup with the rules of a stored review: the scope of the review is
// audit.ServerScope or a namespace that the backup holds, and the row passes
// the checks that the store applies to a review that it reads. A row that
// fails makes the backup corrupt, so a restore never brings in a review that
// the server would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedBreakGlassReviews(ctx context.Context, conn *sql.DB) (err error) {
	const query = `SELECT r.id, r.use_kind, r.use_ref, r.use_scope, r.use_object, r.use_subject, r.use_people,
r.use_reason, r.use_at_ms, r.use_until_ms, r.state, r.created_at_ms, r.due_at_ms, r.calendar,
r.calendar_from_namespace, r.calendar_from_group, r.step, r.next_notice_at_ms, r.verdict_reviewer,
r.verdict_note, r.verdict_auth, r.verdict_auth_at_ms, r.verdict_reviewed_at_ms, r.verdict_act_as, r.version,
n.id IS NOT NULL
FROM break_glass_reviews AS r LEFT JOIN namespaces AS n ON n.name = r.use_scope`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged break-glass reviews: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("read staged break-glass reviews: %w", backup.ErrExportRead)
		}
	}()

	for rows.Next() {
		var (
			row          db.BreakGlassReview
			hasNamespace bool
		)

		scanErr := rows.Scan(
			&row.ID, &row.UseKind, &row.UseRef, &row.UseScope, &row.UseObject, &row.UseSubject, &row.UsePeople,
			&row.UseReason, &row.UseAtMs, &row.UseUntilMs, &row.State, &row.CreatedAtMs, &row.DueAtMs, &row.Calendar,
			&row.CalendarFromNamespace, &row.CalendarFromGroup, &row.Step, &row.NextNoticeAtMs, &row.VerdictReviewer,
			&row.VerdictNote, &row.VerdictAuth, &row.VerdictAuthAtMs, &row.VerdictReviewedAtMs, &row.VerdictActAs,
			&row.Version, &hasNamespace,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged break-glass reviews: %w", backup.ErrExportRead)
		}

		// A review of a namespace that the backup does not hold is corrupt.
		// The LEFT JOIN keeps such a row in the result, and the missing row
		// fails it. A review of audit.ServerScope names no namespace.
		if row.UseScope != audit.ServerScope.String() && !hasNamespace {
			return fmt.Errorf("staged break-glass review %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		if _, reviewErr := breakGlassReviewOf(row); reviewErr != nil {
			return fmt.Errorf("staged break-glass review %q is not valid: %w: %w", row.ID, reviewErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged break-glass reviews: %w", backup.ErrExportRead)
	}

	return nil
}
