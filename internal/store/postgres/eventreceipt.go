package postgres

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/githoststore"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

// errReceiptDuplicate rolls back the transaction of a receipt that hit a key,
// so that its audit entry is not written. RecordEventReceipt turns it into
// false.
var errReceiptDuplicate = errors.New("event receipt is a duplicate")

// RecordEventReceipt implements forge.Store. It deletes the receipts that are
// older than 24 hours at the time of r, checks the bound, and inserts r. A
// receipt that hits the key of its delivery ID or the key of its body digest
// writes nothing and returns false. The audit entry of r, when it has one, is
// appended in the same transaction.
func (g gitHost) RecordEventReceipt(ctx context.Context, r forge.EventReceipt) (bool, error) {
	if err := githoststore.CheckEventReceipt(&r); err != nil {
		return false, fmt.Errorf("record event receipt: %w", err)
	}

	stored := false

	write := func(q *db.Queries) error {
		// The writers of receipts take turns, so the count below is exact.
		if err := q.LockGitHostEventReceipts(ctx); err != nil {
			return fmt.Errorf("lock event receipts: %w", err)
		}

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

// insertEventReceipt runs the purge, the bound and the insert of r in the
// transaction of q. It returns false when r hit a key.
func insertEventReceipt(ctx context.Context, q *db.Queries, r *forge.EventReceipt) (bool, error) {
	at := codec.Millis(r.ReceivedAt)

	if _, err := q.PurgeGitHostEventReceipts(ctx, at-githoststore.EventReceiptMemory.Milliseconds()); err != nil {
		return false, fmt.Errorf("purge event receipts: %w", err)
	}

	n, err := q.CountGitHostEventReceipts(ctx)
	if err != nil {
		return false, fmt.Errorf("count event receipts: %w", err)
	}

	if n >= githoststore.MaxEventReceipts {
		return false, forge.ErrReceiptsFull
	}

	rows, err := q.InsertGitHostEventReceipt(ctx, db.InsertGitHostEventReceiptParams{
		Kind: string(r.Kind), DeliveryID: r.DeliveryID, BodyDigest: r.BodyDigest, InstallationID: r.InstallationID,
		EventKind: string(r.EventKind), Repo: r.Repo, ReviewID: r.ReviewID, ReceivedAtMs: at,
	})
	if err != nil {
		return false, fmt.Errorf("insert event receipt: %w", err)
	}

	return rows > 0, nil
}
