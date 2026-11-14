package triggertest

import (
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// Sizes of the cases of the claim of a slot.
const (
	// claimers is the number of instances that claim one slot at the same time.
	claimers = 16
	// missedHours is the number of hourly slots that an outage skips.
	missedHours = 30
	// pastHours is how far before the start of the current hour the first slot of
	// a case lies. The three slots of a case are in the past, so a claim of each
	// is due on the clock of the store.
	pastHours = 3
	// beyondSkew is a distance after the clock of the store that no clock skew
	// covers.
	beyondSkew = 2 * time.Hour
	// thirdSlotOffset is the distance of the third slot of a case from its first.
	thirdSlotOffset = 2 * time.Hour
)

func claimCases() []suiteCase {
	return []suiteCase{
		{"claim: put records the next slot of an enabled schedule trigger only", caseNextSlotPut},
		{"claim: a replace keeps the slot unless the frequency changes or the trigger was off", caseNextSlotReplace},
		{"claim: next slot refuses an unknown trigger and an invalid ID", caseNextSlotRefusals},
		{"claim: a claim takes the slot and moves the next slot", caseClaimTakes},
		{"claim: a second claim of one slot is not due and appends nothing", caseClaimTwice},
		{"claim: a claim of an older slot is not due", caseClaimOlder},
		{"claim: after an outage one claim of the latest slot skips the missed slots", caseClaimCatchUp},
		{"claim: a time that is not a slot of the schedule is invalid", caseClaimNotSlot},
		{"claim: a slot later than the clock of the store is invalid and moves nothing", caseClaimFuture},
		{"claim: a claim refuses a trigger that is off, unknown or not a schedule trigger", caseClaimRefusals},
		{"claim: a claim refuses a stale fence and a fence of another namespace", caseClaimFence},
		{"claim: a claim refuses an invalid audit entry and one of another namespace", caseClaimEntry},
		{"claim: a claim appends one audit entry", caseClaimAudit},
		{"claim: of many claims of one slot at once exactly one succeeds", caseClaimConcurrent},
		{"claim: a claim of a stored row that fails its checks is invalid", caseClaimCorruptRow},
		{"claim: a claim and a start of one slot give one job", caseClaimThenStart},
	}
}

// hourlyTrigger returns an enabled schedule trigger of u that runs at the start
// of each hour.
func hourlyTrigger(id trigger.ID, u graph.Unit) trigger.Trigger {
	return withFrequency(scheduleTrigger(id, u), cronOf("0 * * * *"), true)
}

// auditAt returns an audit entry of the action, stamped with the time ts.
func auditAt(action string, ts time.Time) audit.Entry {
	e := auditOf(action)
	e.At = ts

	return e
}

// claimEntry returns the audit entry of a claim, stamped with now.
func claimEntry(now time.Time) audit.Entry { return auditAt("trigger:claim", now) }

// claimOf returns the claim of the slot of trigger id in acme.
func claimOf(id trigger.ID, slot time.Time) trigger.Claim {
	return trigger.Claim{Namespace: acme, Trigger: id, Slot: slot}
}

// nextSlot returns the next slot that the store keeps for id in acme.
func nextSlot(t *testing.T, h Harness, id trigger.ID) time.Time {
	t.Helper()

	got, err := h.Store.NextSlot(t.Context(), acme, id)
	must(t, "next slot of "+string(id), err)

	return got
}

// wantSlotAfterNow fails the case unless got is the first slot of tr after the
// clock of the store at some time between before and after. The store reads its
// own clock, so the case cannot know the exact time.
func wantSlotAfterNow(t *testing.T, tr trigger.Trigger, got, before, after time.Time) {
	t.Helper()

	first, err := trigger.NextSlot(tr, before)
	must(t, "slot after the start", err)

	last, err := trigger.NextSlot(tr, after)
	must(t, "slot after the end", err)

	if !got.Equal(first) && !got.Equal(last) {
		t.Errorf("slot of %s = %v, want %v or %v: the first slot after the clock of the store", tr.ID, got, first, last)
	}
}

// pastSlots stores an hourly trigger h1, moves its next slot to three hours
// before the start of the current hour, and returns the trigger with the three
// slots from there. All three are in the past, so a claim of each is due.
func pastSlots(t *testing.T, h Harness, fence namespace.Fence) (trigger.Trigger, [3]time.Time) {
	t.Helper()

	tr := hourlyTrigger("h1", unit(acme, "a"))
	must(t, "put", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	first := h.Now(t).UTC().Truncate(time.Hour).Add(-pastHours * time.Hour)
	h.SetNextSlot(t, acme, "h1", first)

	return tr, [3]time.Time{first, first.Add(time.Hour), first.Add(thirdSlotOffset)}
}

func caseNextSlotPut(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	off := withFrequency(scheduleTrigger("s2", a), trigger.Frequency{Preset: "", Cron: "0 6 * * *"}, false)

	before := h.Now(t).UTC()

	// The audit entry carries a time far from the clock of the store. The slot
	// must come from the clock of the store.
	putAll(t, h, fence, scheduleTrigger("s1", a), off, reviewTrigger("r1", a), followsTrigger("f1", b, a))

	after := h.Now(t).UTC()

	wantSlotAfterNow(t, scheduleTrigger("s1", a),
		nextSlot(t, h, "s1"), before, after)

	for _, id := range []trigger.ID{"s2", "r1", "f1"} {
		if got := nextSlot(t, h, id); !got.IsZero() {
			t.Errorf("next slot of %s = %v, want none", id, got)
		}
	}
}

func caseNextSlotReplace(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tr, past := pastSlots(t, h, fence)

	// A save that keeps the frequency changes no slot, whatever the time of its
	// entry and its saver are.
	tr.LastSaver, tr.LastSaverID = grace, SaverIDOf(grace)
	must(t, "same frequency", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(1))))

	if got := nextSlot(t, h, "h1"); !got.Equal(past[0]) {
		t.Errorf("next slot after a replace with the same frequency = %v, want %v", got, past[0])
	}

	// A new frequency starts from the clock of the store.
	tr.Frequency = cronOf("30 * * * *")
	before := h.Now(t).UTC()
	must(t, "new frequency", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))
	wantSlotAfterNow(t, tr, nextSlot(t, h, "h1"), before, h.Now(t).UTC())

	// Off gives no slot. On again starts from the clock of the store: the time
	// that the trigger was off gives no catch-up.
	tr.Enabled = false
	must(t, "off", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	if got := nextSlot(t, h, "h1"); !got.IsZero() {
		t.Errorf("next slot of a trigger that is off = %v, want none", got)
	}

	tr.Enabled = true
	before = h.Now(t).UTC()
	must(t, "on", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))
	wantSlotAfterNow(t, tr, nextSlot(t, h, "h1"), before, h.Now(t).UTC())
}

