// Package durable defines durable operations: units of background work that
// survive the stop of the instance that runs them. Several instances share
// one database. They coordinate only through the Store port.
//
// An operation has a start key, a kind, an input and ordered steps. One
// instance at a time holds a claim on a running operation. The claim has an
// owner, a token that grows on each new claim, and an expiry on the clock of
// the store. The holder extends the expiry while it works. When the holder
// stops, the expiry passes and another instance can claim the operation.
//
// A write by a holder carries a StepFence. The store checks the token of the
// fence in the transaction of the write, as it checks a namespace fence. So a
// holder that lost its claim cannot write, extend or complete.
//
// The package is tool-neutral. It never imports a store adapter or a
// transport.
package durable

import (
	"errors"
	"slices"
	"time"
)

var (
	// ErrInvalid reports a request with an empty or bad field.
	ErrInvalid = errors.New("durable operation request is not valid")
	// ErrNotFound reports an operation ID that the store does not know.
	ErrNotFound = errors.New("durable operation not found")
	// ErrKeyConflict reports a start whose key names an operation of
	// another kind.
	ErrKeyConflict = errors.New("start key names an operation of another kind")
	// ErrNoWork reports that no operation of the requested kinds can be
	// claimed now.
	ErrNoWork = errors.New("no durable operation to claim")
	// ErrClaimLost reports a write by a claim that is no longer the live
	// claim of its operation: another claim replaced it, it expired, or the
	// operation is no longer running.
	ErrClaimLost = errors.New("claim on the durable operation is lost")
	// ErrStepDone reports a step that the operation already recorded.
	ErrStepDone = errors.New("step of the durable operation is already done")
	// ErrCanceled reports a write to an operation that a cancel stopped.
	ErrCanceled = errors.New("durable operation is canceled")
	// ErrFinished reports a cancel of an operation that already ended.
	ErrFinished = errors.New("durable operation already ended")
)

// ID identifies an operation. The store assigns it at start.
type ID string

// Kind names a type of operation. An instance claims only the kinds that it
// can run.
type Kind string

// State is the life-cycle state of an operation.
type State string

// The states of an operation. Succeeded, failed and canceled are terminal.
const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

// Terminal reports whether s is an end state.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCanceled
}

// Operation is what any instance can read about an operation.
type Operation struct {
	ID   ID
	Key  string
	Kind Kind
	// Version is the version of the kind that started the operation. A
	// runner refuses an operation of a version that it does not support.
	Version int
	Input   []byte
	State   State
	// Steps are the recorded steps, in the order of their record.
	Steps []StepRecord
	// Attempt is the number of claims so far. The first claim sets 1. A
	// release and a takeover also count, so Attempt is no limit for
	// retries: use Failures.
	Attempt int
	// Failures is the number of failed attempts: the calls of Fail.
	Failures int
	// Expiries is the number of claims that took the operation over after
	// the claim before them expired: the number of holders that stopped
	// without an end write. A release, a retry and a first claim do not
	// count.
	Expiries int
	// LastError is the message of the last failed attempt, or empty.
	LastError string
	// Claim is the claim of a running operation. It is the zero Claim when
	// no instance holds the operation: before the first claim, and after
	// Complete, Fail, Release or the end of a cancel. A running operation
	// whose claim expired keeps it until the next claim.
	Claim Claim
	// CancelRequested is true after a cancel of a running operation. The
	// holder sees ErrCanceled at its next write.
	CancelRequested bool
	// NotBefore is the earliest time of the next claim of a pending
	// operation, on the clock of the store. A retry sets it.
	NotBefore time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Step returns the record of the step called name, and false when the
// operation has not recorded it.
func (o Operation) Step(name string) (StepRecord, bool) {
	for _, s := range o.Steps {
		if s.Name == name {
			return s, true
		}
	}

	return StepRecord{Name: "", Result: nil, DoneAt: time.Time{}}, false
}

// StepRecord is the stored outcome of one step.
type StepRecord struct {
	Name   string
	Result []byte
	DoneAt time.Time
}

// Claim is a hold on a running operation. Owner names the instance that
// holds it, for status only. Token grows on each new claim of the operation.
// ExpiresAt is on the clock of the store.
type Claim struct {
	Operation ID
	Owner     string
	Token     int64
	ExpiresAt time.Time
}

// Step returns the fence that a write of the step called name carries.
func (c Claim) Step(name string) StepFence {
	return StepFence{Operation: c.Operation, Token: c.Token, Step: name}
}

// StepFence is the token that a step write carries. A store method that
// takes a StepFence checks, in the transaction of the write, that Token is
// the live claim of Operation, and records the step called Step in the same
// transaction. The write and the record commit together or not at all.
type StepFence struct {
	Operation ID
	Token     int64
	Step      string
}

// StartRequest asks for an operation. Two starts with one Key give one
// operation.
type StartRequest struct {
	Key  string
	Kind Kind
	// Version is the version of the kind, from its registration. It is 0 or
	// more.
	Version int
	Input   []byte
}

// ClaimRequest asks for one operation to run. Lease is the time from the
// claim to its expiry, on the clock of the store.
type ClaimRequest struct {
	Owner string
	Kinds []Kind
	// MaxVersions limits the version of each kind that the claimer runs. A
	// claim never takes an operation whose version is above the limit of its
	// kind. A kind with no entry has no limit, so a nil map limits nothing.
	MaxVersions map[Kind]int
	Lease       time.Duration
}

// Failure ends one attempt of an operation that failed. When Final is true,
// the operation ends failed. Otherwise it returns to pending, and no instance
// can claim it before RetryAfter passes on the clock of the store.
type Failure struct {
	Message    string
	Final      bool
	RetryAfter time.Duration
}

// Filter selects operations for List. A zero field matches every operation.
// Limit 0 means no limit.
type Filter struct {
	Kind   Kind
	States []State
	Limit  int
}

// Validate returns ErrInvalid when req has an empty key or kind, or a
// negative version.
func (req StartRequest) Validate() error {
	if req.Key == "" || req.Kind == "" || req.Version < 0 {
		return ErrInvalid
	}

	return nil
}

// Validate returns ErrInvalid when req has no owner, no kind, an empty kind,
// a negative version limit or a lease that is not positive.
func (req ClaimRequest) Validate() error {
	if req.Owner == "" || len(req.Kinds) == 0 || req.Lease <= 0 || slices.Contains(req.Kinds, "") {
		return ErrInvalid
	}

	for _, v := range req.MaxVersions {
		if v < 0 {
			return ErrInvalid
		}
	}

	return nil
}

// Allows reports whether req lets a claim take an operation of kind at
// version: kind is in Kinds, and version is not above its limit.
func (req ClaimRequest) Allows(kind Kind, version int) bool {
	if !slices.Contains(req.Kinds, kind) {
		return false
	}

	limit, ok := req.MaxVersions[kind]

	return !ok || version <= limit
}
