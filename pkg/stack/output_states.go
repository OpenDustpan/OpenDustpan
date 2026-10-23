package stack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// errNoProject is the cause of a unit name that holds no project.
var errNoProject = errors.New("stack name holds no project")

// StateID returns the ID that outputshare.State.ID holds for the checkpoint
// of the stack id at version: the stable ID of the stack, a colon, and the
// version in decimal. The version alone would repeat in every stack, so the ID
// names the stack too, and the ID of a checkpoint of another stack never
// matches.
func StateID(id ID, version int64) string {
	return id.String() + ":" + strconv.FormatInt(version, 10)
}

// OutputStates serves outputshare.States for the stacks of the Pulumi CLI.
// The ID of the latest state is StateID of the stack and of the version of
// the latest checkpoint. The Pulumi CLI never encrypts the state on the
// client, so a stack is never client-encrypted.
type OutputStates struct {
	stacks  *Service
	updates *Updates
}

var _ outputshare.States = (*OutputStates)(nil)

// NewOutputStates returns the States port over the stacks of stacks and the
// checkpoints of updates.
func NewOutputStates(stacks *Service, updates *Updates) *OutputStates {
	return &OutputStates{stacks: stacks, updates: updates}
}

// Latest implements outputshare.States. It looks the stack up by its
// reference, and then compares the stable ID. A reference with no stable ID
// is an unknown producer, after the same lookup as a known one. A stack with
// no state answers the zero State.
func (s *OutputStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	if !u.IsUnit() {
		return outputshare.State{}, fmt.Errorf("%w: %q is not a unit", outputshare.ErrInvalid, u)
	}

	var (
		ref    Ref
		st     Stack
		getErr error
	)

	project, name, found := strings.Cut(u.UnitName(), "/")

	refErr := errNoProject
	if found {
		ref, refErr = ParseRef(u.Namespace().String(), project, name)
	}

	if refErr == nil {
		st, getErr = s.stacks.Get(ctx, ref)
	}

	switch {
	case refErr != nil, errors.Is(getErr, ErrNotFound):
		return outputshare.State{}, fmt.Errorf("%w: stack %q", outputshare.ErrUnknownProducer, u)
	case getErr != nil:
		return outputshare.State{}, fmt.Errorf("read stack %q: %w", u, getErr)
	case u.UnitID() != st.ID.String():
		return outputshare.State{}, fmt.Errorf("%w: stack %q", outputshare.ErrUnknownProducer, u)
	}

	version, err := s.updates.LatestStateVersion(ctx, ref)
	if errors.Is(err, ErrNoState) {
		return outputshare.State{ID: "", ClientEncrypted: false}, nil
	}

	if err != nil {
		return outputshare.State{}, fmt.Errorf("read the latest checkpoint of %q: %w", u, err)
	}

	return outputshare.State{ID: StateID(st.ID, version), ClientEncrypted: false}, nil
}
