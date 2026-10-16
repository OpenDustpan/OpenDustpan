// Package durable is the spike of the durable-execution port of RFC 36. It
// runs operations (kinds with ordered steps) and schedules on several
// instances over one database. Each step runs in a transaction that the
// adapter opens, so the step's domain write and the adapter's step record
// commit together. Throwaway code: it exists to compare two adapters.
package durable

import (
	"context"
	"errors"
	"time"
)

// Tx is the transaction a step writes its domain rows through.
type Tx interface {
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	QueryRow(ctx context.Context, query string, args ...any) Row
}

// Row is one result row.
type Row interface {
	Scan(dest ...any) error
}

// StepFunc runs one step inside tx. An error rolls back tx and fails the
// attempt.
type StepFunc func(ctx context.Context, tx Tx, input []byte) error

// Step is a named step of a kind.
type Step struct {
	Name string
	Run  StepFunc
}

// Kind names an operation type and its ordered steps.
type Kind struct {
	Name  string
	Steps []Step
}

// Schedule starts an operation of Kind once per Every across all instances.
type Schedule struct {
	Name  string
	Kind  string
	Every time.Duration
}

// State is the state of an operation.
type State string

// States of an operation.
const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
)

// Status is what any instance can read about an operation.
type Status struct {
	ID        string
	Kind      string
	State     State
	StepsDone int
	Holder    string
	Error     string
}

// Engine is the port.
type Engine interface {
	// Start creates the operation for key once. A second start with the same
	// key answers the same ID.
	Start(ctx context.Context, key, kind string, input []byte) (string, error)
	// Get answers the status of the operation id from any instance.
	Get(ctx context.Context, id string) (Status, error)
	// Close stops the workers of this instance and waits for them.
	Close(ctx context.Context) error
}

// ErrNotFound reports an unknown operation.
var ErrNotFound = errors.New("operation not found")

// ErrClaimLost reports a step write by a holder whose claim another instance
// took.
var ErrClaimLost = errors.New("claim lost")
