package codec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/secret"
)

// The size of the server key of the fingerprints and the size of a token, in
// bytes.
const (
	idempotencyServerKeyBytes = 32
	idempotencyTokenBytes     = 16
)

// idempotencyKeyAAD binds the wrapped server key to its use, so the
// key-encryption key opens no other wrapped value as the server key.
const idempotencyKeyAAD = "idempotency-server-key"

// ErrIdempotencyNoWrapper reports a store of replay records made with no
// key-encryption key.
var ErrIdempotencyNoWrapper = errors.New("idempotency store has no key-encryption key")

// errIdempotencyKeyMissing reports a server key that is not in place after
// the insert of it, which only a delete by another process can cause.
var errIdempotencyKeyMissing = errors.New("idempotency server key is not in place after insert")

// IdempotencyOverrideMillis returns now as Unix milliseconds, or 0 for the
// zero time. A zero value tells the query to read the clock of the database.
func IdempotencyOverrideMillis(now time.Time) int64 {
	if now.IsZero() {
		return 0
	}

	return now.UnixMilli()
}

// IdempotencyRow is the column representation of a replay record. Both
// dialects generate the same fields.
type IdempotencyRow struct {
	Fingerprint []byte
	Token       string
	Status      int64
	Headers     string
	Body        []byte
	CreatedAtMs int64
}

