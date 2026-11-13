package jobtest

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// planCases returns the cases of PutPlan that need no state write.
func planCases() []recorderCase {
	return []recorderCase{
		{
			"plans: two plan phases get two plan IDs and two sets of part keys, and both plans read back",
			testPlanTwoPhases,
		},
		{"plans: a second plan for the phase is ErrConflict and leaves the first", testPlanTwice},
		{"plans: no part, an unknown kind, a kind twice or no content is ErrInvalid", testPlanInvalidParts},
		{"plans: a phase that is not a plan phase is ErrInvalid and leaves no blob", testPlanWrongPhase},
		{"plans: a plan is stored during a cancel", testPlanCancel},
		{"plans: the item key of a plan is of the kind plan", testPlanItemKey},
		{"plans: parallel PutPlan calls give one plan", testPlanParallel},
		{"plans: the store refuses hostile parts and keeps the times of the reserve", testPlanHostileRef},
	}
}

// statePlanCases returns the cases of PutPlan that need a state write.
func statePlanCases() []recorderCase {
	return []recorderCase{
		{"plans: PutPlan stores the plan with the hashes, the state version and the retention", testPlanPut},
		{"plans: a state write after the claim fails the phase with ErrPlanStale", testPutPlanStale},
		{"plans: a takeover records the state version again, and the plan reads it", testPlanTakeover},
		{"plans: PutPlan and a state write in parallel agree", testPlanAndStateWrite},
	}
}

// Contents of the plans of the cases.
const (
	displayJSON = `{"changes": 3}`
	applyBinary = "binary plan file"
)

// putPlan stores a plan with the standard contents and fails t on an error.
func putPlan(t *testing.T, h RecorderHarness, a *job.Assignment) job.PlanRef {
	t.Helper()

	p, err := h.Recorder.PutPlan(t.Context(), a.Claim, planParts(displayJSON, applyBinary))
	if err != nil {
		t.Fatalf("PutPlan = %v", err)
	}

	return p
}

func testPlanPut(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	version := writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")
	started := time.Now().Add(-time.Minute)

	p := putPlan(t, h, a)

	if !strings.HasPrefix(p.ID, job.PlanIDPrefix) || p.ItemKey == "" || p.StateVersion != version ||
		len(p.Parts) != planBlobs || p.CreatedAt.Before(started) || !p.RedactedAt.IsZero() ||
		!p.ExpiresAt.Equal(history.ExpiresAt(p.CreatedAt, h.Retention.Plan)) {
		t.Fatalf("PutPlan = %+v, want pln-, an item key, state version %s, two parts, retention", p, version)
	}

	checkPlanParts(t, &p)
	checkPlanRecord(t, h, a, &p)

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != planBlobs {
		t.Fatalf("plan blobs = %d, want %d", n, planBlobs)
	}
}

// checkPlanRecord fails t unless the phase of the job of a holds the plan p
// with its state version and its retention, and the binding of the plan has
// both hashes.
func checkPlanRecord(t *testing.T, h RecorderHarness, a *job.Assignment, p *job.PlanRef) {
	t.Helper()

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Phases[0].Plan == nil || got.Phases[0].Plan.ID != p.ID || got.Phases[0].StateVersion != p.StateVersion ||
		got.Phases[0].Plan.ExpiresAt.IsZero() {
		t.Fatalf("phase = %+v, want the plan %s with state version %s and its retention", got.Phases[0], p.ID,
			p.StateVersion)
	}

	if b := got.Phases[0].Plan.Binding(); b.Plan != p.ID || b.DisplaySHA256 != hashOf(displayJSON) ||
		b.ApplySHA256 != hashOf(applyBinary) || b.StateVersion != p.StateVersion {
		t.Fatalf("binding = %+v, want the plan, both hashes and the state version", b)
	}
}

// checkPlanParts fails t unless p has the readable part and the apply part with
// the sizes and hashes of their plaintext, and only the apply part is marked
// secret.
func checkPlanParts(t *testing.T, p *job.PlanRef) {
	t.Helper()

	display, apply := p.Part(job.OutputPlanDisplay), p.Part(job.OutputPlanApply)
	if display == nil || apply == nil {
		t.Fatalf("parts = %+v, want a display part and an apply part", p.Parts)
	}

	if display.Size != int64(len(displayJSON)) || display.SHA256 != hashOf(displayJSON) || display.Secret ||
		apply.Size != int64(len(applyBinary)) || apply.SHA256 != hashOf(applyBinary) || !apply.Secret {
		t.Fatalf("parts = %+v, want sizes and hashes of the plaintext, and the apply part marked secret", p.Parts)
	}
}

