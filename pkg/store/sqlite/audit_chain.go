package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
)

var _ audit.ChainSwitch = (*Store)(nil)

// SetChain implements audit.ChainSwitch for later appends of this handle.
func (s *Store) SetChain(on bool) {
	s.chainOff.Store(!on)
}

// HasChainedEntries implements audit.ChainSwitch.
func (s *Store) HasChainedEntries(ctx context.Context) (bool, error) {
	found, err := s.queries.HasChainedAuditEntries(ctx)
	if err != nil {
		return false, fmt.Errorf("read chained audit entries: %w", err)
	}

	return found, nil
}

// ChainState implements audit.ChainSwitch. A missing installation row reads
// as no record.
func (s *Store) ChainState(ctx context.Context) (audit.ChainState, error) {
	state, err := s.queries.GetAuditChainState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.ChainUnknown, nil
	}

	if err != nil {
		return audit.ChainUnknown, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainState(state), nil
}

// SaveChainState implements audit.ChainSwitch.
func (s *Store) SaveChainState(ctx context.Context, state audit.ChainState) error {
	if err := s.queries.SetAuditChainState(ctx, string(state)); err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
}
