package selfservicetest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func testSubmitCreates(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, rev := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	if !strings.HasPrefix(string(req.ID), selfservice.RequestIDPrefix) || req.Last != 1 || req.Applied != 0 ||
		req.Version == 0 {
		t.Fatalf("Submit() request = %+v, want a new ID, last 1 and a version", req)
	}

	if rev.Request != req.ID || rev.Number != 1 || rev.State != selfservice.StateAccepted {
		t.Fatalf("Submit() revision = %+v, want revision 1 accepted", rev)
	}

	wantStored(t, h, f, req.ID)
}

func wantStored(t *testing.T, h Harness, f fixture, id selfservice.RequestID) {
	t.Helper()

	if got := get(t, h); got.ID != id || got.Unit.UnitID() != f.unit.UnitID() || got.Home.Kind != selfservice.HomeGit {
		t.Fatalf("Get() = %+v, want the request with its unit and home", got)
	}

	stored, err := h.Requests.Revision(t.Context(), id, 1)
	if err != nil || string(stored.Document.Inputs["size"]) != `"small"` {
		t.Fatalf("Revision() = %+v, %v, want the document", stored, err)
	}
}

func testSubmitStarts(t *testing.T, h Harness, f fixture) {
	t.Helper()

	before := entries(t, h)
	_, rev := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	kind, ok, err := h.Domain.Operation(t.Context(), rev.Key())
	if err != nil || !ok || kind != selfservice.KindStart {
		t.Fatalf("Operation(%s) = %q, %v, %v, want %s", rev.Key(), kind, ok, err, selfservice.KindStart)
	}

	if after := entries(t, h); len(after) != len(before)+1 {
		t.Fatalf("Submit() appended %d audit entries, want 1", len(after)-len(before))
	}
}

func testNotFound(t *testing.T, h Harness, _ fixture) {
	t.Helper()

	if _, err := h.Requests.Get(t.Context(), ref()); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Get(unknown) = %v, want ErrNotFound", err)
	}

	if _, err := h.Requests.Revision(t.Context(), "req-unknown", 1); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Revision(unknown) = %v, want ErrNotFound", err)
	}
}

func testInFlight(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)
	before := entries(t, h)

	_, _, err := h.Requests.Submit(t.Context(), submission(f, req, selfservice.OperationUpdate, "large"), f.fence)
	if !errors.Is(err, selfservice.ErrInFlight) {
		t.Fatalf("Submit(in flight) = %v, want ErrInFlight", err)
	}

	if got := get(t, h); got.Last != 1 {
		t.Fatalf("Get() after ErrInFlight = last %d, want 1", got.Last)
	}

	if after := entries(t, h); len(after) != len(before) {
		t.Fatal("a failed submit appended its audit entries")
	}
}

func testCreateExists(t *testing.T, h Harness, f fixture) {
	t.Helper()

	submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	_, _, err := h.Requests.Submit(
		t.Context(),
		submission(f, none(), selfservice.OperationCreate, "small"),
		f.fence,
	)
	if !errors.Is(err, selfservice.ErrConflict) {
		t.Fatalf("Submit(a second create) = %v, want ErrConflict", err)
	}
}

func testSubmitStale(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)
	mustChange(t, h, f, change(&req, selfservice.StateApplied))

	// req holds the version of the request before the change.
	_, _, err := h.Requests.Submit(t.Context(), submission(f, req, selfservice.OperationUpdate, "large"), f.fence)
	if !errors.Is(err, selfservice.ErrConflict) {
		t.Fatalf("Submit(stale) = %v, want ErrConflict", err)
	}
}

func testChangeApplied(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	c := change(&req, selfservice.StateQueued)
	c.Job = "job-1"
	c.JobVersion = 1
	rev := mustChange(t, h, f, c)

	if rev.State != selfservice.StateQueued || rev.Job != "job-1" {
		t.Fatalf("Change(queued) = %+v, want queued with its job", rev)
	}

	req = get(t, h)
	mustChange(t, h, f, change(&req, selfservice.StateApplied))

	if got := get(t, h); got.Applied != 1 || got.Ensure != selfservice.EnsurePresent {
		t.Fatalf("Get() after applied = %+v, want applied 1 and present", got)
	}
}

func testChangeQuota(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, rev := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	c := change(&req, selfservice.StateRejected)
	c.Failures = []selfservice.Failure{{Input: "network", Code: selfservice.FailureChoice, Message: "Gone."}}
	got := mustChange(t, h, f, c)

	if len(got.Failures) != 1 {
		t.Fatalf("Change(rejected) = %+v, want the failures", got)
	}

	kind, ok, err := h.Domain.Operation(t.Context(), selfservice.QuotaKey(rev.Key()))
	if err != nil || !ok || kind != selfservice.KindQuota {
		t.Fatalf("Operation(quota) = %q, %v, %v, want %s", kind, ok, err, selfservice.KindQuota)
	}
}

func testChangeFinished(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)
	mustChange(t, h, f, change(&req, selfservice.StateCanceled))

	req = get(t, h)

	_, err := h.Requests.Change(t.Context(), change(&req, selfservice.StateRunning), f.fence, nil)
	if !errors.Is(err, selfservice.ErrFinished) {
		t.Fatalf("Change(canceled to running) = %v, want ErrFinished", err)
	}
}

