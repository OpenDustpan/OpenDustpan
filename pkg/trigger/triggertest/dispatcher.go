package triggertest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
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
		{"dispatcher: a stored row that no longer validates starts no plan", caseInvalidRow},
		{"dispatcher: a start that the job service refuses is an error and no start", caseStartRefused},
		{"dispatcher: a start that no person makes runs as the namespace service principal", caseStartActor},
		{"dispatcher: a manual fire passes its code location, and no other start has one", caseStartLocation},
		{"dispatcher: a saver that lost access gets no start, and the refusal is audited", caseSaverRevoked},
		{"dispatcher: the check follows the last saver, not the first", caseSaverReplaced},
		{"dispatcher: a manual fire honors the key of the caller across a minute boundary", caseManualKey},
		{"dispatcher: a manual fire passes the request to the starter", caseManualRequest},
		{"dispatcher: a subject that a new principal reuses gets no start", caseSaverReused},
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

	applied := h.Doubles.Final(t, a)

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

	event := trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)}

	for range twoFollowers {
		_, err := h.Dispatcher.Upstream(t.Context(), event)
		must(t, "upstream", err)
	}

	if got := rec.Jobs(); len(got) != 1 {
		t.Errorf("two events for one apply started %d jobs, want 1", len(got))
	}

	// A second apply is a new event.
	_, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
	must(t, "upstream of a second apply", err)

	if got := rec.Jobs(); len(got) != twoFollowers {
		t.Errorf("two applies started %d jobs, want %d", len(got), twoFollowers)
	}
}

func caseUpstreamNoFollowers(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")

	h.Namespace(t, string(acme))

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
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
		{"a version inside a run", trigger.Applied{Unit: a, Version: h.Doubles.Intermediate(t, a)}},
		{"a final version of another unit", trigger.Applied{Unit: a, Version: h.Doubles.Final(t, b)}},
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

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
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
	return trigger.ManualRequest{Namespace: acme, Trigger: id, Now: now, Reason: "test run", Location: noLocation()}
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
			s.Req.IdempotencyKey != trigger.ManualKey(s.Req.Trigger, actor.Principal.Subject, now) {
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

func caseStartActor(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")
	fence := h.Namespace(t, string(acme))

	putAll(t, h, fence, scheduleTrigger("s1", a), followsTrigger("f1", b, a), reviewTrigger("r1", a))

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	must(t, "scheduled", err)

	_, err = h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
	must(t, "upstream", err)

	caller := ActorOf("user:grace")

	_, err = h.Dispatcher.Manual(t.Context(), caller, manual("r1", at(manualMinute)))
	must(t, "manual", err)

	service := identity.NamespaceServiceSubject(acme)
	want := []struct {
		actor  string
		source job.SourceKind
	}{
		{service, job.SourceSchedule},
		{service, job.SourceTrigger},
		{"user:grace", ""},
	}

	starts := rec.Starts()
	if len(starts) != len(want) {
		t.Fatalf("starts = %+v, want %d", starts, len(want))
	}

	for i := range starts {
		s := &starts[i]
		p := s.Actor.Principal

		if p.Subject != want[i].actor || s.Req.Source != want[i].source || s.Req.Saver != ada {
			t.Errorf("start %d = actor %q, source %q, saver %q; want actor %q, source %q, saver %q",
				i, p.Subject, s.Req.Source, s.Req.Saver, want[i].actor, want[i].source, ada)
		}

		isService := p.IsNamespaceService()
		if isService != (want[i].actor == service) || p.Admin {
			t.Errorf("start %d: service principal %v, admin %v; want service %v and no admin flag",
				i, isService, p.Admin, want[i].actor == service)
		}
	}
}

func caseStartLocation(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")
	fence := h.Namespace(t, string(acme))

	putAll(t, h, fence, scheduleTrigger("s1", a), followsTrigger("f1", b, a))

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	must(t, "scheduled", err)

	_, err = h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
	must(t, "upstream", err)

	loc := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: ""}
	req := manual("s1", at(manualMinute))
	req.Location = loc

	_, err = h.Dispatcher.Manual(t.Context(), ActorOf("user:grace"), req)
	must(t, "manual", err)

	starts := rec.Starts()
	if len(starts) != three {
		t.Fatalf("starts = %+v, want %d", starts, three)
	}

	for i := range starts {
		want := noLocation()
		if i == len(starts)-1 {
			want = loc
		}

		if starts[i].Req.Location != want {
			t.Errorf("start %d has the location %+v, want %+v", i, starts[i].Req.Location, want)
		}
	}
}

func caseSaverRevoked(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a, b := unit(acme, "a"), unit(acme, "b")
	fence := h.Namespace(t, string(acme))

	putAll(t, h, fence, scheduleTrigger("s1", a), followsTrigger("f1", b, a))

	h.Doubles.RevokeSaver(ada)

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	wantErr(t, "scheduled", err, trigger.ErrSaverDenied)
	wantErr(t, "scheduled is a denial", err, identity.ErrDenied)

	caller := ActorOf("user:grace")

	_, err = h.Dispatcher.Manual(t.Context(), caller, manual("s1", at(manualMinute)))
	wantErr(t, "manual", err, trigger.ErrSaverDenied)

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
	must(t, "upstream", err)

	if len(out) != 1 || out[0].Job != "" {
		t.Fatalf("upstream outcomes = %+v, want one outcome and no job", out)
	}

	wantErr(t, "upstream outcome", out[0].Err, trigger.ErrSaverDenied)

	if got := rec.Starts(); len(got) != 0 {
		t.Errorf("a saver that lost access started %v", got)
	}

	refusals := h.Doubles.Refusals(acme)
	if len(refusals) != three {
		t.Fatalf("audit entries of refusals = %d, want %d", len(refusals), three)
	}

	subjects := []string{identity.NamespaceServiceSubject(acme), "user:grace", identity.NamespaceServiceSubject(acme)}
	for i := range refusals {
		e := &refusals[i]
		changes := map[string]string{}

		for _, ch := range e.Changes {
			changes[ch.Key] = ch.New
		}

		if e.Outcome != audit.OutcomeDenied || e.Subject != subjects[i] ||
			e.Action != string(identity.ActionUnitRunPlan) ||
			changes["trigger-saver"] != ada ||
			changes["trigger"] == "" {
			t.Errorf(
				"refusal %d = %+v, want a denied start by %q that names the trigger and the saver",
				i,
				*e,
				subjects[i],
			)
		}
	}

	// The check reads the access of the saver at each start.
	h.Doubles.RestoreSaver(ada)

	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(1))
	must(t, "scheduled after the access came back", err)
}

