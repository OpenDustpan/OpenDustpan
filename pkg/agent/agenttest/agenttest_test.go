// Tests for the conformance suites. Each suite runs against a small fake in
// memory that keeps the contract, so the cases themselves run on each change.
// The fakes are not adapters: they hold no provider, no job model and no SQL.
package agenttest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/agent/agenttest"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/identity"
)

func TestModelSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunModel(t, func(t *testing.T, s agenttest.Scenario) agent.Model {
		t.Helper()

		return fakeModel{scenario: s}
	})
}

func TestChangesSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunChanges(t, func(t *testing.T) agenttest.ChangesHarness {
		t.Helper()

		unit, err := identity.UnitObject("pulumi", "acme", "web")
		if err != nil {
			t.Fatalf("UnitObject() = %v", err)
		}

		f := &fakeChanges{mu: sync.Mutex{}, byKey: map[string]agent.Change{}, specs: map[string]string{}, frozen: false}

		return agenttest.ChangesHarness{
			Changes:       f,
			Actor:         actor("ana"),
			Outsider:      actor("eve"),
			Unit:          unit,
			WriteBackKind: "config",
			RunKind:       "apply",
			Freeze:        func(*testing.T) { f.freeze() },
		}
	})
}

func TestRunStoreSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunRunStore(t, func(t *testing.T) agenttest.RunHarness {
		t.Helper()

		ops := durabletest.NewMem().Store()

		return agenttest.RunHarness{
			Runs: &fakeRuns{
				mu:         sync.Mutex{},
				ops:        ops,
				runs:       nil,
				transcript: map[agent.RunID][]agent.TranscriptEntry{},
				ledger:     map[string]bool{},
			},
			Ops: ops,
		}
	})
}

func actor(subject string) agent.Actor {
	return agent.Actor{
		Principal: identity.Principal{
			Subject:        subject,
			DisplayName:    subject,
			Admin:          false,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
		},
		Channel:    agent.ChannelAgent,
		RunID:      "run-1",
		SessionID:  "run-1",
		BreakGlass: false,
	}
}

// fakeModel plays one scenario. Its errors never hold the request.
type fakeModel struct {
	scenario agenttest.Scenario
}

func (fakeModel) Info() agent.ModelInfo { return agent.ModelInfo{Provider: "fake", Model: "fake-1"} }

func (m fakeModel) Complete(ctx context.Context, req agent.ModelRequest) (agent.ModelResponse, error) {
	var zero agent.ModelResponse
	if len(req.Messages) == 0 || req.MaxOutputTokens <= 0 {
		return zero, agent.ErrInvalid
	}

	errs := map[agenttest.Scenario]error{
		agenttest.ScenarioRateLimited: agent.RateLimitError{RetryAfter: 7 * time.Second},
		agenttest.ScenarioUnavailable: agent.ErrModelUnavailable,
		agenttest.ScenarioAuth:        agent.ErrModelAuth,
		agenttest.ScenarioBadRequest:  agent.ErrModelRequest,
		agenttest.ScenarioTooLong:     agent.ErrContextTooLong,
		agenttest.ScenarioRefusal:     agent.ErrModelRefused,
	}
	if err, ok := errs[m.scenario]; ok {
		return zero, err
	}

	usage := agent.Usage{InputTokens: 12, OutputTokens: 3, CacheReadTokens: 0, CacheWriteTokens: 0}

	switch m.scenario { //nolint:exhaustive // The error scenarios returned above, and text is the default.
	case agenttest.ScenarioHang:
		<-ctx.Done()

		return zero, fmt.Errorf("fake model: %w", ctx.Err())
	case agenttest.ScenarioToolCall:
		part := agenttest.TextPart("")
		part.Kind = agent.PartToolCall
		part.Call = agent.ToolCall{
			ID:        "c1",
			Name:      agent.ToolDescribeUnit,
			Arguments: json.RawMessage(`{"unit":"pulumi:acme/web"}`),
		}

		return agent.ModelResponse{
			Message: agent.Message{Role: agent.RoleAssistant, Parts: []agent.Part{part}},
			Stop:    agent.StopToolUse,
			Usage:   usage,
			Model:   "fake-1",
		}, nil
	default:
		return agent.ModelResponse{
			Message: agent.Message{Role: agent.RoleAssistant, Parts: []agent.Part{agenttest.TextPart("It failed.")}},
			Stop:    agent.StopEnd,
			Usage:   usage,
			Model:   "fake-1",
		}, nil
	}
}

// fakeChanges is a job model in memory. Only "ana" holds the actions.
type fakeChanges struct {
	mu     sync.Mutex
	byKey  map[string]agent.Change
	specs  map[string]string
	frozen bool
}

