package workspace

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrNoStateAt reports a time before the first finalized state version of
// a workspace.
var ErrNoStateAt = errors.New("workspace has no state at that time")

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
// forced write. At is the finalize time. By is the actor of the state
// version.
type ResourceChange struct {
	At           time.Time
	Workspace    ID
	StateVersion StateVersionID
	Address      string
	Kind         ResourceChangeKind
	By           history.Actor
	Ordinal      int64
	ChangedPaths int
	Restored     bool
	Forced       bool
}

// ItemBlame is the blame of the state version StateVersion of the
// workspace Workspace, whose place in the order of finalizes is Ordinal.
// Opaque is true when the client encrypted the state, so the server could
// not read it and wrote no row. The next readable state version compares
// with the last readable one before it.
type ItemBlame struct {
	Workspace    ID
	StateVersion StateVersionID
	Changes      []ResourceChange
	Ordinal      int64
	Opaque       bool
}

// BlameStore is the persistence port of the blame of workspaces. The blame
// operation compares each finalized state version with the previous
// readable one, in the order of finalizes, and writes the rows with this
// port.
type BlameStore interface {
	// RecordResourceChanges writes the blame b, and moves the blame cursor
	// of the workspace b.Workspace to b.Ordinal, in one transaction with the
	// fence of ns. When the cursor is at or past b.Ordinal, it answers nil
	// and changes nothing, so a repeated run writes no row twice. It does not
	// check that the workspace exists: the blame operation reads it first.
	// It returns namespace.ErrFenced when the fence is stale.
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
// time: the one with the latest finalize time at or before the time.
type StateAt struct {
	FinalizedAt  time.Time
	StateVersion StateVersionID
}

// StateAtReader finds the state of a workspace at a time.
type StateAtReader interface {
	// StateAt returns the state version that was current in the workspace
	// id at the time at. It returns ErrNoStateAt when no state version
	// finalized at or before at, and ErrNotFound for an unknown workspace. A
	// soft-deleted workspace answers as before its delete.
	StateAt(ctx context.Context, id ID, at time.Time) (StateAt, error)
}
