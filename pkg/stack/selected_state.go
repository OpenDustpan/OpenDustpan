package stack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// latestStateName names the latest checkpoint of a stack in a selector.
const latestStateName = "latest"

// StateSelector names one checkpoint of a stack: the latest one, or the one
// that the update of Version saved.
type StateSelector struct {
	Latest  bool
	Version int64
}

// ParseStateSelector parses "latest" or a version: a number of 0 or more. Any
// other value fails with ErrInvalidUpdate.
func ParseStateSelector(raw string) (StateSelector, error) {
	if raw == latestStateName {
		return StateSelector{Latest: true, Version: 0}, nil
	}

	version, err := ParseStackVersion(raw)
	if err != nil {
		return StateSelector{Latest: false, Version: 0}, err
	}

	return StateSelector{Latest: false, Version: version}, nil
}

// SelectedState is the checkpoint that a selector names, with the version of
// the update that saved it.
type SelectedState struct {
	Checkpoint Checkpoint
	Version    int64
}

// VersionLabel returns the version of the state as text, for an audit entry.
func (st SelectedState) VersionLabel() string {
	return strconv.FormatInt(st.Version, 10)
}

// SelectCheckpoint returns the checkpoint of ref that sel names. It reads
// the checkpoint only, so a read that needs no version does not fail when the
// update that saved the checkpoint cannot be read. It fails with ErrNoState
// when the version saved no checkpoint, and with the errors of Export and
// ExportVersion.
func (s *Updates) SelectCheckpoint(ctx context.Context, ref Ref, sel StateSelector) (Checkpoint, error) {
	if sel.Latest {
		return s.Export(ctx, ref)
	}

	return s.ExportVersion(ctx, ref, sel.Version)
}

// SelectState returns the checkpoint of ref that sel names, with its
// version. For the latest checkpoint, the version is the version of the
// update that saved it, so SelectState also reads that update and fails when
// the read fails. It fails with ErrNoState when the version saved no
// checkpoint, and with the errors of Export, ExportVersion and Get.
func (s *Updates) SelectState(ctx context.Context, ref Ref, sel StateSelector) (SelectedState, error) {
	var none SelectedState

	cp, err := s.SelectCheckpoint(ctx, ref, sel)
	if err != nil {
		return none, err
	}

	if !sel.Latest {
		return SelectedState{Checkpoint: cp, Version: sel.Version}, nil
	}

	u, err := s.Get(ctx, ref, cp.Update)
	if err != nil {
		return none, fmt.Errorf("version of the latest state of %s: %w", ref, err)
	}

	return SelectedState{Checkpoint: cp, Version: u.Version}, nil
}

// PreviousDeployment returns the deployment of the version before st, or nil
// when there is none: st is the first version, or the version before it
// saved no checkpoint. It returns the other errors of ExportVersion.
func (s *Updates) PreviousDeployment(ctx context.Context, ref Ref, st SelectedState) ([]byte, error) {
	if st.Version <= 1 {
		return nil, nil
	}

	prev, err := s.ExportVersion(ctx, ref, st.Version-1)
	if errors.Is(err, ErrNoState) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return prev.Deployment, nil
}

// CheckServiceSecrets returns ErrOtherSecretsProvider when deployment names a
// secrets provider other than the service provider, the only provider whose
// values the server can decrypt.
func CheckServiceSecrets(deployment []byte) error {
	if provider := SecretsProvider(deployment); provider != serviceSecretsProvider {
		return fmt.Errorf("%w: %q", ErrOtherSecretsProvider, provider)
	}

	return nil
}
