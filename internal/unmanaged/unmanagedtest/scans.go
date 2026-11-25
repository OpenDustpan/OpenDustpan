package unmanagedtest

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// sweepStoreCases returns the cases of the sweep store.
func sweepStoreCases() []suiteCase {
	return []suiteCase{
		{"sweep store: fence", testSweepStoreFence},
		{"sweep store: pool", testSweepStorePool},
		{"sweep store: busy", testSweepStoreBusy},
		{"sweep store: transitions", testSweepStoreTransitions},
		{"sweep store: bounds", testSweepStoreBounds},
		{"sweep store: concurrent fence", testSweepStoreConcurrentFence},
		{"sweep store: rollback", testSweepStoreRollback},
		{"sweep store: rename", testSweepStoreRename},
	}
}

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
		{"sweeps: an audit entry of another namespace alone refuses each write", testScanEntryNamespace},
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

// testScanEntryNamespace proves that each write of the sweep store refuses an
// audit entry that names another namespace while the fence, the target and the
// sweep all name one namespace, and that the refused write changes nothing. The
// entry names a namespace that exists, so only the check of the store against
// the fence refuses it: the removal of that check fails a row of this test.
func testScanEntryNamespace(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	h.Namespace(t, "other")
	e := auditOf("acme", "namespace:manage-sweeps")
	foreign := auditOf("other", "namespace:manage-sweeps")

	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))

	scan, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanState, ""), f, e)
	must(t, "start", err)

	// The order keeps each row a proof of its own call: the delete row runs
	// after the start row, so a start that the check no longer refuses writes a
	// cloud sweep of the target before the delete can remove the target.
	for _, tc := range []struct {
		name  string
		call  func(t *testing.T, fence namespace.Fence) error
		check func(t *testing.T, what string)
	}{
		{
			name: "create a target",
			call: func(t *testing.T, fence namespace.Fence) error {
				t.Helper()

				return h.Scans.CreateTarget(t.Context(), targetOf("acme", "second"), fence, foreign)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "update a target",
			call: func(t *testing.T, fence namespace.Fence) error {
				t.Helper()

				changed := targetOf("acme", "aws")
				changed.Every = 0

				return h.Scans.UpdateTarget(t.Context(), changed, fence, foreign)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "start a sweep",
			call: func(t *testing.T, fence namespace.Fence) error {
				t.Helper()

				_, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), fence, foreign)

				return err
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantNoSweepOfTarget(t, h, what, "aws")
			},
		},
		{
			name: "delete a target",
			call: func(t *testing.T, fence namespace.Fence) error {
				t.Helper()

				return h.Scans.DeleteTarget(t.Context(), "acme", "aws", fence, foreign)
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantOnlyTarget(t, h, what)
			},
		},
		{
			name: "advance a sweep",
			call: func(t *testing.T, fence namespace.Fence) error {
				t.Helper()

				_, err := h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
					Namespace: "acme", ID: scan.ID, Event: unmanaged.ScanEventCancel,
					Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
				}, fence, foreign)

				return err
			},
			check: func(t *testing.T, what string) {
				t.Helper()

				wantScanStatus(t, h, what, scan.ID, unmanaged.ScanQueued)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			what := tc.name + " with an entry of another namespace"

			wantErr(t, what, tc.call(t, f), audit.ErrInvalidEntry)
			tc.check(t, what)
		})
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

