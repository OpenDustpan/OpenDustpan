package jobtest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// planPartCases returns the cases of BeginPlan, PutPlanPart and CommitPlan, and
// of the reserve that they repeat, that need no state write.
func planPartCases() []recorderCase {
	return []recorderCase{
		{"plan parts: an upload that repeats a stored part changes nothing", testPartsRepeat},
		{"plan parts: an upload with another hash than the stated one is refused and leaves no blob", testPartsBadHash},
		{"plan parts: a part above the size limit is ErrInvalid and leaves no blob", testPartsTooLarge},
		{"plan parts: a commit with a wrong hash, size or part changes nothing", testCommitMismatch},
		{"plan parts: parts of an uncommitted plan do not read", testPartsUncommittedUnreadable},
		{"plan parts: a claim that is not live is ErrClaimLost on each call", testPartsClaimLost},
		{"plan parts: a job cannot stage into or commit the plan of another job", testPartsOtherJob},
		{"plan parts: a repeated reserve is the same plan and makes no second key", testReserveRepeat},
		{"plan parts: invalid kinds and hashes are ErrInvalid", testPartsInvalidInput},
	}
}

// statePlanPartCases returns the cases of BeginPlan, PutPlanPart and
// CommitPlan that need a state write.
func statePlanPartCases() []recorderCase {
	return []recorderCase{
		{"plan parts: parts upload one by one and the commit stores the plan", testPartsRoundTrip},
		{
			"plan parts: a second commit with the same parts is the same plan, with other parts ErrConflict",
			testCommitTwice,
		},
		{"plan parts: a state write after the claim is ErrPlanStale on the commit", testCommitStale},
	}
}

// planCalls are the plan calls of the recorder under test.
type planCalls struct {
	job.PlanRecorder
}

// planRecorder returns the plan calls of the recorder of h, or fails t.
func planRecorder(t *testing.T, h RecorderHarness) planCalls {
	t.Helper()

	pr, ok := h.Recorder.(job.PlanRecorder)
	if !ok {
		t.Fatalf("the recorder %T does not implement job.PlanRecorder", h.Recorder)
	}

	return planCalls{PlanRecorder: pr}
}

// putPart stores the part k with the content body and its own hash.
func putPart(t *testing.T, h RecorderHarness, a *job.Assignment, k job.OutputKind, body string) {
	t.Helper()

	err := planRecorder(t, h).PutPlanPart(t.Context(), a.Claim, k, strings.NewReader(body), hashOf(body))
	if err != nil {
		t.Fatalf("PutPlanPart(%s) = %v", k, err)
	}
}

// standardClaim returns the parts of the commit for the standard contents.
func standardClaim() []job.PlanCommitPart {
	return []job.PlanCommitPart{
		{Kind: job.OutputPlanDisplay, Size: int64(len(displayJSON)), SHA256: hashOf(displayJSON)},
		{Kind: job.OutputPlanApply, Size: int64(len(applyBinary)), SHA256: hashOf(applyBinary)},
	}
}

// putStandardParts uploads both standard parts.
func putStandardParts(t *testing.T, h RecorderHarness, a *job.Assignment) {
	t.Helper()

	putPart(t, h, a, job.OutputPlanDisplay, displayJSON)
	putPart(t, h, a, job.OutputPlanApply, applyBinary)
}

// wantNoPlan fails t when the phase of a holds a plan.
func wantNoPlan(t *testing.T, h RecorderHarness, a *job.Assignment) {
	t.Helper()

	if got := jobOf(t, h.Harness, a.Claim.Job); got.Phases[a.Claim.Phase].Plan != nil {
		t.Fatalf("phase plan = %+v, want none", got.Phases[a.Claim.Phase].Plan)
	}
}

func testPartsRoundTrip(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	version := writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")
	pr := planRecorder(t, h)

	begun, err := pr.BeginPlan(t.Context(), a.Claim)
	if err != nil {
		t.Fatalf("BeginPlan = %v", err)
	}

	again, err := pr.BeginPlan(t.Context(), a.Claim)
	if err != nil || again.ID != begun.ID || again.ItemKey != begun.ItemKey || begun.ID != job.PlanIDOf(a.Claim) {
		t.Fatalf("second BeginPlan = %+v, %v; want %+v: one plan for one claim", again, err, begun)
	}

	putStandardParts(t, h, a)

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != planBlobs {
		t.Fatalf("blobs after the uploads = %d, want %d", n, planBlobs)
	}

	p, err := pr.CommitPlan(t.Context(), a.Claim, standardClaim())
	if err != nil {
		t.Fatalf("CommitPlan = %v", err)
	}

	if p.ID != begun.ID || p.ItemKey != begun.ItemKey || p.StateVersion != version {
		t.Fatalf("CommitPlan = %+v, want the begun plan %+v with state version %s", p, begun, version)
	}

	checkPlanParts(t, &p)
	checkPlanRecord(t, h, a, &p)
	assertPart(t, h, p.ID, job.OutputPlanDisplay, displayJSON)
	assertPart(t, h, p.ID, job.OutputPlanApply, applyBinary)
}