// testPlanTwoPhases stores a plan in each of two plan phases and checks that
// the plans keep their own ID, their own part keys and their own content.
func testPlanTwoPhases(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	first := openPlan(t, h, f, "unit-0")
	second := openPlan(t, h, f, "unit-1")

	one := putPlan(t, h, first)

	two, err := h.Recorder.PutPlan(t.Context(), second.Claim, planParts("other display", "other apply"))
	if err != nil {
		t.Fatalf("PutPlan = %v", err)
	}

	if one.ID == two.ID {
		t.Fatalf("plan IDs = %q and %q, want two IDs", one.ID, two.ID)
	}

	firstKeys := []string{
		planKey(t, first.Claim.Job, one.ID, job.OutputPlanDisplay),
		planKey(t, first.Claim.Job, one.ID, job.OutputPlanApply),
	}

	secondKeys := []string{
		planKey(t, second.Claim.Job, two.ID, job.OutputPlanDisplay),
		planKey(t, second.Claim.Job, two.ID, job.OutputPlanApply),
	}

	for _, key := range firstKeys {
		for _, other := range secondKeys {
			if key == other {
				t.Fatalf("part key %s of both plans, want two sets of keys", key)
			}
		}
	}

	assertPart(t, h, one.ID, job.OutputPlanDisplay, displayJSON)
	assertPart(t, h, one.ID, job.OutputPlanApply, applyBinary)
	assertPart(t, h, two.ID, job.OutputPlanDisplay, "other display")
	assertPart(t, h, two.ID, job.OutputPlanApply, "other apply")
}

func testPlanTwice(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	first := putPlan(t, h, a)

	_, err := h.Recorder.PutPlan(t.Context(), a.Claim, planParts("other display", "other apply"))
	if !errors.Is(err, job.ErrConflict) {
		t.Fatalf("second PutPlan = %v, want ErrConflict", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Phases[0].Plan == nil || got.Phases[0].Plan.ID != first.ID {
		t.Fatalf("plan after a refused second plan = %+v, want %s", got.Phases[0].Plan, first.ID)
	}

	// The blobs of the refused plan are gone. The first plan reads as before.
	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != planBlobs {
		t.Fatalf("plan blobs after a refused plan = %d, want 2", n)
	}

	assertPart(t, h, first.ID, job.OutputPlanDisplay, displayJSON)
}

// assertPart fails t unless the part k of the plan reads as want.
func assertPart(t *testing.T, h RecorderHarness, plan string, k job.OutputKind, want string) {
	t.Helper()

	rc, err := h.Reader.OpenPlanPart(t.Context(), "acme", plan, k)
	if err != nil {
		t.Fatalf("OpenPlanPart(%s, %s) = %v", plan, k, err)
	}

	got, err := readAll(rc)
	if err != nil || string(got) != want {
		t.Fatalf("part %s of %s = %q, %v; want %q", k, plan, got, err, want)
	}
}

func testPlanInvalidParts(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	cur := versionOf(t, h, a)

	body := func() *bytes.Reader { return bytes.NewReader([]byte("x")) }

	cases := map[string][]job.PlanUpload{
		"no part":         nil,
		"an unknown kind": {{Kind: valueOther, Body: body()}},
		"a kind twice": {
			{Kind: job.OutputPlanDisplay, Body: body()}, {Kind: job.OutputPlanDisplay, Body: body()},
		},
		"no content": {{Kind: job.OutputPlanDisplay, Body: nil}},
	}

	for what, parts := range cases {
		if _, err := h.Recorder.PutPlan(t.Context(), a.Claim, parts); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("PutPlan with %s = %v, want ErrInvalid", what, err)
		}
	}

	if got := versionOf(t, h, a); got != cur {
		t.Fatalf("job version after refused plans = %d, want %d", got, cur)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after refused plans = %d, want 0", n)
	}

	// A refused plan does not use the phase: a valid plan still goes in.
	putPlan(t, h, a)
}

func testPlanWrongPhase(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	apply := openApply(t, h, f, "unit-0")

	if _, err := h.Recorder.PutPlan(t.Context(), apply.Claim, planParts("a", "b")); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("PutPlan in an apply phase = %v, want ErrInvalid", err)
	}

	deliverJob(t, h.Harness, f, "unit-1")
	deliver := claimOf(t, h.Harness, "instance-a", time.Minute, job.KindWriteBack)

	if _, err := h.Recorder.PutPlan(t.Context(), deliver.Claim, planParts("a", "b")); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("PutPlan in a deliver phase = %v, want ErrInvalid", err)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after plans in the wrong phase = %d, want 0", n)
	}
}