func testSweepStoreFence(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")
	must(t, "create", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))
	wantErr(t, "stale create",
		h.Scans.CreateTarget(t.Context(), targetOf("acme", "b"), stale(f), e), namespace.ErrFenced)
	wantErr(t, "stale delete", h.Scans.DeleteTarget(t.Context(), "acme", "aws", stale(f), e), namespace.ErrFenced)
	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)
	if len(got) != 1 || got[0].Name != "aws" {
		t.Errorf("targets after stale fence writes: %+v", got)
	}

	// A namespace that has no row.
	ghost := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}
	wantErr(t, "missing namespace",
		h.Scans.CreateTarget(t.Context(), targetOf("ghost", "aws"), ghost, auditOf("ghost", "namespace:manage-sweeps")),
		namespace.ErrFenced)
	_, err = h.Scans.StartScan(t.Context(), scanOf("ghost", unmanaged.ScanState, ""), ghost,
		auditOf("ghost", "namespace:start-sweep"))
	wantErr(t, "start in a missing namespace", err, namespace.ErrFenced)

	// A namespace that a purge deleted.
	if h.PurgeNamespace != nil {
		gf := h.Namespace(t, "gone")
		h.PurgeNamespace(t, "gone")
		wantErr(t, "deleted namespace",
			h.Scans.CreateTarget(t.Context(), targetOf("gone", "aws"), gf, auditOf("gone", "namespace:manage-sweeps")),
			namespace.ErrFenced)
	}

	// A write in one namespace leaves the rows of another. The namespace other
	// holds a target of the same name as the target of acme that the writes
	// below change and delete.
	of := h.Namespace(t, "other")
	oe := auditOf("other", "namespace:manage-sweeps")
	must(t, "create in other", h.Scans.CreateTarget(t.Context(), targetOf("other", "aws"), of, oe))
	must(t, "create gcp in other", h.Scans.CreateTarget(t.Context(), targetOf("other", "gcp"), of, oe))
	before, err := h.Scans.Targets(t.Context(), "other")
	must(t, "targets of other before", err)
	if len(before) != 2 {
		t.Fatalf("targets of other before the writes to acme: %+v, want 2", before)
	}

	changed := targetOf("acme", "aws")
	changed.Enabled = false
	must(t, "update in acme", h.Scans.UpdateTarget(t.Context(), changed, f, e))
	must(t, "delete in acme", h.Scans.DeleteTarget(t.Context(), "acme", "aws", f, e))

	after, err := h.Scans.Targets(t.Context(), "other")
	must(t, "targets of other after", err)
	if !reflect.DeepEqual(after, before) {
		t.Errorf("targets of other after writes to acme: %+v, want %+v", after, before)
	}
}

func testSweepStorePool(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	f := h.Namespace(t, "acme")
	h.Namespace(t, "other")
	e := auditOf("acme", "namespace:manage-sweeps")
	jobs := targetOf("acme", "jobs")
	jobs.Pool = JobPool
	wantErr(t, "jobs pool", h.Scans.CreateTarget(t.Context(), jobs, f, e), unmanaged.ErrInvalidTarget)

	missing := targetOf("acme", "missing")
	missing.Pool = "missing-pool"
	wantErr(t, "unknown pool", h.Scans.CreateTarget(t.Context(), missing, f, e), unmanaged.ErrInvalidTarget)

	// A sweep pool that exists in another namespace only.
	if h.Pool != nil {
		h.Pool(t, "other", "other-sweepers", PurposeSweeps)
		foreign := targetOf("acme", "foreign")
		foreign.Pool = "other-sweepers"
		wantErr(t, "sweep pool of another namespace",
			h.Scans.CreateTarget(t.Context(), foreign, f, e), unmanaged.ErrInvalidTarget)
	}

	for _, pool := range []string{"", "Bad Pool!", "../pool", strings.Repeat("p", 300)} {
		bad := targetOf("acme", "bad-pool")
		bad.Pool = pool
		wantErr(t, fmt.Sprintf("malformed pool %.10q", pool),
			h.Scans.CreateTarget(t.Context(), bad, f, e), unmanaged.ErrInvalidTarget)
	}

	bad := targetOf("Bad Namespace!", "aws")
	wantErr(t, "malformed namespace",
		h.Scans.CreateTarget(t.Context(), bad, namespace.Fence{Namespace: bad.Namespace, FailoverVersion: 1},
			auditOf("acme", "namespace:manage-sweeps")), unmanaged.ErrInvalidTarget)

	ok := targetOf("acme", "aws")
	must(t, "sweep pool of team", h.Scans.CreateTarget(t.Context(), ok, f, e))

	moved := targetOf("acme", "aws")
	moved.Pool = JobPool
	wantErr(t, "update to jobs pool", h.Scans.UpdateTarget(t.Context(), moved, f, e), unmanaged.ErrInvalidTarget)

	// A pool that is deleted or changed after the save. Only an adapter without
	// a foreign key from targets to pools can hold this state.
	if h.SetPool != nil {
		se := auditOf("acme", "namespace:start-sweep")

		h.SetPool(t, "acme", Pool, PurposeJobs)
		_, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, se)
		wantErr(t, "start on a pool of another purpose", err, unmanaged.ErrInvalidTarget)
		wantNoSweepOfTarget(t, h, "start on a pool of another purpose", "aws")

		// The SQL adapters read the target joined to its pool, so a target
		// whose pool is gone reads as not found; the refusal and the absent
		// sweep are what the spec asks.
		h.SetPool(t, "acme", Pool, "")
		_, err = h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, se)
		if !errors.Is(err, unmanaged.ErrInvalidTarget) && !errors.Is(err, unmanaged.ErrNotFound) {
			t.Errorf("start on a deleted pool: %v, want ErrInvalidTarget or ErrNotFound", err)
		}
		wantNoSweepOfTarget(t, h, "start on a deleted pool", "aws")
	}
}

