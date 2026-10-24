package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
)

// InventoryTool is the tool code of OpenTofu in the inventory and its change
// log.
const InventoryTool = "opentofu"

// StateAtRead is the masked state of a workspace at a time. State names the
// state version and its actor, and Version is that state version. Opaque is true when the client encrypted the
// state: the server could not read it, so Page is empty. Page holds one page
// of the masked resource list, as ReadResources returns it. Names holds the
// name of the workspace at the time and its name now, from the inventory
// change log. A name is empty when the log does not know it, for example for
// a workspace that the server created before it kept the log.
type StateAtRead struct {
	Page    StatePage
	Version StateVersion
	State   StateAt
	Names   inventory.Names
	Opaque  bool
}

// StateAtRequest asks for the masked state of a workspace at the time At.
// Cursor, Size and Filter work as in ReadResources.
type StateAtRequest struct {
	At     time.Time
	Cursor string
	Filter string
	Size   int
}

// ReadStateAt returns the masked state that the workspace id held at the
// time at: the finalized state version with the latest finalize time at or
// before at. A time before the first finalized state version fails with
// ErrNoStateAt, and its text names the finalize time of the first state
// version. A pending state version does not count. A state the client
// encrypted answers as Opaque, with no resource.
func (s *Service) ReadStateAt(ctx context.Context, id ID, req StateAtRequest) (StateAtRead, error) {
	if s.stateAt == nil {
		return StateAtRead{}, fmt.Errorf("%w: state at a time", ErrMissingDependency)
	}

	// The four reads below (workspace, state version at the time, state
	// version, resources) are not one snapshot. A purge between them answers
	// 404 for the state version that the second read found.
	w, err := s.GetByID(ctx, id)
	if err != nil {
		return StateAtRead{}, err
	}

	found, err := s.stateAt.WorkspaceStateAt(ctx, id, req.At)

	// The error wraps the NoStateAtError, so that an adapter reads the time of
	// the first state version from its field and never from the text.
	if none, ok := errors.AsType[*NoStateAtError](err); ok {
		if none.First.IsZero() {
			return StateAtRead{}, fmt.Errorf("%w: %s has no state version yet", none, w.Ref)
		}

		return StateAtRead{}, fmt.Errorf(
			"%w: the first state version of %s is at %s (%d ms)",
			none, w.Ref, none.First.UTC().Format(time.RFC3339), none.First.UnixMilli(),
		)
	}

	if err != nil {
		return StateAtRead{}, fmt.Errorf("state of %s at %s: %w", w.Ref, req.At.UTC().Format(time.RFC3339), err)
	}

	sv, err := s.ReadState(ctx, found.StateVersion)
	if err != nil {
		return StateAtRead{}, err
	}

	out := StateAtRead{
		Page:    StatePage{Resources: nil, NextCursor: "", Incomplete: false},
		Version: sv,
		State:   found,
		Names:   inventory.Names{AtTime: "", Current: ""},
		Opaque:  false,
	}

	if s.names != nil {
		out.Names, err = s.names.UnitNames(ctx, w.Ref.Namespace, InventoryTool, id.String(), req.At)
		if err != nil {
			return StateAtRead{}, fmt.Errorf("names of %s at %s: %w", w.Ref, req.At.UTC().Format(time.RFC3339), err)
		}
	}

	out.Page, err = s.ReadResources(ctx, found.StateVersion, req.Cursor, req.Size, req.Filter)
	if errors.Is(err, ErrClientEncrypted) {
		out.Page = StatePage{Resources: nil, NextCursor: "", Incomplete: false}
		out.Opaque = true

		return out, nil
	}

	if err != nil {
		return StateAtRead{}, err
	}

	return out, nil
}
