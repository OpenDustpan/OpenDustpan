package stack

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrNoStateAt reports a time before the first checkpoint of a stack.
var ErrNoStateAt = errors.New("stack has no state at that time")

// NoStateAtError is the error that StateAt returns for a time before the first
// checkpoint. First is the end time of the first update that saved a
// checkpoint, or the zero time when the stack has none yet. It matches
// ErrNoStateAt with errors.Is.
type NoStateAtError struct {
	First time.Time
}

// Error implements error.
func (e *NoStateAtError) Error() string {
	return ErrNoStateAt.Error()
}

// Is reports whether target is ErrNoStateAt.
func (e *NoStateAtError) Is(target error) bool {
	return target == ErrNoStateAt
}

// ResourceChangeKind is what one update did to one resource, by URN.
type ResourceChangeKind string

// The kinds of a resource change.
const (
	// ResourceCreated is a URN that the checkpoint holds and the previous
	// checkpoint does not.
	ResourceCreated ResourceChangeKind = "created"
	// ResourceUpdated is a URN with the same ID and other values.
	ResourceUpdated ResourceChangeKind = "updated"
	// ResourceReplaced is a URN with a new ID.
	ResourceReplaced ResourceChangeKind = "replaced"
	// ResourceDeleted is a URN that the previous checkpoint holds and the
	// checkpoint does not.
	ResourceDeleted ResourceChangeKind = "deleted"
)

// ResourceChange is one row of the blame of a stack: what the update Version
// did to the resource URN. ChangedPaths counts the value paths that changed,
// from the masked diff. A secret value counts as changed when the SHA-256 of
// its plaintext differs: the blame operation decrypts the value with the
// data key of the stack inside the server to compute that digest, and never
// stores or returns the digest or the plaintext. A row holds no value and no
// path of a secret value, so a redaction of the update keeps it. At is the
// end time of the update. By is the actor of the update.
type ResourceChange struct {
	At           time.Time
	Stack        ID
	Update       UpdateID
	URN          string
	Kind         ResourceChangeKind
	UpdateKind   UpdateKind
	By           history.Actor
	Version      int
	ChangedPaths int
}

// VersionBlame is the blame of the update Version of the stack Stack: one
// row for each resource that it changed. An update with no change has no
// row and still moves the cursor.
type VersionBlame struct {
	Stack   ID
	Changes []ResourceChange
	Version int
}

// BlameStore is the persistence port of the blame of stacks. The blame
// operation compares the checkpoint of each update with the checkpoint of
// the previous update that saved one, in the order of versions, and writes
// the rows with this port.
type BlameStore interface {
	// RecordResourceChanges writes the rows of b, and moves the blame cursor
	// of the stack b.Stack to b.Version, in one transaction with the fence of
	// ns. When the cursor is at or past b.Version, it answers nil and changes
	// nothing, so a repeated run writes no row twice. It does not check that
	// the stack exists: the blame operation reads the stack first. It
	// returns namespace.ErrFenced when the fence is stale.
	RecordResourceChanges(ctx context.Context, ns namespace.Name, b VersionBlame, fence namespace.Fence) error
	// BlameCursor returns the last version that the blame operation
	// compared for the stack id, or 0 when it compared none.
	BlameCursor(ctx context.Context, id ID) (int, error)
	// ResourceHistory returns at most limit rows of the resource urn of the
	// stack id, newest first by version, with a version below before (0 for
	// the first page).
	ResourceHistory(ctx context.Context, id ID, urn string, before, limit int) ([]ResourceChange, error)
	// VersionChanges returns every row of the update version of the stack
	// id, ordered by URN. An update with no row reads as an empty list.
	VersionChanges(ctx context.Context, id ID, version int) ([]ResourceChange, error)
}

// StateAt names the update that held the state of a stack at a time.
// Version is the update with the highest version that saved a checkpoint
// and ended at or before the time. Running is the version of an update that
// ran at that time, whose checkpoints were not final, or 0. CreatedBy is the
// subject that asked for the update of Version.
type StateAt struct {
	EndedAt   time.Time
	CreatedBy string
	Version   int
	Running   int
}

// StateAtReader finds the state of a stack at a time.
type StateAtReader interface {
	// StateAt returns the update that held the state of the stack id at the
	// time at. It returns ErrNoStateAt when no update that saved a
	// checkpoint ended at or before at, and ErrNotFound for an unknown
	// stack. The ErrNoStateAt error is a *NoStateAtError, and its First holds
	// the end time of the first update that saved a checkpoint, or the zero
	// time when the stack has none. The result is the zero StateAt with
	// each error. A soft-deleted stack answers as before its delete.
	StateAt(ctx context.Context, id ID, at time.Time) (StateAt, error)
}
