package unmanagedtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// scanCases returns the cases of the sweeps.
func scanCases() []suiteCase {
	return []suiteCase{
		{"sweeps: targets have unique names and a sweep pool of their namespace", testTargets},
		{"sweeps: a disabled target reads back disabled", testTargetDisabled},
		{"sweeps: a target above the limit is refused", testTargetLimit},
		{"sweeps: one sweep of a target at a time", testScanBusy},
		{"sweeps: concurrent starts leave one sweep", testScanConcurrent},
		{"sweeps: a refused write stores nothing", testScanRollback},
		{"sweeps: a start after the pool changed is refused", testScanPoolChanged},
		{"sweeps: a delete cancels the sweep that did not end", testDeleteTargetCancelsScan},
		{"sweeps: the state table holds", testScanTable},
		{"sweeps: a stale fence refuses each write", testScansFenced},
	}
}

// scanOf returns a cloud sweep of namespace acme for target aws.
func scanOf() unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: namespace.Name("acme"),
		Kind:      unmanaged.ScanCloud,
		Target:    "aws",
		CreatedAt: at(),
		EndedAt:   time.Time{},
		ID:        "",
		Status:    "",
		StartedBy: "",
		Reason:    "",
		Scopes:    nil,
		Counts: unmanaged.ScanCounts{
			Records: 0, Managed: 0, Elsewhere: 0, NotInAnyUnit: 0, LeftBehind: 0, ManagedTwice: 0, Resolved: 0,
			Rejected: 0, OutOfScope: 0,
		},
	}
}

func testTargets(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	h.Namespace(t, "other")
	e := auditOf("acme", "namespace:manage-sweeps")

	must(t, "create", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))
	wantErr(
		t,
		"create again",
		h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e),
		unmanaged.ErrAlreadyExists,
	)

	foreign := targetOf("acme", "foreign")
	foreign.Pool = "pool-of-other"
	wantErr(
		t,
		"create with an unknown pool",
		h.Scans.CreateTarget(t.Context(), foreign, f, e),
		unmanaged.ErrInvalidTarget,
	)

	jobs := targetOf("acme", "on-jobs")
	jobs.Pool = JobPool
	wantErr(t, "create on a job pool", h.Scans.CreateTarget(t.Context(), jobs, f, e), unmanaged.ErrInvalidTarget)

	moved := targetOf("acme", "aws")
	moved.Pool = JobPool
	wantErr(t, "move to a job pool", h.Scans.UpdateTarget(t.Context(), moved, f, e), unmanaged.ErrInvalidTarget)

	changed := targetOf("acme", "aws")
	changed.Every = 0
	must(t, "update", h.Scans.UpdateTarget(t.Context(), changed, f, e))
	wantErr(
		t,
		"update a missing target",
		h.Scans.UpdateTarget(t.Context(), targetOf("acme", "none"), f, e),
		unmanaged.ErrNotFound,
	)

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(got) != 1 || got[0].Every != 0 {
		t.Errorf("targets %+v, want the one updated target", got)
	}

	must(t, "delete", h.Scans.DeleteTarget(t.Context(), "acme", "aws", f, e))
	wantErr(t, "delete again", h.Scans.DeleteTarget(t.Context(), "acme", "aws", f, e), unmanaged.ErrNotFound)
}

// testTargetDisabled proves that the enabled flag of a target survives a
// write: a target that a person disabled reads back disabled, and an enabled
// one reads back enabled.
func testTargetDisabled(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")

	off := targetOf("acme", "aws")
	off.Enabled = false
	must(t, "create disabled", h.Scans.CreateTarget(t.Context(), off, f, e))

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(got) != 1 || got[0].Enabled {
		t.Errorf("targets %+v, want the one target disabled", got)
	}

	on := targetOf("acme", "aws")
	must(t, "update enabled", h.Scans.UpdateTarget(t.Context(), on, f, e))

	got, err = h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(got) != 1 || !got[0].Enabled {
		t.Errorf("targets %+v, want the one target enabled", got)
	}
}

// testTargetLimit proves the bound of the targets of a namespace: the target
// above MaxTargets is refused.
func testTargetLimit(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")

	for i := range unmanaged.MaxTargets {
		must(t, "create",
			h.Scans.CreateTarget(t.Context(), targetOf("acme", fmt.Sprintf("target-%03d", i)), f, e))
	}

	wantErr(t, "create above the limit",
		h.Scans.CreateTarget(t.Context(), targetOf("acme", "one-too-many"), f, e), unmanaged.ErrLimit)
}