func caseSaverReplaced(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	tr := scheduleTrigger("s1", a)
	must(t, "put", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	tr.LastSaver, tr.LastSaverID = grace, SaverIDOf(grace)
	must(t, "replace", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	h.Doubles.RevokeSaver(ada)

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	must(t, "scheduled with the first saver revoked", err)

	h.Doubles.RevokeSaver(grace)

	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(1))
	wantErr(t, "scheduled with the last saver revoked", err, trigger.ErrSaverDenied)

	if got := rec.Jobs(); len(got) != 1 {
		t.Errorf("started %d jobs, want 1", len(got))
	}
}

// caseInvalidRow stores rows that Validate refuses, as a row of another version
// would be, and fires each. The dispatcher must fail closed.
func caseInvalidRow(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	if h.StoreRaw == nil {
		t.Skip("the harness has no raw store hook")
	}

	a, b := unit(acme, "a"), unit(acme, "b")

	h.Namespace(t, string(acme))

	badSchedule := withFrequency(scheduleTrigger("s1", a), cronOf("* * * * *"), true)
	badFollows := followsTrigger("f1", b, unit(other, "a"))

	h.StoreRaw(t, badSchedule)
	h.StoreRaw(t, badFollows)

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	wantErr(t, "scheduled", err, trigger.ErrInvalid)

	_, err = h.Dispatcher.Manual(t.Context(), ActorOf("user:ada"), manual("s1", at(0)))
	wantErr(t, "manual of a schedule trigger", err, trigger.ErrInvalid)

	_, err = h.Dispatcher.Manual(t.Context(), ActorOf("user:ada"), manual("f1", at(0)))
	wantErr(t, "manual of a follows trigger", err, trigger.ErrInvalid)

	// This row is a follower of a by its source, so Upstream finds it. Its
	// outcome carries the refusal, and no plan starts.
	withFreq := followsTrigger("f2", b, a)
	withFreq.Frequency = trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}
	h.StoreRaw(t, withFreq)

	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: h.Doubles.Final(t, a)})
	must(t, "upstream", err)

	if len(out) != 1 || out[0].Job != "" {
		t.Fatalf("outcomes = %+v, want one outcome and no job", out)
	}

	wantErr(t, "upstream outcome", out[0].Err, trigger.ErrInvalid)

	if len(rec.Starts()) != 0 {
		t.Errorf("rows that do not validate started %v", rec.Starts())
	}
}

