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
	// Minutes after the time of the fixtures, for the saves of the case of a
	// replace. Each save is in a later hour than the one before.
	minutesTwoHours = 120
	minutesTenHours = 600
	// claimSpan is the time of the claims of the case with an older slot.
	claimSpan = 3 * time.Hour
	// ninety and sixty are the minutes after the fixtures of other cases.
	minutesNinety = 90
	minutesHour   = 60
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
		{"claim: a slot after the time of the claim is invalid", caseClaimFuture},
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

// claimOf returns the claim of the slot of trigger id in acme at now.
func claimOf(id trigger.ID, slot, now time.Time) trigger.Claim {
	return trigger.Claim{Namespace: acme, Trigger: id, Slot: slot, Now: now}
}

// nextSlot returns the next slot that the store keeps for id in acme.
func nextSlot(t *testing.T, h Harness, id trigger.ID) time.Time {
	t.Helper()

	got, err := h.Store.NextSlot(t.Context(), acme, id)
	must(t, "next slot of "+string(id), err)

	return got
}

// putHourly stores an hourly schedule trigger at the time of the fixtures and
// returns it with its first two slots.
func putHourly(t *testing.T, h Harness, fence namespace.Fence) (trigger.Trigger, [2]time.Time) {
	t.Helper()

	tr := hourlyTrigger("h1", unit(acme, "a"))
	must(t, "put", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(0))))

	first, err := trigger.NextSlot(tr, at(0))
	must(t, "first slot", err)

	second, err := trigger.NextSlot(tr, first)
	must(t, "second slot", err)

	return tr, [2]time.Time{first, second}
}

func caseNextSlotPut(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	off := withFrequency(scheduleTrigger("s2", a), trigger.Frequency{Preset: "", Cron: "0 6 * * *"}, false)

	putAll(t, h, fence, scheduleTrigger("s1", a), off, reviewTrigger("r1", a), followsTrigger("f1", b, a))

	want, err := trigger.NextSlot(scheduleTrigger("s1", a), at(0))
	must(t, "want", err)

	if got := nextSlot(t, h, "s1"); !got.Equal(want) {
		t.Errorf("next slot of an enabled schedule trigger = %v, want %v", got, want)
	}

	for _, id := range []trigger.ID{"s2", "r1", "f1"} {
		if got := nextSlot(t, h, id); !got.IsZero() {
			t.Errorf("next slot of %s = %v, want none", id, got)
		}
	}
}

func caseNextSlotReplace(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tr, slots := putHourly(t, h, fence)
	first := slots[0]

	// A save that keeps the frequency, a later time and a new saver change no slot.
	tr.LastSaver, tr.LastSaverID = grace, SaverIDOf(grace)
	must(t, "same frequency", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(minutesNinety))))

	if got := nextSlot(t, h, "h1"); !got.Equal(first) {
		t.Errorf("next slot after a replace with the same frequency = %v, want %v", got, first)
	}

	// A new frequency starts from the time of the save.
	tr.Frequency = cronOf("30 * * * *")
	must(t, "new frequency", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(minutesNinety))))

	want, err := trigger.NextSlot(tr, at(minutesNinety))
	must(t, "want", err)

	if got := nextSlot(t, h, "h1"); !got.Equal(want) {
		t.Errorf("next slot after a new frequency = %v, want %v", got, want)
	}

	// Off gives no slot. On again starts from the time of that save: the time
	// that the trigger was off gives no catch-up.
	tr.Enabled = false
	must(t, "off", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(minutesTwoHours))))

	if got := nextSlot(t, h, "h1"); !got.IsZero() {
		t.Errorf("next slot of a trigger that is off = %v, want none", got)
	}

	tr.Enabled = true
	must(t, "on", h.Store.Put(t.Context(), tr, fence, auditAt("trigger:put", at(minutesTenHours))))

	want, err = trigger.NextSlot(tr, at(minutesTenHours))
	must(t, "want", err)

	if got := nextSlot(t, h, "h1"); !got.Equal(want) {
		t.Errorf("next slot after switch-on = %v, want %v", got, want)
	}
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
	_, slots := putHourly(t, h, fence)
	first, second := slots[0], slots[1]

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", first, first), fence, auditAt("trigger:claim", first)))

	if got := nextSlot(t, h, "h1"); !got.Equal(second) {
		t.Errorf("next slot after the claim = %v, want %v", got, second)
	}

	// The claim takes the slot at the very time of the slot, and the next slot
	// is claimed in its turn.
	must(t, "claim of the next slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", second, second), fence, auditAt("trigger:claim", second)))
}

