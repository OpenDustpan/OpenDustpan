package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ secret.ItemKeyStore = (*Store)(nil)

// itemKeyTx is the item key part of one open fenced transaction of the store.
// Its fence is the fence that opened the transaction. attempt is the attempt
// deadline of the transaction: every transaction that makes an itemKeyTx
// that can write arms one at its start.
type itemKeyTx struct {
	q       *db.Queries
	fence   namespace.Fence
	attempt *itemKeyAttempt
}

var _ secret.ItemKeyTx = itemKeyTx{q: nil, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}, attempt: nil}

// itemKeyAttempt is the attempt deadline of one transaction that can write an
// item key record. The writer puts the wrapped key blob inside the
// transaction, before the record, so the reaper rule holds only when the
// transaction commits before the deadline. wrote reports that the transaction
// wrote a record.
type itemKeyAttempt struct {
	deadline time.Time
	wrote    bool
	// now gives the current time of the attempt. newAttempt takes it from
	// the store. Tests replace it.
	now func() time.Time
}

// newAttempt returns the attempt of a transaction that starts now under ctx.
func (s *Store) newAttempt(ctx context.Context) *itemKeyAttempt {
	return &itemKeyAttempt{deadline: s.attemptDeadline(ctx, s.now()), wrote: false, now: s.now}
}

// beforeCommit refuses the commit of a transaction that wrote an item key
// record after its deadline. A transaction that wrote no record passes.
func (a *itemKeyAttempt) beforeCommit() error {
	if a == nil || !a.wrote {
		return nil
	}

	return secret.CheckAttemptDeadline(a.deadline, a.now())
}

// attemptError returns secret.ErrItemKeyAttemptExpired, with no text of the
// driver, when the database ended the transaction at its transaction_timeout,
// or when the transaction wrote a record and failed after its deadline. It
// returns err for any other error.
func (a *itemKeyAttempt) attemptError(err error) error {
	if err == nil || a == nil || errors.Is(err, secret.ErrItemKeyAttemptExpired) {
		return err
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == transactionTimeoutCode {
		return fmt.Errorf("%w: the database ended the transaction at its deadline", secret.ErrItemKeyAttemptExpired)
	}

	if a.wrote && !a.now().Before(a.deadline) {
		return fmt.Errorf("%w: the transaction failed after its deadline", secret.ErrItemKeyAttemptExpired)
	}

	return err
}

// transactionTimeoutCode is the SQLSTATE of a transaction that the database
// ended at its transaction_timeout.
const transactionTimeoutCode = "25P04"

// InItemKeyTx implements secret.ItemKeyStore. The transaction checks fence
// first, and fn writes the item row and the record in it. The transaction ends
// at its attempt deadline: the database ends it with transaction_timeout, on
// its own clock, and a check before the commit refuses a late commit.
func (s *Store) InItemKeyTx(ctx context.Context, fence namespace.Fence, fn func(secret.ItemKeyTx) error) error {
	attempt := s.newAttempt(ctx)

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := q.SetTransactionTimeout(ctx, transactionTimeoutMs(attempt.deadline, s.now())); err != nil {
			return fmt.Errorf("set the attempt deadline: %w", err)
		}

		if err := fn(itemKeyTx{q: q, fence: fence, attempt: attempt}); err != nil {
			return err
		}

		// Defense in depth: the database ends the transaction at the
		// deadline too. This check stays so that a late commit fails on the
		// clock of the server as well, whatever the setting of the database.
		return secret.CheckAttemptDeadline(attempt.deadline, s.now())
	})

	return attempt.attemptError(err)
}

// transactionTimeoutMs returns the time from now to deadline, in whole
// milliseconds, as the value of transaction_timeout. It is at least 1, because
// 0 turns the timeout off.
func transactionTimeoutMs(deadline, now time.Time) string {
	return strconv.FormatInt(max(deadline.Sub(now).Milliseconds(), 1), 10)
}

// CreateItemKey implements secret.ItemKeyTx. It stores k unless a record for
// its item exists, and returns the stored record either way, so two writers of
// the first save of one item agree on one key. It refuses a write after the
// attempt deadline of the transaction, and it arms transaction_timeout for the
// rest of the transaction, so the database ends a late commit on its own
// clock.
func (t itemKeyTx) CreateItemKey(ctx context.Context, k secret.ItemKey) (secret.ItemKey, error) {
	// Defense in depth: the transaction checked the failover version of the
	// namespace of the fence only. This check stays so that a record of one
	// namespace never lands under the fence of another namespace.
	if err := codec.CheckNewItemKey(k, t.fence); err != nil {
		return secret.ItemKey{}, err
	}

	if err := t.armAttempt(ctx); err != nil {
		return secret.ItemKey{}, err
	}

	row := codec.ToItemKeyRow(k)

	_, err := t.q.InsertItemKey(ctx, db.InsertItemKeyParams{
		ID: row.ID, Namespace: row.Namespace, ScopeID: row.ScopeID, Kind: row.Kind, Item: row.Item,
		WrapperID: row.WrapperID, CreatedAtMs: row.CreatedAtMs, BlobVersion: row.BlobVersion,
		BlobSize: row.BlobSize, BlobMd5: row.BlobMd5, BlobAttempt: row.BlobAttempt,
	})
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("insert item key: %w", err)
	}

	return t.GetItemKey(ctx, k.Ref)
}

