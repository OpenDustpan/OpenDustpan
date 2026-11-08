package unmanagedtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// scanCases returns the cases of the sweeps.
func scanCases() []suiteCase {
	return []suiteCase{
		{"sweeps: targets have unique names and a sweep pool of their namespace", testTargets},
		{"sweeps: a disabled target reads back disabled", testTargetDisabled},
		{"sweeps: a target above the limit is refused", testTargetLimit},
		{"sweeps: concurrent creates near the limit keep the target count", testCreateTargetRace},
		{"sweeps: one sweep of a target at a time", testScanBusy},
		{"sweeps: concurrent starts of a target leave one sweep", testStartScanRace},
		{"sweeps: a delete cancels the sweep that did not end", testDeleteTargetCancelsScan},
		{"sweeps: the state table holds", testScanTable},
		{"sweeps: a page size outside the bound is refused", testScansPageSize},
		{
			"sweeps: a fence of another namespace, a stale fence or an entry of another namespace refuses each write",
			testScansFenced,
		},
	}
}

// scanOf returns a sweep of the namespace ns: a cloud sweep of the target name
// target, or a state check.
func scanOf(ns namespace.Name, k unmanaged.ScanKind, target string) unmanaged.Scan {
	return unmanaged.Scan{
		Namespace: ns,
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

// testCreateTargetRace proves the target limit under concurrency: with a
// namespace below MaxTargets, concurrent creates fill it to the limit
// exactly, and every other create returns ErrLimit.
func testCreateTargetRace(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")

	const (
		room  = 3
		calls = 8
	)

	for i := range unmanaged.MaxTargets - room {
		must(t, "create",
			h.Scans.CreateTarget(t.Context(), targetOf("acme", fmt.Sprintf("target-%03d", i)), f, e))
	}

	start := make(chan struct{})
	errs := make([]error, calls)

	var wg sync.WaitGroup

	for i := range calls {
		wg.Go(func() {
			<-start

			errs[i] = h.Scans.CreateTarget(
				t.Context(), targetOf("acme", fmt.Sprintf("race-%03d", i)), f, e,
			)
		})
	}

	close(start)
	wg.Wait()

	created := 0

	for i := range calls {
		if errs[i] == nil {
			created++

			continue
		}

		if !errors.Is(errs[i], unmanaged.ErrLimit) {
			t.Errorf("create %d: %v, want nil or ErrLimit", i, errs[i])
		}
	}

	if created != room {
		t.Errorf("%d targets created, want %d", created, room)
	}

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)

	if len(got) != unmanaged.MaxTargets {
		t.Errorf("%d targets, want %d", len(got), unmanaged.MaxTargets)
	}
}

// testDeleteTargetCancelsScan proves that a delete of a target cancels its
// sweep that did not end.
func testDeleteTargetCancelsScan(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	s, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
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

// testStartScanRace proves the busy rule under concurrency: of concurrent
// starts of one target, one starts a sweep and every other start returns
// ErrScanBusy.
func testStartScanRace(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	const calls = 8

	start := make(chan struct{})
	scans := make([]unmanaged.Scan, calls)
	errs := make([]error, calls)

	var wg sync.WaitGroup

	for i := range calls {
		wg.Go(func() {
			<-start

			scans[i], errs[i] = h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
		})
	}

	close(start)
	wg.Wait()

	started := 0

	for i := range calls {
		if errs[i] == nil {
			started++

			if scans[i].ID == "" || scans[i].Status != unmanaged.ScanQueued {
				t.Errorf("start %d: sweep %+v, want an ID and the status queued", i, scans[i])
			}

			continue
		}

		if !errors.Is(errs[i], unmanaged.ErrScanBusy) {
			t.Errorf("start %d: %v, want nil or ErrScanBusy", i, errs[i])
		}
	}

	if started != 1 {
		t.Errorf("%d sweeps started, want 1", started)
	}
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

// testScansPageSize proves the page bound of the sweep list: a limit below 1
// or above MaxPage is refused with ErrLimit, and the bounds themselves are
// accepted.
func testScansPageSize(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")

	_, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanState, ""), f, e)
	must(t, "start", err)

	for _, tc := range []struct {
		name  string
		limit int
		want  error
	}{
		{"zero", 0, unmanaged.ErrLimit},
		{"negative", -1, unmanaged.ErrLimit},
		{"above the bound", unmanaged.MaxPage + 1, unmanaged.ErrLimit},
		{"above 32 bits", 1 << 32, unmanaged.ErrLimit},
		{"one", 1, nil},
		{"the bound", unmanaged.MaxPage, nil},
	} {
		got, err := h.Scans.Scans(t.Context(), "acme", "", "", tc.limit)

		if tc.want != nil {
			wantErr(t, tc.name, err, tc.want)

			continue
		}

		must(t, tc.name, err)

		if len(got) != 1 {
			t.Errorf("%s: %d sweeps, want the one sweep", tc.name, len(got))
		}
	}
}

// testScansFenced proves that every write of the sweep store refuses a fence
// that names another namespace, a fence that a failover made stale, and an
// entry that names another namespace, and that the refused write changes
// nothing.
func testScansFenced(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	other := h.Namespace(t, "other")
	e := auditOf("acme", "namespace:manage-sweeps")
	foreign := auditOf("other", "namespace:manage-sweeps")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	scan, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanState, ""), f, e)
	must(t, "start", err)

	bad := []struct {
		name  string
		fence namespace.Fence
	}{
		{"a fence of another namespace", other},
		{"a stale fence", stale(f)},
	}

	writes := []struct {
		name  string
		call  func(fence namespace.Fence, e audit.Entry) error
		check func(t *testing.T, what string)
	}{
		{
			name: "create a target",
			call: func(fence namespace.Fence, e audit.Entry) error {
				return h.Scans.CreateTarget(t.Context(), targetOf("acme", "second"), fence, e)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "update a target",
			call: func(fence namespace.Fence, e audit.Entry) error {
				changed := targetOf("acme", "aws")
				changed.Every = 0

				return h.Scans.UpdateTarget(t.Context(), changed, fence, e)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "delete a target",
			call: func(fence namespace.Fence, e audit.Entry) error {
				return h.Scans.DeleteTarget(t.Context(), "acme", "aws", fence, e)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "start a sweep",
			call: func(fence namespace.Fence, e audit.Entry) error {
				_, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), fence, e)

				return err
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantNoSweepOfTarget(t, h, what, "aws")
			},
		},
		{
			name: "advance a sweep",
			call: func(fence namespace.Fence, e audit.Entry) error {
				_, err := h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
					Namespace: "acme",
					ID:        scan.ID,
					Event:     unmanaged.ScanEventCancel,
					Update:    unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
				}, fence, e)

				return err
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantScanStatus(t, h, what, scan.ID, unmanaged.ScanQueued)
			},
		},
	}

	for _, tc := range writes {
		for _, b := range bad {
			what := tc.name + " under " + b.name

			wantErr(t, what, tc.call(b.fence, e), namespace.ErrFenced)
			tc.check(t, what)
		}
	}

	for _, tc := range writes {
		what := tc.name + " with an entry of another namespace"

		wantErr(t, what, tc.call(f, foreign), audit.ErrInvalidEntry)
		tc.check(t, what)
	}
}

// wantOnlyTarget checks that acme holds only the target aws, with the
// schedule of the fixture.
func wantOnlyTarget(t *testing.T, h Harness, what string) {
	t.Helper()

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, what+": targets", err)

	if len(got) != 1 || got[0].Name != "aws" || got[0].Every != 24*time.Hour {
		t.Errorf("%s: targets %+v, want the target aws with its schedule", what, got)
	}
}

// wantNoSweepOfTarget checks that acme holds no sweep of the target name.
func wantNoSweepOfTarget(t *testing.T, h Harness, what, target string) {
	t.Helper()

	got, err := h.Scans.Scans(t.Context(), "acme", target, "", unmanaged.MaxPage)
	must(t, what+": sweeps", err)

	if len(got) != 0 {
		t.Errorf("%s: sweeps %+v, want none of the target %s", what, got, target)
	}
}

// wantScanStatus checks the status of the sweep id of acme.
func wantScanStatus(t *testing.T, h Harness, what, id string, status unmanaged.ScanStatus) {
	t.Helper()

	got, err := h.Scans.Scan(t.Context(), "acme", id)
	must(t, what+": sweep", err)

	if got.Status != status {
		t.Errorf("%s: sweep status = %q, want %q", what, got.Status, status)
	}
}
