package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ idempotency.Store = (*Idempotency)(nil)

// Idempotency is the store of replay records on the database of a Store. It
// implements idempotency.Store.
type Idempotency struct {
	store *Store
}

// Idempotency returns the store of replay records on s.
func (s *Store) Idempotency() *Idempotency {
	return &Idempotency{store: s}
}

// idempotencyServerKey reads the key of the fingerprints, and makes it when
// no row holds it.
func idempotencyServerKey(ctx context.Context, q *db.Queries) ([]byte, error) {
	key, err := q.GetIdempotencyServerKey(ctx)
	if err == nil {
		return key, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("select idempotency server key: %w", err)
	}

	fresh, err := codec.NewIdempotencyServerKey()
	if err != nil {
		return nil, err
	}

	if err = q.InsertIdempotencyServerKey(ctx, fresh); err != nil {
		return nil, fmt.Errorf("insert idempotency server key: %w", err)
	}

	key, err = q.GetIdempotencyServerKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("select idempotency server key: %w", err)
	}

	return key, nil
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

	err = i.store.inTx(ctx, func(q *db.Queries) error {
		serverKey, keyErr := idempotencyServerKey(ctx, q)
		if keyErr != nil {
			return keyErr
		}

		fp := idempotency.Fingerprint(serverKey, material)

		expired := db.DeleteExpiredIdempotencyRecordParams{
			Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
			NowMs: codec.Millis(now), RecordTtlMs: idempotency.RecordTTL.Milliseconds(),
			PendingTtlMs: idempotency.PendingTTL.Milliseconds(),
		}

		if delErr := q.DeleteExpiredIdempotencyRecord(ctx, expired); delErr != nil {
			return fmt.Errorf("delete expired idempotency record: %w", delErr)
		}

		n, insErr := q.InsertIdempotencyRecord(ctx, db.InsertIdempotencyRecordParams{
			Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
			Fingerprint: fp, Token: token, CreatedAtMs: codec.Millis(now),
		})
		if insErr != nil {
			return fmt.Errorf("insert idempotency record: %w", insErr)
		}

		if n == 1 {
			res = codec.IdempotencyOutcome(idempotency.Started)
			res.Token = token

			return nil
		}

		row, getErr := q.GetIdempotencyRecord(ctx, db.GetIdempotencyRecordParams{
			Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
		})
		if getErr != nil {
			return fmt.Errorf("select idempotency record: %w", getErr)
		}

		res, getErr = codec.ClassifyIdempotency(codec.IdempotencyRow(row), fp)

		return getErr
	})
	if err != nil {
		return idempotency.Result{}, err
	}

	return res, nil
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
		NowMs: codec.Millis(now), RecordTtlMs: idempotency.RecordTTL.Milliseconds(),
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