func caseManualKey(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	actor := ActorOf("user:ada")

	keyed := manual("s1", at(manualMinute))
	keyed.IdempotencyKey = "click-1"

	first, err := h.Dispatcher.Manual(t.Context(), actor, keyed)
	must(t, "manual with a key", err)

	// The retry comes in the next minute, and the key still gives the same job.
	keyed.Now = at(manualMinute + 1)

	again, err := h.Dispatcher.Manual(t.Context(), actor, keyed)
	must(t, "manual with the key again", err)

	if first != again || len(rec.Jobs()) != 1 {
		t.Errorf("one key gave %d jobs; %q, %q; want 1", len(rec.Jobs()), first, again)
	}

	if got := rec.Starts()[0].Req.IdempotencyKey; got != "click-1" {
		t.Errorf("key of the start = %q, want the key of the caller", got)
	}

	// Another key is another job.
	keyed.IdempotencyKey = "click-2"

	other, err := h.Dispatcher.Manual(t.Context(), actor, keyed)
	must(t, "manual with another key", err)

	if other == first || len(rec.Jobs()) != 2 {
		t.Errorf("another key gave %d jobs, job %q; want 2", len(rec.Jobs()), other)
	}

	// With no key, the key holds the minute: a fire in the next minute is a
	// new job.
	for _, minute := range []int{manualMinute + 2, manualMinute + 3} {
		_, err = h.Dispatcher.Manual(t.Context(), actor, manual("s1", at(minute)))
		must(t, "manual with no key", err)
	}

	if got := len(rec.Jobs()); got != 4 {
		t.Errorf("fires with no key in two minutes made %d jobs in all, want 4", got)
	}
}

func caseManualRequest(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	req := manual("s1", at(manualMinute))
	req.Request = trigger.RequestInfo{
		Channel: audit.ChannelAPI, SourceIP: "192.0.2.7", UserAgent: "test-agent", RequestID: "req-1",
	}

	_, err := h.Dispatcher.Manual(t.Context(), ActorOf("user:ada"), req)
	must(t, "manual", err)

	starts := rec.Starts()
	if len(starts) != 1 || starts[0].Req.Request != req.Request {
		t.Fatalf("starts = %+v, want one start with the request %+v", starts, req.Request)
	}

	// A start that no person makes carries no request.
	_, err = h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	must(t, "scheduled", err)

	starts = rec.Starts()
	if got := starts[len(starts)-1].Req.Request; got != (trigger.RequestInfo{}) {
		t.Errorf("request of a scheduled start = %+v, want none", got)
	}
}

func caseSaverReused(t *testing.T, h Harness, rec *Recorder) {
	t.Helper()

	a := unit(acme, "a")
	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	// The principal ada is deleted, and a new principal takes the subject.
	h.Doubles.ReuseSubject(ada)

	_, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", at(0))
	wantErr(t, "scheduled with a reused subject", err, trigger.ErrSaverDenied)

	_, err = h.Dispatcher.Manual(t.Context(), ActorOf(ada), manual("s1", at(manualMinute)))
	wantErr(t, "manual with a reused subject", err, trigger.ErrSaverDenied)

	if got := rec.Jobs(); len(got) != 0 {
		t.Errorf("started %d jobs, want none", len(got))
	}
}
