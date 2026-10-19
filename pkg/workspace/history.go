package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrNoStateAt reports a time before the first finalized state version of
// a workspace.
var ErrNoStateAt = errors.New("workspace has no state at that time")

// NoStateAtError is the error that WorkspaceStateAt returns for a time before the
// first finalized state version. First is the finalize time of the first
// state version, or the zero time when the workspace has none yet. It matches
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

// ErrInvalidBlame reports an ItemBlame that the store cannot keep: a row of
// another workspace, state version or ordinal, an unknown kind or an empty
// address, two rows with one address, a row on an opaque blame, an ordinal
// below 1, or no state version.
var ErrInvalidBlame = errors.New("invalid blame")

// ResourceChangeKind is what one state version did to one resource
// instance, by address.
type ResourceChangeKind string

// The kinds of a resource change.
const (
	// ResourceCreated is an address that the state version holds and the
	// previous one does not.
	ResourceCreated ResourceChangeKind = "created"
	// ResourceUpdated is an address with the same id attribute and other
	// values.
	ResourceUpdated ResourceChangeKind = "updated"
	// ResourceReplaced is an address with a new id attribute.
	ResourceReplaced ResourceChangeKind = "replaced"
	// ResourceDeleted is an address that the previous state version holds
	// and the state version does not.
	ResourceDeleted ResourceChangeKind = "deleted"
)

// ResourceChange is one row of the blame of a workspace: what the state
// version StateVersion did to the resource instance Address, for example
// module.net.aws_subnet.a[0]. Ordinal is the place of the state version in
// the order of finalizes of the workspace, from 1. ChangedPaths counts the
// value paths that changed, from the masked diff. A row holds no value and
// no path of a secret value, so a redaction of the state version keeps it.
// Restored is true when the state version is a restore, Forced when it is a
// forced write. LineageChanged is true when the state version has another
// lineage than the state version that it compares with: blame still
// compares across lineages. At is the finalize time. By is the actor of the
// state version.
type ResourceChange struct {
	At             time.Time
	Workspace      ID
	StateVersion   StateVersionID
	Address        string
	Kind           ResourceChangeKind
	By             history.Actor
	Ordinal        int64
	ChangedPaths   int
	Restored       bool
	Forced         bool
	LineageChanged bool
}

// ItemBlame is the blame of the state version StateVersion of the
// workspace Workspace, whose place in the order of finalizes is Ordinal.
// Opaque is true when the client encrypted the state, so the server could
// not read it and wrote no row. The next readable state version compares
// with the last readable one before it. LineageChanged is true when the
// state version has another lineage than the state version that it
// compares with. Blame still compares across lineages, and the mark stays
// also when the blame has no row.
type ItemBlame struct {
	Workspace      ID
	StateVersion   StateVersionID
	Changes        []ResourceChange
	Ordinal        int64
	Opaque         bool
	LineageChanged bool
}

// Validate returns ErrInvalidBlame, with the reason, when b is a blame that
// a BlameStore cannot keep. Every store calls it before it writes.
func (b ItemBlame) Validate() error {
	switch {
	case b.Workspace == "":
		return fmt.Errorf("%w: no workspace", ErrInvalidBlame)
	case b.StateVersion == "":
		return fmt.Errorf("%w: no state version", ErrInvalidBlame)
	case b.Ordinal < 1:
		return fmt.Errorf("%w: ordinal %d is below 1", ErrInvalidBlame, b.Ordinal)
	case b.Opaque && len(b.Changes) > 0:
		return fmt.Errorf("%w: an opaque state version has no row", ErrInvalidBlame)
	}

	seen := make(map[string]bool, len(b.Changes))

	for i := range b.Changes {
		r := &b.Changes[i]

		if err := r.validateIn(b, i); err != nil {
			return err
		}

		if seen[r.Address] {
			return fmt.Errorf("%w: address %q occurs twice", ErrInvalidBlame, r.Address)
		}

		seen[r.Address] = true
	}

	return nil
}