func caseNextSlotRefusals(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	h.Namespace(t, string(acme))

	_, err := h.Store.NextSlot(t.Context(), acme, "nope")
	wantErr(t, "unknown trigger", err, trigger.ErrNotFound)

	_, err = h.Store.NextSlot(t.Context(), acme, "No Way")
	wantErr(t, "invalid ID", err, trigger.ErrInvalid)

	_, err = h.Store.NextSlot(t.Context(), "No Way", "s1")
	wantErr(t, "invalid namespace", err, trigger.ErrInvalid)
}

func caseClaimTakes(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t))))

	if got := nextSlot(t, h, "h1"); !got.Equal(past[1]) {
		t.Errorf("next slot after the claim = %v, want %v", got, past[1])
	}

	must(t, "claim of the next slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", past[1]), fence, claimEntry(h.Now(t))))
}

func caseClaimTwice(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	must(t, "first claim", h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t))))

	before := audited(t, h)

	err := h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t)))
	wantErr(t, "second claim of the slot", err, trigger.ErrNotDue)

	if after := audited(t, h); after != before {
		t.Errorf("a claim that is not due appended %d audit entries", after-before)
	}

	if got := nextSlot(t, h, "h1"); !got.Equal(past[1]) {
		t.Errorf("a claim that is not due moved the next slot to %v", got)
	}
}

func caseClaimOlder(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	must(t, "claim of the third slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", past[2]), fence, claimEntry(h.Now(t))))

	// The first and the second slot are before the next slot now.
	for _, slot := range past {
		err := h.Store.ClaimDue(t.Context(), claimOf("h1", slot), fence, claimEntry(h.Now(t)))
		wantErr(t, "claim of "+slot.Format(time.RFC3339), err, trigger.ErrNotDue)
	}
}

