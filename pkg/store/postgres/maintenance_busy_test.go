package postgres_test

import (
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestEnterBusyConformance runs the busy case of an enter: a write in flight
// that outlasts the lock timeout makes the enter report ErrBusy.
func TestEnterBusyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunEnterBusy(t, func(t *testing.T) (storetest.Store, storetest.BusyHarness) {
		t.Helper()

		s := open(t, base)
		postgres.SetStateLockWaitForTest(s, 200*time.Millisecond)

		return s, storetest.BusyHarness{
			Hold: func(t *testing.T) func() {
				t.Helper()

				return postgres.HoldCheckedWriteForTest(t, s)
			},
		}
	})
}

// TestLeaveReportsUnavailableWhenAWriteDoesNotEnd proves that a leave of
// maintenance mode whose wait for the write lock ends at the lock timeout
// reports ErrUnavailable and not ErrBusy: a leave that times out is a stuck
// write, not an enter to try again.
func TestLeaveReportsUnavailableWhenAWriteDoesNotEnd(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)
	postgres.SetStateLockWaitForTest(s, 200*time.Millisecond)

	ctx := t.Context()

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter: %v", err)
	}

	release := postgres.HoldCheckedWriteForTest(t, s)

	defer release()

	for name, run := range map[string]func() error{
		"SetState": func() error { return s.SetState(ctx, maintenance.StateNormal) },
		"SetStateIf": func() error {
			return s.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateNormal)
		},
	} {
		err := run()
		if !errors.Is(err, maintenance.ErrUnavailable) || errors.Is(err, maintenance.ErrBusy) {
			t.Errorf("%s leave while a write does not end: err = %v, want ErrUnavailable and not ErrBusy", name, err)
		}
	}
}

// TestEnterReportsUnavailableWhenTheLockFailsOtherwise proves that only the
// lock timeout (SQLSTATE 55P03) makes an enter report ErrBusy. Here the wait
// for the write lock ends at the statement timeout (SQLSTATE 57014) before the
// lock timeout, and the context stays alive: the enter reports ErrUnavailable.
func TestEnterReportsUnavailableWhenTheLockFailsOtherwise(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	u, err := url.Parse(pgtest.MigratedURL(t, base))
	if err != nil {
		t.Fatalf("parse the schema URL: %v", err)
	}

	q := u.Query()
	q.Set("statement_timeout", "500")
	u.RawQuery = q.Encode()

	s := openURL(t, u.String())
	postgres.SetStateLockWaitForTest(s, time.Minute)

	release := postgres.HoldCheckedWriteForTest(t, s)

	defer release()

	ctx := t.Context()

	for name, run := range map[string]func() error{
		"SetState": func() error { return s.SetState(ctx, maintenance.StateMaintenance) },
		"SetStateIf": func() error {
			return s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance)
		},
	} {
		err := run()
		if !errors.Is(err, maintenance.ErrUnavailable) || errors.Is(err, maintenance.ErrBusy) {
			t.Errorf(
				"%s enter when the lock fails with another error: err = %v, want ErrUnavailable and not ErrBusy",
				name, err,
			)
		}
	}
}