func testPartsRepeat(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")

	putPart(t, h, a, job.OutputPlanDisplay, displayJSON)
	putPart(t, h, a, job.OutputPlanDisplay, displayJSON)

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 1 {
		t.Fatalf("blobs after a repeated upload = %d, want 1", n)
	}

	putPart(t, h, a, job.OutputPlanApply, applyBinary)

	p, err := planRecorder(t, h).CommitPlan(t.Context(), a.Claim, standardClaim())
	if err != nil {
		t.Fatalf("CommitPlan = %v", err)
	}

	checkPlanParts(t, &p)
}

func testPartsBadHash(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")

	err := planRecorder(t, h).PutPlanPart(
		t.Context(), a.Claim, job.OutputPlanDisplay, strings.NewReader(displayJSON), hashOf("other content"),
	)
	if !errors.Is(err, job.ErrHashMismatch) {
		t.Fatalf("PutPlanPart with another hash = %v, want ErrHashMismatch", err)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after a refused upload = %d, want 0", n)
	}

	// The refused part does not count: a commit that names it finds no part.
	_, err = planRecorder(t, h).CommitPlan(t.Context(), a.Claim, standardClaim()[:1])
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CommitPlan of a refused part = %v, want ErrInvalid", err)
	}

	wantNoPlan(t, h, a)
}

func testPartsTooLarge(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	body := io.LimitReader(zeroReader{}, job.MaxPlanPart+1)

	err := planRecorder(t, h).PutPlanPart(t.Context(), a.Claim, job.OutputPlanDisplay, body, hashOf("x"))
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("PutPlanPart above the limit = %v, want ErrInvalid", err)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after a part above the limit = %d, want 0", n)
	}
}

// zeroReader reads zero bytes without end.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

func testCommitMismatch(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	putStandardParts(t, h, a)

	good := standardClaim()

	for _, tc := range []struct {
		name  string
		parts func() []job.PlanCommitPart
		want  error
	}{
		{"a wrong hash", func() []job.PlanCommitPart {
			p := standardClaim()
			p[1].SHA256 = hashOf("not the plan")

			return p
		}, job.ErrHashMismatch},
		{"a wrong size", func() []job.PlanCommitPart {
			p := standardClaim()
			p[0].Size++

			return p
		}, job.ErrHashMismatch},
		{
			"a kind that repeats", func() []job.PlanCommitPart { return []job.PlanCommitPart{good[0], good[0]} },
			job.ErrInvalid,
		},
	} {
		_, err := planRecorder(t, h).CommitPlan(t.Context(), a.Claim, tc.parts())
		if !errors.Is(err, tc.want) {
			t.Errorf("CommitPlan with %s = %v, want %v", tc.name, err, tc.want)
		}

		wantNoPlan(t, h, a)
	}

	// The staged parts stay: a commit with the right hashes still works.
	p, err := planRecorder(t, h).CommitPlan(t.Context(), a.Claim, good)
	if err != nil {
		t.Fatalf("CommitPlan after the refused commits = %v", err)
	}

	checkPlanParts(t, &p)
}

func testCommitTwice(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")
	putStandardParts(t, h, a)

	pr := planRecorder(t, h)

	first, err := pr.CommitPlan(t.Context(), a.Claim, standardClaim())
	if err != nil {
		t.Fatalf("CommitPlan = %v", err)
	}

	second, err := pr.CommitPlan(t.Context(), a.Claim, standardClaim())
	if err != nil || second.ID != first.ID || len(second.Parts) != len(first.Parts) {
		t.Fatalf("second CommitPlan = %+v, %v; want the first plan %s", second, err, first.ID)
	}

	other := standardClaim()[:1]
	if _, err = pr.CommitPlan(t.Context(), a.Claim, other); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("CommitPlan with other parts = %v, want ErrConflict", err)
	}

	other = standardClaim()
	other[0].SHA256 = hashOf("other display")

	if _, err = pr.CommitPlan(t.Context(), a.Claim, other); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("CommitPlan with another hash = %v, want ErrConflict", err)
	}

	// A part after the commit does not change the plan.
	err = pr.PutPlanPart(t.Context(), a.Claim, job.OutputPlanDisplay, strings.NewReader("late"), hashOf("late"))
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("PutPlanPart after the commit = %v, want ErrConflict", err)
	}

	checkPlanRecord(t, h, a, &first)
	assertPart(t, h, first.ID, job.OutputPlanDisplay, displayJSON)
}

