package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// RollbackChange is what a restore of an old state version changes in the
// workspace: the current state version that the restore replaces, the count of
// resources in the state that the restore brings back, and the resource
// addresses that the current state holds and the restored state does not
// hold.
type RollbackChange struct {
	// Current is the current state version at the time of the read. A restore
	// that expects it fails with ErrConcurrentModification when another
	// version became current in between.
	Current StateVersionID
	// Resources is the count of resources in the restored state.
	Resources int64
	// Removed holds the addresses that the current state holds and the
	// restored state does not hold, sorted. An address has no instance key.
	Removed []string
}

// ReadRollbackChange reads the change that a restore of the finalized state
// version to makes in the workspace id. It reads both states through Open, the
// same path the download uses. It fails closed: the reads of the two states
// are the only source of the change, so a state that the server cannot read
// gives an error and no partial change. A version of another workspace fails
// with ErrStateVersionNotFound as if it did not exist. A state that the client
// encrypted fails with ErrClientEncrypted. A workspace with no current state
// fails with ErrNoState. A state that holds a resource entry the server cannot
// read fails with ErrInvalidState, because the count would be too low.
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

	nextAddresses, err := stateAddresses(next, to)
	if err != nil {
		return RollbackChange{}, err
	}

	curAddresses, err := stateAddresses(cur, current.ID)
	if err != nil {
		return RollbackChange{}, err
	}

	removed := []string{}

	for _, a := range curAddresses {
		if !slices.Contains(nextAddresses, a) {
			removed = append(removed, a)
		}
	}

	slices.Sort(removed)

	return RollbackChange{Current: current.ID, Resources: int64(len(nextAddresses)), Removed: removed}, nil
}

// stateAddresses returns the resource addresses of a state, without
// duplicates. It fails closed on a state the client encrypted, on a document
// that is not a state, and on a resource entry that the server cannot read.
func stateAddresses(state []byte, id StateVersionID) ([]string, error) {
	if err := rejectEncryptedState(state, id); err != nil {
		return nil, err
	}

	doc, err := decodeState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: state %s: %w", ErrInvalidState, id, err)
	}

	entries, skipped := tofuStateResources(doc)
	if skipped {
		return nil, fmt.Errorf("%w: state %s holds a resource entry that the server cannot read", ErrInvalidState, id)
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		a := tofuResourceBase(e.obj)
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}

	return out, nil
}

// errStateChanged is the cause of a restore that expects another current
// version.
var errStateChanged = errors.New("the current state version is not the one that the decision used")

// RestoreExpecting works as Restore, and also checks that the current state
// version is expectCurrent after the lock is held, and before it writes. An
// empty expectCurrent is an error: the caller must name the version that its
// gate decision read. A mismatch fails with ErrConcurrentModification, and the
// service writes nothing. A retried restore answers the version the first call
// made current before it checks, as Restore does.
func (s *Service) RestoreExpecting(
	ctx context.Context, id ID, from StateVersionID, holder string, expectCurrent StateVersionID,
) (StateVersion, error) {
	if expectCurrent == "" {
		return StateVersion{}, fmt.Errorf("%w: restore %s without the expected current version", ErrInvalidState, id)
	}

	return s.restore(ctx, id, from, holder, &expectCurrent)
}
