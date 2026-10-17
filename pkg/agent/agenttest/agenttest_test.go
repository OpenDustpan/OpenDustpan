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

func TestRunStoreSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunRunStore(t, func(t *testing.T) agenttest.RunHarness {
		t.Helper()

		ops := durabletest.NewMem().Store()

		return agenttest.RunHarness{
			Runs: newFakeRuns(ops),
			Ops:  ops,
		}
	})
}

func actor(subject string) agent.Actor {
	return agent.Actor{
		Principal:         principal(subject),
		Agent:             agent.Ref{Channel: agent.ChannelAgent, RunID: "run-1", SessionID: ""},
		BreakGlassSession: false,
	}
}

func principal(subject string) identity.Principal {
	return identity.Principal{
		Subject:        subject,
		DisplayName:    subject,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
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

// fakeRuns is a run store in memory on a durable store in memory. A fenced
// write records its step first and then writes, which is enough for tests
// in one process.
type fakeRuns struct {
	mu         sync.Mutex
	ops        durable.Store
	runs       []agent.Run
	transcript map[agent.RunID][]agent.TranscriptEntry
	ledger     map[string]bool
	reveals    map[agent.RunID][]agent.SealedReveal
}

func newFakeRuns(ops durable.Store) *fakeRuns {
	return &fakeRuns{
		mu:         sync.Mutex{},
		ops:        ops,
		runs:       nil,
		transcript: map[agent.RunID][]agent.TranscriptEntry{},
		ledger:     map[string]bool{},
		reveals:    map[agent.RunID][]agent.SealedReveal{},
	}
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
	case active >= req.Limits.MaxActiveForSubject || f.activeOf("") >= req.Limits.MaxActive:
		return zero, false, agent.ErrConcurrencyLimit
	default:
	}

	if _, _, err := f.ops.Start(ctx, req.First); err != nil {
		return zero, false, fmt.Errorf("start the first turn: %w", err)
	}

	run := agent.Run{
		ID:           req.ID,
		Key:          req.Key,
		Subject:      req.Subject,
		CredentialID: req.CredentialID,
		Namespace:    req.Namespace,
		Parent:       req.Parent,
		Model:        req.Model,
		State:        agent.StatePending,
		Stop:         "",
		LastError:    "",
		Limits:       req.Limits,
		Progress:     agent.Progress{Turns: 0, ToolCalls: 0, Usage: zero.Ledger, Elapsed: 0},
		Ledger:       zero.Ledger,
		ReadPlainSecrets: f.parentRevealed(
			req.Parent,
		),
		CancelRequested: false,
		CanceledBy:      "",
		CreatedAt:       time.Now(),
		EndedAt:         time.Time{},
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

	if filter.Limit < 1 || filter.Limit > agent.MaxRunList {
		return nil, agent.ErrInvalid
	}

	var out []agent.Run

	for i := range slices.Backward(f.runs) {
		if filter.Subject != "" && f.runs[i].Subject != filter.Subject {
			continue
		}

		out = append(out, f.runs[i])
		if len(out) == filter.Limit {
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

	if len(rec.Reveals) > 0 {
		f.reveals[rec.Run] = append(f.reveals[rec.Run], rec.Reveals...)
		f.runs[i].ReadPlainSecrets = true
	}

	if rec.Shred {
		delete(f.reveals, rec.Run)
	}

	if next.Key == "" {
		return nil
	}

	if _, _, err := f.ops.Start(ctx, next); err != nil {
		return fmt.Errorf("start the next turn: %w", err)
	}

	return nil
}

func (f *fakeRuns) Reveals(_ context.Context, id agent.RunID) ([]agent.SealedReveal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.index(id) < 0 {
		return nil, agent.ErrNotFound
	}

	return slices.Clone(f.reveals[id]), nil
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
	delete(f.reveals, end.Run)

	return nil
}

func (f *fakeRuns) index(id agent.RunID) int {
	return slices.IndexFunc(f.runs, func(r agent.Run) bool { return r.ID == id })
}

// activeOf returns the number of runs of subject that did not end, or of
// every subject for "".
func (f *fakeRuns) activeOf(subject string) int {
	active := 0

	for i := range f.runs {
		if (subject == "" || f.runs[i].Subject == subject) && !f.runs[i].State.Terminal() {
			active++
		}
	}

	return active
}

// parentRevealed reports whether the run parent read secret values.
func (f *fakeRuns) parentRevealed(parent agent.RunID) bool {
	i := f.index(parent)

	return i >= 0 && f.runs[i].ReadPlainSecrets
}

// keyIndex returns the index of the run that key names, or -1.
func (f *fakeRuns) keyIndex(key string) int {
	return slices.IndexFunc(f.runs, func(r agent.Run) bool { return r.Key == key })
}

func validNewRun(req agent.NewRun) bool {
	return req.ID != "" && req.Key != "" && req.Subject != "" && req.Prompt != "" && req.Limits.Validate() == nil
}
