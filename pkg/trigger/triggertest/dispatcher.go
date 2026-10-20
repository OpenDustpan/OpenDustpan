package triggertest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func dispatcherCases() []suiteCase {
	return []suiteCase{
		{"dispatcher: upstream starts one plan for each enabled follower", caseUpstream},
		{"dispatcher: a second upstream event for one apply starts nothing new", caseUpstreamRepeat},
		{"dispatcher: upstream starts nothing for a unit that nothing follows", caseUpstreamNoFollowers},
		{"dispatcher: upstream refuses a state version that is not a final version of the unit", caseUpstreamMismatch},
		{"dispatcher: upstream refuses an invalid event", caseUpstreamInvalid},
		{"dispatcher: an error of one follower does not stop the others", caseUpstreamPartial},
		{"dispatcher: scheduled starts the plan of one slot once", caseScheduled},
		{"dispatcher: scheduled refuses a trigger that is off, unknown or not a schedule", caseScheduledRefusals},
		{"dispatcher: manual starts the plan of any kind for the caller", caseManual},
		{"dispatcher: manual refuses a trigger that is off or unknown", caseManualRefusals},
		{"dispatcher: a start that the job service refuses is an error and no start", caseStartRefused},
	}
}

// follower makes the follows trigger id of f on src.
func follower(t *testing.T, h Harness, id trigger.ID, f, src graph.Unit) {
	t.Helper()

	fence := h.Namespace(t, string(f.Namespace))
	must(t, "put "+string(id), h.Store.Put(t.Context(), followsTrigger(id, f, src), fence, auditOf("trigger:put")))
}

// idsOf returns the trigger IDs of out, in order.
func idsOf(out []trigger.Outcome) []trigger.ID {
	ids := make([]trigger.ID, 0, len(out))
	for i := range out {
		ids = append(ids, out[i].Trigger)
	}

	return ids
}

func caseUpstream(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b, c, d := unit(acme, "a"), unit(acme, "b"), unit(acme, "c"), unit(acme, "d")

	follower(t, h, "f2", c, a)
	follower(t, h, "f1", b, a)
	follower(t, h, "f3", d, b)

	off := followsTrigger("f4", unit(acme, "e"), a)
	off.Enabled = false

	must(t, "put off", h.Store.Put(t.Context(), off, h.Namespace(t, string(acme)), auditOf("trigger:put")))

	applied := h.Versions.Final(t, a)

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
	must(t, "upstream", err)

	ids := idsOf(out)
	if !slices.Equal(ids, []trigger.ID{"f1", "f2"}) {
		t.Fatalf("outcomes = %+v, want f1 and f2 in order", out)
	}

	// The follower of a follower and the trigger that is off start nothing.
	wantUnits := []graph.Unit{b, c}
	starts := rec.Starts()

	if len(starts) != len(wantUnits) || len(out) != len(starts) {
		t.Fatalf("%d starts and %d outcomes, want %d", len(starts), len(out), len(wantUnits))
	}

	for i := range starts {
		s := &starts[i]

		if s.Unit != wantUnits[i] || s.Req.Trigger != ids[i] || s.Req.Kind != trigger.KindFollows ||
			s.Req.IdempotencyKey != trigger.UpstreamKey(ids[i], applied) || s.Req.Reason == "" ||
			out[i].Err != nil || out[i].Job != s.Job {
			t.Errorf("start %d = %+v, outcome %+v", i, *s, out[i])
		}
	}
}

func caseUpstreamRepeat(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")

	follower(t, h, "f1", b, a)

	event := trigger.Applied{Unit: a, Version: h.Versions.Final(t, a)}

	for range twoFollowers {
		_, err := h.Dispatcher.Upstream(t.Context(), event)
		must(t, "upstream", err)
	}

	if got := rec.Jobs(); len(got) != 1 {
		t.Errorf("two events for one apply started %d jobs, want 1", len(got))
	}

	// A second apply is a new event.
	_, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Versions.Final(t, a)})
	must(t, "upstream of a second apply", err)

	if got := rec.Jobs(); len(got) != twoFollowers {
		t.Errorf("two applies started %d jobs, want %d", len(got), twoFollowers)
	}
}

