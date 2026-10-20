package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// revocation is one write that only reduces access. The store journals it
// while the installation is in maintenance mode, so a restore can apply it
// again after the swap.
type revocation struct {
	// kind is one of maintenance.RevokedSession, RevokedToken and
	// RevokedPrincipal.
	kind string
	// target is the ID of the session, the token or the principal.
	target string
	// endReason is the end reason of a session, empty for the other kinds.
	endReason string
}

// inRevokingTx runs fn and appends e in the scope @server in one control
// transaction: the change and its audit entry commit together, or neither
// does. The control path has no installation state check, so a write that
// only reduces access runs in maintenance mode: incident response must be
// able to end a session or revoke a token at any time. Only a caller whose
// fn can narrow access may use it. A write that grants or widens access uses
// inAuditedTx, which the state check covers.
//
// The transaction journals rev when the installation is in maintenance mode.
// The caller holds the store lock, as for every write, so a restore swap
// waits for the transaction and the journal it copies is complete.
func (s *Store) inRevokingTx(ctx context.Context, e audit.Entry, rev revocation, fn func(q *db.Queries) error) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	return s.inControlTx(ctx, func(q *db.Queries) error {
		err := q.RecordMaintenanceRevocation(ctx, db.RecordMaintenanceRevocationParams{
			Kind: rev.kind, Target: rev.target, EndReason: rev.endReason, AtMs: codec.Millis(e.At),
		})
		if err != nil {
			return fmt.Errorf("journal revocation: %w", err)
		}

		if err := fn(q); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// clearJournalOnEnter starts a new stay in maintenance mode when next is
// maintenance: it clears the journal of revocations, in the transaction of q
// and before the state changes. It clears nothing when the state is
// maintenance already, so a second enter keeps the journal of the stay.
func clearJournalOnEnter(ctx context.Context, q *db.Queries, next maintenance.State) error {
	if next != maintenance.StateMaintenance {
		return nil
	}

	if err := q.ClearMaintenanceRevocationsOnEnter(ctx); err != nil {
		return fmt.Errorf("store installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// replayStatements apply the journal in the staged file (main) to its access
// tables. Each statement only ends a session, revokes a token or disables a
// principal, and only where the staged row is still live, so a replay never
// grants access and a second replay changes nothing. A target that the
// backup does not hold matches no row.
//
//nolint:gochecknoglobals // A fixed list of SQL statements: Go has no constant slices.
var replayStatements = []string{
	`UPDATE main.sessions AS s SET ended_at_ms = r.at_ms, end_reason = r.end_reason
FROM main.maintenance_revocations AS r
WHERE r.kind = '` + maintenance.RevokedSession + `' AND s.id = r.target AND s.ended_at_ms IS NULL`,
	`UPDATE main.access_tokens AS t SET revoked_at_ms = r.at_ms
FROM main.maintenance_revocations AS r
WHERE r.kind = '` + maintenance.RevokedToken + `' AND t.id = r.target AND t.revoked_at_ms IS NULL`,
	`UPDATE main.principals AS p SET disabled_at_ms = r.at_ms
FROM main.maintenance_revocations AS r
WHERE r.kind = '` + maintenance.RevokedPrincipal + `' AND p.id = r.target
  AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL`,
	`UPDATE main.access_tokens AS t SET revoked_at_ms = r.at_ms
FROM main.maintenance_revocations AS r
WHERE r.kind = '` + maintenance.RevokedPrincipal + `' AND t.principal_id = r.target
  AND t.revoked_at_ms IS NULL AND t.disabled_at_ms IS NULL`,
}

// replayRevocations applies the journal to the staged file, in tx. It runs
// after copyControlTables, which brings the live journal into the staged
// file.
func replayRevocations(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range replayStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("replay revocations: %w", err)
		}
	}

	return nil
}
