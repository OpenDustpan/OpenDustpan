package backup_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreCancel proves the answers of RestoreService.Cancel: before the
// swap it stops the restore, the row ends failed with the cancel text, and
// nothing is applied; a cancel before the enter of maintenance mode fences no
// namespace; a second cancel is accepted again; after the swap, after the end
// and for an unknown ID it refuses.
func TestRestoreCancel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, setup *restoreServiceSetup)
	}{
		{name: "before the swap", run: cancelBeforeSwap},
		{name: "after the swap", run: cancelAfterSwap},
		{name: "after the end", run: cancelAfterEnd},
		{name: "unknown restore", run: cancelUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, newRestoreServiceSetup(t))
		})
	}
}

func cancelBeforeSwap(t *testing.T, setup *restoreServiceSetup) {
	t.Helper()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := setup.service.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed || row.Error != backup.RestoreCanceledText {
		t.Fatalf("restore = %q (%s), want failed with the cancel text", row.State, row.Error)
	}

	// The cancel came before the enter of maintenance mode: the restore
	// changed nothing, so no namespace is fenced.
	if got := setup.live.counts(); got.applied != 0 || got.bumped != 0 {
		t.Fatalf("applied = %d, bumped = %d, want no apply and no fence", got.applied, got.bumped)
	}

	if err := setup.service.Cancel(t.Context(), id); err != nil {
		t.Fatalf("second cancel: %v, want it accepted again", err)
	}
}

func cancelAfterSwap(t *testing.T, setup *restoreServiceSetup) {
	t.Helper()

	reached := make(chan struct{})
	release := make(chan struct{})

	service := restoreServiceWithHook(t, setup, func(ctx context.Context) error {
		close(reached)

		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	setup.service = service

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	ran := make(chan struct{})

	go func() {
		defer close(ran)

		// One run takes every step up to the hook of the after-apply step.
		setup.regs.runWorker(t.Context())
	}()

	<-reached

	cancelErr := service.Cancel(t.Context(), id)

	close(release)
	<-ran

	if !errors.Is(cancelErr, backup.ErrRestoreSwapped) {
		t.Fatalf("cancel after the swap: err = %v, want ErrRestoreSwapped", cancelErr)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore = %q (%s), want succeeded", row.State, row.Error)
	}
}

func cancelAfterEnd(t *testing.T, setup *restoreServiceSetup) {
	t.Helper()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if row := waitRestoreEnd(t, setup.regs, id); row.State != backup.RestoreSucceeded {
		t.Fatalf("restore = %q (%s), want succeeded", row.State, row.Error)
	}

	if err := setup.service.Cancel(t.Context(), id); !errors.Is(err, backup.ErrRestoreNotRunning) {
		t.Fatalf("cancel after the end: err = %v, want ErrRestoreNotRunning", err)
	}
}

func cancelUnknown(t *testing.T, setup *restoreServiceSetup) {
	t.Helper()

	err := setup.service.Cancel(t.Context(), backup.MintRestoreID())
	if !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("cancel of an unknown restore: err = %v, want ErrRestoreNotFound", err)
	}
}

// errBelowMinimum is the answer of a store whose binary is below the minimum
// of the live schema.
var errBelowMinimum = errors.New("binary version is below the minimum of the database schema")

// TestRestoreCheckRefusesABinaryBelowTheMinimum proves that the check step of
// the worker refuses a binary below the minimum binary version of the live
// schema: the restore fails and applies nothing.
func TestRestoreCheckRefusesABinaryBelowTheMinimum(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)

	// Set the answer before the start: the worker can run the check step
	// before the next line of the test.
	setup.live.mu.Lock()
	setup.live.minErr = errBelowMinimum
	setup.live.mu.Unlock()

	id, err := startRestoreOf(t, setup, plainRestore())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreFailed {
		t.Fatalf("restore = %q (%s), want failed", row.State, row.Error)
	}

	if got := setup.live.counts(); got.applied != 0 {
		t.Fatalf("applied = %d, want no apply on a binary below the minimum", got.applied)
	}
}
