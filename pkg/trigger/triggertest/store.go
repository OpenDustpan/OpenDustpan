package triggertest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func storeCases() []suiteCase {
	return []suiteCase{
		{"store: put and get round trip each kind", caseRoundTrip},
		{"store: put refuses an invalid trigger", casePutInvalid},
		{"store: put replaces a trigger of the same ID", casePutReplace},
		{"store: a replace cannot change the unit or the kind", casePutKeepsUnitAndKind},
		{"store: put refuses a stale fence and a fence of another namespace", casePutFence},
		{"store: a failed put changes nothing", casePutChangesNothing},
		{"store: a follows trigger that closes a loop is refused with the path", caseCycleRefused},
		{"store: a loop across tools is refused", caseCycleAcrossTools},
		{"store: a trigger that is off closes no loop and switching it on is checked", caseCycleDisabled},
		{"store: a replace is checked without its old edge", caseCycleReplace},
		{"store: a delete opens the loop again", caseCycleDelete},
		{"store: get and delete of an unknown trigger", caseNotFound},
		{"store: delete refuses a stale fence", caseDeleteFence},
		{"store: list returns the triggers of one unit ordered by ID", caseList},
		{"store: followers returns the enabled followers of one source", caseFollowers},
		{"store: a trigger is invisible to another namespace", caseNamespaceIsolation},
		{"store: two saves at once cannot close a loop", casePutConcurrentCycle},
		{"store: get and delete refuse an invalid namespace", caseInvalidNamespace},
		{"store: a write appends an audit entry", caseAudit},
		{"store: the last saver round trips and a replace sets the new saver", caseLastSaver},
		{"store: put refuses a saver that is empty, too long, hidden, not UTF-8 or a service principal", casePutSaver},
		{"store: put refuses an audit entry of another namespace and an invalid entry", casePutAuditEntry},
	}
}

func caseRoundTrip(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	triggers := []trigger.Trigger{
		followsTrigger("f1", b, a),
		scheduleTrigger("s1", a),
		reviewTrigger("r1", a),
		withFrequency(scheduleTrigger("s2", a), trigger.Frequency{Preset: "", Cron: "30 4 * * 1-5"}, false),
	}

	for i := range triggers {
		tr := &triggers[i]

		must(t, "put "+string(tr.ID), h.Store.Put(t.Context(), *tr, fence, auditOf("trigger:put")))

		got, err := h.Store.Get(t.Context(), acme, tr.ID)
		must(t, "get "+string(tr.ID), err)

		if got != *tr {
			t.Errorf("get %s = %+v, want %+v", tr.ID, got, *tr)
		}
	}
}

func casePutInvalid(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	bad := []struct {
		name string
		tr   trigger.Trigger
	}{
		{"empty ID", followsTrigger("", b, a)},
		{"unknown kind", withKind(scheduleTrigger("x", a), "manual")},
		{"schedule no freq", withFrequency(scheduleTrigger("x", a), noFrequency(), true)},
		{"schedule too fast", withFrequency(scheduleTrigger("x", a), cronOf("*/10 * * * *"), true)},
		{"schedule bad cron", withFrequency(scheduleTrigger("x", a), cronOf("0 0 30 2 *\n"), true)},
		{"follows no source", followsTrigger("x", b, noUnit())},
		{"follows a unit of another namespace", followsTrigger("x", b, unit(other, "a"))},
		{
			"review with a frequency",
			withFrequency(reviewTrigger("x", a), trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, true),
		},
	}

	for i := range bad {
		err := h.Store.Put(t.Context(), bad[i].tr, fence, auditOf("trigger:put"))
		wantErr(t, bad[i].name, err, trigger.ErrInvalid)
	}

	wantErr(
		t,
		"a unit that follows itself",
		h.Store.Put(t.Context(), followsTrigger("x", a, a), fence, auditOf("trigger:put")),
		trigger.ErrCycle,
	)

	list, err := h.Store.List(t.Context(), a)
	must(t, "list", err)

	if len(list) != 0 {
		t.Errorf("invalid triggers were stored: %v", list)
	}
}