func caseClaimCatchUp(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tr, past := pastSlots(t, h, fence)

	// The server was down for many slots. The tick asks for the latest slot that
	// is not after now, with LatestSlot, and claims that one slot.
	first := h.Now(t).UTC().Truncate(time.Hour).Add(-missedHours * time.Hour)
	h.SetNextSlot(t, acme, "h1", first)

	latest, ok, err := trigger.LatestSlot(tr, nextSlot(t, h, "h1"), h.Now(t).UTC())
	must(t, "latest slot", err)

	hour := h.Now(t).UTC().Truncate(time.Hour)
	current := latest.Equal(hour) || latest.Equal(hour.Add(-time.Hour))

	if !ok || !current {
		t.Fatalf("latest slot = %v (%v), want the current hour or the one before", latest, ok)
	}

	must(t, "claim of the latest slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", latest), fence, claimEntry(h.Now(t))))

	if got, want := nextSlot(t, h, "h1"), latest.Add(time.Hour); !got.Equal(want) {
		t.Errorf("next slot after the catch-up = %v, want %v", got, want)
	}

	// No missed slot is due any more: at most one catch-up run.
	for _, skipped := range []time.Time{first, past[0]} {
		err = h.Store.ClaimDue(t.Context(), claimOf("h1", skipped), fence, claimEntry(h.Now(t)))
		wantErr(t, "claim of a skipped slot", err, trigger.ErrNotDue)
	}
}

func caseClaimNotSlot(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	bad := []struct {
		name string
		slot time.Time
	}{
		{"a minute after the slot", past[0].Add(time.Minute)},
		{"half an hour before the slot", past[0].Add(-30 * time.Minute)},
		{"a part of a second after the slot", past[0].Add(time.Millisecond)},
		{"the zero time", time.Time{}},
	}

	for _, tc := range bad {
		err := h.Store.ClaimDue(t.Context(), claimOf("h1", tc.slot), fence, claimEntry(h.Now(t)))
		wantErr(t, tc.name, err, trigger.ErrInvalid)
	}

	if got := nextSlot(t, h, "h1"); !got.Equal(past[0]) {
		t.Errorf("a refused claim moved the next slot to %v", got)
	}
}

func caseClaimFuture(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	// The slots are slots of the schedule, but the store decides from its own
	// clock, so a slot later than that clock plus the skew is refused. An
	// instance with a clock that runs ahead cannot stop the schedule.
	soon := h.Now(t).UTC().Truncate(time.Hour).Add(beyondSkew)
	far := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)

	for name, slot := range map[string]time.Time{"two hours ahead": soon, "the year 2100": far} {
		err := h.Store.ClaimDue(t.Context(), claimOf("h1", slot), fence, claimEntry(h.Now(t)))
		wantErr(t, name, err, trigger.ErrInvalid)
	}

	// The caller's own clock does not matter: the entry carries the year 2100 too.
	err := h.Store.ClaimDue(t.Context(), claimOf("h1", far), fence, auditAt("trigger:claim", far))
	wantErr(t, "an entry in the year 2100", err, trigger.ErrInvalid)

	if got := nextSlot(t, h, "h1"); !got.Equal(past[0]) {
		t.Errorf("a refused claim moved the next slot to %v", got)
	}

	must(t, "claim of a slot that is due",
		h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t))))
}

func caseClaimRefusals(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	off := withFrequency(scheduleTrigger("off", a), cronOf("0 * * * *"), false)

	putAll(t, h, fence, off, reviewTrigger("r1", a))

	slot := h.Now(t).UTC().Truncate(time.Hour).Add(-time.Hour)

	cases := []struct {
		name string
		id   trigger.ID
		want error
	}{
		{"a trigger that is off", "off", trigger.ErrDisabled},
		{"an unknown trigger", "nope", trigger.ErrNotFound},
		{"a trigger of another kind", "r1", trigger.ErrInvalid},
		{"an ID that is not valid", "No Way", trigger.ErrInvalid},
	}

	for _, tc := range cases {
		err := h.Store.ClaimDue(t.Context(), claimOf(tc.id, slot), fence, claimEntry(h.Now(t)))
		wantErr(t, tc.name, err, tc.want)
	}
}

