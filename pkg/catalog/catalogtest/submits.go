package catalogtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/quota"
)

func unitsKey() quota.Key {
	return quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
}

func requestsKey() quota.Key {
	return quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindRequests, Scope: "requester:alice"}
}

// withQuota adds to s a reservation of the revision key key, at most
// maxUnits units, and at most maxRequests submits in one hour.
func withQuota(s *catalog.Submission, key string, maxUnits, maxRequests int) *catalog.Submission {
	at := time.Now().UTC()
	s.Revision.Deadline = catalog.RevisionDeadline(s.Revision.Origin, at)
	s.Request.QuotaKeys = []quota.Key{unitsKey()}
	s.Quota = &catalog.QuotaWrite{
		Reservation: &quota.Reservation{
			ID: quota.ReservationID(key), Namespace: ns, State: quota.ReservationOpen, Keys: []quota.Key{unitsKey()},
			At: at, EndsAt: s.Revision.Deadline,
		},
		Units:    []quota.Check{{Key: unitsKey(), Window: 0, Max: maxUnits}},
		Requests: []quota.Check{{Key: requestsKey(), Window: time.Hour, Max: maxRequests}},
	}

	return s
}

// wantQuota checks the reserved units and the submits in the window now.
func wantQuota(t *testing.T, h Harness, reserved, requests int) {
	t.Helper()

	checks := []quota.Check{
		{Key: unitsKey(), Window: 0, Max: 0},
		{Key: requestsKey(), Window: time.Hour, Max: 0},
	}

	got, err := h.Domain.QuotaUsage(t.Context(), checks, time.Now().UTC())
	if err != nil || len(got) != 2 {
		t.Fatalf("QuotaUsage() = %+v, %v", got, err)
	}

	if got[0].Reserved != reserved || got[1].Requests != requests {
		t.Fatalf("QuotaUsage() = %d reserved and %d requests, want %d and %d",
			got[0].Reserved, got[1].Requests, reserved, requests)
	}
}

func testSubmitQuota(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, rev := submit(t, h, withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 5, 5), f)

	if rev.Reservation != quota.ReservationID("first") {
		t.Fatalf("Submit() revision reservation = %q, want the reservation of the submit", rev.Reservation)
	}

	if got := get(t, h); len(got.QuotaKeys) != 1 || got.QuotaKeys[0] != unitsKey() || got.ID != req.ID {
		t.Fatalf("Get() = %+v, want the quota keys of the unit", got)
	}

	wantQuota(t, h, 1, 1)
}

func testSubmitQuotaFails(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 5, 5), f)
	before := entries(t, h)

	// A second create of the same request: ErrConflict after the store
	// could reserve.
	_, _, err := h.Requests.Submit(t.Context(),
		withQuota(submission(f, none(), catalog.OperationCreate, "small"), "second", 5, 5), f.fence)
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("Submit(a second create) = %v, want ErrConflict", err)
	}

	// An update while the create is in flight.
	s := submission(f, req, catalog.OperationUpdate, "large")
	s.Quota = &catalog.QuotaWrite{
		Reservation: nil, Units: nil,
		Requests: []quota.Check{{Key: requestsKey(), Window: time.Hour, Max: 5}},
	}

	if _, _, err := h.Requests.Submit(t.Context(), s, f.fence); !errors.Is(err, catalog.ErrInFlight) {
		t.Fatalf("Submit(in flight) = %v, want ErrInFlight", err)
	}

	wantQuota(t, h, 1, 1)

	if after := entries(t, h); len(after) != len(before) {
		t.Fatal("a failed submit appended its audit entries")
	}
}

func testSubmitExceeded(t *testing.T, h Harness, f fixture) {
	t.Helper()

	_, _, err := h.Requests.Submit(t.Context(),
		withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 0, 5), f.fence)
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("Submit(above the units limit) = %v, want ErrExceeded", err)
	}

	if _, getErr := h.Requests.Get(t.Context(), ref()); !errors.Is(getErr, catalog.ErrNotFound) {
		t.Fatalf("Get() after a refused submit = %v, want ErrNotFound", getErr)
	}

	wantQuota(t, h, 0, 0)

	_, _, err = h.Requests.Submit(t.Context(),
		withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 5, 0), f.fence)
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("Submit(above the requests limit) = %v, want ErrExceeded", err)
	}

	wantQuota(t, h, 0, 0)
}

func testSubmitDeadline(t *testing.T, h Harness, f fixture) {
	t.Helper()

	s := submission(f, none(), catalog.OperationCreate, "small")
	s.Revision.Deadline = time.Time{}

	if _, _, err := h.Requests.Submit(t.Context(), s, f.fence); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("Submit(no deadline) = %v, want ErrInvalid", err)
	}

	s = withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 5, 5)
	s.Quota.Reservation.EndsAt = s.Revision.Deadline.Add(-time.Hour)

	if _, _, err := h.Requests.Submit(t.Context(), s, f.fence); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("Submit(a reservation that ends before the deadline) = %v, want ErrInvalid", err)
	}

	wantQuota(t, h, 0, 0)
}

// gitSubmission returns a revision from git at commit that supersedes the
// revision n of req.
func gitSubmission(f fixture, req catalog.Request, commit string, n int) *catalog.Submission {
	s := submission(f, req, catalog.OperationCreate, "large")
	s.Revision.Origin = catalog.OriginGit
	s.Revision.Commit = commit
	s.Revision.Deadline = catalog.RevisionDeadline(catalog.OriginGit, time.Now().UTC().Add(time.Hour))
	s.Audit = nil
	s.Supersedes = n

	return s
}