func testPartsUncommittedUnreadable(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	putStandardParts(t, h, a)

	for _, k := range []job.OutputKind{job.OutputPlanDisplay, job.OutputPlanApply} {
		_, err := h.Reader.OpenPlanPart(t.Context(), "acme", job.PlanIDOf(a.Claim), k)
		if !errors.Is(err, job.ErrNotFound) {
			t.Errorf("OpenPlanPart(%s) of an uncommitted plan = %v, want ErrNotFound", k, err)
		}
	}
}

func testPartsClaimLost(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)

	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	putPart(t, h, first, job.OutputPlanDisplay, displayJSON)

	lapse(t, h.Harness, first)

	if _, err := claimAgainOf(t, h.Harness, runnerTwo, runnerKind(&j)); err != nil {
		t.Fatalf("takeover = %v", err)
	}

	pr := planRecorder(t, h)

	if _, err := pr.BeginPlan(t.Context(), first.Claim); !errors.Is(err, job.ErrClaimLost) {
		t.Errorf("BeginPlan by the replaced claim = %v, want ErrClaimLost", err)
	}

	err := pr.PutPlanPart(
		t.Context(), first.Claim, job.OutputPlanApply, strings.NewReader(applyBinary), hashOf(applyBinary),
	)
	if !errors.Is(err, job.ErrClaimLost) {
		t.Errorf("PutPlanPart by the replaced claim = %v, want ErrClaimLost", err)
	}

	if _, err = pr.CommitPlan(t.Context(), first.Claim, standardClaim()[:1]); !errors.Is(err, job.ErrClaimLost) {
		t.Errorf("CommitPlan by the replaced claim = %v, want ErrClaimLost", err)
	}

	wantNoPlan(t, h, first)
}

func testCommitStale(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")
	putStandardParts(t, h, a)

	writeState(t, h.Harness, f, false)

	_, err := planRecorder(t, h).CommitPlan(t.Context(), a.Claim, standardClaim())
	if !errors.Is(err, job.ErrPlanStale) {
		t.Fatalf("CommitPlan after a state write = %v, want ErrPlanStale", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.State != job.StateFailed || got.Phases[0].Plan != nil {
		t.Fatalf("job = %s, plan %+v; want failed, no plan", got.State, got.Phases[0].Plan)
	}
}

func testPartsOtherJob(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "one")
	b := openPlan(t, h, f, "two")
	pr := planRecorder(t, h)

	putStandardParts(t, h, a)

	if job.PlanIDOf(a.Claim) == job.PlanIDOf(b.Claim) {
		t.Fatalf("two claims of two jobs have the plan %s", job.PlanIDOf(a.Claim))
	}

	// The other job stages nothing, so it cannot commit the parts of the first.
	if _, err := pr.CommitPlan(t.Context(), b.Claim, standardClaim()); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CommitPlan of the other job = %v, want ErrInvalid", err)
	}

	wantNoPlan(t, h, b)

	// The store layer: a reserve of the other job for the plan of the first
	// job is refused, and so is a put with the claim of the other job.
	planA, err := pr.BeginPlan(t.Context(), a.Claim)
	if err != nil {
		t.Fatalf("BeginPlan = %v", err)
	}

	np := newPlanFor(t, h, b, recordingKey(nil))
	np.Plan.ID = planA.ID
	jobB := jobOf(t, h.Harness, b.Claim.Job)
	np.Ref = itemKeyOf(t, &jobB, secret.ItemKindPlan, planA.ID)

	if _, err = h.Records.ReservePlan(t.Context(), b.Claim, np); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("ReservePlan of the plan of another job = %v, want ErrConflict", err)
	}

	hostile := planA
	hostile.Parts = []job.PlanPart{{
		Kind: job.OutputPlanDisplay, Size: 1, SHA256: hashOf("x"), Secret: false, StoredSize: 0, StoredMD5: "",
	}}

	if _, err = h.Records.PutPlan(t.Context(), b.Claim, hostile); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("PutPlan of the plan of another job = %v, want ErrNotFound", err)
	}

	wantNoPlan(t, h, a)
	wantNoPlan(t, h, b)

	// The first job still commits its own plan.
	p, err := pr.CommitPlan(t.Context(), a.Claim, standardClaim())
	if err != nil || p.ID != planA.ID {
		t.Fatalf("CommitPlan of the first job = %+v, %v; want %s", p, err, planA.ID)
	}
}