func testPutPlanStale(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	first := writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")

	// A state write lands during the plan phase.
	second := writeState(t, h.Harness, f, false)

	_, err := h.Recorder.PutPlan(t.Context(), a.Claim, planParts("a", "b"))
	if !errors.Is(err, job.ErrPlanStale) {
		t.Fatalf("PutPlan after a state write = %v, want ErrPlanStale", err)
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.State != job.StateFailed || got.Phases[0].Plan != nil ||
		!strings.Contains(got.Reason, first) || !strings.Contains(got.Reason, second) {
		t.Fatalf("job = %s, plan %+v, reason %q; want failed, no plan, a reason that names both versions",
			got.State, got.Phases[0].Plan, got.Reason)
	}

	if op := opState(t, h.Harness, a.Claim); op.State != durable.StateFailed {
		t.Fatalf("operation after a stale plan = %s, want failed", op.State)
	}

	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != 0 {
		t.Fatalf("blobs after a stale plan = %d, want 0", n)
	}

	// The phase ended, so the claim is gone.
	if _, err = h.Recorder.PutPlan(t.Context(), a.Claim, planParts("a", "b")); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("PutPlan after the failed phase = %v, want ErrClaimLost", err)
	}
}

func testPlanTakeover(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	current := writeState(t, h.Harness, f, false)

	lapse(t, h.Harness, first)

	second, err := claimAgainOf(t, h.Harness, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	// The first claim read the old version and lost its claim.
	if _, err = h.Recorder.PutPlan(t.Context(), first.Claim, planParts("a", "b")); !errors.Is(err, job.ErrClaimLost) {
		t.Fatalf("PutPlan by the replaced claim = %v, want ErrClaimLost", err)
	}

	p := putPlan(t, h, second)
	if p.StateVersion != current {
		t.Fatalf("plan state version = %q, want %q: the takeover read it again", p.StateVersion, current)
	}
}

func testPlanCancel(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	cancelJob(t, h.Harness, f, a.Claim.Job)

	p := putPlan(t, h, a)

	if got := jobOf(t, h.Harness, a.Claim.Job); got.State != job.StateCanceling || got.Phases[0].Plan == nil ||
		got.Phases[0].Plan.ID != p.ID {
		t.Fatalf("job after a plan during a cancel = %s, plan %+v; want canceling with the plan", got.State,
			got.Phases[0].Plan)
	}
}

func testPlanItemKey(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openPlan(t, h, setUp(t, h.Harness), "")
	p := putPlan(t, h, a)
	j := jobOf(t, h.Harness, a.Claim.Job)

	k, err := h.ItemKeys.GetItemKey(t.Context(), itemKeyOf(t, &j, secret.ItemKindPlan, p.ID))
	if err != nil || k.ID != p.ItemKey || k.Shredded() {
		t.Fatalf("item key of the plan = %+v, %v; want the live key %s", k, err, p.ItemKey)
	}
}

func testPlanParallel(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")

	const writers = 6

	plans := make([]job.PlanRef, writers)
	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		display := strings.Repeat(string(rune('a'+i)), repeatPlan)
		apply := strings.Repeat(string(rune('A'+i)), repeatPlan)

		wg.Go(func() { plans[i], errs[i] = h.Recorder.PutPlan(t.Context(), a.Claim, planParts(display, apply)) })
	}

	wg.Wait()

	winner := -1

	for i, err := range errs {
		switch {
		case err == nil && winner < 0:
			winner = i
		case err == nil:
			t.Errorf("writers %d and %d both stored a plan", winner, i)
		case !errors.Is(err, job.ErrConflict):
			t.Errorf("writer %d: PutPlan = %v, want nil or ErrConflict", i, err)
		default:
		}
	}

	if winner < 0 {
		t.Fatal("no writer stored a plan")
	}

	got := jobOf(t, h.Harness, a.Claim.Job)
	if got.Phases[0].Plan == nil || got.Phases[0].Plan.ID != plans[winner].ID {
		t.Fatalf("plan of the job = %+v, want the plan of writer %d", got.Phases[0].Plan, winner)
	}

	// Only the blobs of the winner stay, and they read as its content.
	if n := len(blobsUnder(t, h, f.fence.Namespace)); n != planBlobs {
		t.Fatalf("plan blobs = %d, want the 2 of the winner", n)
	}

	assertPart(t, h, plans[winner].ID, job.OutputPlanDisplay, strings.Repeat(string(rune('a'+winner)), repeatPlan))
	assertPart(t, h, plans[winner].ID, job.OutputPlanApply, strings.Repeat(string(rune('A'+winner)), repeatPlan))
}

