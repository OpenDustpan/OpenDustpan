package unmanagedtest

import (
	"fmt"
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
		{"sweeps: a delete cancels the sweep that did not end", testDeleteTargetCancelsScan},
		{"sweeps: the state table holds", testScanTable},
		{"sweeps: a stale fence refuses each write", testScansFenced},
	}
}

// scanOf returns a sweep of the namespace ns: a cloud sweep of the target name
// target, or a state check.
func scanOf(ns string, k unmanaged.ScanKind, target string) unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: namespace.Name(ns),
		Kind:      k,
		Target:    target,
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

	s, err := h.Scans.StartScan(t.Context(), scanOf("acme", unmanaged.ScanCloud, "aws"), f, e)
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
