package jobtest

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// perNamespace is the count of jobs that each namespace gets in the case of
// the parallel creates, and namespaces is the count of namespaces. Their
// product stays under job.MaxOpenPerNamespace and each unit under
// job.MaxOpenPerUnit.
const (
	perNamespace = 8
	namespaces   = 4
	pageSize     = 7
	nsPageSize   = 3
)

// testRenamedUnitLocked proves that a lock still stops the dispatch of a unit
// that was renamed after its job started: the job holds the name at the start,
// and the lock follows the stable unit ID.
func testRenamedUnitLocked(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	queued := create(t, h, f, newJob(unit, job.OperationRefresh, ""), nil)

	if _, err := h.Domain.RenameUnit(t.Context(), f.fence, unit, "web/live"); err != nil {
		t.Fatalf("RenameUnit = %v", err)
	}

	target := manuallock.NamespaceTarget(f.fence.Namespace)
	if err := h.Domain.PutManualLock(t.Context(), f.fence, target, lockHolder, lockReason); err != nil {
		t.Fatalf("PutManualLock = %v", err)
	}

	if _, err := h.Jobs.Apply(t.Context(), dispatch(&queued), f.fence, nil); !errors.Is(err, job.ErrLocked) {
		t.Fatalf("dispatch of a renamed unit under the lock of its namespace = %v, want ErrLocked", err)
	}

	assertState(t, h, queued.ID, job.StateQueued)

	if err := h.Domain.RemoveManualLock(t.Context(), f.fence, target); err != nil {
		t.Fatalf("RemoveManualLock = %v", err)
	}

	if got := apply(t, h, f, dispatch(&queued)); got.State != job.StateDispatched {
		t.Fatalf("dispatch of a renamed unit with no lock = %s, want dispatched", got.State)
	}
}

// testRenamedUnitOwnLock is the case above for the lock of the unit itself.
func testRenamedUnitOwnLock(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	queued := create(t, h, f, newJob(unit, job.OperationRefresh, ""), nil)

	renamed, err := h.Domain.RenameUnit(t.Context(), f.fence, unit, "web/live")
	if err != nil {
		t.Fatalf("RenameUnit = %v", err)
	}

	// The lock is put on the unit under its new name, as a person would.
	target := manuallock.UnitTarget(unitRef(renamed))
	if err := h.Domain.PutManualLock(t.Context(), f.fence, target, lockHolder, lockReason); err != nil {
		t.Fatalf("PutManualLock = %v", err)
	}

	if _, err := h.Jobs.Apply(t.Context(), dispatch(&queued), f.fence, nil); !errors.Is(err, job.ErrLocked) {
		t.Fatalf("dispatch of a renamed unit under its own lock = %v, want ErrLocked", err)
	}

	assertState(t, h, queued.ID, job.StateQueued)
}

// testUnitGone proves that a dispatch fails closed for a unit that left the
// inventory: no lock can be read, so no operation starts.
func testUnitGone(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	queued := create(t, h, f, newJob(unit, job.OperationRefresh, ""), nil)

	if err = h.Domain.RemoveUnit(t.Context(), f.fence, unit); err != nil {
		t.Fatalf("RemoveUnit = %v", err)
	}

	_, err = h.Jobs.Apply(t.Context(), dispatch(&queued), f.fence, nil)
	if err == nil {
		t.Fatal("dispatch of a unit that left the inventory = nil, want an error")
	}

	if !errors.Is(err, job.ErrUnitGone) {
		t.Fatalf("dispatch of a unit that left the inventory = %v, want ErrUnitGone", err)
	}

	if errors.Is(err, job.ErrLocked) {
		t.Fatalf("dispatch of a unit that left the inventory = %v, want an error that is not ErrLocked", err)
	}

	assertState(t, h, queued.ID, job.StateQueued)

	got, err := h.Jobs.Get(t.Context(), queued.ID)
	if err != nil || got.Phases[got.Current].Operation != "" {
		t.Fatalf("job after the refused dispatch = operation %q, %v; want no operation",
			got.Phases[got.Current].Operation, err)
	}
}

