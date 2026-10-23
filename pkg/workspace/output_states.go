package workspace

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// OutputStates serves outputshare.States and outputshare.Outputs for the
// workspaces of OpenTofu. The ID of the latest state is the ID of the current
// state version.
type OutputStates struct {
	svc *Service
}

var (
	_ outputshare.States  = (*OutputStates)(nil)
	_ outputshare.Outputs = (*OutputStates)(nil)
)

// NewOutputStates returns the States and Outputs ports over the workspaces of
// svc.
func NewOutputStates(svc *Service) *OutputStates {
	return &OutputStates{svc: svc}
}

// Latest implements outputshare.States. It looks the workspace up by its
// reference, and then compares the stable ID. A reference with no stable ID
// is an unknown producer, after the same lookup as a known one. A workspace
// with no state answers the zero State.
func (s *OutputStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	ws, err := s.lookup(ctx, u)
	if err != nil {
		return outputshare.State{}, err
	}

	sv, err := s.svc.Current(ctx, ws.ID)
	if errors.Is(err, ErrNoState) {
		return outputshare.State{ID: "", ClientEncrypted: false}, nil
	}

	if err != nil {
		return outputshare.State{}, fmt.Errorf("read the current state of %q: %w", u, err)
	}

	encrypted, err := s.svc.StateClientEncrypted(ctx, sv.ID)
	if err != nil {
		return outputshare.State{}, fmt.Errorf("read the state marker of %q: %w", u, err)
	}

	return outputshare.State{ID: sv.ID.String(), ClientEncrypted: encrypted}, nil
}

// Outputs implements outputshare.Outputs. It looks the workspace up as Latest
// does, and reads the outputs that the current state version stores. A
// sensitive output has no value. The server stores no output for a state that
// the client encrypted, so such a state answers an empty list.
func (s *OutputStates) Outputs(
	ctx context.Context, u identity.Object, stateID string,
) ([]outputshare.Output, error) {
	ws, err := s.lookup(ctx, u)
	if err != nil {
		return nil, err
	}

	sv, err := s.svc.Current(ctx, ws.ID)

	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoState):
		return nil, s.gone(ctx, u)
	case err != nil:
		return nil, fmt.Errorf("read the current state of %q: %w", u, err)
	case sv.ID.String() != stateID:
		return nil, fmt.Errorf("%w: workspace %q", outputshare.ErrStateMoved, u)
	case len(sv.Outputs) == 0:
		return []outputshare.Output{}, nil
	}

	stored, err := ParseStoredOutputs(sv.Outputs)
	if err != nil {
		return nil, fmt.Errorf("decode the outputs of state %s: %w", sv.ID, err)
	}

	out := make([]outputshare.Output, 0, len(stored))

	for _, name := range slices.Sorted(maps.Keys(stored)) {
		entry := stored[name]
		output := outputshare.Output{Name: name, Secret: entry.Sensitive, Value: nil}

		if !entry.Sensitive {
			output.Value = entry.Value
		}

		out = append(out, output)
	}

	return out, nil
}

// gone returns the error of a read of the current state of u that found no
// state, after the lookup of u succeeded. The store reads a missing workspace
// as no state, so a workspace with no state and a workspace that a delete
// removed give the same error there. Another lookup tells them apart: a
// workspace that is gone is an unknown producer, and a workspace that still
// exists has a state that moved.
func (s *OutputStates) gone(ctx context.Context, u identity.Object) error {
	if _, err := s.lookup(ctx, u); err != nil {
		return err
	}

	return fmt.Errorf("%w: workspace %q has no state", outputshare.ErrStateMoved, u)
}

// lookup returns the workspace of the unit u. It looks the workspace up by
// its reference, and then compares the stable ID. A reference that no
// workspace can have, an unknown workspace and a stable ID of another
// workspace give ErrUnknownProducer, after the same lookup as a known
// workspace.
func (s *OutputStates) lookup(ctx context.Context, u identity.Object) (Workspace, error) {
	if !u.IsUnit() {
		return Workspace{}, fmt.Errorf("%w: %q is not a unit", outputshare.ErrInvalid, u)
	}

	name, nameErr := ParseName(u.UnitName())

	var (
		ws     Workspace
		getErr error
	)

	if nameErr == nil {
		ws, getErr = s.svc.Get(ctx, Ref{Namespace: u.Namespace(), Name: name})
	}

	switch {
	case nameErr != nil, errors.Is(getErr, ErrNotFound):
		return Workspace{}, fmt.Errorf("%w: workspace %q", outputshare.ErrUnknownProducer, u)
	case getErr != nil:
		return Workspace{}, fmt.Errorf("read workspace %q: %w", u, getErr)
	case u.UnitID() != ws.ID.String():
		return Workspace{}, fmt.Errorf("%w: workspace %q", outputshare.ErrUnknownProducer, u)
	}

	return ws, nil
}
