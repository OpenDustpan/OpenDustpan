package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ idempotency.Store = (*Idempotency)(nil)

// Idempotency is the store of replay records on the database of a Store. It
// implements idempotency.Store.
//
// The server key of the fingerprints is a random key that the key-encryption
// key seals in the database. A process keeps the key it unwrapped, and uses it only while the
// database row has the same sealed bytes. When
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

// sharedKeyOps returns the operations of the key row in the transaction of q.
// The row has a share lock.
func sharedKeyOps(q *db.Queries) codec.IdempotencyKeyOps {
	return idempotencyKeyOps(q, func(ctx context.Context) (codec.IdempotencyKeyRow, error) {
		row, err := q.GetIdempotencyServerKeyShared(ctx)

		return codec.IdempotencyKeyRow{WrappedKey: row.WrappedKey, WrapperID: row.WrapperID}, err
	})
}

// exclusiveKeyOps returns the operations of the key row in the transaction of
// q. The row has an exclusive lock.
func exclusiveKeyOps(q *db.Queries) codec.IdempotencyKeyOps {
	ops := idempotencyKeyOps(q, func(ctx context.Context) (codec.IdempotencyKeyRow, error) {
		row, err := q.GetIdempotencyServerKeyExclusive(ctx)

		return codec.IdempotencyKeyRow{WrappedKey: row.WrappedKey, WrapperID: row.WrapperID}, err
	})
	ops.Exclusive = true

	return ops
}

// idempotencyKeyOps returns the operations of the key row in the transaction
// of q. get reads the row with the lock that the caller wants.
func idempotencyKeyOps(
	q *db.Queries, get func(context.Context) (codec.IdempotencyKeyRow, error),
) codec.IdempotencyKeyOps {
	return codec.IdempotencyKeyOps{
		Exclusive: false,
		Get: func(ctx context.Context) (codec.IdempotencyKeyRow, bool, error) {
			var none codec.IdempotencyKeyRow

			row, err := get(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return none, false, nil
			}

			if err != nil {
				return none, false, fmt.Errorf("select idempotency server key: %w", err)
			}

			return row, true, nil
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
		Replace: func(ctx context.Context, old []byte, row codec.IdempotencyKeyRow) (bool, error) {
			n, err := q.ReplaceIdempotencyServerKey(ctx, db.ReplaceIdempotencyServerKeyParams{
				NewWrappedKey: row.WrappedKey, NewWrapperID: row.WrapperID, OldWrappedKey: old,
			})
			if err != nil {
				return false, fmt.Errorf("replace idempotency server key: %w", err)
			}

			return n == 1, nil
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

	at := codec.IdempotencyAttempt{Ref: ref, Fingerprint: nil, Token: token, Now: now}

	res, key, err := i.attempt(ctx, at, material, sharedKeyOps)
	if errors.Is(err, codec.ErrIdempotencyResetNeeded) {
		res, key, err = i.attempt(ctx, at, material, exclusiveKeyOps)
	}

	if err != nil {
		return idempotency.Result{}, err
	}

	i.keys.Remember(key)

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

	return i.store.inTx(ctx, func(q *db.Queries) error {
		return completeIdempotencyRecord(ctx, q, ref, token, a)
	})
}

// Abandon implements idempotency.Store.
func (i *Idempotency) Abandon(ctx context.Context, ref idempotency.Ref, token string) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	err := i.store.inTx(ctx, func(q *db.Queries) error {
		return q.AbandonIdempotencyRecord(ctx, db.AbandonIdempotencyRecordParams{
			Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key), Token: token,
		})
	})
	if err != nil {
		return fmt.Errorf("delete idempotency record: %w", err)
	}

	return nil
}

// Sweep implements idempotency.Store.
func (i *Idempotency) Sweep(ctx context.Context, now time.Time) (int, error) {
	var n int64

	err := i.store.inTx(ctx, func(q *db.Queries) error {
		var err error

		n, err = q.SweepIdempotencyRecords(ctx, db.SweepIdempotencyRecordsParams{
			NowOverrideMs: codec.IdempotencyOverrideMillis(now), RecordTtlMs: idempotency.RecordTTL.Milliseconds(),
			PendingTtlMs: idempotency.PendingTTL.Milliseconds(),
		})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sweep idempotency records: %w", err)
	}

	return int(n), nil
}

// attempt runs one transaction of Begin with the key operations that keyOps
// makes. It returns codec.ErrIdempotencyResetNeeded when the key row needs a
// reset and the operations do not hold the row exclusively.
func (i *Idempotency) attempt(
	ctx context.Context, at codec.IdempotencyAttempt, material []byte,
	keyOps func(*db.Queries) codec.IdempotencyKeyOps,
) (idempotency.Result, codec.IdempotencyKey, error) {
	var (
		res idempotency.Result
		key codec.IdempotencyKey
	)

	err := i.store.inTx(ctx, func(q *db.Queries) error {
		var keyErr error

		if key, keyErr = i.keys.Resolve(ctx, keyOps(q)); keyErr != nil {
			return keyErr
		}

		at.Fingerprint = idempotency.Fingerprint(key.Key, material)

		var txErr error

		res, txErr = startOrFind(ctx, q, at)

		return txErr
	})

	return res, key, err
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
