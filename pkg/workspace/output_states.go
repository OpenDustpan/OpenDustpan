package workspace

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// OutputStates serves outputshare.States for the workspaces of OpenTofu. The
// ID of the latest state is the ID of the current state version.
type OutputStates struct {
	svc *Service
}

var _ outputshare.States = (*OutputStates)(nil)

// NewOutputStates returns the States port over the workspaces of svc.
func NewOutputStates(svc *Service) *OutputStates {
	return &OutputStates{svc: svc}
}

// Latest implements outputshare.States. It looks the workspace up by its
// reference, and then compares the stable ID. A reference with no stable ID
// is an unknown producer, after the same lookup as a known one. A workspace
// with no state answers the zero State.
func (s *OutputStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	if !u.IsUnit() {
		return outputshare.State{}, fmt.Errorf("%w: %q is not a unit", outputshare.ErrInvalid, u)
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
		return outputshare.State{}, fmt.Errorf("%w: workspace %q", outputshare.ErrUnknownProducer, u)
	case getErr != nil:
		return outputshare.State{}, fmt.Errorf("read workspace %q: %w", u, getErr)
	case u.UnitID() != ws.ID.String():
		return outputshare.State{}, fmt.Errorf("%w: workspace %q", outputshare.ErrUnknownProducer, u)
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
