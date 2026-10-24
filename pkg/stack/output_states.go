package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
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
// names the stack too. The reads of the checkpoint go by the name of the stack,
// so OutputStates looks the stack up again after them.
func StateID(id ID, version int64) string {
	return id.String() + ":" + strconv.FormatInt(version, 10)
}

// OutputStates serves outputshare.States and outputshare.Outputs for the
// stacks of the Pulumi CLI. The ID of the latest state is StateID of the stack
// and of the version of the latest checkpoint. The Pulumi CLI never encrypts
// the state on the client, so a stack is never client-encrypted.
type OutputStates struct {
	stacks  *Service
	updates *Updates
}

var (
	_ outputshare.States  = (*OutputStates)(nil)
	_ outputshare.Outputs = (*OutputStates)(nil)
)

// NewOutputStates returns the States and Outputs ports over the stacks of
// stacks and the checkpoints of updates.
func NewOutputStates(stacks *Service, updates *Updates) *OutputStates {
	return &OutputStates{stacks: stacks, updates: updates}
}

// Latest implements outputshare.States. It looks the stack up by its
// reference, and then compares the stable ID. It looks the stack up again
// after the read of the checkpoint, because the read goes by name. A reference with no stable ID
// is an unknown producer, after the same lookup as a known one. A stack with
// no state answers the zero State.
func (s *OutputStates) Latest(ctx context.Context, u identity.Object) (outputshare.State, error) {
	ref, st, err := s.lookup(ctx, u)
	if err != nil {
		return outputshare.State{}, err
	}

	version, err := s.updates.LatestStateVersion(ctx, ref)
	noState := errors.Is(err, ErrNoState)

	if err != nil && !noState {
		return outputshare.State{}, fmt.Errorf("read the latest checkpoint of %q: %w", u, err)
	}

	if recheckErr := s.recheck(ctx, u); recheckErr != nil {
		return outputshare.State{}, recheckErr
	}

	if noState {
		return outputshare.State{ID: "", ClientEncrypted: false}, nil
	}

	return outputshare.State{ID: StateID(st.ID, version), ClientEncrypted: false}, nil
}

// Outputs implements outputshare.Outputs. It looks the stack up as Latest
// does, before the reads and after them. It reads the blob of the checkpoint once, for the version that
// stateID names, so a checkpoint that a new update saves in between is not
// mixed in. The checkpoint holds a secret output as ciphertext, and the read
// answers it as secret with no value: the server decrypts nothing here.
func (s *OutputStates) Outputs(
	ctx context.Context, u identity.Object, stateID string,
) ([]outputshare.Output, error) {
	ref, st, err := s.lookup(ctx, u)
	if err != nil {
		return nil, err
	}

	version, err := s.updates.LatestStateVersion(ctx, ref)
	if err != nil {
		return nil, s.gone(ctx, u, fmt.Errorf("read the latest checkpoint of %q: %w", u, err))
	}

	if StateID(st.ID, version) != stateID {
		return nil, fmt.Errorf("%w: stack %q", outputshare.ErrStateMoved, u)
	}

	cp, err := s.updates.ExportVersion(ctx, ref, version)
	if err != nil {
		return nil, s.gone(ctx, u, fmt.Errorf("read the checkpoint of %q: %w", u, err))
	}

	if recheckErr := s.recheck(ctx, u); recheckErr != nil {
		return nil, recheckErr
	}

	return rootOutputs(cp.Deployment)
}

// recheck looks the stack of u up again, after a read of its checkpoint by
// name. A delete and a create of the same name between the lookup and the read
// leave a stack with another stable ID, and the read then answered the
// checkpoint of that stack. The second lookup gives ErrUnknownProducer in that
// case, as the first lookup does for a stable ID of another stack. Two lookups
// cannot detect a delete and a restore of the same stable ID inside one read,
// because both lookups then see the same ID. Only a read keyed by the stable ID
// would close that case.
func (s *OutputStates) recheck(ctx context.Context, u identity.Object) error {
	_, _, err := s.lookup(ctx, u)

	return err
}

// lookup returns the reference and the stack of the unit u. It looks the
// stack up by its reference, and then compares the stable ID. A reference
// that no stack can have, an unknown stack and a stable ID of another stack
// give ErrUnknownProducer, after the same lookup as a known stack.
func (s *OutputStates) lookup(ctx context.Context, u identity.Object) (Ref, Stack, error) {
	if !u.IsUnit() {
		return Ref{}, Stack{}, fmt.Errorf("%w: %q is not a unit", outputshare.ErrInvalid, u)
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
		return Ref{}, Stack{}, fmt.Errorf("%w: stack %q", outputshare.ErrUnknownProducer, u)
	case getErr != nil:
		return Ref{}, Stack{}, fmt.Errorf("read stack %q: %w", u, getErr)
	case u.UnitID() != st.ID.String():
		return Ref{}, Stack{}, fmt.Errorf("%w: stack %q", outputshare.ErrUnknownProducer, u)
	}

	return ref, st, nil
}

// gone returns the error of a read of the checkpoint of u that failed with
// err, after the lookup of u succeeded. The stores read a missing stack as no
// state, so a stack with no state and a stack that a delete removed give the
// same error here. Another lookup tells them apart: a stack that is gone is an
// unknown producer, and a stack that still exists has a state that moved. Any
// other error stays as it is.
func (s *OutputStates) gone(ctx context.Context, u identity.Object, err error) error {
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNoState) && !errors.Is(err, ErrUpdateNotFound) {
		return err
	}

	if _, _, lookupErr := s.lookup(ctx, u); lookupErr != nil {
		return lookupErr
	}

	return fmt.Errorf("%w: stack %q has no state", outputshare.ErrStateMoved, u)
}

// rootOutputs returns the root outputs of the deployment of a checkpoint,
// ordered by name. A secret output has no value. The masked read marks a
// secret as an object with a secret member and a path member, and the read
// keeps the marker of a secret inside an object or a list as it is.
func rootOutputs(deployment []byte) ([]outputshare.Output, error) {
	masked, err := ReadStateOutputs(deployment)
	if err != nil {
		return nil, fmt.Errorf("read the outputs of the checkpoint: %w", err)
	}

	root, ok := masked.Outputs.(map[string]any)
	if !ok {
		return []outputshare.Output{}, nil
	}

	out := make([]outputshare.Output, 0, len(root))

	for _, name := range slices.Sorted(maps.Keys(root)) {
		output, err := rootOutput(name, root[name])
		if err != nil {
			return nil, err
		}

		out = append(out, output)
	}

	return out, nil
}

// rootOutput returns one root output of a stack from its masked value. A
// value that the masked read replaced by the secret marker is a secret output
// with no value. A secret inside an object or a list stays a marker in the
// value, so the answer holds no ciphertext either.
func rootOutput(name string, masked any) (outputshare.Output, error) {
	if marker, ok := masked.(map[string]any); ok && len(marker) == secretMarkerFields {
		_, hasPath := marker["path"].(string)
		if secret, isBool := marker["secret"].(bool); isBool && secret && hasPath {
			return outputshare.Output{Name: name, Secret: true, Value: nil}, nil
		}
	}

	raw, err := json.Marshal(masked)
	if err != nil {
		return outputshare.Output{}, fmt.Errorf("encode output %q: %w", name, err)
	}

	return outputshare.Output{Name: name, Secret: false, Value: raw}, nil
}

// secretMarkerFields is the number of members of the marker that the masked
// read puts in place of a secret value: secret and path.
const secretMarkerFields = 2
