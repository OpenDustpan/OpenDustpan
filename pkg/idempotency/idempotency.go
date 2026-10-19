// Package idempotency holds the replay records of the Idempotency-Key header
// of the tool-neutral API. A record keeps the first answer of a request, so
// a client that retries a call that timed out gets that answer and the
// operation runs one time.
//
// A record belongs to the caller, the method, the path and the key. A record
// never holds a request body: it holds the keyed hash of the body.
//
// The hash key is a random server key. The key-encryption key seals it in the
// database, so a copy of the database holds no key that can test a guess of
// a body. If the key-encryption key cannot open the sealed key (the operator
// lost it or changed it), the store makes a new key and deletes all records
// in one transaction. A retry then does its operation again.
package idempotency

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxKeyLength is the longest key, in characters.
	MaxKeyLength = 255
	// RecordTTL is how long a record lives, from the first request.
	RecordTTL = 24 * time.Hour
	// PendingTTL is how long a record with no final answer lives. After it, a
	// request with the same key does the operation.
	PendingTTL = 5 * time.Minute
	// MaxAnswerBytes is the largest body of a stored answer. The server does
	// not store a larger answer: the first request still answers, and a retry
	// with the same key does the operation again.
	MaxAnswerBytes = 1 << 20
)

// The statuses that the server never stores. They are the values of the
// net/http constants of the same names: a domain package does not import
// net/http.
const (
	statusBadRequest          = 400
	statusUnauthorized        = 401
	statusForbidden           = 403
	statusNotFound            = 404
	statusConflict            = 409
	statusPreconditionFailed  = 412
	statusUnprocessableEntity = 422
	statusLocked              = 423
	statusTooManyRequests     = 429
)

var (
	// ErrInvalidKey reports a key that is empty, longer than MaxKeyLength, or
	// holds a character that is not printable ASCII.
	ErrInvalidKey = errors.New("invalid idempotency key")
	// ErrInvalid reports a call with a field that the port refuses, for
	// example an empty scope or an answer with a status below 200.
	ErrInvalid = errors.New("invalid idempotency record")
	// ErrLost reports that the pending record of a request is not in place:
	// it expired and a retry took the key, or a sweep deleted it. A write
	// that completes the record in its transaction rolls back on it.
	ErrLost = errors.New("idempotency record is not pending")
)

// Key is a client-chosen string of 1 to MaxKeyLength printable ASCII
// characters.
type Key string

// ParseKey returns s as a Key, or ErrInvalidKey.
func ParseKey(s string) (Key, error) {
	if s == "" || len(s) > MaxKeyLength {
		return "", fmt.Errorf("%w: a key has 1 to %d characters", ErrInvalidKey, MaxKeyLength)
	}

	for i := range len(s) {
		if s[i] < ' ' || s[i] > '~' {
			return "", fmt.Errorf("%w: a key has printable ASCII characters only", ErrInvalidKey)
		}
	}

	return Key(s), nil
}

// Scope is the part of a record that the key does not name: the caller, the
// method and the path. The same key in another scope is another key.
type Scope struct {
	// Principal names the caller, for example the subject of the principal.
	Principal string
	Method    string
	Path      string
}

// Ref names one record.
type Ref struct {
	Scope

	Key Key
}

// Validate returns ErrInvalid for a field that is empty and ErrInvalidKey
// for a key that ParseKey refuses.
func (r Ref) Validate() error {
	if r.Principal == "" || r.Method == "" || r.Path == "" {
		return fmt.Errorf("%w: a record names a principal, a method and a path", ErrInvalid)
	}

	if _, err := ParseKey(string(r.Key)); err != nil {
		return err
	}

	return nil
}

// Answer is the first answer of a request: the status, the headers that a
// replay sends again and the body.
type Answer struct {
	Headers map[string]string
	Body    []byte
	Status  int
}

// Validate returns ErrInvalid for a status outside 200 to 499 and for a body
// over MaxAnswerBytes.
func (a Answer) Validate() error {
	if a.Status < 200 || a.Status > 499 {
		return fmt.Errorf("%w: a stored answer has a status from 200 to 499, got %d", ErrInvalid, a.Status)
	}

	if len(a.Body) > MaxAnswerBytes {
		return fmt.Errorf(
			"%w: a stored answer has at most %d body bytes, got %d",
			ErrInvalid,
			MaxAnswerBytes,
			len(a.Body),
		)
	}

	return nil
}

// Storable reports whether the server stores an answer with this status.
// The server stores a 2xx answer and a 4xx answer that the operation gave
// after it started. It never stores the statuses that a retry with the same
// key can fix: 400, 401, 403, 404, 409, 412, 422, 423 and 429, and never 5xx.
func Storable(status int) bool {
	switch status {
	case statusBadRequest, statusUnauthorized, statusForbidden, statusNotFound, statusConflict,
		statusPreconditionFailed, statusUnprocessableEntity, statusLocked, statusTooManyRequests:
		return false
	}

	return (status >= 200 && status < 300) || (status >= 400 && status < 500)
}

// Outcome is what Begin found.
type Outcome int

// The outcomes of Begin.
const (
	// Started: the request is the first one with this key. A pending record
	// is in place. The caller runs the operation, then calls Complete or
	// Abandon.
	Started Outcome = iota + 1
	// Replayed: a record with the same body holds a final answer.
	Replayed
	// Reused: a record holds another body.
	Reused
	// InProgress: the first request has no final answer yet.
	InProgress
)

// Result is the answer of Begin. Token is set for Started, and Answer is set
// for Replayed.
type Result struct {
	Answer  Answer
	Token   string
	Outcome Outcome
}

// Store keeps the records.
//
// A record that is older than RecordTTL, or pending and older than
// PendingTTL, counts as absent for every method. All methods return
// ErrInvalid or ErrInvalidKey for an invalid Ref, and change nothing then.
//
// Time comes from the clock of the store, so the clock of one instance
// changes no decision. The now argument of Begin and Sweep overrides that
// clock, for tests. The zero time means the clock of the store, and every
// caller outside a test passes it.
type Store interface {
	// Begin finds the record of ref, or writes a pending record. material
	// is the bytes that the request is compared by, for example the body.
	// The store keeps only its HMAC-SHA-256 under a key of the store, never
	// the bytes. A record of another caller never shows: scopes are separate.
	Begin(ctx context.Context, ref Ref, material []byte, now time.Time) (Result, error)
	// Complete stores the final answer of the pending record that holds
	// token. ErrLost when no pending record holds it.
	Complete(ctx context.Context, ref Ref, token string, a Answer) error
	// Abandon deletes the pending record that holds token, so a retry with
	// the same key does the operation. It returns nil when none holds it.
	Abandon(ctx context.Context, ref Ref, token string) error
	// Sweep deletes the records past RecordTTL and the pending records past
	// PendingTTL, and returns how many it deleted.
	Sweep(ctx context.Context, now time.Time) (int, error)
}

// Fingerprint returns the HMAC-SHA-256 of material under key. Adapters use
// it, so every adapter compares by the same value.
func Fingerprint(key, material []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(material)

	return mac.Sum(nil)
}