func (*fakeChanges) Support(_ context.Context, _ identity.Object, kind string) (agent.KindSupport, error) {
	switch kind {
	case "config":
		return agent.KindSupport{WriteBack: true}, nil
	case "apply":
		return agent.KindSupport{WriteBack: false}, nil
	default:
		return agent.KindSupport{WriteBack: false}, agent.ErrUnknownChangeKind
	}
}

func (f *fakeChanges) Propose(ctx context.Context, req agent.ChangeRequest) (agent.Change, error) {
	var zero agent.Change

	support, err := f.Support(ctx, req.Unit, req.Kind)

	f.mu.Lock()
	defer f.mu.Unlock()

	sig := fmt.Sprintf("%s|%s|%s|%s", req.Unit, req.Kind, req.Mode, req.Spec)

	switch {
	case req.Key == "":
		return zero, agent.ErrInvalid
	case err != nil:
		return zero, err
	case req.Mode == agent.ModeReviewRequest && !support.WriteBack:
		return zero, agent.ErrModeNotSupported
	case req.Actor.Principal.Subject != "ana":
		return zero, fmt.Errorf("propose: %w", identity.ErrDenied)
	case f.frozen:
		return zero, agent.ErrChangeFrozen
	case f.specs[req.Key] != "" && f.specs[req.Key] != sig:
		return zero, agent.ErrChangeKeyConflict
	default:
	}

	if c, ok := f.byKey[req.Key]; ok {
		return c, nil
	}

	c := agent.Change{
		ID:        agent.ChangeID("change-" + req.Key),
		Key:       req.Key,
		Unit:      req.Unit,
		Kind:      req.Kind,
		Mode:      req.Mode,
		State:     agent.ChangeWaiting,
		ReviewURL: "https://review.example/" + req.Key,
		Proposer:  req.Actor.Principal.Subject,
		RunID:     req.Actor.RunID,
		CreatedAt: time.Now(),
	}
	f.byKey[req.Key], f.specs[req.Key] = c, sig

	return c, nil
}

func (f *fakeChanges) Get(_ context.Context, a agent.Actor, id agent.ChangeID) (agent.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.find(a, id)
}

func (f *fakeChanges) Cancel(_ context.Context, a agent.Actor, id agent.ChangeID) (agent.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, err := f.find(a, id)
	if err != nil {
		return c, err
	}

	c.State = agent.ChangeCanceled
	f.byKey[c.Key] = c

	return c, nil
}

func (f *fakeChanges) freeze() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.frozen = true
}

func (f *fakeChanges) find(a agent.Actor, id agent.ChangeID) (agent.Change, error) {
	for key := range f.byKey {
		if c := f.byKey[key]; c.ID == id && a.Principal.Subject == "ana" {
			return c, nil
		}
	}

	var zero agent.Change

	return zero, agent.ErrChangeNotFound
}

// fakeRuns is a run store in memory on a durable store in memory. A fenced
// write records its step first and then writes, which is enough for tests
// in one process.
type fakeRuns struct {
	mu         sync.Mutex
	ops        durable.Store
	runs       []agent.Run
	transcript map[agent.RunID][]agent.TranscriptEntry
	ledger     map[string]bool
}

func (f *fakeRuns) Create(ctx context.Context, req agent.NewRun) (agent.Run, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var zero agent.Run

	if !validNewRun(req) {
		return zero, false, agent.ErrInvalid
	}

	active, i := f.activeOf(req.Subject), f.keyIndex(req.Key)

	switch {
	case i >= 0 && f.runs[i].Subject != req.Subject:
		return zero, false, agent.ErrKeyConflict
	case i >= 0:
		return f.runs[i], false, nil
	case req.MaxActiveForSubject > 0 && active >= req.MaxActiveForSubject:
		return zero, false, agent.ErrConcurrencyLimit
	default:
	}

	if _, _, err := f.ops.Start(ctx, req.First); err != nil {
		return zero, false, fmt.Errorf("start the first turn: %w", err)
	}

	run := agent.Run{
		ID: req.ID, Key: req.Key, Subject: req.Subject, CredentialID: req.CredentialID, Namespace: req.Namespace,
		Parent: req.Parent, Model: req.Model, State: agent.StatePending, Stop: "", LastError: "", Limits: req.Limits,
		Progress: agent.Progress{Turns: 0, ToolCalls: 0, Usage: zero.Ledger, Elapsed: 0}, Ledger: zero.Ledger,
		CancelRequested: false, CanceledBy: "", CreatedAt: time.Now(), EndedAt: time.Time{},
	}
	f.runs = append(f.runs, run)
	f.transcript[run.ID] = []agent.TranscriptEntry{{
		Run: run.ID, Turn: 0, Index: 0, At: run.CreatedAt,
		Message: agent.Message{Role: agent.RoleUser, Parts: []agent.Part{agenttest.TextPart(req.Prompt)}},
	}}

	return run, true, nil
}