func testPlanAndStateWrite(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	first := writeState(t, h.Harness, f, false)

	a := openPlan(t, h, f, "")

	if h.WriteState == nil {
		t.Fatal("the harness gives no state writer")
	}

	var (
		wg       sync.WaitGroup
		planErr  error
		stateErr error
	)

	wg.Go(func() { _, planErr = h.Recorder.PutPlan(t.Context(), a.Claim, planParts("a", "b")) })
	wg.Go(func() { _, stateErr = h.WriteState(t.Context(), f.fence, f.unit, false) })
	wg.Wait()

	if stateErr != nil {
		t.Fatalf("WriteState = %v", stateErr)
	}

	// Either the plan went in before the state write, or the state write came
	// first and the phase failed. Never a plan that read another version, never
	// a phase that failed with a plan stored.
	got := jobOf(t, h.Harness, a.Claim.Job)

	switch {
	case planErr == nil:
		if got.Phases[0].Plan == nil || got.Phases[0].Plan.StateVersion != first ||
			got.State != job.StateRunning {
			t.Fatalf("plan stored: job %s, plan %+v; want running with a plan of state version %s", got.State,
				got.Phases[0].Plan, first)
		}
	case errors.Is(planErr, job.ErrPlanStale):
		if got.State != job.StateFailed || got.Phases[0].Plan != nil {
			t.Fatalf("plan stale: job %s, plan %+v; want failed with no plan", got.State, got.Phases[0].Plan)
		}
	default:
		t.Fatalf("PutPlan = %v, want nil or ErrPlanStale", planErr)
	}
}

// planReadCases returns the cases of the reads of plans that need no state
// write.
func planReadCases() []recorderCase {
	return []recorderCase{
		{"plan reads: both parts read as their content", testPlanReadParts},
		{"plan reads: an unknown plan, part or namespace is ErrNotFound", testPlanReadUnknown},
		{"plan reads: a shredded key is ErrShredded, also with no blob left", testPlanReadShredded},
		{"plan reads: a damaged or moved part blob does not read as content", testPlanReadDamaged},
	}
}

// statePlanReadCases returns the cases of the reads of plans that need a
// state write.
func statePlanReadCases() []recorderCase {
	return []recorderCase{
		{"plan reads: a replan clears the phase and the old plan still reads", testPlanReadAfterReplan},
		{
			"plan reads: a second plan of the same job after a replan gets its own part keys, and both plans read back",
			testPlanSecondPlanAfterReplan,
		},
	}
}

func testPlanReadParts(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openPlan(t, h, setUp(t, h.Harness), "")
	p := putPlan(t, h, a)

	assertPart(t, h, p.ID, job.OutputPlanDisplay, displayJSON)
	assertPart(t, h, p.ID, job.OutputPlanApply, applyBinary)
}