func testSweepStoreBusy(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")
	must(t, "create target", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))
	open, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
	must(t, "start", err)
	_, err = h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
	wantErr(t, "busy", err, unmanaged.ErrScanBusy)

	endEachWay(t, h, f, e, open)

	// Two simultaneous starts on one target: the second sweep is open now, so
	// end it and race on the free target.
	got, err := h.Scans.Scans(t.Context(), "acme", "aws", "", unmanaged.MaxPage)
	must(t, "sweeps", err)
	for i := range got {
		if got[i].Status.Terminal() {
			continue
		}
		_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
			Namespace: "acme", ID: got[i].ID, Event: unmanaged.ScanEventCancel,
			Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
		}, f, e)
		must(t, "end the open sweep", err)
	}

	wantOneConcurrentStart(t, h, f, e, len(got))
}

// endEachWay ends the open sweep of the target aws of acme in each end
// status, and checks that each ended sweep accepts a new start. The expired
// path is a dispatched sweep that receives the expire event. The last start
// stays open.
func endEachWay(t *testing.T, h Harness, f namespace.Fence, e audit.Entry, open unmanaged.Scan) {
	t.Helper()

	var err error

	for _, end := range []struct {
		status unmanaged.ScanStatus
		events []unmanaged.ScanEvent
	}{
		{unmanaged.ScanSucceeded, []unmanaged.ScanEvent{
			unmanaged.ScanEventDispatch, unmanaged.ScanEventClaim, unmanaged.ScanEventUploaded, unmanaged.ScanEventMatched,
		}},
		{unmanaged.ScanFailed, []unmanaged.ScanEvent{
			unmanaged.ScanEventDispatch, unmanaged.ScanEventClaim, unmanaged.ScanEventFail,
		}},
		{unmanaged.ScanCanceled, []unmanaged.ScanEvent{unmanaged.ScanEventCancel}},
		{unmanaged.ScanExpired, []unmanaged.ScanEvent{unmanaged.ScanEventDispatch, unmanaged.ScanEventExpire}},
	} {
		what := "end " + string(end.status)
		for _, ev := range end.events {
			reason := ""
			if ev == unmanaged.ScanEventFail {
				reason = "the command failed"
			}
			_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
				Namespace: "acme", ID: open.ID, Event: ev,
				Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: reason},
			}, f, e)
			must(t, what+": "+string(ev), err)
		}
		wantScanStatus(t, h, what, open.ID, end.status)

		open, err = h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
		must(t, "start after the sweep is "+string(end.status), err)
	}
}

// wantOneConcurrentStart races eight starts on the free target aws of acme,
// which has before sweeps, and checks that exactly one start succeeds.
func wantOneConcurrentStart(t *testing.T, h Harness, f namespace.Fence, e audit.Entry, before int) {
	t.Helper()

	const calls = 8
	errs := make([]error, calls)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			<-start
			_, errs[i] = h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
		})
	}
	close(start)
	wg.Wait()

	started := 0
	for i, err := range errs {
		switch {
		case err == nil:
			started++
		case !errors.Is(err, unmanaged.ErrScanBusy):
			t.Errorf("start %d: %v, want nil or ErrScanBusy", i, err)
		default:
			// A busy refusal: the other start won.
		}
	}
	if started != 1 {
		t.Errorf("%d concurrent starts succeeded, want exactly 1", started)
	}

	got, err := h.Scans.Scans(t.Context(), "acme", "aws", "", unmanaged.MaxPage)
	must(t, "sweeps", err)
	if len(got) != before+1 {
		t.Errorf("%d sweeps, want %d", len(got), before+1)
	}
}