func (f *fakeRuns) Get(_ context.Context, id agent.RunID) (agent.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	i := f.index(id)
	if i < 0 {
		var zero agent.Run

		return zero, agent.ErrNotFound
	}

	return f.runs[i], nil
}

func (f *fakeRuns) List(_ context.Context, filter agent.RunFilter) ([]agent.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []agent.Run

	for i := range slices.Backward(f.runs) {
		if filter.Subject != "" && f.runs[i].Subject != filter.Subject {
			continue
		}

		out = append(out, f.runs[i])
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}

	return out, nil
}

func (f *fakeRuns) Transcript(_ context.Context, id agent.RunID) ([]agent.TranscriptEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.index(id) < 0 {
		return nil, agent.ErrNotFound
	}

	return slices.Clone(f.transcript[id]), nil
}

func (f *fakeRuns) RecordTurn(
	ctx context.Context,
	fence durable.StepFence,
	rec agent.TurnRecord,
	next durable.StartRequest,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	i := f.index(rec.Run)
	if i < 0 {
		return agent.ErrNotFound
	}

	if err := f.ops.RecordStep(ctx, fence, nil); err != nil {
		return fmt.Errorf("record the turn: %w", err)
	}

	for n, m := range rec.Messages {
		f.transcript[rec.Run] = append(f.transcript[rec.Run], agent.TranscriptEntry{
			Run: rec.Run, Turn: rec.Turn, Index: n, Message: m, At: time.Now(),
		})
	}

	f.runs[i].Progress = rec.Progress
	f.runs[i].State = agent.StateRunning

	if next.Key == "" {
		return nil
	}

	if _, _, err := f.ops.Start(ctx, next); err != nil {
		return fmt.Errorf("start the next turn: %w", err)
	}

	return nil
}

func (f *fakeRuns) RecordUsage(_ context.Context, rec agent.UsageRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	i := f.index(rec.Run)
	if i < 0 {
		return agent.ErrNotFound
	}

	key := fmt.Sprintf("%s/%d/%d", rec.Run, rec.Turn, rec.Claim)
	if !f.ledger[key] {
		f.ledger[key] = true
		f.runs[i].Ledger = f.runs[i].Ledger.Add(rec.Usage)
	}

	return nil
}

func (f *fakeRuns) RequestCancel(_ context.Context, id agent.RunID, by string) (agent.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var zero agent.Run

	i := f.index(id)

	switch {
	case i < 0:
		return zero, agent.ErrNotFound
	case f.runs[i].State.Terminal():
		return zero, agent.ErrFinished
	case !f.runs[i].CancelRequested:
		f.runs[i].CancelRequested, f.runs[i].CanceledBy = true, by
	default:
	}

	return f.runs[i], nil
}

func (f *fakeRuns) Finish(ctx context.Context, fence durable.StepFence, end agent.RunEnd) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	i := f.index(end.Run)

	switch {
	case !end.State.Terminal():
		return agent.ErrInvalid
	case i < 0:
		return agent.ErrNotFound
	default:
	}

	if err := f.ops.RecordStep(ctx, fence, nil); err != nil {
		return fmt.Errorf("finish the run: %w", err)
	}

	if f.runs[i].CancelRequested {
		end.State, end.Stop = agent.StateCanceled, agent.StopCanceled
	}

	f.runs[i].State, f.runs[i].Stop, f.runs[i].LastError, f.runs[i].EndedAt = end.State, end.Stop, end.LastError, time.Now()

	return nil
}

func (f *fakeRuns) index(id agent.RunID) int {
	return slices.IndexFunc(f.runs, func(r agent.Run) bool { return r.ID == id })
}

// activeOf returns the number of runs of subject that did not end.
func (f *fakeRuns) activeOf(subject string) int {
	active := 0

	for i := range f.runs {
		if f.runs[i].Subject == subject && !f.runs[i].State.Terminal() {
			active++
		}
	}

	return active
}

// keyIndex returns the index of the run that key names, or -1.
func (f *fakeRuns) keyIndex(key string) int {
	return slices.IndexFunc(f.runs, func(r agent.Run) bool { return r.Key == key })
}

func validNewRun(req agent.NewRun) bool {
	return req.ID != "" && req.Key != "" && req.Subject != "" && req.Prompt != "" && req.Limits.Validate() == nil
}