func testChangeStale(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)
	mustChange(t, h, f, change(&req, selfservice.StateQueued))

	_, err := h.Requests.Change(t.Context(), change(&req, selfservice.StateRunning), f.fence, nil)
	if !errors.Is(err, selfservice.ErrConflict) {
		t.Fatalf("Change(stale) = %v, want ErrConflict", err)
	}
}

func testNextRevision(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)
	mustChange(t, h, f, change(&req, selfservice.StateApplied))

	req, rev := submit(t, h, submission(f, get(t, h), selfservice.OperationUpdate, "large"), f)
	if rev.Number != 2 || req.Last != 2 || req.Applied != 1 {
		t.Fatalf("Submit(second) = request %+v, revision %d, want last 2 and applied 1", req, rev.Number)
	}
}

func testSubmitFenced(t *testing.T, h Harness, f fixture) {
	t.Helper()

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence() = %v", err)
	}

	_, _, err := h.Requests.Submit(
		t.Context(),
		submission(f, none(), selfservice.OperationCreate, "small"),
		f.fence,
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Submit(stale fence) = %v, want ErrFenced", err)
	}

	if _, err := h.Requests.Get(t.Context(), ref()); !errors.Is(err, selfservice.ErrNotFound) {
		t.Fatalf("Get() after a fenced submit = %v, want ErrNotFound", err)
	}
}

func testObserve(t *testing.T, h Harness, f fixture) {
	t.Helper()

	s := submission(f, none(), selfservice.OperationCreate, "small")
	s.Revision.Origin = selfservice.OriginGit
	s.Revision.Commit = "c0ffee"
	req, _ := submit(t, h, s, f)
	mustChange(t, h, f, change(&req, selfservice.StateAwaitingState))

	at := time.Now().UTC()
	version := func(commit string, final bool) *job.StateVersion {
		return &job.StateVersion{Unit: f.unit, VersionID: "sv-" + commit, Commit: commit, Job: "", At: at, Final: final}
	}

	for _, v := range []*job.StateVersion{version("c0ffee", false), version("badcafe", true)} {
		if _, changed, err := h.Requests.ObserveStateVersion(t.Context(), *v, f.fence); err != nil || changed {
			t.Fatalf("ObserveStateVersion(%s, final %v) = %v, %v, want no change", v.Commit, v.Final, changed, err)
		}
	}

	rev, changed, err := h.Requests.ObserveStateVersion(t.Context(), *version("c0ffee", true), f.fence)
	if err != nil || !changed || rev.State != selfservice.StateApplied {
		t.Fatalf("ObserveStateVersion(c0ffee) = %+v, %v, %v, want applied", rev, changed, err)
	}

	if got := get(t, h); got.Applied != 1 {
		t.Fatalf("Get() = applied %d, want 1", got.Applied)
	}
}

func testList(t *testing.T, h Harness, f fixture) {
	t.Helper()

	submit(t, h, submission(f, none(), selfservice.OperationCreate, "small"), f)

	cases := []struct {
		filter selfservice.ListFilter
		want   int
	}{
		{
			filter: selfservice.ListFilter{Namespace: ns, Blueprint: "", UnitID: "", States: nil, After: "", Limit: 0},
			want:   1,
		},
		{
			filter: selfservice.ListFilter{
				Namespace: ns,
				Blueprint: "postgres",
				UnitID:    "",
				States:    nil,
				After:     "",
				Limit:     0,
			},
			want: 1,
		},
		{
			filter: selfservice.ListFilter{
				Namespace: ns,
				Blueprint: "redis",
				UnitID:    "",
				States:    nil,
				After:     "",
				Limit:     0,
			},
			want: 0,
		},
		{filter: selfservice.ListFilter{
			Namespace: ns, Blueprint: "", UnitID: f.unit.UnitID(), States: nil, After: "", Limit: 0,
		}, want: 1},
		{filter: selfservice.ListFilter{
			Namespace: ns,
			Blueprint: "",
			UnitID:    "",
			States:    []selfservice.RevisionState{selfservice.StateAccepted},
			After:     "",
			Limit:     0,
		}, want: 1},
		{filter: selfservice.ListFilter{
			Namespace: ns,
			Blueprint: "",
			UnitID:    "",
			States:    []selfservice.RevisionState{selfservice.StateApplied},
			After:     "",
			Limit:     0,
		}, want: 0},
		{
			filter: selfservice.ListFilter{
				Namespace: "other",
				Blueprint: "",
				UnitID:    "",
				States:    nil,
				After:     "",
				Limit:     0,
			},
			want: 0,
		},
	}

	for i := range cases {
		tc := &cases[i]

		got, err := h.Requests.ListRequests(t.Context(), tc.filter)
		if err != nil || len(got) != tc.want {
			t.Fatalf("List(%+v) = %d requests, %v, want %d", tc.filter, len(got), err, tc.want)
		}
	}
}

func mustChange(t *testing.T, h Harness, f fixture, c selfservice.RevisionChange) selfservice.Revision {
	t.Helper()

	rev, err := h.Requests.Change(t.Context(), c, f.fence, []audit.Entry{entry(selfservice.ActionSelfServiceRead, "x")})
	if err != nil {
		t.Fatalf("Change(%s) = %v", c.To, err)
	}

	return rev
}

func entries(t *testing.T, h Harness) []audit.Entry {
	t.Helper()

	got, err := h.Domain.AuditEntries(t.Context(), ns)
	if err != nil {
		t.Fatalf("AuditEntries() = %v", err)
	}

	return got
}
