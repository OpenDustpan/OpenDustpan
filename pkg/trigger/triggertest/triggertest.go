// Package triggertest is the conformance suite of the ports of package
// trigger: Store and Dispatcher. A new adapter runs Run with a Factory.
//
// The cases run on the memory reference in this package's tests. The cases
// for the audit entry run when the harness gives the hook that reads them.
// The cases for the review-request-push kind wait for the slice that adds that
// port.
package triggertest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// Harness is one fresh, empty set of ports for one subtest. Dispatcher starts
// its plans through the Starter that the Factory received, and Doubles holds the
// doubles behind it: the harness builds the Dispatcher with Doubles.Dispatcher.
// Namespace makes the namespace name and returns its current fence. Audited is
// an optional hook: a case that needs it skips when it is nil. It returns the
// number of audit entries of the namespace. StoreRaw is another optional hook:
// it writes tr into the store without Validate and without the cycle check, as
// a row of another version or a restore would. A case that needs it skips when
// it is nil. SetNextSlot and Now are required by the claim cases.
type Harness struct {
	Store      trigger.Store
	Dispatcher trigger.Dispatcher
	Doubles    *Doubles
	Namespace  func(t *testing.T, name string) namespace.Fence
	Audited    func(t *testing.T, ns string) int
	StoreRaw   func(t *testing.T, tr trigger.Trigger)
	// SetNextSlot writes the next slot of the enabled schedule trigger id of ns
	// without any check, as if the time since its save had passed. The claim cases
	// use it: the store reads its own clock, which a case cannot move, so a case
	// moves the stored slot into the past instead. It must be set.
	SetNextSlot func(t *testing.T, ns namespace.Name, id trigger.ID, slot time.Time)
	// Now gives the current time of the clock that the store reads (for a SQL
	// store, the database clock). The claim cases build their expected slots
	// from it. It must be set.
	Now func(t *testing.T) time.Time
}

// Factory returns a fresh harness for one subtest. The dispatcher of the
// harness must start its plans through starter.
type Factory func(t *testing.T, starter trigger.Starter) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness, rec *Recorder)
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := slices.Concat(storeCases(), claimCases(), dispatcherCases())

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := NewRecorder()
			h := newHarness(t, rec)

			if h.Now == nil {
				t.Fatal("the harness does not set Now")
			}

			tc.test(t, h, rec)
		})
	}
}

// Start is one call of Recorder.StartPlan.
type Start struct {
	Actor job.Actor
	Unit  graph.Unit
	Req   trigger.PlanRequest
	Job   job.ID
}

// Recorder is the Starter of the suite. It keeps each start, and it gives one
// job for each idempotency key, as the job service does. It never starts a
// real job.
type Recorder struct {
	failFor map[graph.Unit]error
	byKey   map[string]job.ID
	starts  []Start
	mu      sync.Mutex
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{
		failFor: map[graph.Unit]error{},
		byKey:   map[string]job.ID{},
		starts:  nil,
		mu:      sync.Mutex{},
	}
}

// FailFor makes each start of unit return err.
func (r *Recorder) FailFor(unit graph.Unit, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failFor[unit] = err
}

// StartPlan implements trigger.Starter.
func (r *Recorder) StartPlan(
	_ context.Context,
	actor job.Actor,
	unit graph.Unit,
	req trigger.PlanRequest,
) (job.ID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err, ok := r.failFor[unit]; ok {
		return "", err
	}

	id, seen := r.byKey[req.IdempotencyKey]
	if !seen {
		id = job.ID("plan-" + req.IdempotencyKey)
		r.byKey[req.IdempotencyKey] = id
	}

	r.starts = append(r.starts, Start{Actor: actor, Unit: unit, Req: req, Job: id})

	return id, nil
}

// Starts returns each call of StartPlan, in order.
func (r *Recorder) Starts() []Start {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.starts)
}

// Jobs returns the distinct jobs that the calls started: the calls with one
// idempotency key count once.
func (r *Recorder) Jobs() []job.ID {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]job.ID, 0, len(r.byKey))
	for _, id := range r.byKey {
		out = append(out, id)
	}

	slices.Sort(out)

	return out
}