func testPlanReadUnknown(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openPlan(t, h, setUp(t, h.Harness), "")

	one, err := h.Recorder.PutPlan(t.Context(), a.Claim, []job.PlanUpload{
		{Kind: job.OutputPlanDisplay, Body: strings.NewReader("only display")},
	})
	if err != nil {
		t.Fatalf("PutPlan = %v", err)
	}

	_, err = h.Reader.OpenPlanPart(t.Context(), "acme", one.ID, job.OutputPlanApply)
	if !errors.Is(
		err,
		job.ErrNotFound,
	) {
		t.Errorf("OpenPlanPart of a part that the plan has not = %v, want ErrNotFound", err)
	}

	if _, err = h.Reader.OpenPlanPart(t.Context(), "acme", one.ID, valueOther); !errors.Is(err, job.ErrNotFound) {
		t.Errorf("OpenPlanPart of an unknown kind = %v, want ErrNotFound", err)
	}

	for _, id := range []string{"pln-unknown", "", "jlv-x", "../x"} {
		_, err = h.Reader.OpenPlanPart(t.Context(), "acme", id, job.OutputPlanDisplay)
		if !errors.Is(
			err,
			job.ErrNotFound,
		) &&
			!errors.Is(err, job.ErrInvalid) {
			t.Errorf("OpenPlanPart(%q) = %v, want ErrNotFound", id, err)
		}
	}

	if _, err = h.Domain.CreateNamespace(t.Context(), valueOther); err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	_, err = h.Reader.OpenPlanPart(t.Context(), valueOther, one.ID, job.OutputPlanDisplay)
	if !errors.Is(
		err,
		job.ErrNotFound,
	) {
		t.Errorf("OpenPlanPart of a plan of another namespace = %v, want ErrNotFound", err)
	}
}

func testPlanReadShredded(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	p := putPlan(t, h, a)
	j := jobOf(t, h.Harness, a.Claim.Job)

	_, err := h.ItemKeys.ShredItemKey(t.Context(), itemKeyOf(t, &j, secret.ItemKindPlan, p.ID), time.Now(), f.fence)
	if err != nil {
		t.Fatalf("ShredItemKey = %v", err)
	}

	for _, k := range []job.OutputKind{job.OutputPlanDisplay, job.OutputPlanApply} {
		_, openErr := h.Reader.OpenPlanPart(t.Context(), "acme", p.ID, k)
		if !errors.Is(openErr, secret.ErrShredded) {
			t.Errorf("OpenPlanPart(%s) of a shredded plan = %v, want ErrShredded", k, openErr)
		}
	}

	for _, o := range blobsUnder(t, h, f.fence.Namespace) {
		if delErr := h.Blobs.Delete(t.Context(), o.Key); delErr != nil {
			t.Fatalf("delete blob: %v", delErr)
		}
	}

	_, err = h.Reader.OpenPlanPart(t.Context(), "acme", p.ID, job.OutputPlanDisplay)
	if !errors.Is(
		err,
		secret.ErrShredded,
	) {
		t.Errorf("OpenPlanPart of a shredded plan with no blob = %v, want ErrShredded", err)
	}
}

func testPlanReadDamaged(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	p := putPlan(t, h, a)

	var dKey, aKey blob.Key

	objects := blobsUnder(t, h, f.fence.Namespace)
	for _, o := range objects {
		switch {
		case strings.HasSuffix(o.Key.String(), "/"+string(job.OutputPlanDisplay)):
			dKey = o.Key
		case strings.HasSuffix(o.Key.String(), "/"+string(job.OutputPlanApply)):
			aKey = o.Key
		default:
		}
	}

	if dKey == "" || aKey == "" {
		t.Fatalf("plan blobs = %v, want a display and an apply part", objects)
	}

	goodDisplay, goodApply := rawBlob(t, h, dKey), rawBlob(t, h, aKey)

	flipped := bytes.Clone(goodDisplay)
	flipped[len(flipped)-1] ^= 0x01

	cases := []struct {
		name    string
		display []byte
		apply   []byte
	}{
		{"a flipped byte", flipped, goodApply},
		{"parts swapped", goodApply, goodDisplay},
		{"a cut part", goodDisplay[:len(goodDisplay)-4], goodApply},
	}

	for _, tc := range cases {
		replaceRaw(t, h, dKey, tc.display)
		replaceRaw(t, h, aKey, tc.apply)

		rc, err := h.Reader.OpenPlanPart(t.Context(), "acme", p.ID, job.OutputPlanDisplay)
		if err == nil {
			_, err = readAll(rc)
		}

		if !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("read of the display part with %s = %v, want ErrDecrypt", tc.name, err)
		}
	}
}

