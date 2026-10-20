package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
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

// maintenanceWrite is one write that maintenance mode must refuse.
type maintenanceWrite struct {
	run  func() error
	name string
}

// maintenanceWrites returns the writes that the state check covers: one at
// installation scope and four at namespace scope. existing is a stack of ns.
func maintenanceWrites(ctx context.Context, s Store, ns namespace.Namespace, existing stack.Stack) []maintenanceWrite {
	other := namespace.Namespace{
		CreatedAt:       now(),
		ID:              namespace.NewID(),
		Name:            "other",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	return []maintenanceWrite{
		{
			name: "installation scope: create namespace",
			run:  func() error { return s.CreateNamespace(ctx, other) },
		},
		{
			name: "namespace scope: create stack",
			run:  func() error { return s.CreateStack(ctx, newStack(ns.Name, "web", "dev"), ns.Fence()) },
		},
		{
			name: "namespace scope: update tags",
			run: func() error {
				return s.UpdateStackTags(ctx, existing.Ref, map[string]string{"team": "other"}, now(), ns.Fence())
			},
		},
		{
			name: "namespace scope: rename stack",
			run:  func() error { return s.RenameStack(ctx, existing.Ref, "renamed", ns.Fence()) },
		},
		{
			name: "namespace scope: set overrides",
			run: func() error {
				return s.SetOverrides(ctx, ns.Name, namespace.Overrides{Forced: true, Unlocked: false}, ns.Fence())
			},
		},
	}
}

// testMaintenanceRefusesWrites covers the write check: in maintenance mode a
// write at namespace scope and a write at installation scope each fail with
// maintenance.ErrInMaintenance and change nothing, reads keep working, and
// the same writes pass again after the state returns to normal.
func testMaintenanceRefusesWrites(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ns := newNamespace(t, s, "acme")
	existing := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(ctx, existing, ns.Fence()); err != nil {
		t.Fatalf("create stack before maintenance: %v", err)
	}

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	writes := maintenanceWrites(ctx, s, ns, existing)

	for _, w := range writes {
		if err := w.run(); !errors.Is(err, maintenance.ErrInMaintenance) {
			t.Fatalf("%s in maintenance mode: err = %v, want ErrInMaintenance", w.name, err)
		}
	}

	assertUnchangedInMaintenance(t, s, ns, existing)

	if err := s.SetState(ctx, maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal: %v", err)
	}

	for _, w := range writes {
		if err := w.run(); err != nil {
			t.Fatalf("%s after maintenance mode: %v", w.name, err)
		}
	}
}

// assertUnchangedInMaintenance checks that the refused writes changed nothing
// and that a read still answers in maintenance mode.
func assertUnchangedInMaintenance(t *testing.T, s Store, ns namespace.Namespace, existing stack.Stack) {
	t.Helper()

	ctx := t.Context()

	if _, err := s.GetNamespace(ctx, "other"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get refused namespace: err = %v, want ErrNotFound", err)
	}

	stacks, err := s.ListStacks(ctx, stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil || len(stacks) != 1 || stacks[0].Ref != existing.Ref || stacks[0].Tags["team"] != "platform" {
		t.Fatalf("list stacks in maintenance mode = %+v, %v, want the one unchanged stack", stacks, err)
	}
}

// writerCount is the number of concurrent writers in the enter case.
const writerCount = 4

// writers are goroutines that create stacks in ns until one write fails.
type writers struct {
	store   Store
	ns      namespace.Namespace
	started chan struct{}
	first   atomic.Value
	counter atomic.Int64
	once    sync.Once
	group   sync.WaitGroup
}

// startWriters starts writerCount writers. It closes started once they have
// committed a few rows.
func startWriters(ctx context.Context, s Store, ns namespace.Namespace) *writers {
	//nolint:exhaustruct_v5 // The counter, the first error, the once and the group start zero.
	w := &writers{store: s, ns: ns, started: make(chan struct{})}

	for range writerCount {
		w.group.Go(func() { w.run(ctx) })
	}

	return w
}

// run writes until a write fails. It keeps the first error that is not
// maintenance.ErrInMaintenance.
func (w *writers) run(ctx context.Context) {
	for {
		name := fmt.Sprintf("stack-%d", w.counter.Add(1))

		err := w.store.CreateStack(ctx, newStack(w.ns.Name, "web", name), w.ns.Fence())
		if err != nil {
			if !errors.Is(err, maintenance.ErrInMaintenance) {
				w.first.Store(err)
			}

			return
		}

		if w.counter.Load() >= writerCount*3 {
			w.once.Do(func() { close(w.started) })
		}
	}
}

// wait returns after every writer ended, with the first error that is not
// maintenance.ErrInMaintenance.
func (w *writers) wait() error {
	w.group.Wait()

	if err, ok := w.first.Load().(error); ok {
		return err
	}

	return nil
}

// testMaintenanceEnterStopsWrites covers a write in flight at enter. Writers
// run while the state changes. When SetStateIf returns, every write that
// started before it has committed or failed, so the stored stacks do not
// change after the return, and each later write fails with ErrInMaintenance.
func testMaintenanceEnterStopsWrites(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	ns := newNamespace(t, s, "acme")
	w := startWriters(ctx, s, ns)

	// Let the writers commit some rows before the enter.
	<-w.started

	if err := s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	afterEnter, err := s.ListStacks(ctx, stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list stacks after enter: %v", err)
	}

	if writerErr := w.wait(); writerErr != nil {
		t.Fatalf("a writer failed with an error other than ErrInMaintenance: %v", writerErr)
	}

	final, err := s.ListStacks(ctx, stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list stacks after the writers ended: %v", err)
	}

	if len(final) != len(afterEnter) {
		t.Fatalf("stacks after enter = %d, after the writers ended = %d: a write committed after enter returned",
			len(afterEnter), len(final))
	}

	late := s.CreateStack(ctx, newStack(ns.Name, "web", "late"), ns.Fence())
	if !errors.Is(late, maintenance.ErrInMaintenance) {
		t.Fatalf("write after enter: err = %v, want ErrInMaintenance", late)
	}
}

// StateFault breaks the stored installation state of one store so that a read
// of it fails, and repairs it again. An adapter builds it with direct SQL.
type StateFault struct {
	Break  func(t *testing.T)
	Repair func(t *testing.T)
}

// FaultFactory returns a fresh store and the fault that the adapter makes on
// its installation state for the case called name.
type FaultFactory func(t *testing.T, name string) (Store, StateFault)

// RunWritesFailClosed checks that a write does not pass when the store cannot
// read the installation state. For each name, newStore gives a store and a
// fault. The write fails with maintenance.ErrUnavailable only (never
// ErrInMaintenance, and never nil) and changes nothing. A write at
// installation scope and a write that carries a fence both follow the rule.
func RunWritesFailClosed(t *testing.T, names []string, newStore FaultFactory) {
	t.Helper()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, fault := newStore(t, name)
			testWriteFailsClosed(t, s, fault)
		})
	}
}

func testWriteFailsClosed(t *testing.T, s Store, fault StateFault) {
	t.Helper()

	ctx := t.Context()
	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	other := ns
	other.ID = namespace.NewID()
	other.Name = "other"

	fault.Break(t)

	writes := []maintenanceWrite{
		{name: "installation scope", run: func() error { return s.CreateNamespace(ctx, other) }},
		{name: "namespace scope", run: func() error { return s.CreateStack(ctx, st, ns.Fence()) }},
	}

	for _, w := range writes {
		err := w.run()
		if !errors.Is(err, maintenance.ErrUnavailable) || errors.Is(err, maintenance.ErrInMaintenance) {
			t.Fatalf("%s write with an unreadable state: err = %v, want ErrUnavailable only", w.name, err)
		}
	}

	fault.Repair(t)

	if _, err := s.GetNamespace(ctx, other.Name); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get namespace of the refused write: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetStack(ctx, st.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get stack of the refused write: err = %v, want ErrNotFound", err)
	}
}
