package storefault_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/storefault"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestTransparency runs the namespace and stack suite through the wrapper
// with no faults configured, to prove the wrapper changes nothing.
func TestTransparency(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return wrapWithNamespacePurge(t)
	})
}

// TestUpdatesTransparency runs the update-cycle suite through the wrapper
// with no faults configured.
func TestUpdatesTransparency(t *testing.T) {
	t.Parallel()

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return wrap(t)
	})
}

// TestStateAtTransparency runs the state-at cases through the wrapper with
// no faults configured.
func TestStateAtTransparency(t *testing.T) {
	t.Parallel()

	storetest.RunStateAt(t, func(t *testing.T) storetest.StateAtStore {
		t.Helper()

		return wrap(t)
	})
}

// TestWorkspaceStateAtTransparency runs the workspace state-at cases through
// the wrapper with no faults configured.
func TestWorkspaceStateAtTransparency(t *testing.T) {
	t.Parallel()

	storetest.RunWorkspaceStateAt(t, func(t *testing.T) storetest.WorkspaceStateAtStore {
		t.Helper()

		return wrap(t)
	})
}

// TestWorkspaceTransparency runs the workspace suite through the wrapper with
// no faults configured.
func TestWorkspaceTransparency(t *testing.T) {
	t.Parallel()

	storetest.RunWorkspace(t, func(t *testing.T) storetest.WorkspaceStore {
		t.Helper()

		return wrap(t)
	})
}

// TestHistoryTransparency runs the update-history suite through the wrapper
// with no faults configured.
func TestHistoryTransparency(t *testing.T) {
	t.Parallel()

	storetest.RunUpdateHistory(t, func(t *testing.T) storetest.HistoryStore {
		t.Helper()

		return wrap(t)
	})
}

// TestFailOnce fails the first GetNamespace with the domain sentinel for a
// store that does not answer, and lets the second through.
func TestFailOnce(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "GetNamespace",
		Call:   1,
		Kind:   storefault.KindFail,
		Err:    nil,
		Delay:  0,
	})
	ctx := t.Context()

	if _, err := wrapped.GetNamespace(ctx, "acme"); !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("first get: err = %v, want ErrUnavailable", err)
	}

	if _, err := wrapped.GetNamespace(ctx, "acme"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("second get: err = %v, want ErrNotFound", err)
	}

	if n := wrapped.Calls("GetNamespace"); n != 2 {
		t.Fatalf("get calls = %d, want 2", n)
	}
}

// TestFailTryLockSweep fails the first TryLockSweep with the default sentinel
// of the namespace port and takes no lock; the second call succeeds, takes the
// lock and releases it at once.
func TestFailTryLockSweep(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "TryLockSweep",
		Call:   1,
		Kind:   storefault.KindFail,
		Err:    nil,
		Delay:  0,
	})

	unlock, err := wrapped.TryLockSweep(t.Context())
	if err == nil {
		t.Fatal("faulted try lock sweep: err = nil, want ErrUnavailable")
	}

	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("faulted try lock sweep: err = %v, want ErrUnavailable", err)
	}

	if unlock != nil {
		t.Fatal("faulted try lock sweep returned an unlock function, want nil")
	}

	unlock, err = wrapped.TryLockSweep(t.Context())
	if err != nil {
		t.Fatalf("second try lock sweep: err = %v, want nil", err)
	}

	if unlock == nil {
		t.Fatal("second try lock sweep returned no unlock function, want one")
	}

	unlock()

	if n := wrapped.Calls("TryLockSweep"); n != 2 {
		t.Fatalf("try lock sweep calls = %d, want 2", n)
	}
}

// TestFailCustomError answers the configured sentinel, so tests model the
// documented outcome they need.
func TestFailCustomError(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "Ping",
		Call:   1,
		Kind:   storefault.KindFail,
		Err:    namespace.ErrUnavailable,
		Delay:  0,
	})

	if err := wrapped.Ping(t.Context()); !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ping with a custom fault: err = %v, want ErrUnavailable", err)
	}

	if err := wrapped.Ping(t.Context()); err != nil {
		t.Fatalf("ping after a one-shot fault: %v", err)
	}
}