// validateIn returns ErrInvalidBlame when the row r, at place i, is not a
// valid row of the blame b.
func (r *ResourceChange) validateIn(b ItemBlame, i int) error {
	if r.Workspace != b.Workspace || r.StateVersion != b.StateVersion || r.Ordinal != b.Ordinal {
		return fmt.Errorf("%w: row %d is not a row of this state version", ErrInvalidBlame, i)
	}

	if r.Address == "" || !r.Kind.valid() || r.ChangedPaths < 0 {
		return fmt.Errorf(
			"%w: row %d has an empty address, an unknown kind or a negative count", ErrInvalidBlame, i,
		)
	}

	return nil
}

// valid reports whether k is one of the kinds of a resource change.
func (k ResourceChangeKind) valid() bool {
	switch k {
	case ResourceCreated, ResourceUpdated, ResourceReplaced, ResourceDeleted:
		return true
	}

	return false
}

// BlameStore is the persistence port of the blame of workspaces. The blame
// operation compares each finalized state version with the previous
// readable one, in the order of finalizes, and writes the rows with this
// port.
type BlameStore interface {
	// RecordResourceChanges writes the blame b, and moves the blame cursor
	// of the workspace b.Workspace to b.Ordinal, in one transaction with the
	// fence of ns. When the cursor is at or past b.Ordinal, it answers nil
	// and changes nothing, so a repeated run writes no row twice. It returns
	// ErrInvalidBlame for a blame that the store cannot keep, before it
	// writes anything. It returns ErrNotFound when the workspace
	// b.Workspace does not exist in ns, also for a workspace of another
	// namespace, and namespace.ErrFenced when the fence is stale. A workspace
	// in the retention period of a delete exists.
	RecordResourceChanges(ctx context.Context, ns namespace.Name, b ItemBlame, fence namespace.Fence) error
	// BlameCursor returns the last ordinal that the blame operation compared
	// for the workspace id, or 0 when it compared none.
	BlameCursor(ctx context.Context, id ID) (int64, error)
	// ResourceHistory returns at most limit rows of the resource instance
	// address of the workspace id, newest first by ordinal, with an ordinal
	// below before (0 for the first page).
	ResourceHistory(ctx context.Context, id ID, address string, before int64, limit int) ([]ResourceChange, error)
	// ItemChanges returns the blame of the state version sv of the
	// workspace id, with its rows ordered by address. A state version that
	// the blame operation did not compare reads as an empty blame with the
	// workspace and the state version set and the ordinal 0.
	ItemChanges(ctx context.Context, id ID, sv StateVersionID) (ItemBlame, error)
}

// StateAt names the state version that was current in a workspace at a
// time: the one with the latest finalize time at or before the time. By is
// the holder of the workspace lock when the client declared the state
// version, as the state version lists it.
type StateAt struct {
	FinalizedAt  time.Time
	By           string
	StateVersion StateVersionID
}

// StateAtReader finds the state of a workspace at a time.
type StateAtReader interface {
	// WorkspaceStateAt returns the state version that was current in the
	// workspace id at the time at. The method name differs from the name
	// of the same read in the other domain packages, so that one store can
	// implement all of them. It returns ErrNoStateAt when no state version
	// finalized at or before at, and ErrNotFound for an unknown workspace.
	// A finalize stamps a time after every earlier finalize of the
	// workspace (see StateStore.FinalizeStateVersion and FenceAndUnlock), so
	// the version that became current last has the latest finalize time,
	// and a read at a time after it returns the current state version.
	// Versions with the same finalize time, which only rows that the stamp
	// did not write can have, break the tie by ID.
	// The ErrNoStateAt error is a *NoStateAtError, and its First holds the
	// finalize time of the first state version, or the zero time when the
	// workspace has none. A pending state version does not count. The result
	// is the zero StateAt with each error. A soft-deleted workspace answers
	// as before its delete.
	WorkspaceStateAt(ctx context.Context, id ID, at time.Time) (StateAt, error)
}
