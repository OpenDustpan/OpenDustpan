package dustpan

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// workspaceOutputs reads the root outputs of the current state version of an
// OpenTofu workspace.
type workspaceOutputs struct {
	workspaces *workspace.Service
}

var _ outputsharesvc.Source = workspaceOutputs{workspaces: nil}

// Outputs implements outputsharesvc.Source. The workspace is found by the
// stable ID of u. The outputs of a state that the client encrypted are empty,
// because the server stores none for it.
func (s workspaceOutputs) Outputs(
	ctx context.Context,
	u identity.Object,
	stateID string,
) ([]outputsharesvc.Output, error) {
	sv, err := s.workspaces.Current(ctx, workspace.ID(u.UnitID()))
	if err != nil {
		return nil, fmt.Errorf("read the current state of %q: %w", u, err)
	}

	if sv.ID.String() != stateID {
		return nil, outputsharesvc.ErrStateMoved
	}

	if len(sv.Outputs) == 0 {
		return []outputsharesvc.Output{}, nil
	}

	stored, err := workspace.ParseStoredOutputs(sv.Outputs)
	if err != nil {
		return nil, fmt.Errorf("decode the outputs of state %s: %w", sv.ID, err)
	}

	out := make([]outputsharesvc.Output, 0, len(stored))

	for _, name := range slices.Sorted(maps.Keys(stored)) {
		entry := stored[name]
		output := outputsharesvc.Output{Name: name, Secret: entry.Sensitive, Value: nil}

		if !entry.Sensitive {
			output.Value = entry.Value
		}

		out = append(out, output)
	}

	return out, nil
}

// stackOutputs reads the root outputs of the latest checkpoint of a Pulumi
// stack. The checkpoint holds a secret output as ciphertext, and the read
// answers it as secret with no value: the server decrypts nothing here.
type stackOutputs struct {
	stacks  *stack.Service
	updates *stack.Updates
}

var _ outputsharesvc.Source = stackOutputs{stacks: nil, updates: nil}

// Outputs implements outputsharesvc.Source. The stack is found by its
// reference, and its stable ID must be the one of u.
func (s stackOutputs) Outputs(ctx context.Context, u identity.Object, stateID string) ([]outputsharesvc.Output, error) {
	project, name, found := strings.Cut(u.UnitName(), "/")
	if !found {
		return nil, fmt.Errorf("%w: %q holds no project", outputshare.ErrInvalid, u)
	}

	ref, err := stack.ParseRef(u.Namespace().String(), project, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", outputshare.ErrInvalid, err)
	}

	st, err := s.stacks.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read stack %q: %w", u, err)
	}

	if st.ID.String() != u.UnitID() {
		return nil, fmt.Errorf("%w: stack %q", outputshare.ErrUnknownProducer, u)
	}

	cp, err := s.updates.Export(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("read the latest checkpoint of %q: %w", u, err)
	}

	update, err := s.updates.Get(ctx, ref, cp.Update)
	if err != nil {
		return nil, fmt.Errorf("read the update of the latest checkpoint of %q: %w", u, err)
	}

	if stack.StateID(st.ID, update.Version) != stateID {
		return nil, outputsharesvc.ErrStateMoved
	}

	return stackRootOutputs(cp.Deployment)
}

// stackRootOutputs returns the root outputs of the deployment of a checkpoint.
// A secret output has no value. The masked read of the stack package marks a
// secret as an object with a secret member, and the read keeps the marker of a
// secret inside an object or a list as it is.
func stackRootOutputs(deployment []byte) ([]outputsharesvc.Output, error) {
	masked, err := stack.ReadStateOutputs(deployment)
	if err != nil {
		return nil, fmt.Errorf("read the outputs of the checkpoint: %w", err)
	}

	root, ok := masked.Outputs.(map[string]any)
	if !ok {
		return []outputsharesvc.Output{}, nil
	}

	out := make([]outputsharesvc.Output, 0, len(root))

	for _, name := range slices.Sorted(maps.Keys(root)) {
		output, err := stackOutput(name, root[name])
		if err != nil {
			return nil, err
		}

		out = append(out, output)
	}

	return out, nil
}

// stackOutput returns one root output of a stack from its masked value. A
// value that the masked read replaced by the secret marker is a secret output
// with no value. A secret inside an object or a list stays a marker in the
// value, so the answer holds no ciphertext either.
func stackOutput(name string, masked any) (outputsharesvc.Output, error) {
	if marker, ok := masked.(map[string]any); ok && len(marker) == secretMarkerFields {
		_, hasPath := marker["path"].(string)
		if secret, isBool := marker["secret"].(bool); isBool && secret && hasPath {
			return outputsharesvc.Output{Name: name, Secret: true, Value: nil}, nil
		}
	}

	raw, err := json.Marshal(masked)
	if err != nil {
		return outputsharesvc.Output{}, fmt.Errorf("encode output %q: %w", name, err)
	}

	return outputsharesvc.Output{Name: name, Secret: false, Value: raw}, nil
}

// secretMarkerFields is the number of members of the marker that the masked
// read puts in place of a secret value: secret and path.
const secretMarkerFields = 2