func caseUpstreamNoFollowers(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")

	h.Namespace(t, string(acme))

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Versions.Final(t, a)})
	must(t, "upstream", err)

	if len(out) != 0 || len(rec.Starts()) != 0 {
		t.Errorf("outcomes %v and starts %v for a unit that nothing follows", out, rec.Starts())
	}
}

func caseUpstreamMismatch(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")

	follower(t, h, "f1", b, a)

	events := []struct {
		name  string
		event trigger.Applied
	}{
		{"a version inside a run", trigger.Applied{Unit: a, Version: h.Versions.Intermediate(t, a)}},
		{"a final version of another unit", trigger.Applied{Unit: a, Version: h.Versions.Final(t, b)}},
		{"an unknown version", trigger.Applied{Unit: a, Version: "version-unknown"}},
	}

	for i := range events {
		_, err := h.Dispatcher.Upstream(t.Context(), events[i].event)
		wantErr(t, events[i].name, err, trigger.ErrVersionMismatch)
	}

	if len(rec.Starts()) != 0 {
		t.Errorf("a version that is not a final version of the unit started %v", rec.Starts())
	}
}

func caseUpstreamInvalid(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")

	h.Namespace(t, string(acme))

	events := []struct {
		name  string
		event trigger.Applied
	}{
		{"no version", trigger.Applied{Unit: a, Version: ""}},
		{"no unit", trigger.Applied{Unit: noUnit(), Version: "v-1"}},
	}

	for i := range events {
		_, err := h.Dispatcher.Upstream(t.Context(), events[i].event)
		wantErr(t, events[i].name, err, trigger.ErrInvalid)
	}

	if len(rec.Starts()) != 0 {
		t.Errorf("an invalid event started %v", rec.Starts())
	}
}

func caseUpstreamPartial(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b, c := unit(acme, "a"), unit(acme, "b"), unit(acme, "c")

	follower(t, h, "f1", b, a)
	follower(t, h, "f2", c, a)

	rec.FailFor(b, job.ErrLimit)

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Versions.Final(t, a)})
	must(t, "upstream", err)

	if len(out) != twoFollowers {
		t.Fatalf("outcomes = %+v, want %d", out, twoFollowers)
	}

	wantErr(t, "outcome of f1", out[0].Err, job.ErrLimit)

	if out[0].Job != "" || out[1].Err != nil || out[1].Job == "" {
		t.Errorf("outcomes = %+v, want an error for f1 and a job for f2", out)
	}
}

func caseScheduled(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	slot := at(0)

	first, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", slot)
	must(t, "scheduled", err)

	// A second instance fires the same slot.
	second, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", slot.In(time.FixedZone("x", otherZoneOffset)))
	must(t, "scheduled again", err)

	if first == "" || first != second || len(rec.Jobs()) != 1 {
		t.Errorf("jobs %q and %q, %d distinct, want one job", first, second, len(rec.Jobs()))
	}

	next, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", slot.Add(oneDay))
	must(t, "next slot", err)

	if next == first || len(rec.Jobs()) != twoFollowers {
		t.Errorf("the next slot did not start a new job: %q, %q", next, first)
	}

	s := rec.Starts()[0]
	if s.Unit != a || s.Req.Trigger != "s1" || s.Req.Kind != trigger.KindSchedule ||
		s.Req.IdempotencyKey != trigger.ScheduleKey("s1", slot) || !strings.Contains(s.Req.Reason, "s1") {
		t.Errorf("start = %+v", s)
	}
}

