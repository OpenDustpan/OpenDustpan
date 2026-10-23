package jobtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// deliveryCases returns the cases of Delivered.
func deliveryCases() []recorderCase {
	return []recorderCase{
		{"delivery: Delivered keeps the result and writes the audit entry in one write", testDeliveredRecords},
		{"delivery: Finish after Delivered moves the job to awaiting-state with the delivery", testDeliveredFinish},
		{"delivery: the same result twice changes nothing, another result is ErrConflict", testDeliveredTwice},
		{"delivery: a phase that is not a deliver phase is ErrInvalid", testDeliveredWrongPhase},
		{"delivery: a result that is not valid is ErrInvalid and changes nothing", testDeliveredInvalid},
		{"delivery: a review request result keeps its URL", testDeliveredReview},
	}
}

// openDeliver stores a write-back job on the unit unit-0 and claims its deliver
// phase.
func openDeliver(t *testing.T, h RecorderHarness, f fixture) *job.Assignment {
	t.Helper()

	deliverJob(t, h.Harness, f, "unit-0")

	return claimOf(t, h.Harness, "instance-a", time.Minute, job.KindWriteBack)
}

func testDeliveredRecords(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openDeliver(t, h, f)
	d := delivered(commitDef, job.DeliveryDirectPush)

	if err := h.Recorder.Delivered(t.Context(), a.Claim, d); err != nil {
		t.Fatalf("Delivered = %v", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Delivery == nil || !got.Delivery.Same(&d) || got.State != job.StateRunning {
		t.Fatalf("job after Delivered = %s, delivery %+v; want running with %+v", got.State, got.Delivery, d)
	}

	checkDeliveryEntry(t, h, f, a, &got)
}

// checkDeliveryEntry fails t unless the audit log of the namespace of f holds
// the entry of the delivery of the job of a.
func checkDeliveryEntry(t *testing.T, h RecorderHarness, f fixture, a *job.Assignment, got *job.Job) {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	for i := range entries {
		e := &entries[i]
		if e.Action != string(job.ActionUnitWriteBack) {
			continue
		}

		if e.JobID != string(a.Claim.Job) || e.Version != commitDef || e.Subject != "instance-a" ||
			e.Object != got.Spec.Unit.String() || e.ObjectID != got.Spec.Unit.UnitID() || e.Reason == "" {
			t.Fatalf("audit entry of the delivery = %+v, want the job, the commit and the instance", *e)
		}

		return
	}

	t.Fatalf("no audit entry of the action %s in %d entries", job.ActionUnitWriteBack, len(entries))
}

func testDeliveredFinish(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openDeliver(t, h, setUp(t, h.Harness))
	d := delivered(commitDef, job.DeliveryDirectPush)

	if err := h.Recorder.Delivered(t.Context(), a.Claim, d); err != nil {
		t.Fatalf("Delivered = %v", err)
	}

	if err := h.Claimer.Finish(t.Context(), a.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish after Delivered = %v", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.State != job.StateAwaitingState || got.Delivery == nil || !got.Delivery.Same(&d) {
		t.Fatalf("job after Finish = %s, delivery %+v; want awaiting-state with the delivery", got.State, got.Delivery)
	}
}

func testDeliveredTwice(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openDeliver(t, h, f)
	d := delivered(commitDef, job.DeliveryDirectPush)

	if err := h.Recorder.Delivered(t.Context(), a.Claim, d); err != nil {
		t.Fatalf("Delivered = %v", err)
	}

	cur := jobOf(t, h.Harness, a.Claim.Job)

	before, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	// A retry after a lost answer, or a takeover that finds the commit again.
	if err = h.Recorder.Delivered(t.Context(), a.Claim, d); err != nil {
		t.Fatalf("second Delivered with the same result = %v", err)
	}

	other := delivered("fedcba", job.DeliveryDirectPush)
	if err = h.Recorder.Delivered(t.Context(), a.Claim, other); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("Delivered with another result = %v, want ErrConflict", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Version != cur.Version || got.Delivery.Commit != commitDef {
		t.Fatalf(
			"job after repeats: version %d, commit %q; want %d, def456",
			got.Version,
			got.Delivery.Commit,
			cur.Version,
		)
	}

	after, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries after repeats = %d, %v; want %d", len(after), err, len(before))
	}
}

func testDeliveredWrongPhase(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "unit-0")
	b := openApply(t, h, f, "unit-1")

	for _, as := range []*job.Assignment{a, b} {
		err := h.Recorder.Delivered(t.Context(), as.Claim, delivered(commitDef, job.DeliveryDirectPush))
		if !errors.Is(err, job.ErrInvalid) {
			t.Errorf("Delivered in a %s phase = %v, want ErrInvalid", as.Job.Phases[as.Claim.Phase].Kind, err)
		}
	}

	if got := jobOf(t, h.Harness, a.Claim.Job); got.Delivery != nil {
		t.Fatalf("delivery of a plan job = %+v, want none", got.Delivery)
	}
}

func testDeliveredInvalid(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openDeliver(t, h, f)
	cur := jobOf(t, h.Harness, a.Claim.Job)

	good := delivered(commitDef, job.DeliveryDirectPush)

	mutate := func(fn func(d *job.DeliveryResult)) job.DeliveryResult {
		d := good
		fn(&d)

		return d
	}

	cases := map[string]job.DeliveryResult{
		"no commit":             mutate(func(d *job.DeliveryResult) { d.Commit = "" }),
		"a commit with a break": mutate(func(d *job.DeliveryResult) { d.Commit = "abc\ndef" }),
		"a commit that is too long": mutate(
			func(d *job.DeliveryResult) { d.Commit = string(make([]byte, longCommit)) },
		),
		"no branch":                mutate(func(d *job.DeliveryResult) { d.Branch = "" }),
		"an unknown delivery":      mutate(func(d *job.DeliveryResult) { d.Delivery = valueOther }),
		"a direct push with a URL": mutate(func(d *job.DeliveryResult) { d.ReviewRequest = "https://x.example/1" }),
		"a review request with no URL": mutate(func(d *job.DeliveryResult) {
			d.Delivery = job.DeliveryReviewRequest
		}),
		"a merge time":     mutate(func(d *job.DeliveryResult) { d.MergedAt = time.Now() }),
		"no delivery time": mutate(func(d *job.DeliveryResult) { d.DeliveredAt = time.Time{} }),
		"a hidden character": mutate(
			func(d *job.DeliveryResult) { d.Branch = "dustpan/" + string(rune(hiddenRune)) + "evil" },
		),
	}

	for what, d := range cases {
		if err := h.Recorder.Delivered(t.Context(), a.Claim, d); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("Delivered with %s = %v, want ErrInvalid", what, err)
		}
	}

	if got := jobOf(t, h.Harness, a.Claim.Job); got.Version != cur.Version || got.Delivery != nil {
		t.Fatalf("job after refused results: version %d, delivery %+v; want %d, none", got.Version, got.Delivery,
			cur.Version)
	}
}

func testDeliveredReview(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openDeliver(t, h, f)
	d := delivered(commitDef, job.DeliveryReviewRequest)

	if err := h.Recorder.Delivered(t.Context(), a.Claim, d); err != nil {
		t.Fatalf("Delivered = %v", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Delivery == nil || got.Delivery.ReviewRequest != d.ReviewRequest ||
		got.Delivery.Delivery != job.DeliveryReviewRequest {
		t.Fatalf("delivery = %+v, want the review request %s", got.Delivery, d.ReviewRequest)
	}
}