func testSupersedeCreate(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, first := submit(t, h, withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 1, 5), f)

	c := change(&req, catalog.StateAwaitingMerge)
	c.Job, c.JobVersion = "job-1", 3
	mustChange(t, h, f, c)

	// The revision from git asks for no reservation: it takes over the one
	// of the create that it supersedes, with a limit of 1 that is full.
	_, rev := submit(t, h, gitSubmission(f, get(t, h), "c0ffee", 1), f)

	if rev.Number != 2 || rev.Origin != catalog.OriginGit || rev.Reservation != first.Reservation {
		t.Fatalf("Submit(git) = %+v, want revision 2 with the reservation of revision 1", rev)
	}

	wantSuperseded(t, h, f, req.ID, &first)

	// The reservation now ends at the deadline of the new revision.
	for _, tc := range []struct {
		at   time.Time
		want int
	}{{rev.Deadline.Add(-time.Minute), 1}, {rev.Deadline, 0}} {
		got, err := h.Domain.QuotaUsage(t.Context(), []quota.Check{{Key: unitsKey(), Window: 0, Max: 1}}, tc.at)
		if err != nil || len(got) != 1 || got[0].Reserved != tc.want {
			t.Fatalf("QuotaUsage(%v) = %+v, %v, want %d reserved", tc.at, got, err, tc.want)
		}
	}
}

// wantSuperseded checks that revision 1 of id, first, is superseded, and
// that its unit and its reservation stay with the request.
func wantSuperseded(t *testing.T, h Harness, f fixture, id catalog.RequestID, first *catalog.Revision) {
	t.Helper()

	old, err := h.Requests.Revision(t.Context(), id, 1)
	if err != nil || old.State != catalog.StateSuperseded {
		t.Fatalf("Revision(1) = %+v, %v, want superseded", old, err)
	}

	if got := get(t, h); got.Unit.UnitID() != f.unit.UnitID() {
		t.Fatalf("Get() unit = %v, want the unit of the create", got.Unit)
	}

	// The superseded create starts no quota operation: its unit and its
	// reservation belong to the new revision.
	if _, ok, err := h.Domain.Operation(t.Context(), catalog.QuotaKey(first.Key())); err != nil || ok {
		t.Fatalf("Operation(quota of revision 1) = %v, %v, want none", ok, err)
	}

	wantQuota(t, h, 1, 1)
}

func testSupersedeDelivered(t *testing.T, h Harness, f fixture) {
	t.Helper()

	req, _ := submit(t, h, submission(f, none(), catalog.OperationCreate, "small"), f)

	c := change(&req, catalog.StateAwaitingState)
	c.Job, c.JobVersion = "job-1", 3
	mustChange(t, h, f, c)

	_, _, err := h.Requests.Submit(t.Context(), gitSubmission(f, get(t, h), "c0ffee", 1), f.fence)
	if !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("Submit(supersede of a change in git) = %v, want ErrConflict", err)
	}

	// A supersede that names a revision that is not the last one.
	if _, _, err := h.Requests.Submit(t.Context(), gitSubmission(f, get(t, h), "c0ffee", 7), f.fence); !errors.Is(
		err, catalog.ErrConflict,
	) {
		t.Fatalf("Submit(supersede of revision 7) = %v, want ErrConflict", err)
	}

	if got := get(t, h); got.Last != 1 {
		t.Fatalf("Get() = last %d, want 1", got.Last)
	}
}

func testSubmitProvisions(t *testing.T, h Harness, f fixture) {
	t.Helper()

	provision := func(s *catalog.Submission) *catalog.Submission {
		s.Request.Unit = identity.Object{}
		s.Request.UnitName = "databases/from-git"
		s.Revision.Origin = catalog.OriginGit
		s.Revision.Commit = "c0ffee"
		s.Provision = &catalog.Provision{
			Request: "", Tool: "opentofu", Unit: "databases/from-git", Ref: ref(),
			Blueprint: catalog.BlueprintRef{Namespace: ns, Name: "postgres"}, Version: 1,
		}

		return s
	}

	// A submit that its quota refuses makes no unit.
	refused := provision(withQuota(submission(f, none(), catalog.OperationCreate, "small"), "first", 0, 5))
	if _, _, err := h.Requests.Submit(t.Context(), refused, f.fence); !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("Submit(refused) = %v, want ErrExceeded", err)
	}

	if _, ok, err := h.Domain.Unit(t.Context(), ns, "opentofu", "databases/from-git"); err != nil || ok {
		t.Fatalf("Unit() after a refused submit = %v, %v, want none", ok, err)
	}

	req, _ := submit(t, h, provision(submission(f, none(), catalog.OperationCreate, "small")), f)

	u, ok, err := h.Domain.Unit(t.Context(), ns, "opentofu", "databases/from-git")
	if err != nil || !ok || u.UnitID() == "" || req.Unit.UnitID() != u.UnitID() {
		t.Fatalf("Unit() = %v, %v, %v, want the unit of the request %v", u, ok, err, req.Unit)
	}

	// A git revision has no start operation: the render made its unit.
	if _, ok, err := h.Domain.Operation(t.Context(), catalog.RevisionKey(req.ID, 1)); err != nil || ok {
		t.Fatalf("Operation(start of a revision from git) = %v, %v, want none", ok, err)
	}
}
