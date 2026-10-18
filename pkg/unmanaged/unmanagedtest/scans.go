package unmanagedtest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
)

// scanCases returns the cases of the sweeps.
func scanCases() []suiteCase {
	return []suiteCase{
		{"sweeps: targets have unique names and a sweep pool of their namespace", testTargets},
		{"sweeps: one sweep of a target at a time", testScanBusy},
		{"sweeps: the state table holds", testScanTable},
		{"sweeps: a stale fence refuses each write", testScansFenced},
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
		"acme",
		first.ID,
		unmanaged.ScanEventCancel,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
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
		"acme",
		s.ID,
		unmanaged.ScanEventDispatch,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		f,
		e,
	)
	wantErr(t, "dispatch a state check", err, unmanaged.ErrTransition)

	_, err = h.Scans.AdvanceScan(
		t.Context(),
		"acme",
		"scn-none",
		unmanaged.ScanEventCancel,
		unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
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
			"acme",
			s.ID,
			ev,
			unmanaged.ScanUpdate{Counts: &counts, Scopes: nil, Reason: ""},
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