func testSweepStoreTransitions(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	testScanTable(t, h)
	f := h.Namespace(t, "acme2")
	e := auditOf("acme2", "namespace:start-sweep")
	s, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanState, ""), f, e)
	must(t, "start", err)
	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme2", ID: s.ID, Event: unmanaged.ScanEventDispatch,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, f, e)
	wantErr(t, "dispatch a state check", err, unmanaged.ErrTransition)
	queued, err := h.Scans.Scan(t.Context(), "acme2", s.ID)
	must(t, "sweep after the refused dispatch", err)
	if queued.Status != unmanaged.ScanQueued {
		t.Errorf("status after the refused dispatch = %q, want %q", queued.Status, unmanaged.ScanQueued)
	}
	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme2", ID: s.ID, Event: unmanaged.ScanEventUploaded,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, f, e)
	must(t, "uploaded", err)
	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme2", ID: s.ID, Event: unmanaged.ScanEventMatched,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, f, e)
	must(t, "matched", err)
	_, err = h.Scans.AdvanceScan(t.Context(), unmanaged.ScanAdvance{
		Namespace: "acme2", ID: s.ID, Event: unmanaged.ScanEventUploaded,
		Update: unmanaged.ScanUpdate{Scopes: nil, Counts: nil, Reason: ""},
	}, f, e)
	wantErr(t, "event after end", err, unmanaged.ErrTransition)

	// The refused event keeps the status of the ended sweep.
	kept, err := h.Scans.Scan(t.Context(), "acme2", s.ID)
	must(t, "sweep after the refused event", err)
	if kept.Status != unmanaged.ScanSucceeded {
		t.Errorf("status after the refused event = %q, want %q", kept.Status, unmanaged.ScanSucceeded)
	}
}

func testSweepStoreBounds(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	testScansPageSize(t, h)

	// Only an adapter that holds the finding store runs the batch bound.
	if h.Findings == nil {
		return
	}

	// A second namespace: the page case above leaves a sweep open in acme.
	f := h.Namespace(t, "acme2")
	s := matchingScan(t, h, f, unmanaged.ScanState, nil)

	obs := make([]unmanaged.Observation, 0, unmanaged.MaxObservations+1)
	for i := range unmanaged.MaxObservations + 1 {
		obs = append(obs, observation(AWSKey(fmt.Sprintf("bucket-%04d", i)), unmanaged.ClassNotInAnyUnit))
	}

	_, err := h.Findings.Record(t.Context(), s, obs, at(), f)
	wantErr(t, "1001 observations", err, unmanaged.ErrLimit)

	none, err := h.Findings.List(
		t.Context(), "acme2",
		unmanaged.Filter{Unit: nil, Provider: "", Scope: "", Type: "", Kinds: nil, Statuses: nil},
		"", unmanaged.MaxPage,
	)
	must(t, "list", err)
	if len(none) != 0 {
		t.Errorf("%d findings after a refused batch, want none", len(none))
	}

	_, err = h.Findings.Record(t.Context(), s, obs[:unmanaged.MaxObservations], at(), f)
	must(t, "1000 observations", err)
}

func testSweepStoreConcurrentFence(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	f := h.Namespace(t, "acme")
	if h.Failover == nil {
		t.Skip("adapter cannot change the failover version")
	}
	e := auditOf("acme", "namespace:manage-sweeps")
	const calls = 8
	start := make(chan struct{})
	errs := make([]error, calls+1)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			<-start
			errs[i] = h.Scans.CreateTarget(t.Context(), targetOf("acme", fmt.Sprintf("t-%d", i)), f, e)
		})
	}
	// The failover version changes while the writes run. Failover returns its
	// error: a test may stop only on its own goroutine.
	var (
		live        namespace.Fence
		failoverErr error
	)
	wg.Go(func() {
		<-start
		live, failoverErr = h.Failover(t.Context(), "acme")
	})
	close(start)
	wg.Wait()
	must(t, "failover", failoverErr)

	created := 0
	for i := range calls {
		switch {
		case errs[i] == nil:
			created++
		case !errors.Is(errs[i], namespace.ErrFenced):
			t.Errorf("create %d: %v, want nil or ErrFenced", i, errs[i])
		default:
			// A fenced refusal: the failover version changed first.
		}
	}

	// After the change, the old fence is stale for every write.
	wantErr(t, "write with the old fence", h.Scans.CreateTarget(t.Context(), targetOf("acme", "late"), f, e),
		namespace.ErrFenced)
	must(t, "write with the new fence", h.Scans.CreateTarget(t.Context(), targetOf("acme", "late"), live, e))

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)
	if len(got) != created+1 {
		t.Errorf("%d targets, want %d: each refused write leaves no row", len(got), created+1)
	}
}