// testDeleteTargetCancelsScan proves that a delete of a target cancels its
// sweep that did not end.
func testDeleteTargetCancelsScan(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	s, err := h.Scans.StartScan(t.Context(), scanOf(), f, e)
	must(t, "start", err)

	must(t, "delete target", h.Scans.DeleteTarget(t.Context(), "acme", "aws", f, e))

	got, err := h.Scans.Scan(t.Context(), "acme", s.ID)
	must(t, "sweep", err)

	if got.Status != unmanaged.ScanCanceled {
		t.Errorf("sweep status = %q, want canceled", got.Status)
	}
}

func testScanBusy(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	first, err := h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanCloud,
			Target:    "aws",
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		f,
		e,
	)
	must(t, "start", err)

	if first.ID == "" || first.Status != unmanaged.ScanQueued {
		t.Errorf("sweep %+v, want an ID and the status queued", first)
	}

	_, err = h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanCloud,
			Target:    "aws",
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		f,
		e,
	)
	wantErr(t, "start a second sweep", err, unmanaged.ErrScanBusy)

	_, err = h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanCloud,
			Target:    "none",
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		f,
		e,
	)
	wantErr(t, "start a sweep of a missing target", err, unmanaged.ErrNotFound)

	_, err = h.Scans.AdvanceScan(
		t.Context(),
		unmanaged.ScanAdvance{
			Namespace: "acme",
			ID:        first.ID,
			Event:     unmanaged.ScanEventCancel,
			Update:    unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		},
		f,
		e,
	)
	must(t, "cancel", err)

	_, err = h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanCloud,
			Target:    "aws",
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		f,
		e,
	)
	must(t, "start after the end of the first", err)
}

func testScanTable(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")

	s, err := h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanState,
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Target:    "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		f,
		e,
	)
	must(t, "start", err)

	_, err = h.Scans.AdvanceScan(
		t.Context(),
		unmanaged.ScanAdvance{
			Namespace: "acme",
			ID:        s.ID,
			Event:     unmanaged.ScanEventDispatch,
			Update:    unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		},
		f,
		e,
	)
	wantErr(t, "dispatch a state check", err, unmanaged.ErrTransition)

	_, err = h.Scans.AdvanceScan(
		t.Context(),
		unmanaged.ScanAdvance{
			Namespace: "acme",
			ID:        "scn-none",
			Event:     unmanaged.ScanEventCancel,
			Update:    unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		},
		f,
		e,
	)
	wantErr(t, "advance a missing sweep", err, unmanaged.ErrNotFound)

	counts := unmanaged.ScanCounts{
		Records:      3,
		LeftBehind:   1,
		Managed:      0,
		Elsewhere:    0,
		NotInAnyUnit: 0,
		ManagedTwice: 0,
		Resolved:     0,
		Rejected:     0,
		OutOfScope:   0,
	}

	for _, ev := range []unmanaged.ScanEvent{unmanaged.ScanEventUploaded, unmanaged.ScanEventMatched} {
		s, err = h.Scans.AdvanceScan(
			t.Context(),
			unmanaged.ScanAdvance{
				Namespace: "acme",
				ID:        s.ID,
				Event:     ev,
				Update:    unmanaged.ScanUpdate{Counts: &counts, Scopes: nil, Reason: ""},
			},
			f,
			e,
		)
		must(t, string(ev), err)
	}

	got, err := h.Scans.Scan(t.Context(), "acme", s.ID)
	must(t, "sweep", err)

	if got.Status != unmanaged.ScanSucceeded || got.Counts != counts {
		t.Errorf("sweep %+v, want succeeded with its counts", got)
	}

	list, err := h.Scans.Scans(t.Context(), "acme", "", "", 10)
	must(t, "sweeps", err)

	if len(list) != 1 || list[0].ID != s.ID {
		t.Errorf("sweeps %+v, want the one sweep", list)
	}
}

func testScansFenced(t *testing.T, h Harness) {
	t.Helper()

	old := stale(h.Namespace(t, "acme"))
	e := auditOf("acme", "namespace:manage-sweeps")

	wantErr(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), old, e), namespace.ErrFenced)

	_, err := h.Scans.StartScan(
		t.Context(),
		unmanaged.Scan{
			Namespace: "acme",
			Kind:      unmanaged.ScanState,
			CreatedAt: at(),
			EndedAt:   time.Time{},
			ID:        "",
			Target:    "",
			Status:    "",
			StartedBy: "",
			Reason:    "",
			Scopes:    nil,
			Counts: unmanaged.ScanCounts{
				Records:      0,
				Managed:      0,
				Elsewhere:    0,
				NotInAnyUnit: 0,
				LeftBehind:   0,
				ManagedTwice: 0,
				Resolved:     0,
				Rejected:     0,
				OutOfScope:   0,
			},
		},
		old,
		e,
	)
	wantErr(t, "start", err, namespace.ErrFenced)
}