func caseClaimFence(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	otherFence := h.Namespace(t, string(other))
	_, past := pastSlots(t, h, fence)

	c := claimOf("h1", past[0])

	wantErr(t, "stale fence", h.Store.ClaimDue(t.Context(), c, stale(fence), claimEntry(h.Now(t))), namespace.ErrFenced)
	wantErr(t, "fence of another namespace", h.Store.ClaimDue(t.Context(), c, otherFence, claimEntry(h.Now(t))),
		namespace.ErrFenced)

	if got := nextSlot(t, h, "h1"); !got.Equal(past[0]) {
		t.Errorf("a fenced claim moved the next slot to %v", got)
	}

	// The slot is still free after the refusals.
	must(t, "claim with the fence", h.Store.ClaimDue(t.Context(), c, fence, claimEntry(h.Now(t))))
}

func caseClaimEntry(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)
	c := claimOf("h1", past[0])

	foreign := claimEntry(h.Now(t))
	foreign.Namespace = other

	empty := auditAt("", h.Now(t).UTC())

	wantErr(t, "entry of another namespace", h.Store.ClaimDue(t.Context(), c, fence, foreign), audit.ErrInvalidEntry)
	wantErr(t, "invalid entry", h.Store.ClaimDue(t.Context(), c, fence, empty), audit.ErrInvalidEntry)

	if got := nextSlot(t, h, "h1"); !got.Equal(past[0]) {
		t.Errorf("a claim with a bad entry moved the next slot to %v", got)
	}
}

// audited returns the number of audit entries of acme, or zero when the harness
// has no audit hook.
func audited(t *testing.T, h Harness) int {
	t.Helper()

	if h.Audited == nil {
		return 0
	}

	return h.Audited(t, string(acme))
}

func caseClaimAudit(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	if h.Audited == nil {
		t.Skip("the harness has no audit hook")
	}

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	before := h.Audited(t, string(acme))

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t))))

	if got := h.Audited(t, string(acme)); got != before+1 {
		t.Errorf("audit entries after a claim = %d, want %d", got, before+1)
	}
}

func caseClaimConcurrent(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []error
	)

	for range claimers {
		wg.Go(func() {
			err := h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t)))

			mu.Lock()
			defer mu.Unlock()

			results = append(results, err)
		})
	}

	wg.Wait()

	won := 0

	for _, err := range results {
		if err == nil {
			won++

			continue
		}

		wantErr(t, "a claim that lost", err, trigger.ErrNotDue)
	}

	if won != 1 {
		t.Errorf("%d claims of one slot succeeded, want 1", won)
	}
}

func caseClaimCorruptRow(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	if h.StoreRaw == nil {
		t.Skip("the harness cannot store a row that fails its checks")
	}

	fence := h.Namespace(t, string(acme))

	// A row of another version, with a cron expression that asks for a run every
	// minute, must not be claimed.
	bad := withFrequency(scheduleTrigger("bad", unit(acme, "a")), cronOf("* * * * *"), true)
	h.StoreRaw(t, bad)

	slot := h.Now(t).UTC().Truncate(time.Hour).Add(-time.Hour)

	err := h.Store.ClaimDue(t.Context(), claimOf("bad", slot), fence, claimEntry(h.Now(t)))
	wantErr(t, "claim of a bad row", err, trigger.ErrInvalid)
}

func caseClaimThenStart(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, past := pastSlots(t, h, fence)

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", past[0]), fence, claimEntry(h.Now(t))))

	one, err := h.Dispatcher.Scheduled(t.Context(), acme, "h1", past[0])
	must(t, "start", err)

	// An instance that lost the claim does not start. A start that repeats
	// anyway, as after a crash between the claim and the start, gives the same job.
	again, err := h.Dispatcher.Scheduled(t.Context(), acme, "h1", past[0])
	must(t, "second start", err)

	if one != again || len(rec.Jobs()) != 1 {
		t.Errorf("jobs = %v, first %q again %q, want one job", rec.Jobs(), one, again)
	}
}