func caseClaimTwice(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first, second := slots[0], slots[1]

	must(
		t,
		"first claim",
		h.Store.ClaimDue(t.Context(), claimOf("h1", first, first), fence, auditAt("trigger:claim", first)),
	)

	before := audited(t, h)

	err := h.Store.ClaimDue(t.Context(), claimOf("h1", first, second), fence, auditAt("trigger:claim", second))
	wantErr(t, "second claim of the slot", err, trigger.ErrNotDue)

	if after := audited(t, h); after != before {
		t.Errorf("a claim that is not due appended %d audit entries", after-before)
	}

	if got := nextSlot(t, h, "h1"); !got.Equal(second) {
		t.Errorf("a claim that is not due moved the next slot to %v", got)
	}
}

func caseClaimOlder(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tr, slots := putHourly(t, h, fence)
	first, second := slots[0], slots[1]

	third, err := trigger.NextSlot(tr, second)
	must(t, "third slot", err)

	must(t, "claim of the third slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", third, third), fence, auditAt("trigger:claim", third)))

	// The first and the second slot are before the next slot now.
	for _, slot := range []time.Time{first, second, third} {
		err := h.Store.ClaimDue(t.Context(), claimOf("h1", slot, third), fence, auditAt("trigger:claim", third))
		wantErr(t, "claim of "+slot.Format(time.RFC3339), err, trigger.ErrNotDue)
	}
}

func caseClaimCatchUp(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tr, slots := putHourly(t, h, fence)
	first := slots[0]

	// The server was down for many slots. The tick asks for the latest slot
	// that is not after now, with LatestSlot, and claims that one slot.
	now := first.Add(missedHours*time.Hour + 20*time.Minute)

	stored := nextSlot(t, h, "h1")

	latest, ok, err := trigger.LatestSlot(tr, stored, now)
	must(t, "latest slot", err)

	if want := first.Add(missedHours * time.Hour); !ok || !latest.Equal(want) {
		t.Fatalf("latest slot = %v (%v), want %v", latest, ok, want)
	}

	must(t, "claim of the latest slot",
		h.Store.ClaimDue(t.Context(), claimOf("h1", latest, now), fence, auditAt("trigger:claim", now)))

	want := latest.Add(time.Hour)
	if got := nextSlot(t, h, "h1"); !got.Equal(want) {
		t.Errorf("next slot after the catch-up = %v, want %v", got, want)
	}

	// No missed slot is due any more: at most one catch-up run.
	err = h.Store.ClaimDue(t.Context(), claimOf("h1", first, now), fence, auditAt("trigger:claim", now))
	wantErr(t, "claim of a skipped slot", err, trigger.ErrNotDue)
}

func caseClaimNotSlot(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first := slots[0]
	now := first.Add(claimSpan)

	bad := []struct {
		name string
		slot time.Time
	}{
		{"a minute after the slot", first.Add(time.Minute)},
		{"half an hour before the slot", first.Add(-30 * time.Minute)},
		{"a part of a second after the slot", first.Add(time.Millisecond)},
		{"the zero time", time.Time{}},
	}

	for _, tc := range bad {
		err := h.Store.ClaimDue(t.Context(), claimOf("h1", tc.slot, now), fence, auditAt("trigger:claim", now))
		wantErr(t, tc.name, err, trigger.ErrInvalid)
	}

	if got := nextSlot(t, h, "h1"); !got.Equal(first) {
		t.Errorf("a refused claim moved the next slot to %v", got)
	}
}

