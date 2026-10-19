package idempotencytest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// Memory is a store of replay records in memory. It is the reference for the
// suite and a store for tests of other packages. It keeps one server key for
// its life.
type Memory struct {
	mu      sync.Mutex
	key     []byte
	records map[idempotency.Ref]*memRecord
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{mu: sync.Mutex{}, key: []byte("test key"), records: map[idempotency.Ref]*memRecord{}}
}

var _ idempotency.Store = (*Memory)(nil)

// tokenBytes is the size of a token, in bytes.
const tokenBytes = 16

type memRecord struct {
	created     time.Time
	answer      idempotency.Answer
	token       string
	fingerprint []byte
	done        bool
}

func expired(r *memRecord, now time.Time) bool {
	age := now.Sub(r.created)

	return age > idempotency.RecordTTL || (!r.done && age > idempotency.PendingTTL)
}

// Begin implements idempotency.Store.
func (m *Memory) Begin(
	_ context.Context,
	ref idempotency.Ref,
	material []byte,
	now time.Time,
) (idempotency.Result, error) {
	if err := ref.Validate(); err != nil {
		return idempotency.Result{}, err
	}

	if now.IsZero() {
		now = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	fp := idempotency.Fingerprint(m.key, material)

	var none idempotency.Answer

	rec, ok := m.records[ref]
	if ok && !expired(rec, now) {
		switch {
		case !bytes.Equal(rec.fingerprint, fp):
			return idempotency.Result{Answer: none, Token: "", Outcome: idempotency.Reused}, nil
		case rec.done:
			return idempotency.Result{Answer: rec.answer, Token: "", Outcome: idempotency.Replayed}, nil
		default:
			return idempotency.Result{Answer: none, Token: "", Outcome: idempotency.InProgress}, nil
		}
	}

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return idempotency.Result{}, fmt.Errorf("make token: %w", err)
	}

	token := hex.EncodeToString(raw)
	m.records[ref] = &memRecord{created: now, answer: none, token: token, fingerprint: fp, done: false}

	return idempotency.Result{Answer: none, Token: token, Outcome: idempotency.Started}, nil
}

// Complete implements idempotency.Store.
func (m *Memory) Complete(_ context.Context, ref idempotency.Ref, token string, a idempotency.Answer) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	if err := a.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.records[ref]
	if !ok || rec.done || rec.token != token {
		return idempotency.ErrLost
	}

	rec.done, rec.answer = true, a

	return nil
}

// Abandon implements idempotency.Store.
func (m *Memory) Abandon(_ context.Context, ref idempotency.Ref, token string) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.records[ref]; ok && !rec.done && rec.token == token {
		delete(m.records, ref)
	}

	return nil
}

// Sweep implements idempotency.Store.
func (m *Memory) Sweep(_ context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for ref, rec := range m.records {
		if expired(rec, now) {
			delete(m.records, ref)

			n++
		}
	}

	return n, nil
}
