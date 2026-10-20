package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
// The transaction first journals rev when the installation is in maintenance
// mode. The journal insert queues behind the lock that a restore takes on the
// journal for its swap, so the change that follows lands in the restored
// schema, never in the one that the swap drops. Then it takes the advisory
// lock of the scope, as every audited change does.
func (s *Store) inRevokingTx(ctx context.Context, e audit.Entry, rev revocation, fn func(q *db.Queries) error) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := journalRevocation(ctx, q, rev, e); err != nil {
			return err
		}

		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
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

// journalRevocation journals rev in the transaction of q when the
// installation is in maintenance mode. In normal operation it writes nothing.
func journalRevocation(ctx context.Context, q *db.Queries, rev revocation, e audit.Entry) error {
	err := q.RecordMaintenanceRevocation(ctx, db.RecordMaintenanceRevocationParams{
		Kind: rev.kind, Target: rev.target, EndReason: rev.endReason, AtMs: codec.Millis(e.At),
	})
	if err != nil {
		return fmt.Errorf("journal revocation: %w", err)
	}

	return nil
}

// lockRevocationJournal locks the journal in share mode for the rest of tx.
// A write to the journal waits until tx ends, and tx waits for the writes in
// flight, so the replay reads every journaled revocation and no later one
// lands in the schema that the swap drops.
func lockRevocationJournal(ctx context.Context, tx pgx.Tx, live string) error {
	query := "LOCK TABLE " + revocationJournal(live) + " IN SHARE MODE"
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("lock revocation journal: %w", err)
	}

	return nil
}

// revocationJournal returns the qualified name of the journal: the table
// lives in the control schema of the live schema, which a swap never renames.
func revocationJournal(live string) string {
	return pgIdent(live+"_control") + ".maintenance_revocations"
}

// replayRevocations applies the journal to the restored schema live, in tx.
// Each statement only ends a session, revokes a token or disables a
// principal, and only where the restored row is still live, so a replay
// never grants access and a second replay changes nothing. A target that the
// backup does not hold matches no row.
func replayRevocations(ctx context.Context, tx pgx.Tx, live string) error {
	schema := pgIdent(live)
	journal := revocationJournal(live)

	stmts := []string{
		"UPDATE " + schema + ".sessions s SET ended_at_ms = r.at_ms, end_reason = r.end_reason FROM " + journal +
			" r WHERE r.kind = '" + maintenance.RevokedSession + "' AND s.id = r.target AND s.ended_at_ms IS NULL",
		"UPDATE " + schema + ".access_tokens t SET revoked_at_ms = r.at_ms FROM " + journal +
			" r WHERE r.kind = '" + maintenance.RevokedToken + "' AND t.id = r.target AND t.revoked_at_ms IS NULL",
		"UPDATE " + schema + ".principals p SET disabled_at_ms = r.at_ms FROM " + journal +
			" r WHERE r.kind = '" + maintenance.RevokedPrincipal + "' AND p.id = r.target" +
			" AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL",
		"UPDATE " + schema + ".access_tokens t SET revoked_at_ms = r.at_ms FROM " + journal +
			" r WHERE r.kind = '" + maintenance.RevokedPrincipal + "' AND t.principal_id = r.target" +
			" AND t.revoked_at_ms IS NULL AND t.disabled_at_ms IS NULL",
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("replay revocations: %w", err)
		}
	}

	return nil
}
