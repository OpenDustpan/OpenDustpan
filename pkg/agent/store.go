package agent

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// NewRun asks for a run. ID is the ID of the new run: the server makes it
// from a random source before the create, so First can name it. Prompt is
// the first message of the person. First is the start of the operation of
// turn 1: the store starts it in the transaction of the create, with the
// rules of durable.Store.Start. Limits.MaxActiveForSubject and
// Limits.MaxActive bound the runs that are not terminal, for the subject and
// for the installation, at the create.
type NewRun struct {
	ID           RunID
	Key          string
	Subject      string
	CredentialID string
	Namespace    namespace.Name
	Parent       RunID
	Model        string
	Limits       Limits
	Prompt       string
	First        durable.StartRequest
}

// MaxRunList bounds the runs of one List.
const MaxRunList = 100

// RunFilter selects runs for List. An empty Subject, Namespace or States
// matches every run. Limit is between 1 and MaxRunList: 0 is not valid and
// never means "no limit".
type RunFilter struct {
	Subject   string
	Namespace namespace.Name
	States    []State
	Limit     int
}

// TranscriptEntry is one message of a run, at its turn and its index in the
// turn. Turn 0 index 0 is the prompt of the person.
type TranscriptEntry struct {
	Run     RunID
	Turn    int
	Index   int
	Message Message
	At      time.Time
}

// TurnRecord is what a step of a turn records: the messages of the step, in
// order, and the progress of the run after the step. Every message is
// masked (MaskMessage) before the record. Reveals are the sealed results of
// the tool calls of a tools step that read secret values in plain text. A
// record with Reveals also marks the run with ReadPlainSecrets. Shred is
// true for the record of a model step: the store then deletes every sealed
// reveal of the run in the transaction of the record.
type TurnRecord struct {
	Run      RunID
	Turn     int
	Messages []Message
	Progress Progress
	Reveals  []SealedReveal
	Shred    bool
}

// RunEnd is how a run ends. LastError is the message of a failed run, in
// plain text, with no value from a tool result.
type RunEnd struct {
	Run       RunID
	State     State
	Stop      StopReason
	LastError string
}

// UsageRecord is the token use of one model call attempt. Claim is the token
// of the claim that made the call, so the ledger counts an attempt whose
// claim was lost before its step record, and counts each attempt once.
type UsageRecord struct {
	Run   RunID
	Turn  int
	Claim int64
	Model string
	Usage Usage
	At    time.Time
}

// RunStore is the persistence port for runs. Each SQL store adapter
// implements it in its own SQL, and runs the conformance suite in package
// agenttest. The transcript can hold text that a person or a tool result
// gave, so a store keeps it in its own table, never in an audit entry or a
// log record.
//
// A method that takes a durable.StepFence checks and records the step with
// the rules of durable.Store.RecordStep, in the transaction of its own write.
//
//nolint:interfacebloat // One contract for the rows of a run: the run, its transcript, its ledger and its reveals.
type RunStore interface {
	// Create makes a pending run for req and starts the operation of its
	// first turn, or returns the run that req.Key already names for
	// req.Subject, whatever its other fields. created is true only for a
	// new run. It returns ErrInvalid for an empty ID, key, subject or
	// prompt, or for limits that fail Limits.Validate, ErrKeyConflict when
	// the key names a run of another subject, and ErrConcurrencyLimit when
	// the subject has req.Limits.MaxActiveForSubject runs that did not end,
	// or the installation has req.Limits.MaxActive. A run whose parent read
	// secret values in plain text starts with ReadPlainSecrets set.
	Create(ctx context.Context, req NewRun) (run Run, created bool, err error)

	// Get returns the run id, or ErrNotFound.
	Get(ctx context.Context, id RunID) (Run, error)

	// List returns at most f.Limit runs that f selects, newest first: in the
	// reverse order of their create, also for runs with one creation time.
	// It returns ErrInvalid for a limit outside 1 to MaxRunList.
	List(ctx context.Context, f RunFilter) ([]Run, error)

	// Transcript returns the messages of the run id, in the order of turn
	// and index. It returns ErrNotFound for an unknown run.
	Transcript(ctx context.Context, id RunID) ([]TranscriptEntry, error)

	// Reveals returns the sealed reveals of the run id that no model step
	// shredded yet, in the order of turn and call. It returns ErrNotFound
	// for an unknown run.
	Reveals(ctx context.Context, id RunID) ([]SealedReveal, error)

	// RecordTurn appends rec.Messages after the last message of rec.Turn,
	// writes rec.Reveals, shreds the reveals of the run when rec.Shred is
	// set, sets the progress of the run and records the step f. When next is
	// not the zero StartRequest, it also starts the operation next. It
	// returns ErrInvalid, ErrNotFound, and the errors of
	// durable.Store.RecordStep. On an error it changes nothing.
	RecordTurn(ctx context.Context, f durable.StepFence, rec TurnRecord, next durable.StartRequest) error

	// RecordUsage adds rec to the token ledger of its run. A second record
	// with one run, turn and claim changes nothing. It takes no fence, so a
	// holder that lost its claim still records the tokens that it used. It
	// returns ErrInvalid and ErrNotFound.
	RecordUsage(ctx context.Context, rec UsageRecord) error

	// RequestCancel records a cancel of the run id by subject by, and
	// returns the run. A second cancel changes nothing. It returns
	// ErrNotFound, and ErrFinished for a run that ended. The caller also
	// cancels the durable operation of the current turn.
	RequestCancel(ctx context.Context, id RunID, by string) (Run, error)

	// Finish ends the run end.Run with end.State, end.Stop and
	// end.LastError, shreds its sealed reveals, and records the step f. It returns ErrInvalid for a
	// state that is not terminal, ErrNotFound, and the errors of
	// durable.Store.RecordStep. A run whose cancel was requested ends
	// canceled whatever end asks for.
	Finish(ctx context.Context, f durable.StepFence, end RunEnd) error
}
