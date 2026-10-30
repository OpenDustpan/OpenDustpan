package storecheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// State is the life-cycle state of a check row.
type State string

// The states of a check row. A running check has no end time.
const (
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
)

// Record is one row of the store checks table. Report holds the JSON of a
// Report and is nil until the check ends with a report. Error holds fixed
// text and is empty unless the check failed.
type Record struct {
	StartedAt   time.Time
	EndedAt     time.Time
	ID          ID
	Target      Target
	State       State
	RequestedBy string
	Error       string
	Report      []byte
}

// Finish is the end of a running check row.
type Finish struct {
	EndedAt time.Time
	State   State
	Error   string
	Report  []byte
}

// Records is the persistence port of the checks. Each SQL store adapter
// implements it, and runs the conformance suite in pkg/store/storetest. The
// table lives in the control schema, so a restore keeps its rows.
type Records interface {
	// StartCheck stores r as a running check and starts the operation start,
	// in one transaction. It returns ErrExists when the ID is taken,
	// ErrRunning when another check runs, ErrCooldown when a check on the
	// same target ended after coolingSince, and durable.ErrKeyConflict when
	// start is not of OperationKind under OperationKey(r.ID) or its key is
	// taken. It returns durable.ErrInvalid for a start that fails Validate.
	// On an error it stores nothing.
	StartCheck(ctx context.Context, r Record, start durable.StartRequest, coolingSince time.Time) error

	// GetCheck returns the row id, or ErrNotFound.
	GetCheck(ctx context.Context, id ID) (Record, error)

	// ListChecks returns every row, newest first.
	ListChecks(ctx context.Context) ([]Record, error)

	// FinishCheck checks the fence f, records its step and moves the running
	// check id to the end state in end, in one transaction. A fence that is not
	// the live claim of the operation of the check returns the durable
	// sentinel, and changes nothing. It returns ErrNotFound for an unknown ID
	// and ErrNotRunning for a row that is not running. A state other than
	// succeeded or failed returns ErrNotRunning.
	FinishCheck(ctx context.Context, f durable.StepFence, id ID, end Finish) error

	// FailEndedChecks marks failed each running row whose operation ended
	// failed or canceled without a finish write, and returns the count. It
	// sets the end time to endedAt.
	FailEndedChecks(ctx context.Context, endedAt time.Time) (int, error)
}

// ErrInvalidRecord reports a row that fails ValidateStart or a finish that
// fails Finish.Validate.
var ErrInvalidRecord = errors.New("invalid store check row")

// maxRequestedBy is the longest requester text, in bytes.
const maxRequestedBy = 256

// ValidateStart checks the row of a new check: a valid ID and target, the
// state running, no end time and a requester. The adapters check it again
// before they write, without relying on the service.
func (r Record) ValidateStart() error {
	if _, err := ParseID(r.ID.String()); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}

	if _, err := ParseTarget(string(r.Target)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}

	switch {
	case r.State != StateRunning:
		return fmt.Errorf("%w: a new check is running", ErrInvalidRecord)
	case r.RequestedBy == "" || len(r.RequestedBy) > maxRequestedBy:
		return fmt.Errorf("%w: the requester needs 1 to %d bytes", ErrInvalidRecord, maxRequestedBy)
	case !r.EndedAt.IsZero():
		return fmt.Errorf("%w: a new check has no end time", ErrInvalidRecord)
	default:
		return nil
	}
}

// ValidateOperation checks that start is the operation of check id: the kind,
// and the key of OperationKey(id).
func ValidateOperation(id ID, start durable.StartRequest) error {
	if err := start.Validate(); err != nil {
		return fmt.Errorf("start store check %s: %w", id, err)
	}

	if start.Key != OperationKey(id) || start.Kind != OperationKind {
		return fmt.Errorf(
			"start store check %s: operation %q of kind %q: %w",
			id,
			start.Key,
			start.Kind,
			durable.ErrKeyConflict,
		)
	}

	return nil
}

// Validate checks the end of a check: succeeded or failed, with an end time.
func (f Finish) Validate() error {
	if f.State != StateSucceeded && f.State != StateFailed {
		return ErrNotRunning
	}

	if f.EndedAt.IsZero() {
		return fmt.Errorf("%w: a finish needs an end time", ErrInvalidRecord)
	}

	return nil
}

// EndedWithoutFinishText is the error text of a row that FailEndedChecks
// marks failed.
const EndedWithoutFinishText = "the check ended without a report"

// OperationKeyPrefix is the start key prefix of every check operation.
const OperationKeyPrefix = operationKeyPrefix