func casePutReplace(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	tr := scheduleTrigger("s1", a)
	must(t, "put", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	tr.Frequency = trigger.Frequency{Preset: "", Cron: "0 6 * * *"}
	tr.Enabled = false
	must(t, "replace", h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")))

	got, err := h.Store.Get(t.Context(), acme, "s1")
	must(t, "get", err)

	if got != tr {
		t.Errorf("get = %+v, want %+v", got, tr)
	}

	list, err := h.Store.List(t.Context(), a)
	must(t, "list", err)

	if len(list) != 1 {
		t.Errorf("a replace made %d triggers, want 1", len(list))
	}
}

func casePutKeepsUnitAndKind(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("t1", a), fence, auditOf("trigger:put")))

	wantErr(
		t,
		"move to another unit",
		h.Store.Put(t.Context(), scheduleTrigger("t1", b), fence, auditOf("trigger:put")),
		trigger.ErrInvalid,
	)
	wantErr(
		t,
		"change the kind",
		h.Store.Put(t.Context(), reviewTrigger("t1", a), fence, auditOf("trigger:put")),
		trigger.ErrInvalid,
	)

	got, err := h.Store.Get(t.Context(), acme, "t1")
	must(t, "get", err)

	if got != scheduleTrigger("t1", a) {
		t.Errorf("a refused replace changed the trigger: %+v", got)
	}
}

func casePutFence(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	otherFence := h.Namespace(t, string(other))
	tr := scheduleTrigger("s1", unit(acme, "a"))

	wantErr(t, "stale fence", h.Store.Put(t.Context(), tr, stale(fence), auditOf("trigger:put")), namespace.ErrFenced)
	wantErr(
		t,
		"fence of another namespace",
		h.Store.Put(t.Context(), tr, otherFence, auditOf("trigger:put")),
		namespace.ErrFenced,
	)
	wantErr(t, "get after the refusals", getErr(t, h, acme, "s1"), trigger.ErrNotFound)
}

func casePutChangesNothing(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	must(t, "put", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))

	changed := followsTrigger("f1", b, a)
	changed.Enabled = false

	wantErr(
		t,
		"stale fence",
		h.Store.Put(t.Context(), changed, stale(fence), auditOf("trigger:put")),
		namespace.ErrFenced,
	)

	got, err := h.Store.Get(t.Context(), acme, "f1")
	must(t, "get", err)

	if !got.Enabled {
		t.Error("a put with a stale fence changed the trigger")
	}
}

func caseCycleRefused(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b, c := unit(acme, "a"), unit(acme, "b"), unit(acme, "c")

	must(t, "b follows a", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))
	must(t, "c follows b", h.Store.Put(t.Context(), followsTrigger("f2", c, b), fence, auditOf("trigger:put")))

	err := h.Store.Put(t.Context(), followsTrigger("f3", a, c), fence, auditOf("trigger:put"))
	wantErr(t, "a follows c", err, trigger.ErrCycle)

	var cycle *trigger.CycleError

	if !errors.As(err, &cycle) {
		t.Fatalf("a follows c: %v, want a *CycleError", err)
	}

	want := []graph.Unit{c, a, b, c}
	if !slices.Equal(cycle.Path, want) {
		t.Errorf("path = %v, want %v", cycle.Path, want)
	}

	wantErr(t, "the refused trigger", getErr(t, h, acme, "f3"), trigger.ErrNotFound)
}

func caseCycleAcrossTools(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	tofu := graph.Unit{Namespace: acme, Tool: toolOpenTofu, ID: "net"}
	other := graph.Unit{Namespace: acme, Tool: "pulumi", ID: "app"}

	must(
		t,
		"pulumi follows opentofu",
		h.Store.Put(t.Context(), followsTrigger("f1", other, tofu), fence, auditOf("trigger:put")),
	)
	wantErr(
		t,
		"opentofu follows pulumi",
		h.Store.Put(t.Context(), followsTrigger("f2", tofu, other), fence, auditOf("trigger:put")),
		trigger.ErrCycle,
	)
}

