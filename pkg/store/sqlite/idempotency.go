package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ idempotency.Store = (*Idempotency)(nil)

// Idempotency is the store of replay records on the database of a Store. It
// implements idempotency.Store.
//
// The server key of the fingerprints is a random key that the key-encryption
// key seals in the database. A process unwraps it one time and keeps it. When
// the key-encryption key cannot open the sealed key (the operator lost it or
// changed it), the next Begin replaces the sealed key and deletes every
// record in one transaction: a retry then does its operation again, and no
// request fails. The wrapper must not be nil.
type Idempotency struct {
	store *Store
	keys  *codec.IdempotencyKeys
}

// Idempotency returns the store of replay records on s. wrapper seals the
// server key of the fingerprints.
func (s *Store) Idempotency(wrapper secret.Wrapper) *Idempotency {
	return &Idempotency{store: s, keys: codec.NewIdempotencyKeys(wrapper)}
}

// idempotencyKeyOps returns the operations of the key row in the transaction
// of q.
func idempotencyKeyOps(q *db.Queries) codec.IdempotencyKeyOps {
	return codec.IdempotencyKeyOps{
		Get: func(ctx context.Context) (codec.IdempotencyKeyRow, bool, error) {
			var none codec.IdempotencyKeyRow

			row, err := q.GetIdempotencyServerKey(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				return none, false, nil
			}

			if err != nil {
				return none, false, fmt.Errorf("select idempotency server key: %w", err)
			}

			return codec.IdempotencyKeyRow{WrappedKey: row.WrappedKey, WrapperID: row.WrapperID}, true, nil
		},
		Insert: func(ctx context.Context, row codec.IdempotencyKeyRow) error {
			err := q.InsertIdempotencyServerKey(ctx, db.InsertIdempotencyServerKeyParams{
				WrappedKey: row.WrappedKey, WrapperID: row.WrapperID,
			})
			if err != nil {
				return fmt.Errorf("insert idempotency server key: %w", err)
			}

			return nil
		},
		Replace: func(ctx context.Context, row codec.IdempotencyKeyRow) error {
			err := q.ReplaceIdempotencyServerKey(ctx, db.ReplaceIdempotencyServerKeyParams{
				WrappedKey: row.WrappedKey, WrapperID: row.WrapperID,
			})
			if err != nil {
				return fmt.Errorf("replace idempotency server key: %w", err)
			}

			return nil
		},
		DeleteRecords: func(ctx context.Context) error {
			if err := q.DeleteAllIdempotencyRecords(ctx); err != nil {
				return fmt.Errorf("delete idempotency records: %w", err)
			}

			return nil
		},
	}
}

// Begin implements idempotency.Store.
func (i *Idempotency) Begin(
	ctx context.Context, ref idempotency.Ref, material []byte, now time.Time,
) (idempotency.Result, error) {
	if err := ref.Validate(); err != nil {
		return idempotency.Result{}, err
	}

	token, err := codec.NewIdempotencyToken()
	if err != nil {
		return idempotency.Result{}, err
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	var res idempotency.Result

	serverKey, cached := i.keys.Cached()

	err = i.store.inTx(ctx, func(q *db.Queries) error {
		if !cached {
			var keyErr error

			if serverKey, keyErr = i.keys.Resolve(ctx, idempotencyKeyOps(q)); keyErr != nil {
				return keyErr
			}
		}

		var txErr error

		res, txErr = startOrFind(ctx, q, codec.IdempotencyAttempt{
			Ref: ref, Fingerprint: idempotency.Fingerprint(serverKey, material), Token: token, Now: now,
		})

		return txErr
	})
	if err != nil {
		return idempotency.Result{}, err
	}

	if !cached {
		i.keys.Remember(serverKey)
	}

	return res, nil
}

// startOrFind deletes the record of ref when it expired, then writes a
// pending record with token, or finds the record that holds the name.
func startOrFind(ctx context.Context, q *db.Queries, at codec.IdempotencyAttempt) (idempotency.Result, error) {
	ref, fp, token := at.Ref, at.Fingerprint, at.Token
	override := codec.IdempotencyOverrideMillis(at.Now)

	expired := db.DeleteExpiredIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
		NowOverrideMs: override, RecordTtlMs: idempotency.RecordTTL.Milliseconds(),
		PendingTtlMs: idempotency.PendingTTL.Milliseconds(),
	}

	if err := q.DeleteExpiredIdempotencyRecord(ctx, expired); err != nil {
		return idempotency.Result{}, fmt.Errorf("delete expired idempotency record: %w", err)
	}

	n, err := q.InsertIdempotencyRecord(ctx, db.InsertIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
		Fingerprint: fp, Token: token, NowOverrideMs: override,
	})
	if err != nil {
		return idempotency.Result{}, fmt.Errorf("insert idempotency record: %w", err)
	}

	if n == 1 {
		started := codec.IdempotencyOutcome(idempotency.Started)
		started.Token = token

		return started, nil
	}

	row, err := q.GetIdempotencyRecord(ctx, db.GetIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
	})
	if err != nil {
		return idempotency.Result{}, fmt.Errorf("select idempotency record: %w", err)
	}

	return codec.ClassifyIdempotency(codec.IdempotencyRow(row), fp)
}