// testScanConcurrent proves that concurrent starts of a sweep of one target
// leave one sweep: one start wins, the others meet ErrScanBusy.
func testScanConcurrent(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	const callers = 8

	ctx := t.Context()

	var wg sync.WaitGroup

	start := make(chan struct{})

	errs := make([]error, callers)

	for i := range callers {
		wg.Go(func() {
			<-start

			_, errs[i] = h.Scans.StartScan(ctx, scanOf(), f, e)
		})
	}

	close(start)
	wg.Wait()

	won := 0

	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, unmanaged.ErrScanBusy):
		default:
			t.Errorf("start = %v, want nil or ErrScanBusy", err)
		}
	}

	if won != 1 {
		t.Errorf("%d starts won, want exactly one", won)
	}

	list, err := h.Scans.Scans(t.Context(), "acme", "aws", "", 10)
	must(t, "sweeps", err)

	if len(list) != 1 {
		t.Errorf("sweeps = %d, want one after the race", len(list))
	}
}

// testScanRollback proves that a write that fails part way stores nothing: a
// refused create, a busy start, a refused transition and a fenced delete leave
// the targets and the sweeps that the store held before.
func testScanRollback(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")
	s := auditOf("acme", "namespace:start-sweep")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	wantErr(t, "create again",
		h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e), unmanaged.ErrAlreadyExists)

	jobs := targetOf("acme", "on-jobs")
	jobs.Pool = JobPool
	wantErr(t, "create on a job pool", h.Scans.CreateTarget(t.Context(), jobs, f, e), unmanaged.ErrInvalidTarget)

	targets, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(targets) != 1 {
		t.Fatalf("targets = %d, want the one target before the refused writes", len(targets))
	}

	first, err := h.Scans.StartScan(t.Context(), scanOf(), f, s)
	must(t, "start", err)

	_, err = h.Scans.StartScan(t.Context(), scanOf(), f, s)
	wantErr(t, "start a second sweep", err, unmanaged.ErrScanBusy)

	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme",
		ID:        first.ID,
		Event:     unmanaged.ScanEventUploaded,
		Update:    unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, f, s)
	wantErr(t, "upload a queued cloud sweep", err, unmanaged.ErrTransition)

	got, err := h.Scans.Scan(t.Context(), "acme", first.ID)
	must(t, "sweep", err)

	if got.Status != unmanaged.ScanQueued {
		t.Errorf("sweep status = %q, want the refused advance to keep queued", got.Status)
	}

	list, err := h.Scans.Scans(t.Context(), "acme", "aws", "", 10)
	must(t, "sweeps", err)

	if len(list) != 1 {
		t.Errorf("sweeps = %d, want the one sweep before the refused writes", len(list))
	}

	staleFence := stale(f)

	wantErr(t, "delete with a stale fence",
		h.Scans.DeleteTarget(t.Context(), "acme", "aws", staleFence, e), namespace.ErrFenced)

	targets, err = h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(targets) != 1 {
		t.Errorf("targets = %d, want the one target after the fenced delete", len(targets))
	}
}

// testScanPoolChanged proves that a start re-reads the pool of its target: a
// pool whose purpose changed after the save refuses the start with
// ErrInvalidTarget and records no sweep.
func testScanPoolChanged(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")
	s := auditOf("acme", "namespace:start-sweep")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	h.SetPoolPurpose(t, "acme", Pool, PurposeJobs)

	_, err := h.Scans.StartScan(t.Context(), scanOf(), f, s)
	wantErr(t, "start after the purpose changed", err, unmanaged.ErrInvalidTarget)

	list, err := h.Scans.Scans(t.Context(), "acme", "", "", 10)
	must(t, "sweeps", err)

	if len(list) != 0 {
		t.Errorf("sweeps = %d, want none after the refused start", len(list))
	}

	h.SetPoolPurpose(t, "acme", Pool, PurposeSweeps)

	_, err = h.Scans.StartScan(t.Context(), scanOf(), f, s)
	must(t, "start after the purpose returned", err)
}