func caseCycleDisabled(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	must(t, "b follows a", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))

	off := followsTrigger("f2", a, b)
	off.Enabled = false

	must(t, "a follows b, off", h.Store.Put(t.Context(), off, fence, auditOf("trigger:put")))

	on := off
	on.Enabled = true

	wantErr(t, "switch it on", h.Store.Put(t.Context(), on, fence, auditOf("trigger:put")), trigger.ErrCycle)

	got, err := h.Store.Get(t.Context(), acme, "f2")
	must(t, "get", err)

	if got.Enabled {
		t.Error("a refused switch-on changed the trigger")
	}
}

func caseCycleReplace(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b, c := unit(acme, "a"), unit(acme, "b"), unit(acme, "c")

	must(t, "b follows a", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))
	must(t, "a follows c", h.Store.Put(t.Context(), followsTrigger("f2", a, c), fence, auditOf("trigger:put")))

	// f1 moves its source from a to c: the old edge a -> b is gone, so the
	// new edge c -> b closes no loop.
	must(t, "b follows c", h.Store.Put(t.Context(), followsTrigger("f1", b, c), fence, auditOf("trigger:put")))
}

func caseCycleDelete(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	must(t, "b follows a", h.Store.Put(t.Context(), followsTrigger("f1", b, a), fence, auditOf("trigger:put")))
	wantErr(
		t,
		"a follows b",
		h.Store.Put(t.Context(), followsTrigger("f2", a, b), fence, auditOf("trigger:put")),
		trigger.ErrCycle,
	)

	must(t, "delete f1", h.Store.Delete(t.Context(), acme, "f1", fence, auditOf("trigger:delete")))
	must(t, "a follows b", h.Store.Put(t.Context(), followsTrigger("f2", a, b), fence, auditOf("trigger:put")))
}

func caseNotFound(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))

	wantErr(t, "get", getErr(t, h, acme, "nope"), trigger.ErrNotFound)
	wantErr(
		t,
		"delete",
		h.Store.Delete(t.Context(), acme, "nope", fence, auditOf("trigger:delete")),
		trigger.ErrNotFound,
	)
	wantErr(t, "get with a bad ID", getErr(t, h, acme, "No Way"), trigger.ErrInvalid)
	wantErr(
		t,
		"delete with a bad ID",
		h.Store.Delete(t.Context(), acme, "", fence, auditOf("trigger:delete")),
		trigger.ErrInvalid,
	)

	_, err := h.Store.List(t.Context(), noUnit())
	wantErr(t, "list of an invalid unit", err, trigger.ErrInvalid)

	_, err = h.Store.Followers(t.Context(), noUnit())
	wantErr(t, "followers of an invalid unit", err, trigger.ErrInvalid)
}

func caseDeleteFence(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", unit(acme, "a")), fence, auditOf("trigger:put")))
	wantErr(
		t,
		"delete with a stale fence",
		h.Store.Delete(t.Context(), acme, "s1", stale(fence), auditOf("trigger:delete")),
		namespace.ErrFenced,
	)

	must(t, "get", getErr(t, h, acme, "s1"))
}

func caseList(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	empty, err := h.Store.List(t.Context(), a)
	must(t, "list of an empty unit", err)

	if len(empty) != 0 {
		t.Errorf("list of a unit with no trigger = %v", empty)
	}

	putAll(
		t,
		h,
		fence,
		scheduleTrigger("t3", a),
		reviewTrigger("t1", a),
		followsTrigger("t2", a, b),
		scheduleTrigger("t9", b),
	)

	got, err := h.Store.List(t.Context(), a)
	must(t, "list", err)

	ids := triggerIDs(got)
	if !slices.Equal(ids, []trigger.ID{"t1", "t2", "t3"}) {
		t.Errorf("list = %v, want [t1 t2 t3]", ids)
	}
}

func caseFollowers(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b, c, d := unit(acme, "a"), unit(acme, "b"), unit(acme, "c"), unit(acme, "d")

	off := followsTrigger("f4", d, a)
	off.Enabled = false

	putAll(
		t,
		h,
		fence,
		followsTrigger("f3", c, a),
		followsTrigger("f1", b, a),
		followsTrigger("f5", d, c),
		off,
		scheduleTrigger("s1", b),
	)

	got, err := h.Store.Followers(t.Context(), a)
	must(t, "followers", err)

	ids := triggerIDs(got)
	if !slices.Equal(ids, []trigger.ID{"f1", "f3"}) {
		t.Errorf("followers of a = %v, want [f1 f3]", ids)
	}

	none, err := h.Store.Followers(t.Context(), d)
	must(t, "followers of d", err)

	if len(none) != 0 {
		t.Errorf("followers of a unit nothing follows = %v", none)
	}
}