func testSweepStoreRollback(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:manage-sweeps")
	must(t, "create", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))
	wantErr(t, "bad fence", h.Scans.DeleteTarget(t.Context(), "acme", "aws", stale(f), e), namespace.ErrFenced)

	// Writes that fail after the fence check: the fence is live, and the
	// pool, the target or the sweep is refused inside the write.
	jobs := targetOf("acme", "on-jobs")
	jobs.Pool = JobPool
	wantErr(t, "jobs pool", h.Scans.CreateTarget(t.Context(), jobs, f, e), unmanaged.ErrInvalidTarget)
	_, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "none"), f,
		auditOf("acme", "namespace:start-sweep"))
	wantErr(t, "start on a missing target", err, unmanaged.ErrNotFound)
	wantErr(t, "update a missing target", h.Scans.UpdateTarget(t.Context(), targetOf("acme", "none"), f, e),
		unmanaged.ErrNotFound)

	got, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets", err)
	if len(got) != 1 || got[0].Name != "aws" {
		t.Errorf("rollback/fence changed rows: %+v", got)
	}
	wantNoSweepOfTarget(t, h, "refused writes", "none")
	wantNoSweepOfTarget(t, h, "refused writes", "aws")

	// A write that fails after the fence check and after it changed rows: the
	// delete removes the target and cancels its sweep, then the append of the
	// audit entry fails. The transaction rolls back both changes.
	if h.FailAudit == nil {
		return
	}

	s, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f,
		auditOf("acme", "namespace:start-sweep"))
	must(t, "start", err)

	h.FailAudit(t)

	if h.Scans.DeleteTarget(t.Context(), "acme", "aws", f, e) == nil {
		t.Fatal("delete with a forced error after the write: nil error, want an error")
	}
	if h.Scans.CreateTarget(t.Context(), targetOf("acme", "gcp"), f, e) == nil {
		t.Fatal("create with a forced error after the write: nil error, want an error")
	}

	got, err = h.Scans.Targets(t.Context(), "acme")
	must(t, "targets after the forced error", err)
	if len(got) != 1 || got[0].Name != "aws" {
		t.Errorf("targets after the forced error: %+v, want aws only", got)
	}
	wantScanStatus(t, h, "forced error", s.ID, unmanaged.ScanQueued)
}

// testSweepStoreRename renames a namespace and reads its rows by the new
// name: the rows key on the namespace ID, not on the name.
func testSweepStoreRename(t *testing.T, h Harness) {
	t.Helper()
	if h.Scans == nil {
		t.Skip("sweep store not wired")
	}
	if h.RenameNamespace == nil {
		t.Skip("adapter cannot rename a namespace")
	}
	f := h.Namespace(t, "acme")
	e := auditOf("acme", "namespace:start-sweep")
	must(t, "create", h.Scans.CreateTarget(t.Context(), targetOf("acme", "aws"), f, e))
	s, err := h.Scans.StartScan(t.Context(), scanOf(f.Namespace, unmanaged.ScanCloud, "aws"), f, e)
	must(t, "start", err)

	h.RenameNamespace(t, "acme", "acme-renamed")
	nf := h.Namespace(t, "acme-renamed")

	got, err := h.Scans.Targets(t.Context(), "acme-renamed")
	must(t, "targets after the rename", err)
	if len(got) != 1 || got[0].Name != "aws" || got[0].Pool != Pool {
		t.Errorf("targets after the rename: %+v, want aws on %s", got, Pool)
	}

	sweep, err := h.Scans.Scan(t.Context(), "acme-renamed", s.ID)
	must(t, "sweep after the rename", err)
	if sweep.Target != "aws" || sweep.Status != unmanaged.ScanQueued {
		t.Errorf("sweep after the rename: target %q, status %q, want aws, %q",
			sweep.Target, sweep.Status, unmanaged.ScanQueued)
	}

	old, err := h.Scans.Targets(t.Context(), "acme")
	must(t, "targets of the old name", err)
	if len(old) != 0 {
		t.Errorf("targets of the old name: %+v, want none", old)
	}

	// The open sweep still holds the target under the new name.
	ne := auditOf("acme-renamed", "namespace:start-sweep")
	_, err = h.Scans.StartScan(t.Context(), scanOf(nf.Namespace, unmanaged.ScanCloud, "aws"), nf, ne)
	wantErr(t, "start after the rename", err, unmanaged.ErrScanBusy)
}