func testReserveRepeat(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")

	var keys int

	counting := func(next job.NewItemKeyFunc) job.NewItemKeyFunc {
		return func(ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence) (secret.ItemKey, error) {
			keys++

			return next(ctx, tx, ref, fence)
		}
	}

	np := newPlanFor(t, h, a, counting(recordingKey(nil)))

	first, err := h.Records.ReservePlan(t.Context(), a.Claim, np)
	if err != nil {
		t.Fatalf("ReservePlan = %v", err)
	}

	second, err := h.Records.ReservePlan(t.Context(), a.Claim, np)
	if err != nil {
		t.Fatalf("second ReservePlan = %v", err)
	}

	// The store keeps a time at millisecond precision.
	if second.ID != first.ID || second.ItemKey != first.ItemKey ||
		!second.CreatedAt.Truncate(time.Millisecond).Equal(first.CreatedAt.Truncate(time.Millisecond)) ||
		len(second.Parts) != 0 {
		t.Fatalf("second ReservePlan = %+v, want the first reserve %+v", second, first)
	}

	if keys != 1 {
		t.Fatalf("item key functions called = %d, want 1: a repeat makes no second key", keys)
	}
}

func testPartsInvalidInput(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	pr := planRecorder(t, h)

	for name, tc := range map[string]struct {
		kind job.OutputKind
		body *strings.Reader
		sum  string
	}{
		"a log kind":       {job.OutputKind("log"), strings.NewReader("x"), hashOf("x")},
		"an empty kind":    {job.OutputKind(""), strings.NewReader("x"), hashOf("x")},
		"a path as kind":   {job.OutputKind("../x"), strings.NewReader("x"), hashOf("x")},
		"no hash":          {job.OutputPlanDisplay, strings.NewReader("x"), ""},
		"an upper hash":    {job.OutputPlanDisplay, strings.NewReader("x"), strings.ToUpper(hashOf("x"))},
		"a hash cut off":   {job.OutputPlanDisplay, strings.NewReader("x"), hashOf("x")[:10]},
		"a non hex hash":   {job.OutputPlanDisplay, strings.NewReader("x"), strings.Repeat("z", len(hashOf("x")))},
		"an apply no hash": {job.OutputPlanApply, strings.NewReader("x"), ""},
	} {
		if err := pr.PutPlanPart(t.Context(), a.Claim, tc.kind, tc.body, tc.sum); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("PutPlanPart with %s = %v, want ErrInvalid", name, err)
		}
	}

	noBody := pr.PutPlanPart(t.Context(), a.Claim, job.OutputPlanDisplay, nil, hashOf("x"))
	if !errors.Is(noBody, job.ErrInvalid) {
		t.Errorf("PutPlanPart with no body = %v, want ErrInvalid", noBody)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after invalid uploads = %d, want 0", n)
	}

	// Nothing is staged, so a commit that names a part finds none.
	if _, err := pr.CommitPlan(t.Context(), a.Claim, standardClaim()[:1]); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("CommitPlan of a part that was not uploaded = %v, want ErrInvalid", err)
	}

	for name, parts := range map[string][]job.PlanCommitPart{
		"an empty list": nil,
		"three parts":   {standardClaim()[0], standardClaim()[1], standardClaim()[1]},
		"a log kind":    {{Kind: "log", Size: 1, SHA256: hashOf("x")}},
		"a hash of two": {{Kind: job.OutputPlanDisplay, Size: 1, SHA256: "ab"}},
		"a negative":    {{Kind: job.OutputPlanDisplay, Size: -1, SHA256: hashOf("x")}},
		"a huge size":   {{Kind: job.OutputPlanDisplay, Size: job.MaxPlanPart + 1, SHA256: hashOf("x")}},
	} {
		if _, err := pr.CommitPlan(t.Context(), a.Claim, parts); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("CommitPlan with %s = %v, want ErrInvalid", name, err)
		}
	}
}