// TestFailMaintenanceDefaultsToMaintenanceUnavailable proves a fault on the
// maintenance methods answers the sentinel the maintenance port documents,
// so callers behave as with a real adapter.
func TestFailMaintenanceDefaultsToMaintenanceUnavailable(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t,
		storefault.Fault{Method: "GetState", Call: 1, Kind: storefault.KindFail, Err: nil, Delay: 0},
		storefault.Fault{Method: "SetStateIf", Call: 1, Kind: storefault.KindFail, Err: nil, Delay: 0},
	)
	ctx := t.Context()

	if _, err := wrapped.GetState(ctx); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("faulted get state: err = %v, want ErrUnavailable", err)
	}

	if err := wrapped.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); !errors.Is(
		err,
		maintenance.ErrUnavailable,
	) {
		t.Fatalf("faulted set state if: err = %v, want ErrUnavailable", err)
	}

	if err := wrapped.SetState(ctx, maintenance.StateNormal); err != nil {
		t.Fatalf("unfaulted set state: %v", err)
	}
}

// TestFailListUnitsAnswersPlainError proves a fault on ListUnits answers a
// non-nil error that matches no domain sentinel, as the inventory port
// defines none.
func TestFailListUnitsAnswersPlainError(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "ListUnits",
		Call:   1,
		Kind:   storefault.KindFail,
		Err:    nil,
		Delay:  0,
	})

	_, err := wrapped.ListUnits(t.Context(), "acme")
	if err == nil {
		t.Fatal("faulted list units: err = nil, want a plain error")
	}

	if errors.Is(err, namespace.ErrUnavailable) || errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("faulted list units: err = %v, want no domain sentinel", err)
	}
}

// TestStallEndsWithContext proves a stalled call waits for the context and
// then answers its error, instead of answering at once.
func TestStallEndsWithContext(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "ListNamespaces",
		Call:   1,
		Kind:   storefault.KindStall,
		Err:    nil,
		Delay:  0,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	got := make(chan error, 1)

	go func() {
		_, err := wrapped.ListNamespaces(ctx)
		got <- err
	}()

	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()

	select {
	case err := <-got:
		t.Fatalf("stalled list answered %v before the cancel", err)
	case <-timer.C:
	}

	cancel()

	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("stalled list: err = %v, want context.Canceled", err)
	}
}

// TestDelayCallsThroughAfterTheDelay proves a delayed call reaches the inner
// store once the delay passes.
func TestDelayCallsThroughAfterTheDelay(t *testing.T) {
	t.Parallel()

	wrapped := wrap(t, storefault.Fault{
		Method: "Ping",
		Call:   1,
		Kind:   storefault.KindDelay,
		Err:    nil,
		Delay:  50 * time.Millisecond,
	})

	start := time.Now()

	if err := wrapped.Ping(t.Context()); err != nil {
		t.Fatalf("delayed ping: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("delayed ping took %s, want at least 50ms", elapsed)
	}
}

// wrap opens a fresh SQLite store and wraps it with faults. The wrapper
// closes the inner store in cleanup.
func wrap(t *testing.T, faults ...storefault.Fault) *storefault.Store {
	t.Helper()

	wrapped, _ := wrapAt(t, faults...)

	return wrapped
}

// purgingStore is a wrapped store with the test hook that the suite uses to
// delete a namespace row: the wrapper does not forward it, because the hook
// exists only in the test build of the adapter.
type purgingStore struct {
	*storefault.Store

	path string
}

// PurgeNamespace deletes the namespace row of ns through a second handle to
// the database file, and keeps the rows that name it.
func (p purgingStore) PurgeNamespace(ctx context.Context, ns namespace.Name) error {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(10000)")

	dsn := (&url.URL{Scheme: "file", Path: p.path, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	_, execErr := conn.ExecContext(ctx, `DELETE FROM namespaces WHERE name = ?`, ns.String())
	if closeErr := conn.Close(); closeErr != nil && execErr == nil {
		return fmt.Errorf("close raw database: %w", closeErr)
	}

	if execErr != nil {
		return fmt.Errorf("delete namespace: %w", execErr)
	}

	return nil
}

// wrapWithNamespacePurge wraps a store with no faults and adds the hook.
func wrapWithNamespacePurge(t *testing.T) purgingStore {
	t.Helper()

	wrapped, path := wrapAt(t)

	return purgingStore{Store: wrapped, path: path}
}

// wrapAt wraps a new SQLite store and returns the wrapper and the path of its
// database file.
func wrapAt(t *testing.T, faults ...storefault.Fault) (*storefault.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	inner, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	wrapped, err := storefault.New(inner, faults...)
	if err != nil {
		t.Fatalf("wrap store: %v", err)
	}

	t.Cleanup(func() {
		if err := wrapped.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return wrapped, path
}