// Sizes and times that the cases use.
const (
	// twoFollowers is the number of followers in the cases with two.
	twoFollowers = 2
	// twoAudited is the number of audit entries after two writes.
	twoAudited = 2
	// manualMinute is the minute of the manual fires.
	manualMinute = 7
	// doubleClickSpan is a span inside one minute, for a double click.
	doubleClickSpan = 20 * time.Second
	// otherZoneOffset is the offset in seconds of a zone that is not UTC.
	otherZoneOffset = 3600
	// concurrentRounds is the number of rounds of the case with two saves at once.
	concurrentRounds = 50
	// three is the count in the cases with three starts or three refusals.
	three = 3
	// oneDay is the distance of two slots of a daily schedule.
	oneDay = 24 * time.Hour
)

// toolOpenTofu is the tool of the units of the fixtures.
const toolOpenTofu = "opentofu"

// The namespace of the fixtures, and the epoch of at.
const (
	acme     namespace.Name = "acme"
	other    namespace.Name = "other"
	fixtureT                = 1_800_000_000_000
	// ada and grace are the subjects of two savers.
	ada   = "user:ada"
	grace = "user:grace"
)

// at returns the time of the fixtures, plus n minutes.
func at(n int) time.Time {
	return time.UnixMilli(fixtureT).UTC().Add(time.Duration(n) * time.Minute)
}

// unit returns a valid unit of namespace ns with ID id.
func unit(ns namespace.Name, id string) graph.Unit {
	return graph.Unit{Namespace: ns, Tool: toolOpenTofu, ID: id}
}

// stale returns a fence that does not match the one the store holds.
func stale(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// noUnit returns the zero unit, for the source of a trigger that has none.
func noUnit() graph.Unit {
	return graph.Unit{Namespace: "", Tool: "", ID: ""}
}

// noFrequency returns the zero frequency, for a trigger that has none.
func noFrequency() trigger.Frequency {
	return trigger.Frequency{Preset: "", Cron: ""}
}

// followsTrigger returns an enabled trigger that makes u follow src.
func followsTrigger(id trigger.ID, u, src graph.Unit) trigger.Trigger {
	return trigger.Trigger{
		ID:        id,
		Unit:      u,
		Kind:      trigger.KindFollows,
		Frequency: noFrequency(),
		Source:    src,
		Enabled:   true,
		LastSaver: ada, LastSaverID: SaverIDOf(ada),
	}
}

// scheduleTrigger returns an enabled schedule trigger of u that runs daily.
func scheduleTrigger(id trigger.ID, u graph.Unit) trigger.Trigger {
	return trigger.Trigger{
		ID: id, Unit: u, Kind: trigger.KindSchedule,
		Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""}, Source: noUnit(), Enabled: true,
		LastSaver: ada, LastSaverID: SaverIDOf(ada),
	}
}

// reviewTrigger returns an enabled review-request-push trigger of u.
func reviewTrigger(id trigger.ID, u graph.Unit) trigger.Trigger {
	return trigger.Trigger{
		ID:        id,
		Unit:      u,
		Kind:      trigger.KindReviewRequestPush,
		Frequency: noFrequency(),
		Source:    noUnit(),
		Enabled:   true,
		LastSaver: ada, LastSaverID: SaverIDOf(ada),
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func auditOf(action string) audit.Entry {
	return audit.Entry{
		At:            at(0),
		Namespace:     acme,
		Subject:       "user:ada",
		Action:        action,
		IDPSubject:    "",
		TokenID:       "",
		Object:        "opentofu:acme/a",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// ActorOf returns an actor for the person with the subject subject. A harness
// uses it for the principal of a person, and a case uses it for a manual fire.
func ActorOf(subject string) job.Actor {
	return job.Actor{
		Principal: identity.Principal{
			Subject:                subject,
			DisplayName:            "",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Kind:                   "",
			Credential:             "",
			JobID:                  "",
		},
		Agent:       "",
		Mode:        "",
		ConfirmAuth: "",
		AuthAt:      time.Time{},
	}
}

// noLocation returns the zero code location, for a start that names none.
func noLocation() job.Location {
	return job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
}
