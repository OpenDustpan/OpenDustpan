package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/secret"
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
	store     *Store
	keys      *codec.IdempotencyKeys
	retention idempotency.Retention
}

// Idempotency returns the store of replay records on s. wrapper seals the
// server key of the fingerprints. It keeps a record for the job-start
// retention.
func (s *Store) Idempotency(wrapper secret.Wrapper) *Idempotency {
	return &Idempotency{
		store: s, keys: codec.NewIdempotencyKeys(wrapper), retention: idempotency.JobStartRetention(),
	}
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

// Begin implements idempotency.Store. A Reducing request writes its record on
// the control path, so maintenance mode does not refuse the record of a route
// that ends access.
func (i *Idempotency) Begin(
	ctx context.Context, ref idempotency.Ref, material []byte, now time.Time, opts ...idempotency.BeginOption,
) (idempotency.Result, error) {
	if err := ref.Validate(); err != nil {
		return idempotency.Result{}, err
	}

	token, err := codec.NewIdempotencyToken()
	if err != nil {
		return idempotency.Result{}, err
	}

	// Defense in depth: the route decides Reducing. The routes of
	// maintenance.ReducingRoutes pass the runner maintenance gate
	// (withRunnerMaintenance in pkg/dustpan), and the installation API has no
	// maintenance gate. This store does not check the route, so a caller that
	// passes Reducing for a route that is not on the list could write its
	// record in maintenance mode. No test checks that every route that uses
	// Reducing is on the list.
	run := i.store.inTx
	if slices.Contains(opts, idempotency.Reducing) {
		run = i.store.inControlTx
	}

	at := codec.IdempotencyAttempt{Ref: ref, Fingerprint: nil, Token: token, Now: now}

	res, key, err := i.attempt(ctx, run, at, material, sharedKeyOps)
	if errors.Is(err, codec.ErrIdempotencyResetNeeded) {
		res, key, err = i.attempt(ctx, run, at, material, exclusiveKeyOps)
	}

	if err != nil {
		return idempotency.Result{}, err
	}

	i.keys.Remember(key)

	return res, nil
}

// startOrFind deletes the record of ref when it expired, then writes a
// pending record with token, or finds the record that holds the name. A record
// past the retention of the store counts as absent: it is deleted, and the
// pending record takes its place.
func startOrFind(
	ctx context.Context, q *db.Queries, at codec.IdempotencyAttempt, retention idempotency.Retention,
) (idempotency.Result, error) {
	nowMs := codec.IdempotencyOverrideMillis(at.Now)
	if nowMs == 0 {
		var err error

		nowMs, err = q.DatabaseNowMs(ctx)
		if err != nil {
			return idempotency.Result{}, fmt.Errorf("read the clock of the store: %w", err)
		}
	}

	for attempt := 1; ; attempt++ {
		res, live, err := startAttempt(ctx, q, at, nowMs, retention)
		if err != nil {
			return idempotency.Result{}, err
		}

		if live {
			return res, nil
		}

		// The row passed its expiry between the delete and the read: its time
		// came in that moment, or the clock of the request is ahead of the
		// clock of the delete. Delete it again and write the pending record.
		if attempt == codec.IdempotencyReadAttempts {
			return idempotency.Result{}, codec.ErrIdempotencyRecordExpired
		}
	}
}

// startAttempt deletes the expired record of at, writes its pending record, or
// reads the record that holds the name. It reports false when the record that
// it read is past its expiry, so that the caller deletes it and tries again.
func startAttempt(
	ctx context.Context, q *db.Queries, at codec.IdempotencyAttempt, nowMs int64,
	retention idempotency.Retention,
) (idempotency.Result, bool, error) {
	ref, fp, token := at.Ref, at.Fingerprint, at.Token
	override := codec.IdempotencyOverrideMillis(at.Now)

	expired := db.DeleteExpiredIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
		NowOverrideMs: override, RecordTtlMs: retention.Period().Milliseconds(),
		PendingTtlMs: idempotency.PendingTTL.Milliseconds(),
	}

	if err := q.DeleteExpiredIdempotencyRecord(ctx, expired); err != nil {
		return idempotency.Result{}, false, fmt.Errorf("delete expired idempotency record: %w", err)
	}

	n, err := q.InsertIdempotencyRecord(ctx, db.InsertIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
		Fingerprint: fp, Token: token, NowOverrideMs: override,
	})
	if err != nil {
		return idempotency.Result{}, false, fmt.Errorf("insert idempotency record: %w", err)
	}

	if n == 1 {
		started := codec.IdempotencyOutcome(idempotency.Started)
		started.Token = token

		return started, true, nil
	}

	return readIdempotency(ctx, q, at, nowMs, retention)
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

	// Abandon frees a key and writes no domain data, so it runs on the control
	// path in every mode. A reducing route that fails must free its key in
	// maintenance mode too.
	err := i.store.inControlTx(ctx, func(q *db.Queries) error {
		return q.AbandonIdempotencyRecord(ctx, db.AbandonIdempotencyRecordParams{
			Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key), Token: token,
		})
	})
	if err != nil {
		return fmt.Errorf("delete idempotency record: %w", err)
	}

	return nil
}

