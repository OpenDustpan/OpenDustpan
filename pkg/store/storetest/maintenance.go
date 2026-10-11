package storetest

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// testMaintenanceState covers the installation state (ADR 38 decision 4): a
// fresh store reads normal, setting maintenance reads back, setting normal
// reads back, and any other value reports ErrInvalidState and changes
// nothing. A closed store reports maintenance.ErrUnavailable without driver
// text, here forced by Close.
func testMaintenanceState(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get installation state = %q, %v, want maintenance without error", got, err)
	}

	if err := s.SetState(ctx, maintenance.State("offline")); !errors.Is(err, maintenance.ErrInvalidState) {
		t.Fatalf("set installation state to offline: err = %v, want ErrInvalidState", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get installation state = %q, %v, want maintenance without error", got, err)
	}

	if err := s.SetState(ctx, maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal: %v", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}
}

// testMaintenanceStateIf covers the conditional write of the installation
// state: it stores next only when the stored state is expected, and reports
// ErrStateChanged and changes nothing otherwise.
func testMaintenanceStateIf(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}

	if err := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance when normal: %v", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get installation state = %q, %v, want maintenance without error", got, err)
	}

	// The expected state no longer matches: the write changes nothing.
	if err := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); !errors.Is(
		err, maintenance.ErrStateChanged,
	) {
		t.Fatalf("set installation state to maintenance when maintenance: err = %v, want ErrStateChanged", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get installation state = %q, %v, want maintenance without error", got, err)
	}
}

// testMaintenanceStateIfRelease covers the way back: a matching same-value
// write succeeds without changing anything, and the conditional write moves
// maintenance back to normal, which is how the non-force rollback works.
func testMaintenanceStateIfRelease(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	if err := s.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance when maintenance with matching expected: %v", err)
	}

	if err := s.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal when maintenance: %v", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}
}

// testMaintenanceStateIfInvalid covers the conditional write with values
// outside the known set: any other expected or next value reports
// ErrInvalidState and changes nothing.
func testMaintenanceStateIfInvalid(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	if err := s.SetStateIf(ctx, maintenance.State("offline"), maintenance.StateNormal); !errors.Is(
		err, maintenance.ErrInvalidState,
	) {
		t.Fatalf("set installation state with offline expected: err = %v, want ErrInvalidState", err)
	}

	if err := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.State("offline")); !errors.Is(
		err, maintenance.ErrInvalidState,
	) {
		t.Fatalf("set installation state to offline: err = %v, want ErrInvalidState", err)
	}

	if got, err := s.GetState(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get installation state = %q, %v, want normal without error", got, err)
	}
}

// testMaintenanceStateUnavailable covers the installation state on a closed
// store: reads and writes report maintenance.ErrUnavailable without driver
// text, here forced by Close.
func testMaintenanceStateUnavailable(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	closeStore(t, s)

	if err := s.SetState(ctx, maintenance.StateMaintenance); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("set installation state on a closed store: err = %v, want ErrUnavailable", err)
	}

	condErr := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance)
	if !errors.Is(condErr, maintenance.ErrUnavailable) {
		t.Fatalf("conditional set installation state on a closed store: err = %v, want ErrUnavailable", condErr)
	}

	_, err := s.GetState(ctx)
	if !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("get installation state on a closed store: err = %v, want ErrUnavailable", err)
	}

	// The adapters drop the driver error, so none of the texts the drivers
	// report may reach the caller.
	for _, text := range []string{"sql: database is closed", "closed pool", "SQLITE", "sqlite3", "pgx"} {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("installation state on a closed store error %q holds driver text %q", err, text)
		}
	}
}