func caseScheduledRefusals(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")
	fence := h.Namespace(t, string(acme))

	off := scheduleTrigger("s1", a)
	off.Enabled = false

	must(t, "put off", h.Store.Put(t.Context(), off, fence, auditOf("trigger:put")))
	must(t, "put follows", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))
	must(t, "put on", h.Store.Put(t.Context(), scheduleTrigger("s2", a), fence, auditOf("trigger:put")))

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	wantErr(t, "trigger that is off", err, trigger.ErrDisabled)

	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "nope", at(0))
	wantErr(t, "unknown trigger", err, trigger.ErrNotFound)

	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "f1", at(0))
	wantErr(t, "follows trigger", err, trigger.ErrInvalid)

	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "s2", time.Time{})
	wantErr(t, "zero slot", err, trigger.ErrInvalid)

	_, err = h.Dispatcher.Scheduled(t.Context(), other, "s2", at(0))
	wantErr(t, "trigger of another namespace", err, trigger.ErrNotFound)

	if len(rec.Starts()) != 0 {
		t.Errorf("refused fires started %v", rec.Starts())
	}
}

// manual returns a manual request for the trigger id at now.
func manual(id trigger.ID, now time.Time) trigger.ManualRequest {
	return trigger.ManualRequest{Namespace: acme, Trigger: id, Now: now, Reason: "test run"}
}

func caseManual(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")
	fence := h.Namespace(t, string(acme))

	triggers := []trigger.Trigger{scheduleTrigger("s1", a), followsTrigger("f1", b, a), reviewTrigger("r1", a)}
	for i := range triggers {
		must(t, "put", h.Store.Put(t.Context(), triggers[i], fence, auditOf("trigger:put")))
	}

	actor := ActorOf("user:ada")
	now := at(manualMinute)

	for _, id := range []trigger.ID{"s1", "f1", "r1"} {
		_, err := h.Dispatcher.Manual(t.Context(), actor, manual(id, now))
		must(t, "manual "+string(id), err)
	}

	// A double click is one job.
	first, err := h.Dispatcher.Manual(t.Context(), actor, manual("s1", now.Add(doubleClickSpan)))
	must(t, "manual again", err)

	again, err := h.Dispatcher.Manual(t.Context(), actor, manual("s1", now))
	must(t, "manual a third time", err)

	if first != again || len(rec.Jobs()) != len(triggers) {
		t.Errorf("a double click started %d jobs for %d triggers; %q, %q", len(rec.Jobs()), len(triggers), first, again)
	}

	starts := rec.Starts()
	for i := range starts {
		s := &starts[i]

		if s.Actor.Principal.Subject != actor.Principal.Subject || s.Req.Reason == "" ||
			s.Req.IdempotencyKey != trigger.ManualKey(s.Req.Trigger, now) {
			t.Errorf("start = %+v, want the actor of the caller and a manual key", *s)
		}
	}
}

func caseManualRefusals(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	off := scheduleTrigger("s1", a)
	off.Enabled = false

	must(t, "put off", h.Store.Put(t.Context(), off, fence, auditOf("trigger:put")))

	actor := ActorOf("user:ada")

	_, err := h.Dispatcher.Manual(t.Context(), actor, manual("s1", at(0)))
	wantErr(t, "trigger that is off", err, trigger.ErrDisabled)

	_, err = h.Dispatcher.Manual(t.Context(), actor, manual("nope", at(0)))
	wantErr(t, "unknown trigger", err, trigger.ErrNotFound)

	_, err = h.Dispatcher.Manual(t.Context(), actor, manual("", at(0)))
	wantErr(t, "empty ID", err, trigger.ErrInvalid)

	if len(rec.Starts()) != 0 {
		t.Errorf("refused fires started %v", rec.Starts())
	}
}

func caseStartRefused(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	for _, refusal := range []error{job.ErrFrozen, job.ErrPolicyDenied, job.ErrUnsupported} {
		rec.FailFor(a, refusal)

		_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
		if !errors.Is(err, refusal) {
			t.Errorf("Scheduled = %v, want %v", err, refusal)
		}
	}

	if got := rec.Starts(); len(got) != 0 {
		t.Errorf("refused starts were recorded: %v", got)
	}
}