func caseClaimFuture(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first, second := slots[0], slots[1]

	// The slot is a slot of the schedule, but it is after the time of the claim:
	// a claim of it would skip the slots before it.
	err := h.Store.ClaimDue(t.Context(), claimOf("h1", second, first), fence, auditAt("trigger:claim", first))
	wantErr(t, "future slot", err, trigger.ErrInvalid)

	if got := nextSlot(t, h, "h1"); !got.Equal(first) {
		t.Errorf("a refused claim moved the next slot to %v", got)
	}
}

func caseClaimRefusals(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	off := withFrequency(scheduleTrigger("off", a), cronOf("0 * * * *"), false)

	putAll(t, h, fence, off, reviewTrigger("r1", a))

	slot, err := trigger.NextSlot(hourlyTrigger("x", a), at(0))
	must(t, "slot", err)

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
		err := h.Store.ClaimDue(t.Context(), claimOf(tc.id, slot, slot), fence, auditAt("trigger:claim", slot))
		wantErr(t, tc.name, err, tc.want)
	}
}

func caseClaimFence(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	otherFence := h.Namespace(t, string(other))
	_, slots := putHourly(t, h, fence)
	first := slots[0]

	c := claimOf("h1", first, first)

	wantErr(t, "stale fence", h.Store.ClaimDue(t.Context(), c, stale(fence), auditAt("trigger:claim", first)),
		namespace.ErrFenced)
	wantErr(t, "fence of another namespace",
		h.Store.ClaimDue(t.Context(), c, otherFence, auditAt("trigger:claim", first)), namespace.ErrFenced)

	if got := nextSlot(t, h, "h1"); !got.Equal(first) {
		t.Errorf("a fenced claim moved the next slot to %v", got)
	}

	// The slot is still free after the refusals.
	must(t, "claim with the fence", h.Store.ClaimDue(t.Context(), c, fence, auditAt("trigger:claim", first)))
}

func caseClaimEntry(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first := slots[0]
	c := claimOf("h1", first, first)

	foreign := auditAt("trigger:claim", first)
	foreign.Namespace = other

	empty := auditAt("", first)

	wantErr(t, "entry of another namespace", h.Store.ClaimDue(t.Context(), c, fence, foreign), audit.ErrInvalidEntry)
	wantErr(t, "invalid entry", h.Store.ClaimDue(t.Context(), c, fence, empty), audit.ErrInvalidEntry)

	if got := nextSlot(t, h, "h1"); !got.Equal(first) {
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
	_, slots := putHourly(t, h, fence)
	first := slots[0]

	before := h.Audited(t, string(acme))

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", first, first), fence, auditAt("trigger:claim", first)))

	if got := h.Audited(t, string(acme)); got != before+1 {
		t.Errorf("audit entries after a claim = %d, want %d", got, before+1)
	}
}

func caseClaimConcurrent(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first := slots[0]

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []error
	)

	for range claimers {
		wg.Go(func() {
			err := h.Store.ClaimDue(t.Context(), claimOf("h1", first, first), fence, auditAt("trigger:claim", first))

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

	err := h.Store.ClaimDue(
		t.Context(),
		claimOf("bad", at(minutesHour), at(minutesHour)),
		fence,
		auditAt("trigger:claim", at(minutesHour)),
	)
	wantErr(t, "claim of a bad row", err, trigger.ErrInvalid)
}

func caseClaimThenStart(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	_, slots := putHourly(t, h, fence)
	first := slots[0]

	must(t, "claim", h.Store.ClaimDue(t.Context(), claimOf("h1", first, first), fence, auditAt("trigger:claim", first)))

	one, err := h.Dispatcher.Scheduled(t.Context(), acme, "h1", first)
	must(t, "start", err)

	// An instance that lost the claim does not start. A start that repeats
	// anyway, as after a crash between the claim and the start, gives the same job.
	again, err := h.Dispatcher.Scheduled(t.Context(), acme, "h1", first)
	must(t, "second start", err)

	if one != again || len(rec.Jobs()) != 1 {
		t.Errorf("jobs = %v, first %q again %q, want one job", rec.Jobs(), one, again)
	}
}
