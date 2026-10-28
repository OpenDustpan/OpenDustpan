package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// runRestoreWorker runs one restore operation that is due, as a worker
// does. A case with no worker calls it while it waits for a restore.
func runRestoreWorker(t *testing.T, restores *backup.RestoreService) {
	t.Helper()

	if err := restores.RunOnce(t.Context()); err != nil && !errors.Is(err, durable.ErrNoWork) &&
		t.Context().Err() == nil {
		t.Logf("run a restore operation: %v", err)
	}
}

// StateMaintenance is a backup.RestoreMaintenance over a maintenance.Store
// with no active work: the maintenance of the restore rigs of the store
// adapters. Each state write goes through the writer of the restore, as the
// domain service does. Hold is the maintenance hold of the server.
type StateMaintenance struct {
	State maintenance.Store
	Hold  bool
}

// Get implements backup.RestoreMaintenance.
func (m StateMaintenance) Get(ctx context.Context) (maintenance.State, error) {
	state, err := m.State.GetState(ctx)
	if err != nil {
		return "", fmt.Errorf("get the installation state: %w", err)
	}

	return state, nil
}

// EnterWith implements backup.RestoreMaintenance.
func (m StateMaintenance) EnterWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	none := maintenance.EnterResult{Canceled: nil}

	state, err := m.Get(ctx)
	if err != nil || state == maintenance.StateMaintenance {
		return none, err
	}

	if err := opts.Writer.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		return none, fmt.Errorf("enter maintenance mode: %w", err)
	}

	return none, nil
}

// RecheckWith implements backup.RestoreMaintenance: no work is active.
func (StateMaintenance) RecheckWith(ctx context.Context, _ maintenance.EnterOptions) (maintenance.EnterResult, error) {
	if err := ctx.Err(); err != nil {
		return maintenance.EnterResult{Canceled: nil}, fmt.Errorf("list the active work: %w", err)
	}

	return maintenance.EnterResult{Canceled: nil}, nil
}

// LeaveWith implements backup.RestoreMaintenance.
func (m StateMaintenance) LeaveWith(ctx context.Context, w maintenance.StateWriter) error {
	state, err := m.Get(ctx)
	if err != nil || state == maintenance.StateNormal {
		return err
	}

	if m.Hold {
		return maintenance.ErrHeld
	}

	if err := w.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		return fmt.Errorf("leave maintenance mode: %w", err)
	}

	return nil
}

// Held implements backup.RestoreMaintenance.
func (m StateMaintenance) Held(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("read the maintenance hold: %w", err)
	}

	return m.Hold, nil
}
