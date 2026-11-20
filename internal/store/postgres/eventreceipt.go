package postgres

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/githoststore"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/forge"
)

// errReceiptDuplicate rolls back the transaction of a receipt that hit a key,
// so that its audit entry is not written. RecordEventReceipt turns it into
// false.
var errReceiptDuplicate = errors.New("event receipt is a duplicate")

// RecordEventReceipt implements forge.Store. It deletes the receipts that are
// older than 24 hours at the time of r, inserts r, and checks the bound. A
// receipt that hits the key of its delivery ID or the key of its body digest
// writes nothing and returns false. The audit entry of r, when it has one, is
// appended in the same transaction.
func (g gitHost) RecordEventReceipt(ctx context.Context, r forge.EventReceipt) (bool, error) {
	if err := githoststore.CheckEventReceipt(&r); err != nil {
		return false, fmt.Errorf("record event receipt: %w", err)
	}

	stored := false

	write := func(q *db.Queries) error {
		var err error

		stored, err = insertEventReceipt(ctx, q, &r)

		return err
	}

	if r.Audit == nil {
		if err := g.s.inTx(ctx, write); err != nil {
			return false, err
		}

		return stored, nil
	}

	e := *r.Audit

	err := g.s.inAuditedTxOf(ctx, &e, func(tx serverAuditTx) error {
		if err := write(tx.q); err != nil {
			return err
		}

		if !stored {
			return errReceiptDuplicate
		}

		return nil
	})
	if errors.Is(err, errReceiptDuplicate) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return stored, nil
}

// insertEventReceipt runs the purge, the insert and the bound of r in the
// transaction of q. It returns false when r hit a key.
//
// The bound is the counter row of git_host_event_receipt_count, which each
// write of the table changes by the number of rows that it added less the
// number that it deleted. The counter statement runs last, so a writer waits
// for another writer only from that statement to the commit, and a write does
// not scan the table. The statement adds nothing, and the transaction rolls
// back the insert, when the new count would pass MaxEventReceipts.
func insertEventReceipt(ctx context.Context, q *db.Queries, r *forge.EventReceipt) (bool, error) {
	at := codec.Millis(r.ReceivedAt)

	purged, err := q.PurgeGitHostEventReceipts(ctx, at-githoststore.EventReceiptMemory.Milliseconds())
	if err != nil {
		return false, fmt.Errorf("purge event receipts: %w", err)
	}

	rows, err := q.InsertGitHostEventReceipt(ctx, db.InsertGitHostEventReceiptParams{
		Kind: string(r.Kind), DeliveryID: r.DeliveryID, BodyDigest: r.BodyDigest, InstallationID: r.InstallationID,
		EventKind: string(r.EventKind), Repo: r.Repo, ReviewID: r.ReviewID, ReceivedAtMs: at,
	})
	if err != nil {
		return false, fmt.Errorf("insert event receipt: %w", err)
	}

	// A receipt that hit a key adds no row, so the count changes by the purge only.
	delta := rows - purged
	if delta == 0 {
		return rows > 0, nil
	}

	if err = q.EnsureGitHostEventReceiptCount(ctx, githoststore.MaxEventReceipts); err != nil {
		return false, fmt.Errorf("make event receipt count: %w", err)
	}

	_, err = q.AddGitHostEventReceiptCount(ctx, db.AddGitHostEventReceiptCountParams{
		Delta: delta, MaxReceipts: githoststore.MaxEventReceipts,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, forge.ErrReceiptsFull
	}

	if err != nil {
		return false, fmt.Errorf("count event receipts: %w", err)
	}

	return rows > 0, nil
}
