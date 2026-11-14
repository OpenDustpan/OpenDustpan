package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// MaxRollbackInstances is the largest count of resource instances that the
// read of a rollback accepts in one state. A state above it fails with
// ErrTooManyResources: the read refuses it and never cuts it, because a count
// that the server cut would hide a removal from a gate.
const MaxRollbackInstances = 100_000

// ErrTooManyResources reports a state with more than MaxRollbackInstances
// resource instances.
var ErrTooManyResources = errors.New("the state holds too many resource instances to compare")

// RollbackChange is what a restore of an old state version changes in the
// workspace: the current state version that the restore replaces, the count of
// resource instances in the state that the restore brings back, and the
// instance addresses that the current state holds and the restored state does
// not hold.
type RollbackChange struct {
	// Current is the current state version at the time of the read. A restore
	// that expects it fails with ErrConcurrentModification when another
	// version became current in between.
	Current StateVersionID
	// Resources is the count of resource instances in the restored state. A
	// resource with count or for_each gives one instance for each index key.
	Resources int64
	// Removed holds the instance addresses that the current state holds and
	// the restored state does not hold, sorted. An address has the index key
	// of its instance, for example web[1] or web["a"].
	Removed []string
	// Encrypted is true when the client encrypted the current state or the
	// restored state. The server cannot read the resources of such a state,
	// so Resources and Removed hold no value. This is a known condition and
	// not an error: Current is set, so the restore still checks it.
	Encrypted bool
}

// ReadRollbackChange reads the change that a restore of the finalized state
// version to makes in the workspace id. It reads both states through Open, the
// same path the download uses. It fails closed: the reads of the two states
// are the only source of the change, so a state that the server cannot read
// gives an error and no partial change. A version of another workspace fails
// with ErrStateVersionNotFound as if it did not exist. A state in the
// encryption envelope of the client is a known condition: the read gives
// Encrypted true and no resource data. A document that holds the member
// encrypted_data and is not exactly that envelope fails with ErrInvalidState:
// the read refuses it, so its resources never hide from the gates and never
// read as plaintext. A workspace with no current state fails with ErrNoState.
// A state that holds a resource entry the server cannot read fails with
// ErrInvalidState, because the count would be too low. A state with more than
// MaxRollbackInstances instances fails with ErrTooManyResources.
func (s *Service) ReadRollbackChange(ctx context.Context, id ID, to StateVersionID) (RollbackChange, error) {
	next, nextVersion, err := s.openStateBytes(ctx, to)
	if err != nil {
		return RollbackChange{}, err
	}

	// Defense in depth: the handler and Restore check the workspace of the
	// version too. This check stays so that a caller of this read cannot learn
	// the state of another workspace.
	if nextVersion.Workspace != id {
		return RollbackChange{}, fmt.Errorf("%w: state version %s", ErrStateVersionNotFound, to)
	}

	current, err := s.states.CurrentStateVersion(ctx, id)
	if err != nil {
		return RollbackChange{}, fmt.Errorf("current state of %s: %w", id, err)
	}

	cur, _, err := s.openStateBytes(ctx, current.ID)
	if err != nil {
		return RollbackChange{}, err
	}

	return rollbackChangeOf(next, to, cur, current.ID)
}

// rollbackChangeOf computes the change from the two plain states. It reads no
// store. The time and the memory of the computation grow with the size of the
// states, not with its square, and MaxRollbackInstances bounds both.
func rollbackChangeOf(next []byte, nextID StateVersionID, cur []byte, curID StateVersionID) (RollbackChange, error) {
	nextEnc, err := IsClientEncrypted(next)
	if err != nil {
		return RollbackChange{}, fmt.Errorf("read the restored state %s: %w", nextID, err)
	}

	curEnc, err := IsClientEncrypted(cur)
	if err != nil {
		return RollbackChange{}, fmt.Errorf("read the current state %s: %w", curID, err)
	}

	if nextEnc || curEnc {
		return RollbackChange{Current: curID, Resources: 0, Removed: nil, Encrypted: true}, nil
	}

	nextSet, err := stateInstances(next, nextID)
	if err != nil {
		return RollbackChange{}, err
	}

	curSet, err := stateInstances(cur, curID)
	if err != nil {
		return RollbackChange{}, err
	}

	removed := []string{}

	for a := range curSet {
		if _, kept := nextSet[a]; !kept {
			removed = append(removed, a)
		}
	}

	slices.Sort(removed)

	return RollbackChange{Current: curID, Resources: int64(len(nextSet)), Removed: removed, Encrypted: false}, nil
}

// stateInstances returns the set of the resource instance addresses of a
// plaintext state. It fails closed on a document that is not a state, on a
// resource entry or an instance entry that the server cannot read, and on a
// state with more than MaxRollbackInstances instances.
func stateInstances(state []byte, id StateVersionID) (map[string]struct{}, error) {
	doc, err := decodeState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: state %s: %w", ErrInvalidState, id, err)
	}

	entries, skipped := tofuStateResources(doc)
	if skipped {
		return nil, fmt.Errorf("%w: state %s holds a resource entry that the server cannot read", ErrInvalidState, id)
	}

	out := map[string]struct{}{}

	for _, e := range entries {
		raw, present := e.obj["instances"]
		if !present {
			continue
		}

		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: state %s holds instances that the server cannot read", ErrInvalidState, id)
		}

		for _, elem := range list {
			inst, isObj := elem.(map[string]any)
			if !isObj {
				return nil, fmt.Errorf(
					"%w: state %s holds an instance that the server cannot read",
					ErrInvalidState,
					id,
				)
			}

			out[tofuInstanceAddress(e.obj, inst)] = struct{}{}

			if len(out) > MaxRollbackInstances {
				return nil, fmt.Errorf(
					"%w: state %s has more than %d instances",
					ErrTooManyResources,
					id,
					MaxRollbackInstances,
				)
			}
		}
	}

	return out, nil
}

// RestoreExpecting works as Restore, and also finalizes the restored version
// only if the current state version is expectCurrent. The store checks it in
// the finalize transaction, under the lock of the workspace row, so a write by
// the same holder under the same lock cannot slip in between the read of the
// current version and the finalize. An empty expectCurrent is an error: the
// caller must name the version that its gate decision read. A mismatch fails
// with ErrConcurrentModification, and the service writes nothing. A retried
// restore answers the version the first call made current before it checks, as
// Restore does.
func (s *Service) RestoreExpecting(
	ctx context.Context, id ID, from StateVersionID, holder string, expectCurrent StateVersionID,
) (StateVersion, error) {
	if expectCurrent == "" {
		return StateVersion{}, fmt.Errorf("%w: restore %s without the expected current version", ErrInvalidState, id)
	}

	return s.restoreChecked(ctx, id, from, holder, &expectCurrent)
}