// testParallelNamespaces creates jobs in several namespaces at once and pages
// through them all. The sequence of the jobs counts per namespace: two creates
// in two namespaces never share a maximum, and paging misses no job.
func testParallelNamespaces(t *testing.T, h Harness) {
	t.Helper()

	fixtures := make([]fixture, namespaces)

	for i := range fixtures {
		fence, err := h.Domain.CreateNamespace(t.Context(), nsName(i))
		if err != nil {
			t.Fatalf("CreateNamespace = %v", err)
		}

		unit, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", "network")
		if err != nil {
			t.Fatalf("CreateUnit = %v", err)
		}

		fixtures[i] = fixture{fence: fence, unit: unit}
	}

	want, err := createInParallel(t, h, fixtures)
	if err != nil {
		t.Fatalf("parallel creates: %v", err)
	}

	got := pageAll(t, h, job.Filter{
		Namespace: "", UnitID: "", States: nil, Limit: pageSize, After: "",
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	})
	if len(got) != namespaces*perNamespace || !sameSet(got, want) {
		t.Fatalf("paging over all namespaces returned %d jobs (%d distinct), want %d", len(got),
			len(slices.Compact(sortedIDs(got))), namespaces*perNamespace)
	}

	for i := range fixtures {
		ns := fixtures[i].fence.Namespace

		own := pageAll(t, h, job.Filter{
			Namespace: ns, UnitID: "", States: nil, Limit: nsPageSize, After: "",
			StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
		})
		if len(own) != perNamespace {
			t.Fatalf("paging in %s returned %d jobs, want %d", ns, len(own), perNamespace)
		}
	}
}

// createInParallel starts one goroutine for each fixture. Each creates
// perNamespace jobs. It returns the IDs of the stored jobs.
func createInParallel(t *testing.T, h Harness, fixtures []fixture) (map[job.ID]bool, error) {
	t.Helper()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		want = map[job.ID]bool{}
	)

	for i := range fixtures {
		f := &fixtures[i]

		wg.Go(func() {
			for range perNamespace {
				stored, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, ""), nil,
					f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunApply)})

				mu.Lock()

				if err != nil || !created {
					errs = append(errs, fmt.Errorf("create in %s: created %v: %w", f.fence.Namespace, created, err))
				} else {
					want[stored.ID] = true
				}

				mu.Unlock()
			}
		})
	}

	wg.Wait()

	return want, errors.Join(errs...)
}

// pageAll lists every page of f and returns the IDs in the order of the pages.
func pageAll(t *testing.T, h Harness, f job.Filter) []job.ID {
	t.Helper()

	var out []job.ID

	for range namespaces * perNamespace {
		page, err := h.Jobs.List(t.Context(), f)
		if err != nil {
			t.Fatalf("List = %v", err)
		}

		out = append(out, ids(page.Jobs)...)

		if page.After == "" {
			return out
		}

		f.After = page.After
	}

	t.Fatalf("paging did not end after %d pages", namespaces*perNamespace)

	return nil
}

func sortedIDs(in []job.ID) []job.ID {
	out := slices.Clone(in)
	slices.Sort(out)

	return out
}

func sameSet(got []job.ID, want map[job.ID]bool) bool {
	seen := map[job.ID]bool{}

	for _, id := range got {
		if !want[id] || seen[id] {
			return false
		}

		seen[id] = true
	}

	return len(seen) == len(want)
}

func nsName(i int) namespace.Name {
	return namespace.Name(fmt.Sprintf("ns-%d", i))
}

func unitRef(o identity.Object) collection.UnitRef {
	return collection.UnitRef{Namespace: o.Namespace(), Tool: o.Tool(), Name: o.UnitName()}
}