func testPlanReadAfterReplan(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationApply, steps: nil})
	a := claimOf(t, h.Harness, runnerOne, time.Minute, runnerKind(&j))
	p := putPlan(t, h, a)

	if err := h.Claimer.Finish(t.Context(), a.Claim, job.Outcome{Succeeded: true, Message: ""}); err != nil {
		t.Fatalf("Finish = %v", err)
	}

	writeState(t, h.Harness, f, false)

	cur := jobOf(t, h.Harness, j.ID)
	c := dispatch(&cur)
	c.PlanNotBefore = time.Now().Add(-job.DefaultMaxPlanAge)

	if _, err := h.Jobs.Apply(t.Context(), c, f.fence, nil); !errors.Is(err, job.ErrPlanStale) {
		t.Fatalf("dispatch of the apply phase after a state write = %v, want ErrPlanStale", err)
	}

	back := apply(t, h.Harness, f, change(&cur, job.EventReplan))
	if back.Phases[0].Plan != nil {
		t.Fatalf("plan after a replan = %+v, want none", back.Phases[0].Plan)
	}

	// The old plan stays a history item until its retention ends.
	assertPart(t, h, p.ID, job.OutputPlanDisplay, displayJSON)
}

// testPlanSecondPlanAfterReplan stores a plan in a plan phase, replans the
// job and stores a second plan in the same phase of the same job. The phase
// runs again under a second claim of its operation, so that both plans
// belong to one job. The two plans must keep their own part keys, and both
// must read back.
func testPlanSecondPlanAfterReplan(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationApply, steps: nil})
	a := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))
	p := putPlan(t, h, a)

	// The phase ends without ending its operation: the replan below gives
	// the phase back to a new claim of that operation, and a completed
	// operation cannot be claimed again.
	cur := jobOf(t, h.Harness, j.ID)
	cur = apply(t, h.Harness, f, change(&cur, job.EventPhaseDone))

	writeState(t, h.Harness, f, false)

	c := dispatch(&cur)
	c.PlanNotBefore = time.Now().Add(-job.DefaultMaxPlanAge)

	if _, err := h.Jobs.Apply(t.Context(), c, f.fence, nil); !errors.Is(err, job.ErrPlanStale) {
		t.Fatalf("dispatch of the apply phase after a state write = %v, want ErrPlanStale", err)
	}

	back := apply(t, h.Harness, f, change(&cur, job.EventReplan))
	if back.Phases[0].Plan != nil {
		t.Fatalf("plan after a replan = %+v, want none", back.Phases[0].Plan)
	}

	lapse(t, h.Harness, a)

	again := apply(t, h.Harness, f, dispatch(&back))
	second := claimOf(t, h.Harness, runnerTwo, time.Minute, runnerKind(&again))

	two, err := h.Recorder.PutPlan(t.Context(), second.Claim, planParts("other display", "other apply"))
	if err != nil {
		t.Fatalf("PutPlan = %v", err)
	}

	if a.Claim.Job != second.Claim.Job {
		t.Fatalf("plan jobs = %s and %s, want one job", a.Claim.Job, second.Claim.Job)
	}

	if p.ID == two.ID {
		t.Fatalf("plan IDs = %q and %q, want two IDs", p.ID, two.ID)
	}

	// No part key of one plan equals a part key of the other, because the
	// key carries the plan ID.
	kinds := []job.OutputKind{job.OutputPlanDisplay, job.OutputPlanApply}

	for _, one := range kinds {
		for _, other := range kinds {
			firstKey := planKey(t, a.Claim.Job, p.ID, one)
			secondKey := planKey(t, second.Claim.Job, two.ID, other)
			if firstKey == secondKey {
				t.Fatalf("part key %s of both plans of one job, want two sets of keys", firstKey)
			}
		}
	}

	assertPart(t, h, p.ID, job.OutputPlanDisplay, displayJSON)
	assertPart(t, h, p.ID, job.OutputPlanApply, applyBinary)
	assertPart(t, h, two.ID, job.OutputPlanDisplay, "other display")
	assertPart(t, h, two.ID, job.OutputPlanApply, "other apply")
}

// hostilePart returns a valid part of kind k, which the cases change.
func hostilePart(k job.OutputKind) job.PlanPart {
	return job.PlanPart{
		Kind: k, Size: 1, SHA256: emptySHA256, Secret: k == job.OutputPlanApply, StoredSize: 0, StoredMD5: "",
	}
}

