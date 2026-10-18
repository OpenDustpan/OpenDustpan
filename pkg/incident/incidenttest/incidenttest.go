// Package incidenttest is the conformance suite for incident.Store
// implementations. Every store adapter runs it:
//
//	func TestIncidentConformance(t *testing.T) {
//		t.Parallel()
//		incidenttest.Run(t, func(t *testing.T) incidenttest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package incidenttest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/incident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Store  incident.Store
	Domain Domain
}

// Domain makes the rows that an incident needs and that other ports own,
// and reads the audit entries that the store appends.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"create stores an incident and get returns it", testCreateGet},
		{"create refuses an incident that is not new", testCreateNotNew},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"update keeps the namespace, the classifications and the reports", testUpdateKeeps},
		{"update keeps the state, the step times and the opener", testUpdateKeepsState},
		{"update refuses a result that fails Validate", testUpdateInvalid},
		{"step applies the step in the transaction", testStep},
		{"step refuses a step that the stored state does not allow", testStepRefused},
		{"concurrent steps on one version: exactly one wins", testStepOnce},
		{"each write refuses an audit entry of another namespace", testEntryScope},
		{"a closed incident takes notes, classifications and reports only", testClosed},
		{"classifications and reports append, oldest first", testAppend},
		{"list filters by state and unit, newest first", testList},
		{"notes list oldest first, also after the close", testNotes},
		{"each write refuses a fence of another namespace or version", testFences},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop = namespace.Name("shop")
	lab  = namespace.Name("lab")

	actionRespond  = "namespace:respond-incidents"
	actionClassify = "namespace:classify-incidents"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// entryFor returns a valid audit entry of the kind change in the scope of
// shop, for a write that the suite makes.
func entryFor(action string) audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    shop,
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       shop.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "suite",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-suite",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

func entry() audit.Entry { return entryFor(actionRespond) }

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string, opened time.Time, units ...string) incident.Incident {
	return incident.Incident{
		ID:              id,
		Namespace:       shop,
		Title:           "Checkout errors",
		Severity:        2,
		State:           incident.StateOpen,
		DetectedAt:      opened,
		OpenedAt:        opened,
		AcknowledgedAt:  time.Time{},
		MitigatedAt:     time.Time{},
		ResolvedAt:      time.Time{},
		ClosedAt:        time.Time{},
		OpenedBy:        "alice",
		Commander:       "",
		OnCall:          "schedule:network",
		Units:           units,
		Jobs:            nil,
		Reviews:         nil,
		Impact:          "",
		Classifications: nil,
		RootCause:       "",
		Lessons:         "",
		Reports:         nil,
		Version:         0,
	}
}

func classification(major bool, by string) incident.Classification {
	return incident.Classification{
		Major:        major,
		Criteria:     []string{"clients-affected"},
		ClassifiedBy: by,
		ClassifiedAt: now(),
	}
}

func report(kind string) incident.Report {
	return incident.Report{Kind: kind, Reference: "REF-" + kind, SentAt: now(), By: "carol"}
}

func note(id, text string) incident.Note {
	return incident.Note{Incident: id, At: now(), By: "bob", Text: text}
}

func create(t *testing.T, h Harness, fence namespace.Fence, in incident.Incident) {
	t.Helper()

	if err := h.Store.Create(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Create %s: %v", in.ID, err)
	}
}

func get(t *testing.T, h Harness, id string) incident.Incident {
	t.Helper()

	in, err := h.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}

	return in
}

// step runs the step s on the stored version of the incident id.
func step(t *testing.T, h Harness, fence namespace.Fence, id string, s incident.Step) {
	t.Helper()

	_, err := h.Store.Step(
		t.Context(),
		incident.StepRequest{ID: id, Version: get(t, h, id).Version, Step: s, By: "bob", At: now()},
		fence,
		entry(),
	)
	if err != nil {
		t.Fatalf("Step %s: %v", s, err)
	}
}

// closeIncident creates the incident id and takes it through the review
// to the state closed.
func closeIncident(t *testing.T, h Harness, fence namespace.Fence, id string) {
	t.Helper()

	create(t, h, fence, sample(id, now()))
	step(t, h, fence, id, incident.StepResolve)

	in := get(t, h, id)
	in.RootCause, in.Lessons = "A bad route table", "Plan route changes in a freeze-free window"

	if err := h.Store.Update(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Update the review: %v", err)
	}

	step(t, h, fence, id, incident.StepClose)
}