// NewIdempotencyToken returns a random token for a pending record.
func NewIdempotencyToken() (string, error) {
	raw := make([]byte, idempotencyTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("make idempotency token: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// NewIdempotencyServerKey returns a random key for the fingerprints.
func NewIdempotencyServerKey() ([]byte, error) {
	raw := make([]byte, idempotencyServerKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("make idempotency server key: %w", err)
	}

	return raw, nil
}

// EncodeIdempotencyHeaders returns the stored form of the headers of an answer.
func EncodeIdempotencyHeaders(h map[string]string) (string, error) {
	if len(h) == 0 {
		return "", nil
	}

	raw, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("encode idempotency headers: %w", err)
	}

	return string(raw), nil
}

// IdempotencyOutcome returns the result of Begin that holds no answer and no
// token.
func IdempotencyOutcome(outcome idempotency.Outcome) idempotency.Result {
	var none idempotency.Answer

	return idempotency.Result{Answer: none, Token: "", Outcome: outcome}
}

// ClassifyIdempotency returns what a request with the fingerprint fp finds in
// the live record row.
func ClassifyIdempotency(row IdempotencyRow, fp []byte) (idempotency.Result, error) {
	switch {
	case !bytes.Equal(row.Fingerprint, fp):
		return IdempotencyOutcome(idempotency.Reused), nil
	case row.Status == 0:
		return IdempotencyOutcome(idempotency.InProgress), nil
	}

	headers := map[string]string{}

	if row.Headers != "" {
		if err := json.Unmarshal([]byte(row.Headers), &headers); err != nil {
			return idempotency.Result{}, fmt.Errorf("decode idempotency headers: %w", err)
		}
	}

	return idempotency.Result{
		Answer:  idempotency.Answer{Headers: headers, Body: row.Body, Status: int(row.Status)},
		Token:   "",
		Outcome: idempotency.Replayed,
	}, nil
}

// IdempotencyKeyRow is the stored form of the server key: the key wrapped by
// the key-encryption key, and the ID of the wrapper that did it.
type IdempotencyKeyRow struct {
	WrappedKey []byte
	WrapperID  string
}

// IdempotencyKeyOps are the operations of one dialect on the key row and the
// records, all in one transaction.
type IdempotencyKeyOps struct {
	// Get returns the key row, and false when none exists.
	Get func(ctx context.Context) (IdempotencyKeyRow, bool, error)
	// Insert writes the row. It changes nothing when a row exists.
	Insert func(ctx context.Context, row IdempotencyKeyRow) error
	// Replace writes the row over the one in place.
	Replace func(ctx context.Context, row IdempotencyKeyRow) error
	// DeleteRecords deletes every replay record.
	DeleteRecords func(ctx context.Context) error
}

// IdempotencyKeys holds the key-encryption key of a store of replay records
// and the server key that a process unwrapped from it. The process reads and
// unwraps the row one time.
type IdempotencyKeys struct {
	wrapper secret.Wrapper
	mu      sync.Mutex
	key     []byte
}

// NewIdempotencyKeys returns the keys of a store that seals with wrapper.
func NewIdempotencyKeys(wrapper secret.Wrapper) *IdempotencyKeys {
	return &IdempotencyKeys{wrapper: wrapper, mu: sync.Mutex{}, key: nil}
}

// Cached returns the server key that an earlier Remember kept.
func (k *IdempotencyKeys) Cached() ([]byte, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.key, k.key != nil
}

// Remember keeps key. Call it after the transaction of Resolve committed.
func (k *IdempotencyKeys) Remember(key []byte) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.key = key
}

// Resolve returns the server key, in the transaction that ops belongs to. It
// makes and seals a key when no row exists. When the key-encryption key
// cannot open the row (the operator lost it or changed it), it replaces the
// row with a new key and deletes every record: a record made under the old
// key can never match a new fingerprint. The first writer of a new row wins,
// and every other writer reads that row.
func (k *IdempotencyKeys) Resolve(ctx context.Context, ops IdempotencyKeyOps) ([]byte, error) {
	if k.wrapper == nil {
		return nil, ErrIdempotencyNoWrapper
	}

	row, found, err := ops.Get(ctx)
	if err != nil {
		return nil, err
	}

	if !found {
		if _, insErr := k.writeNew(ctx, ops.Insert); insErr != nil {
			return nil, insErr
		}

		if row, found, err = ops.Get(ctx); err != nil {
			return nil, err
		}

		if !found {
			return nil, errIdempotencyKeyMissing
		}
	}

	key, err := k.open(ctx, row)
	if err == nil {
		return key, nil
	}

	if !errors.Is(err, secret.ErrDecrypt) {
		return nil, err
	}

	if delErr := ops.DeleteRecords(ctx); delErr != nil {
		return nil, delErr
	}

	return k.writeNew(ctx, ops.Replace)
}

// open unwraps row. A row of another wrapper is a decrypt failure without a
// call to the wrapper.
func (k *IdempotencyKeys) open(ctx context.Context, row IdempotencyKeyRow) ([]byte, error) {
	if row.WrapperID != k.wrapper.ID() {
		return nil, fmt.Errorf("%w: the server key is sealed by wrapper %q, this process has %q",
			secret.ErrDecrypt, row.WrapperID, k.wrapper.ID())
	}

	key, err := k.wrapper.Unwrap(ctx, row.WrappedKey, []byte(idempotencyKeyAAD))
	if err != nil {
		return nil, fmt.Errorf("unwrap idempotency server key: %w", err)
	}

	return key, nil
}

// writeNew makes a key, seals it and writes it with write. It returns the key.
func (k *IdempotencyKeys) writeNew(
	ctx context.Context,
	write func(context.Context, IdempotencyKeyRow) error,
) ([]byte, error) {
	fresh, err := NewIdempotencyServerKey()
	if err != nil {
		return nil, err
	}

	wrapped, err := k.wrapper.Wrap(ctx, fresh, []byte(idempotencyKeyAAD))
	if err != nil {
		return nil, fmt.Errorf("wrap idempotency server key: %w", err)
	}

	row := IdempotencyKeyRow{WrappedKey: wrapped, WrapperID: k.wrapper.ID()}

	if writeErr := write(ctx, row); writeErr != nil {
		return nil, writeErr
	}

	return fresh, nil
}

// IdempotencyAttempt is one Begin: the record it looks for, the fingerprint of
// the request, the token of a new pending record and the time override.
type IdempotencyAttempt struct {
	Ref         idempotency.Ref
	Fingerprint []byte
	Token       string
	// Now is the time override of a test. The zero time is the clock of the
	// database.
	Now time.Time
}