// hostilePartCases returns the lists of parts that the store refuses, by what
// is wrong with each.
func hostilePartCases() map[string][]job.PlanPart {
	upper := hostilePart(job.OutputPlanDisplay)
	upper.SHA256 = strings.ToUpper(emptySHA256)

	short := hostilePart(job.OutputPlanDisplay)
	short.SHA256 = emptySHA256[:10]

	negative := hostilePart(job.OutputPlanDisplay)
	negative.Size = -1

	huge := hostilePart(job.OutputPlanDisplay)
	huge.Size = job.MaxPlanPart + 1

	unknown := hostilePart(valueOther)

	notSecret := hostilePart(job.OutputPlanApply)
	notSecret.Secret = false

	secretDisplay := hostilePart(job.OutputPlanDisplay)
	secretDisplay.Secret = true

	cases := map[string][]job.PlanPart{
		"no part": nil,
		"three parts": {
			hostilePart(job.OutputPlanDisplay),
			hostilePart(job.OutputPlanApply),
			hostilePart(job.OutputPlanApply),
		},
		"an unknown kind":                  {unknown},
		"a kind twice":                     {hostilePart(job.OutputPlanApply), hostilePart(job.OutputPlanApply)},
		"a hash in capitals":               {upper},
		"a short hash":                     {short},
		"a negative size":                  {negative},
		"a size above the limit":           {huge},
		"an apply part that is not secret": {notSecret},
		"a display part that is secret":    {secretDisplay},
	}

	return cases
}

// testPlanHostileRef sends a hostile PlanRef directly to the store, as if the
// recorder had not checked its uploads.
//
// Defense in depth: the recorder checks the uploads and builds the parts
// itself. The store checks the parts again, and takes the times from the
// reserve, so a caller of the store cannot skip either layer.
func testPlanHostileRef(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h, f, "")
	np := newPlanFor(t, h, a, recordingKey(nil))

	reserved, err := h.Records.ReservePlan(t.Context(), a.Claim, np)
	if err != nil {
		t.Fatalf("ReservePlan = %v", err)
	}

	cases := hostilePartCases()

	for what, parts := range cases {
		hostile := reserved
		hostile.Parts = parts

		if _, putErr := h.Records.PutPlan(t.Context(), a.Claim, hostile); !errors.Is(putErr, job.ErrInvalid) {
			t.Errorf("PutPlan with %s = %v, want ErrInvalid", what, putErr)
		}
	}

	if _, readErr := h.Records.Plan(t.Context(), f.fence.Namespace, reserved.ID); !errors.Is(readErr, job.ErrNotFound) {
		t.Fatalf("Plan after the refused parts = %v, want ErrNotFound: it still has no part", readErr)
	}

	// Valid parts with the times of the caller: the store keeps the times of
	// the reserve.
	hostile := reserved
	hostile.Parts = []job.PlanPart{hostilePart(job.OutputPlanDisplay)}
	hostile.CreatedAt = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	hostile.ExpiresAt = time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	hostile.RedactedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	out, err := h.Records.PutPlan(t.Context(), a.Claim, hostile)
	if err != nil {
		t.Fatalf("PutPlan with valid parts = %v", err)
	}

	if !out.CreatedAt.Equal(reserved.CreatedAt) || !out.ExpiresAt.Equal(reserved.ExpiresAt) ||
		!out.RedactedAt.IsZero() ||
		len(out.Parts) != 1 {
		t.Fatalf("PutPlan = %+v, want the times of the reserve %+v and one part", out, reserved)
	}

	checkStoredTimes(t, h, f.fence.Namespace, &reserved)
}

// checkStoredTimes fails t unless the stored plan has the times of reserved and
// no redaction.
func checkStoredTimes(t *testing.T, h RecorderHarness, ns namespace.Name, reserved *job.PlanRef) {
	t.Helper()

	rec, err := h.Records.Plan(t.Context(), ns, reserved.ID)
	if err != nil || rec.Plan == nil || !rec.Plan.CreatedAt.Equal(reserved.CreatedAt) ||
		!rec.Plan.ExpiresAt.Equal(reserved.ExpiresAt) || !rec.Plan.RedactedAt.IsZero() {
		t.Fatalf("stored plan = %+v, %v; want the times of the reserve", rec.Plan, err)
	}
}