func auditCount(t *testing.T, h Harness, scope namespace.Name) int {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil {
		t.Fatalf("AuditEntries %s: %v", scope, err)
	}

	return len(entries)
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now(), "opentofu:shop/network"))

	got, err := h.Store.Get(t.Context(), "inc-1")
	if err != nil || got.Version != 1 || got.OnCall != "schedule:network" || len(got.Units) != 1 {
		t.Fatalf("Get = %+v (%v), want the created incident with version 1", got, err)
	}

	if err := h.Store.Create(t.Context(), sample("inc-1", now()), fence, entry()); !errors.Is(err, incident.ErrExists) {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}

	if _, err := h.Store.Get(t.Context(), "inc-none"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}

	if n := auditCount(t, h, shop); n != 1 {
		t.Fatalf("audit entries = %d, want 1", n)
	}
}

func testCreateNotNew(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	cases := []struct {
		name   string
		change func(in *incident.Incident)
	}{
		{
			"acknowledged",
			func(in *incident.Incident) { in.State, in.AcknowledgedAt = incident.StateAcknowledged, now() },
		},
		{"closed", func(in *incident.Incident) {
			in.State, in.ResolvedAt, in.ClosedAt = incident.StateClosed, now(), now()
			in.RootCause, in.Lessons = "cause", "lessons"
		}},
		{"open with a time of resolve", func(in *incident.Incident) { in.ResolvedAt = now() }},
		{"in the server scope", func(in *incident.Incident) { in.Namespace = audit.ServerScope }},
	}

	for _, tc := range cases {
		in := sample("inc-1", now())
		tc.change(&in)

		if err := h.Store.Create(t.Context(), in, fence, entry()); !errors.Is(err, incident.ErrInvalid) {
			t.Fatalf("Create %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if _, err := h.Store.Get(t.Context(), "inc-1"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("Get: err = %v, want ErrNotFound", err)
	}

	if n := auditCount(t, h, shop); n != 0 {
		t.Fatalf("audit entries = %d, want none", n)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	in := get(t, h, "inc-1")
	in.Commander = "bob"

	if err := h.Store.Update(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	in.Commander = "carol"

	if err := h.Store.Update(t.Context(), in, fence, entry()); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}

	if got := get(t, h, "inc-1"); got.Version != 2 || got.Commander != "bob" {
		t.Fatalf("Get = version %d, commander %q, want 2 and bob", got.Version, got.Commander)
	}
}

func testUpdateKeepsState(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	in := get(t, h, "inc-1")
	in.Title = "Checkout down"
	in.State, in.AcknowledgedAt, in.MitigatedAt = incident.StateClosed, now(), now()
	in.ResolvedAt, in.ClosedAt = now(), now()
	in.OpenedAt, in.OpenedBy = now().Add(-time.Hour), "mallory"

	if err := h.Store.Update(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := get(t, h, "inc-1")

	switch {
	case got.Title != "Checkout down", got.State != incident.StateOpen, !got.AcknowledgedAt.IsZero(),
		!got.MitigatedAt.IsZero(), !got.ResolvedAt.IsZero(), !got.ClosedAt.IsZero(),
		!got.OpenedAt.Equal(now()), got.OpenedBy != "alice", got.ID != "inc-1", got.Namespace != shop:
		t.Fatalf("Get = %+v, want the new title and the stored state, step times and opener", got)
	default:
		// Update changed the title only.
	}
}

func testUpdateInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	stored := get(t, h, "inc-1")

	cases := []struct {
		name   string
		change func(in *incident.Incident)
	}{
		{"no title", func(in *incident.Incident) { in.Title = "" }},
		{"a severity of 0", func(in *incident.Incident) { in.Severity = 0 }},
		{"detected after the stored open", func(in *incident.Incident) { in.DetectedAt = now().Add(time.Minute) }},
	}

	for _, tc := range cases {
		in := stored
		tc.change(&in)

		if err := h.Store.Update(t.Context(), in, fence, entry()); !errors.Is(err, incident.ErrInvalid) {
			t.Fatalf("Update with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	if got := get(t, h, "inc-1"); got.Version != stored.Version || got.Title != stored.Title {
		t.Fatalf("Get = %+v, want the incident as created", got)
	}

	if n := auditCount(t, h, shop); n != 1 {
		t.Fatalf("audit entries = %d, want 1", n)
	}
}

func testStep(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	later := now().Add(time.Minute)

	got, err := h.Store.Step(
		t.Context(),
		incident.StepRequest{ID: "inc-1", Version: 1, Step: incident.StepAcknowledge, By: "bob", At: later},
		fence,
		entry(),
	)
	if err != nil || got.State != incident.StateAcknowledged || got.Commander != "bob" ||
		!got.AcknowledgedAt.Equal(later) || got.Version != 2 {
		t.Fatalf("Step = %+v (%v), want acknowledged by bob at version 2", got, err)
	}

	if stored := get(t, h, "inc-1"); stored.State != incident.StateAcknowledged || stored.Version != 2 {
		t.Fatalf("Get = %+v, want the acknowledged incident", stored)
	}

	_, err = h.Store.Step(
		t.Context(),
		incident.StepRequest{ID: "inc-1", Version: 1, Step: incident.StepMitigate, By: "bob", At: later},
		fence,
		entry(),
	)
	if !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("Step on an older version: err = %v, want ErrConflict", err)
	}

	_, err = h.Store.Step(
		t.Context(),
		incident.StepRequest{ID: "inc-none", Version: 1, Step: incident.StepMitigate, By: "bob", At: later},
		fence,
		entry(),
	)
	if !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("Step on an unknown incident: err = %v, want ErrNotFound", err)
	}

	if n := auditCount(t, h, shop); n != 2 {
		t.Fatalf("audit entries = %d, want 2", n)
	}
}

func testStepRefused(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	cases := []struct {
		name string
		step incident.Step
		at   time.Time
		want error
	}{
		{"close an open incident", incident.StepClose, now(), incident.ErrTransition},
		{"reopen an open incident", incident.StepReopen, now(), incident.ErrTransition},
		{"an unknown step", incident.Step("bogus"), now(), incident.ErrTransition},
		{"acknowledge before the open", incident.StepAcknowledge, now().Add(-time.Minute), incident.ErrInvalid},
	}

	for _, tc := range cases {
		_, err := h.Store.Step(
			t.Context(),
			incident.StepRequest{ID: "inc-1", Version: 1, Step: tc.step, By: "bob", At: tc.at},
			fence,
			entry(),
		)
		if !errors.Is(err, tc.want) {
			t.Fatalf("Step %s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	step(t, h, fence, "inc-1", incident.StepResolve)

	_, err := h.Store.Step(
		t.Context(),
		incident.StepRequest{ID: "inc-1", Version: 2, Step: incident.StepClose, By: "bob", At: now()},
		fence,
		entry(),
	)
	if !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("Step close with no review: err = %v, want ErrInvalid", err)
	}

	if got := get(t, h, "inc-1"); got.State != incident.StateResolved || got.Version != 2 {
		t.Fatalf("Get = %+v, want the resolved incident at version 2", got)
	}

	if n := auditCount(t, h, shop); n != 2 {
		t.Fatalf("audit entries = %d, want 2", n)
	}
}

func testStepOnce(t *testing.T, h Harness) {
	t.Helper()

	const n = 8

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	var (
		wg   sync.WaitGroup
		errs [n]error
	)

	for i := range n {
		wg.Go(func() {
			_, errs[i] = h.Store.Step(
				context.WithoutCancel(t.Context()),
				incident.StepRequest{
					ID:      "inc-1",
					Version: 1,
					Step:    incident.StepAcknowledge,
					By:      principal(i),
					At:      now(),
				},
				fence,
				entry(),
			)
		})
	}

	wg.Wait()

	winner := oneWinner(t, errs[:])

	got := get(t, h, "inc-1")
	if winner == "" || got.Version != 2 || got.Commander != winner {
		t.Fatalf("Get = version %d, commander %q, want 2 and the one winner %q", got.Version, got.Commander, winner)
	}

	if c := auditCount(t, h, shop); c != 2 {
		t.Fatalf("audit entries = %d, want 2", c)
	}
}

// oneWinner returns the principal of the one step in errs that succeeded,
// and fails t when none or more than one succeeded, or when a step failed
// with another error than ErrConflict or ErrTransition.
func oneWinner(t *testing.T, errs []error) string {
	t.Helper()

	winner := ""

	for i, err := range errs {
		switch {
		case err == nil && winner == "":
			winner = principal(i)
		case err == nil:
			t.Fatalf("Step %d: a second step won", i)
		case !errors.Is(err, incident.ErrConflict) && !errors.Is(err, incident.ErrTransition):
			t.Fatalf("Step %d: err = %v, want ErrConflict or ErrTransition", i, err)
		default:
			// Another step won first.
		}
	}

	if winner == "" {
		t.Fatal("no step won")
	}

	return winner
}

// principal returns the name of the principal i of a concurrent case.
func principal(i int) string { return fmt.Sprintf("user-%d", i) }

// testEntryScope runs each write with the fence of shop and an audit entry
// that names lab, and checks that no write changes a row or appends an
// entry.
func testEntryScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	if _, err := h.Domain.CreateNamespace(t.Context(), lab); err != nil {
		t.Fatalf("CreateNamespace %s: %v", lab, err)
	}

	stored := get(t, h, "inc-1")

	for _, w := range entryWrites(t, h, stored) {
		if got := w.write(fence); !errors.Is(got, w.scope) {
			t.Fatalf("%s with an entry of %s: err = %v, want %v", w.name, lab, got, w.scope)
		}
	}

	checkUnchanged(t, h, stored)
}

// labEntry returns entryFor(action) in the scope of lab.
func labEntry(action string) audit.Entry {
	e := entryFor(action)
	e.Namespace, e.Object = lab, lab.String()

	return e
}

// entryWrites returns each write of the store on the incident stored, and
// a create of the incident inc-2, each with an audit entry of lab.
func entryWrites(t *testing.T, h Harness, stored incident.Incident) []fenceWrite {
	t.Helper()

	return []fenceWrite{
		{"Create", incident.ErrInvalid, func(f namespace.Fence) error {
			return h.Store.Create(t.Context(), sample("inc-2", now()), f, labEntry(actionRespond))
		}},
		{"Update", incident.ErrNotFound, func(f namespace.Fence) error {
			in := stored
			in.Title = "Changed"

			return h.Store.Update(t.Context(), in, f, labEntry(actionRespond))
		}},
		{"Step", incident.ErrNotFound, func(f namespace.Fence) error {
			_, err := h.Store.Step(
				t.Context(),
				incident.StepRequest{
					ID:      stored.ID,
					Version: stored.Version,
					Step:    incident.StepAcknowledge,
					By:      "bob",
					At:      now(),
				},
				f,
				labEntry(actionRespond),
			)

			return err
		}},
		{"SetClassification", incident.ErrNotFound, func(f namespace.Fence) error {
			c := classification(true, "carol")

			return h.Store.SetClassification(t.Context(), stored.ID, c, f, labEntry(actionClassify))
		}},
		{"AddReport", incident.ErrNotFound, func(f namespace.Fence) error {
			return h.Store.AddReport(t.Context(), stored.ID, report("initial"), f, labEntry(actionClassify))
		}},
		{"AddNote", incident.ErrNotFound, func(f namespace.Fence) error {
			return h.Store.AddNote(t.Context(), note(stored.ID, "text"), f, labEntry(actionRespond))
		}},
	}
}

func testUpdateKeeps(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	err := h.Store.SetClassification(
		t.Context(),
		"inc-1",
		classification(true, "carol"),
		fence,
		entryFor(actionClassify),
	)
	if err != nil {
		t.Fatalf("SetClassification: %v", err)
	}

	in := get(t, h, "inc-1")
	in.Title, in.Classifications, in.Reports = "Checkout down", nil, []incident.Report{report("initial")}

	if err := h.Store.Update(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := get(t, h, "inc-1")
	if c, ok := got.Classification(); got.Title != "Checkout down" || !ok || !c.Major || len(got.Reports) != 0 {
		t.Fatalf("Get = %+v, want the new title, the stored classification and no report", got)
	}

	moved := got
	moved.Namespace = lab

	if err := h.Store.Update(t.Context(), moved, fence, entry()); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("Update to another namespace: err = %v, want ErrInvalid", err)
	}
}

func testClosed(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	closeIncident(t, h, fence, "inc-1")

	in := get(t, h, "inc-1")
	if in.State != incident.StateClosed {
		t.Fatalf("state = %s, want closed", in.State)
	}

	changed := in
	changed.Title = "Changed"

	if err := h.Store.Update(t.Context(), changed, fence, entry()); !errors.Is(err, incident.ErrTransition) {
		t.Fatalf("Update of a closed incident: err = %v, want ErrTransition", err)
	}

	err := h.Store.SetClassification(
		t.Context(),
		"inc-1",
		classification(true, "carol"),
		fence,
		entryFor(actionClassify),
	)
	if err != nil {
		t.Fatalf("SetClassification of a closed incident: %v", err)
	}

	if err := h.Store.AddReport(t.Context(), "inc-1", report("final"), fence, entryFor(actionClassify)); err != nil {
		t.Fatalf("AddReport to a closed incident: %v", err)
	}

	if err := h.Store.AddNote(t.Context(), note("inc-1", "Sent the final report"), fence, entry()); err != nil {
		t.Fatalf("AddNote to a closed incident: %v", err)
	}

	got := get(t, h, "inc-1")
	if got.State != incident.StateClosed || got.Title != in.Title || got.Version != in.Version+2 ||
		len(got.Classifications) != 1 || len(got.Reports) != 1 || got.Reports[0].Kind != "final" {
		t.Fatalf("Get = %+v, want the closed incident with one classification and one report", got)
	}
}

func testAppend(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	for _, c := range []incident.Classification{classification(false, "carol"), classification(true, "dave")} {
		if err := h.Store.SetClassification(t.Context(), "inc-1", c, fence, entryFor(actionClassify)); err != nil {
			t.Fatalf("SetClassification: %v", err)
		}
	}

	for _, kind := range []string{"initial", "intermediate"} {
		if err := h.Store.AddReport(t.Context(), "inc-1", report(kind), fence, entryFor(actionClassify)); err != nil {
			t.Fatalf("AddReport: %v", err)
		}
	}

	bad := []struct {
		name  string
		write func() error
		want  error
	}{
		{"classification with no person", func() error {
			return h.Store.SetClassification(
				t.Context(),
				"inc-1",
				classification(true, ""),
				fence,
				entryFor(actionClassify),
			)
		}, incident.ErrInvalid},
		{"report with no reference", func() error {
			r := report("final")
			r.Reference = ""

			return h.Store.AddReport(t.Context(), "inc-1", r, fence, entryFor(actionClassify))
		}, incident.ErrInvalid},
		{"classification of an unknown incident", func() error {
			return h.Store.SetClassification(
				t.Context(),
				"inc-none",
				classification(true, "carol"),
				fence,
				entryFor(actionClassify),
			)
		}, incident.ErrNotFound},
		{"report on an unknown incident", func() error {
			return h.Store.AddReport(t.Context(), "inc-none", report("final"), fence, entryFor(actionClassify))
		}, incident.ErrNotFound},
	}

	for _, tc := range bad {
		if err := tc.write(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	got := get(t, h, "inc-1")
	c, ok := got.Classification()

	switch {
	case got.Version != 5, len(got.Classifications) != 2, !ok, c.ClassifiedBy != "dave",
		got.Classifications[0].ClassifiedBy != "carol", len(got.Reports) != 2, got.Reports[0].Kind != "initial":
		t.Fatalf("Get = %+v, want two classifications and two reports, oldest first, version 5", got)
	default:
		// The incident holds both lists in order.
	}

	if n := auditCount(t, h, shop); n != 5 {
		t.Fatalf("audit entries = %d, want 5", n)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now(), "opentofu:shop/network"))
	create(t, h, fence, sample("inc-2", now().Add(time.Hour), "opentofu:shop/db"))

	got, err := h.Store.List(t.Context(), incident.Filter{Namespace: shop, State: "", Unit: "", After: "", Limit: 0})
	if err != nil || len(got) != 2 || got[0].ID != "inc-2" {
		t.Fatalf("List = %d (%v), want inc-2 then inc-1", len(got), err)
	}

	got, err = h.Store.List(
		t.Context(),
		incident.Filter{Namespace: shop, State: "", Unit: "opentofu:shop/network", After: "", Limit: 0},
	)
	if err != nil || len(got) != 1 || got[0].ID != "inc-1" {
		t.Fatalf("List by unit = %d (%v), want inc-1", len(got), err)
	}

	got, err = h.Store.List(
		t.Context(),
		incident.Filter{Namespace: shop, State: incident.StateClosed, Unit: "", After: "", Limit: 0},
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("List closed = %d (%v), want none", len(got), err)
	}
}

func testNotes(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	closeIncident(t, h, fence, "inc-1")

	for i, text := range []string{"Paged the network team", "Rolled back"} {
		n := note("inc-1", text)
		n.At = now().Add(time.Duration(i) * time.Minute)

		if err := h.Store.AddNote(t.Context(), n, fence, entry()); err != nil {
			t.Fatalf("AddNote: %v", err)
		}
	}

	if err := h.Store.AddNote(t.Context(), note("inc-1", ""), fence, entry()); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("AddNote with no text: err = %v, want ErrInvalid", err)
	}

	err := h.Store.AddNote(t.Context(), note("inc-none", "text"), fence, entry())
	if !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("AddNote to an unknown incident: err = %v, want ErrNotFound", err)
	}

	got, err := h.Store.Notes(t.Context(), "inc-1")
	if err != nil || len(got) != 2 || got[0].Text != "Paged the network team" {
		t.Fatalf("Notes = %+v (%v), want two, oldest first", got, err)
	}
}

// fenceWrite is one write of the store with the fence f, for the fence
// cases.
type fenceWrite struct {
	name string
	// scope is the error of a zero fence or the fence of another
	// namespace for this write.
	scope error
	write func(f namespace.Fence) error
}

// fenceWrites returns each write of the store on the incident stored, and
// a create of the incident inc-2.
func fenceWrites(t *testing.T, h Harness, stored incident.Incident) []fenceWrite {
	t.Helper()

	return []fenceWrite{
		{"Create", incident.ErrInvalid, func(f namespace.Fence) error {
			return h.Store.Create(t.Context(), sample("inc-2", now()), f, entry())
		}},
		{"Update", incident.ErrNotFound, func(f namespace.Fence) error {
			in := stored
			in.Title = "Changed"

			return h.Store.Update(t.Context(), in, f, entry())
		}},
		{"Step", incident.ErrNotFound, func(f namespace.Fence) error {
			_, err := h.Store.Step(
				t.Context(),
				incident.StepRequest{
					ID:      stored.ID,
					Version: stored.Version,
					Step:    incident.StepAcknowledge,
					By:      "bob",
					At:      now(),
				},
				f,
				entry(),
			)

			return err
		}},
		{"SetClassification", incident.ErrNotFound, func(f namespace.Fence) error {
			c := classification(true, "carol")

			return h.Store.SetClassification(t.Context(), stored.ID, c, f, entryFor(actionClassify))
		}},
		{"AddReport", incident.ErrNotFound, func(f namespace.Fence) error {
			return h.Store.AddReport(t.Context(), stored.ID, report("initial"), f, entryFor(actionClassify))
		}},
		{"AddNote", incident.ErrNotFound, func(f namespace.Fence) error {
			return h.Store.AddNote(t.Context(), note(stored.ID, "text"), f, entry())
		}},
	}
}

// testFences runs each write with a zero fence, the fence of another
// namespace and a stale fence, and checks that no write changes a row or
// appends an audit entry.
func testFences(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	other, err := h.Domain.CreateNamespace(t.Context(), lab)
	if err != nil {
		t.Fatalf("CreateNamespace %s: %v", lab, err)
	}

	stored := get(t, h, "inc-1")

	fences := []struct {
		name  string
		fence namespace.Fence
		stale bool
	}{
		{"a zero fence", namespace.Fence{Namespace: "", FailoverVersion: 0}, false},
		{"the fence of another namespace", other, false},
		{"a stale fence", fence, true},
	}

	_, err = h.Domain.BumpFence(t.Context(), fence)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	for _, f := range fences {
		for _, w := range fenceWrites(t, h, stored) {
			want := w.scope
			if f.stale {
				want = namespace.ErrFenced
			}

			if got := w.write(f.fence); !errors.Is(got, want) {
				t.Fatalf("%s with %s: err = %v, want %v", w.name, f.name, got, want)
			}
		}
	}

	checkUnchanged(t, h, stored)
}

// checkUnchanged fails t when the incident stored, its timeline or the
// audit log changed, or when the incident inc-2 exists.
func checkUnchanged(t *testing.T, h Harness, stored incident.Incident) {
	t.Helper()

	got := get(t, h, stored.ID)
	if got.Version != stored.Version || got.Title != stored.Title || len(got.Classifications) != 0 ||
		len(got.Reports) != 0 {
		t.Fatalf("Get = %+v, want the incident as created", got)
	}

	_, err := h.Store.Get(t.Context(), "inc-2")
	if !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("Get inc-2: err = %v, want ErrNotFound", err)
	}

	notes, err := h.Store.Notes(t.Context(), stored.ID)
	if err != nil || len(notes) != 0 {
		t.Fatalf("Notes = %d (%v), want none", len(notes), err)
	}

	if n, m := auditCount(t, h, shop), auditCount(t, h, lab); n != 1 || m != 0 {
		t.Fatalf("audit entries = %d in %s and %d in %s, want 1 and 0", n, shop, m, lab)
	}
}