// GetNamespace implements secret.ItemKeyTx.
func (t itemKeyTx) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	row, err := t.q.GetNamespace(ctx, name.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
}

// GetItemKey implements secret.ItemKeyTx.
func (t itemKeyTx) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	return shreddedAsError(itemKeyOf(ctx, t.q, ref))
}

// armAttempt checks the attempt deadline before a record write, and arms
// transaction_timeout at the deadline. A transaction with no attempt writes
// no record (fail closed).
func (t itemKeyTx) armAttempt(ctx context.Context) error {
	if t.attempt == nil {
		return fmt.Errorf("%w: the transaction has no attempt deadline", secret.ErrItemKeyAttemptExpired)
	}

	// Defense in depth: the check before the commit and transaction_timeout
	// stop a late commit too. This check stays so that a write that starts
	// after the deadline never reaches the database.
	if err := secret.CheckAttemptDeadline(t.attempt.deadline, t.attempt.now()); err != nil {
		return err
	}

	if err := t.q.SetTransactionTimeout(ctx, transactionTimeoutMs(t.attempt.deadline, t.attempt.now())); err != nil {
		return fmt.Errorf("set the attempt deadline: %w", err)
	}

	t.attempt.wrote = true

	return nil
}

// shreddedAsError turns a shredded record into secret.ErrShredded, with the
// record.
func shreddedAsError(got secret.ItemKey, err error) (secret.ItemKey, error) {
	if err != nil {
		return secret.ItemKey{}, err
	}

	if got.Shredded() {
		return got, fmt.Errorf("item key of %s: %w", got.Ref, secret.ErrShredded)
	}

	return got, nil
}

// CreateItemKey implements secret.ItemKeyIndex in a transaction of its own.
// A caller that creates the item too uses InItemKeyTx and writes both in one
// transaction.
func (s *Store) CreateItemKey(ctx context.Context, k secret.ItemKey, fence namespace.Fence) (secret.ItemKey, error) {
	var stored secret.ItemKey

	err := s.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		got, err := tx.CreateItemKey(ctx, k)
		stored = got

		return err
	})
	if err != nil {
		if errors.Is(err, secret.ErrShredded) {
			return stored, err
		}

		return secret.ItemKey{}, err
	}

	return stored, nil
}

// GetItemKey implements secret.ItemKeyIndex.
func (s *Store) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	return itemKeyTx{
		q: s.queries, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}, attempt: nil,
	}.GetItemKey(ctx, ref)
}

// itemKeyOf reads the record of ref, shredded or not. It returns
// secret.ErrKeyNotFound for no record.
func itemKeyOf(ctx context.Context, q *db.Queries, ref secret.ItemRef) (secret.ItemKey, error) {
	row, err := q.GetItemKey(ctx, db.GetItemKeyParams{
		Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID, Kind: string(ref.Kind), Item: ref.Item,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return secret.ItemKey{}, fmt.Errorf("item key of %s: %w", ref, secret.ErrKeyNotFound)
	}

	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("select item key: %w", err)
	}

	return codec.FromItemKeyRow(codec.ItemKeyRow(row))
}

// ShredItemKey implements secret.ItemKeyIndex. Only a record that is not
// shredded changes, so a second shred keeps the first shred time.
func (s *Store) ShredItemKey(
	ctx context.Context, ref secret.ItemRef, at time.Time, fence namespace.Fence,
) (secret.ItemKey, error) {
	if err := codec.CheckShredItemKey(ref, at, fence); err != nil {
		return secret.ItemKey{}, err
	}

	var stored secret.ItemKey

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.ShredItemKey(ctx, db.ShredItemKeyParams{
			ShreddedAtMs: codec.Millis(at), Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID,
			Kind: string(ref.Kind), Item: ref.Item,
		})
		if err != nil {
			return fmt.Errorf("shred item key: %w", err)
		}

		got, err := itemKeyOf(ctx, q, ref)
		if err != nil {
			return err
		}

		stored = got

		return nil
	})
	if err != nil {
		return secret.ItemKey{}, err
	}

	return stored, nil
}

// ListItemKeys implements secret.ItemKeyIndex.
func (s *Store) ListItemKeys(
	ctx context.Context, scope secret.Scope, after secret.ItemKeyID, limit int,
) ([]secret.ItemKey, error) {
	if err := codec.CheckListItemKeys(scope, limit); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListItemKeys(ctx, db.ListItemKeysParams{
		Namespace: scope.Namespace.String(), ScopeID: scope.ID, After: string(after), MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select item keys: %w", err)
	}

	out := make([]secret.ItemKey, 0, len(rows))

	for i := range rows {
		k, convErr := codec.FromItemKeyRow(codec.ItemKeyRow(rows[i]))
		if convErr != nil {
			return nil, convErr
		}

		out = append(out, k)
	}

	return out, nil
}

// ListItemKeyWrapperIDs implements secret.ItemKeyIndex.
func (s *Store) ListItemKeyWrapperIDs(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ListItemKeyWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("select item key wrapper IDs: %w", err)
	}

	if ids == nil {
		return []string{}, nil
	}

	return ids, nil
}