// completeIdempotencyRecord stores a on the pending record that holds token,
// in the transaction of q. It returns idempotency.ErrLost when none holds it.
func completeIdempotencyRecord(
	ctx context.Context, q *db.Queries, ref idempotency.Ref, token string, a idempotency.Answer,
) error {
	if err := a.Validate(); err != nil {
		return err
	}

	headers, err := codec.EncodeIdempotencyHeaders(a.Headers)
	if err != nil {
		return err
	}

	body := a.Body
	if body == nil {
		body = []byte{}
	}

	n, err := q.CompleteIdempotencyRecord(ctx, db.CompleteIdempotencyRecordParams{
		Status: int64(a.Status), Headers: headers, Body: body,
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key), Token: token,
	})
	if err != nil {
		return fmt.Errorf("update idempotency record: %w", err)
	}

	if n == 0 {
		return idempotency.ErrLost
	}

	return nil
}

// Complete implements idempotency.Store.
func (i *Idempotency) Complete(ctx context.Context, ref idempotency.Ref, token string, a idempotency.Answer) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	if err := a.Validate(); err != nil {
		return err
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	return i.store.inTx(ctx, func(q *db.Queries) error {
		return completeIdempotencyRecord(ctx, q, ref, token, a)
	})
}

// Abandon implements idempotency.Store.
func (i *Idempotency) Abandon(ctx context.Context, ref idempotency.Ref, token string) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	err := i.store.queries.AbandonIdempotencyRecord(ctx, db.AbandonIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key), Token: token,
	})
	if err != nil {
		return fmt.Errorf("delete idempotency record: %w", err)
	}

	return nil
}

// Sweep implements idempotency.Store.
func (i *Idempotency) Sweep(ctx context.Context, now time.Time) (int, error) {
	i.store.mu.RLock()
	defer i.store.mu.RUnlock()

	n, err := i.store.queries.SweepIdempotencyRecords(ctx, db.SweepIdempotencyRecordsParams{
		NowOverrideMs: codec.IdempotencyOverrideMillis(now), RecordTtlMs: idempotency.RecordTTL.Milliseconds(),
		PendingTtlMs: idempotency.PendingTTL.Milliseconds(),
	})
	if err != nil {
		return 0, fmt.Errorf("sweep idempotency records: %w", err)
	}

	return int(n), nil
}

// finishCompletion stores the answer of the Completion that ctx carries, in
// the transaction of q, for the result of the write. A context with no
// Completion changes nothing. It returns idempotency.ErrLost when the pending
// record of the request is not in place, and the write then rolls back.
func finishCompletion(ctx context.Context, q *db.Queries, result any) error {
	c, ok := idempotency.CompletionFrom(ctx)
	if !ok {
		return nil
	}

	return c.Finish(result, func(token string, a idempotency.Answer) error {
		return completeIdempotencyRecord(ctx, q, c.Ref, token, a)
	})
}