func caseNamespaceIsolation(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	h.Namespace(t, string(other))

	// The same unit ID and the same trigger ID in two namespaces are two
	// objects.
	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", unit(acme, "a")), fence, auditOf("trigger:put")))
	wantErr(t, "get in another namespace", getErr(t, h, other, "s1"), trigger.ErrNotFound)

	list, err := h.Store.List(t.Context(), unit(other, "a"))
	must(t, "list", err)

	if len(list) != 0 {
		t.Errorf("a trigger of one namespace is listed in another: %v", list)
	}
}

// casePutConcurrentCycle puts "a follows b" and "b follows a" at the same
// time, many times over. Each round has the same two saves, and exactly one of
// them must win. An adapter that does not serialize the writes of a namespace
// lets both pass the cycle check.
func casePutConcurrentCycle(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))

	for round := range concurrentRounds {
		x := unit(acme, fmt.Sprintf("x%d", round))
		y := unit(acme, fmt.Sprintf("y%d", round))
		triggers := []trigger.Trigger{
			followsTrigger(trigger.ID(fmt.Sprintf("p%d", round)), x, y),
			followsTrigger(trigger.ID(fmt.Sprintf("q%d", round)), y, x),
		}
		errs := make([]error, len(triggers))

		var wg sync.WaitGroup

		start := make(chan struct{})

		for i := range triggers {
			wg.Go(func() {
				<-start

				errs[i] = h.Store.Put(t.Context(), triggers[i], fence, auditOf("trigger:put"))
			})
		}

		close(start)
		wg.Wait()

		refused := 0

		for _, err := range errs {
			switch {
			case err == nil:
			case errors.Is(err, trigger.ErrCycle):
				refused++
			default:
				t.Fatalf("round %d: put = %v, want nil or ErrCycle", round, err)
			}
		}

		if refused != 1 {
			t.Fatalf("round %d: %d saves got ErrCycle, want exactly 1 (errors %v)", round, refused, errs)
		}
	}
}

func caseInvalidNamespace(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))

	for _, ns := range []namespace.Name{"", "bad ns", "..", "acme\n"} {
		wantErr(t, fmt.Sprintf("get in %q", ns), getErr(t, h, ns, "s1"), trigger.ErrInvalid)
		wantErr(
			t,
			fmt.Sprintf("delete in %q", ns),
			h.Store.Delete(t.Context(), ns, "s1", fence, auditOf("trigger:delete")),
			trigger.ErrInvalid,
		)
	}
}

func caseAudit(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	if h.Audited == nil {
		t.Skip("the harness has no audit hook")
	}

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	must(t, "put", h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, auditOf("trigger:put")))

	if got := h.Audited(t, string(acme)); got != 1 {
		t.Fatalf("audit entries after a put = %d, want 1", got)
	}

	// A refused write appends nothing.
	wantErr(
		t,
		"refused put",
		h.Store.Put(t.Context(), scheduleTrigger("s1", a), stale(fence), auditOf("trigger:put")),
		namespace.ErrFenced,
	)
	wantErr(
		t,
		"refused delete",
		h.Store.Delete(t.Context(), acme, "nope", fence, auditOf("trigger:delete")),
		trigger.ErrNotFound,
	)

	if got := h.Audited(t, string(acme)); got != 1 {
		t.Fatalf("audit entries after refused writes = %d, want 1", got)
	}

	must(t, "delete", h.Store.Delete(t.Context(), acme, "s1", fence, auditOf("trigger:delete")))

	if got := h.Audited(t, string(acme)); got != twoAudited {
		t.Fatalf("audit entries after a delete = %d, want %d", got, twoAudited)
	}
}