// readIdempotency returns the stored record of ref with its outcome for fp,
// and false when the record is past its expiry: such a record counts as
// absent.
func readIdempotency(
	ctx context.Context, q *db.Queries, at codec.IdempotencyAttempt, nowMs int64,
	retention idempotency.Retention,
) (idempotency.Result, bool, error) {
	ref := at.Ref
	row, err := q.GetIdempotencyRecord(ctx, db.GetIdempotencyRecordParams{
		Principal: ref.Principal, Method: ref.Method, Path: ref.Path, IdemKey: string(ref.Key),
	})
	if err != nil {
		var none idempotency.Result

		return none, false, fmt.Errorf("select idempotency record: %w", err)
	}

	if codec.IdempotencyExpired(row.CreatedAtMs, nowMs, retention) {
		var none idempotency.Result

		return none, false, nil
	}

	res, err := codec.ClassifyIdempotency(codec.IdempotencyRow(row), at.Fingerprint)

	return res, err == nil, err
}

// Sweep implements idempotency.Store.
func (i *Idempotency) Sweep(ctx context.Context, now time.Time) (int, error) {
	var n int64

	err := i.store.inTx(ctx, func(q *db.Queries) error {
		var err error

		n, err = q.SweepIdempotencyRecords(ctx, db.SweepIdempotencyRecordsParams{
			NowOverrideMs: codec.IdempotencyOverrideMillis(now),
			RecordTtlMs:   i.retention.Period().Milliseconds(),
			PendingTtlMs:  idempotency.PendingTTL.Milliseconds(),
		})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sweep idempotency records: %w", err)
	}

	return int(n), nil
}

// attempt runs one transaction of Begin with the key operations that keyOps
// makes, on the transaction path that run gives. It returns
// codec.ErrIdempotencyResetNeeded when the key row needs a reset and the
// operations do not hold the row exclusively.
func (i *Idempotency) attempt(
	ctx context.Context, run txRunner, at codec.IdempotencyAttempt, material []byte,
	keyOps func(*db.Queries) codec.IdempotencyKeyOps,
) (idempotency.Result, codec.IdempotencyKey, error) {
	var (
		res idempotency.Result
		key codec.IdempotencyKey
	)

	err := run(ctx, func(q *db.Queries) error {
		var keyErr error

		if key, keyErr = i.keys.Resolve(ctx, keyOps(q)); keyErr != nil {
			return keyErr
		}

		at.Fingerprint = idempotency.Fingerprint(key.Key, material)

		var txErr error

		res, txErr = startOrFind(ctx, q, at, i.retention)

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