func caseLastSaver(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a, b := unit(acme, "a"), unit(acme, "b")

	putAll(t, h, fence, scheduleTrigger("s1", a), followsTrigger("f1", b, a))

	// A replace by another person makes that person the last saver.
	changed := scheduleTrigger("s1", a)
	changed.LastSaver = grace
	changed.Enabled = false

	must(t, "replace", h.Store.Put(t.Context(), changed, fence, auditOf("trigger:put")))

	got, err := h.Store.Get(t.Context(), acme, "s1")
	must(t, "get", err)

	if got.LastSaver != grace || got != changed {
		t.Errorf("get = %+v, want %+v", got, changed)
	}

	list, err := h.Store.List(t.Context(), a)
	must(t, "list", err)

	if len(list) != 1 || list[0].LastSaver != grace {
		t.Errorf("list = %+v, want one trigger saved by %s", list, grace)
	}

	followers, err := h.Store.Followers(t.Context(), a)
	must(t, "followers", err)

	if len(followers) != 1 || followers[0].LastSaver != ada {
		t.Errorf("followers = %+v, want one trigger saved by %s", followers, ada)
	}
}

func casePutSaver(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	for name, saver := range map[string]string{
		"empty":                     "",
		"too long":                  strings.Repeat("s", trigger.MaxSaverBytes+1),
		"a newline":                 "user:ada\nroot",
		"a bidirectional mark":      "user:ada\u202e",
		"not UTF-8":                 "user:\xff",
		"a service principal":       "system:namespace-service:acme",
		"a service principal cased": "System:Namespace-Service:acme",
	} {
		tr := scheduleTrigger("s1", a)
		tr.LastSaver = saver

		wantErr(
			t,
			"saver that is "+name,
			h.Store.Put(t.Context(), tr, fence, auditOf("trigger:put")),
			trigger.ErrInvalid,
		)
	}

	wantErr(t, "get after the refusals", getErr(t, h, acme, "s1"), trigger.ErrNotFound)
}

func casePutAuditEntry(t *testing.T, h Harness, _ *Recorder) {
	t.Helper()

	fence := h.Namespace(t, string(acme))
	a := unit(acme, "a")

	foreign := auditOf("trigger:put")
	foreign.Namespace = other

	noSubject := auditOf("trigger:put")
	noSubject.Subject = ""

	entries := []struct {
		name  string
		entry *audit.Entry
	}{{"an entry of another namespace", &foreign}, {"an entry with no subject", &noSubject}}

	for i := range entries {
		wantErr(t, entries[i].name,
			h.Store.Put(t.Context(), scheduleTrigger("s1", a), fence, *entries[i].entry), audit.ErrInvalidEntry)
	}

	wantErr(t, "delete with an entry of another namespace", h.Store.Delete(t.Context(), acme, "s1", fence, foreign),
		audit.ErrInvalidEntry)
	wantErr(t, "get after the refusals", getErr(t, h, acme, "s1"), trigger.ErrNotFound)
}

// getErr returns the error of Store.Get.
func getErr(t *testing.T, h Harness, ns namespace.Name, id trigger.ID) error {
	t.Helper()

	_, err := h.Store.Get(t.Context(), ns, id)

	return err
}

// withFrequency returns tr with the frequency f, switched on or off.
func withFrequency(tr trigger.Trigger, f trigger.Frequency, enabled bool) trigger.Trigger {
	tr.Frequency = f
	tr.Enabled = enabled

	return tr
}

// withKind returns tr with the kind k.
func withKind(tr trigger.Trigger, k trigger.Kind) trigger.Trigger {
	tr.Kind = k

	return tr
}

// cronOf returns the frequency of a custom cron expression.
func cronOf(expr string) trigger.Frequency {
	return trigger.Frequency{Preset: "", Cron: expr}
}

// putAll stores each trigger, and fails the case when a put fails.
func putAll(t *testing.T, h Harness, fence namespace.Fence, triggers ...trigger.Trigger) {
	t.Helper()

	for i := range triggers {
		must(t, "put "+string(triggers[i].ID), h.Store.Put(t.Context(), triggers[i], fence, auditOf("trigger:put")))
	}
}

// triggerIDs returns the IDs of triggers, in order.
func triggerIDs(triggers []trigger.Trigger) []trigger.ID {
	ids := make([]trigger.ID, 0, len(triggers))
	for i := range triggers {
		ids = append(ids, triggers[i].ID)
	}

	return ids
}
