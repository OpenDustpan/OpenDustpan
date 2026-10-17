package agenttest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// RunHarness is one fresh, empty database for one case of the run store
// suite. Ops is the store of durable operations on the same database, so a
// case can claim the operation of a turn.
type RunHarness struct {
	Runs agent.RunStore
	Ops  durable.Store
}

// RunFactory returns a fresh harness for one case.
type RunFactory func(t *testing.T) RunHarness

// runCase is one case of the run store suite.
type runCase struct {
	name string
	run  func(t *testing.T, h RunHarness)
}

// testLease is the lease of the claims of the suite. No case waits for it.
const testLease = time.Minute

// RunRunStore runs the run store suite against the store that f makes.
func RunRunStore(t *testing.T, f RunFactory) {
	t.Helper()

	for _, c := range runCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// NewRunFor returns a request for a run of subject with key. The ID of the
// run is "run-" and the key.
func NewRunFor(subject, key string) agent.NewRun {
	id := agent.RunID("run-" + key)

	return agent.NewRun{
		ID:                  id,
		Key:                 key,
		Subject:             subject,
		CredentialID:        "token-" + subject,
		Namespace:           namespace.Name("acme"),
		Parent:              "",
		Model:               "test-model",
		Limits:              agent.DefaultLimits(),
		Prompt:              "Why did the last update of pulumi:acme/web fail?",
		MaxActiveForSubject: 0,
		MaxActive:           0,
		First:               turnStart(id, 1),
	}
}

func turnStart(id agent.RunID, n int) durable.StartRequest {
	return durable.StartRequest{
		Key:     agent.TurnKey(id, n),
		Kind:    agent.KindTurn,
		Version: 0,
		Input:   fmt.Appendf(nil, `{"run":%q,"turn":%d}`, id, n),
	}
}

func runCases() []runCase {
	return []runCase{
		{name: "create", run: caseCreate},
		{name: "create twice with one key", run: caseCreateTwice},
		{name: "one key for two subjects", run: caseCreateKeyConflict},
		{name: "create with bad fields", run: caseCreateInvalid},
		{name: "active runs of a subject", run: caseConcurrency},
		{name: "record a turn", run: caseRecordTurn},
		{name: "record a turn with a lost claim", run: caseRecordTurnLost},
		{name: "record a step twice", run: caseRecordTurnTwice},
		{name: "usage ledger", run: caseUsage},
		{name: "cancel", run: caseRequestCancel},
		{name: "finish", run: caseFinish},
		{name: "finish after a cancel", run: caseFinishAfterCancel},
		{name: "list", run: caseList},
	}
}

func mustCreate(t *testing.T, h RunHarness, req agent.NewRun) agent.Run {
	t.Helper()

	run, created, err := h.Runs.Create(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("Create() = %+v, %v, %v, want a new run", run, created, err)
	}

	return run
}

// claimTurn claims the one operation of a turn that can run.
func claimTurn(t *testing.T, h RunHarness) durable.Operation {
	t.Helper()

	op, err := h.Ops.Claim(t.Context(), durable.ClaimRequest{
		Owner:       "agenttest",
		Kinds:       []durable.Kind{agent.KindTurn},
		MaxVersions: nil,
		Lease:       testLease,
	})
	if err != nil {
		t.Fatalf("Claim() = %v", err)
	}

	return op
}

func caseCreate(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))

	checkNewRun(t, run)

	if op := claimTurn(t, h); op.Key != agent.TurnKey(run.ID, 1) {
		t.Fatalf("first turn operation key = %q, want %q", op.Key, agent.TurnKey(run.ID, 1))
	}

	entries, err := h.Runs.Transcript(t.Context(), run.ID)
	if err != nil || len(entries) != 1 || entries[0].Turn != 0 || entries[0].Message.Role != agent.RoleUser {
		t.Fatalf("Transcript() = %+v, %v, want the prompt at turn 0", entries, err)
	}
}

func checkNewRun(t *testing.T, run agent.Run) {
	t.Helper()

	switch {
	case run.ID != "run-k1" || run.Subject != "ana" || run.Key != "k1" || run.CredentialID != "token-ana":
		t.Fatalf("Create() = %+v, want the ID and the fields of the request", run)
	case run.State != agent.StatePending || run.Stop != "" || run.CreatedAt.IsZero():
		t.Fatalf("Create() state %q stop %q created %v, want pending", run.State, run.Stop, run.CreatedAt)
	case run.Limits != agent.DefaultLimits():
		t.Fatalf("Create() limits = %+v, want the limits of the request", run.Limits)
	default:
	}
}

func caseCreateTwice(t *testing.T, h RunHarness) {
	t.Helper()

	first := mustCreate(t, h, NewRunFor("ana", "k1"))

	second, created, err := h.Runs.Create(t.Context(), NewRunFor("ana", "k1"))
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second Create() = %+v, %v, %v, want run %q and created false", second, created, err, first.ID)
	}
}

func caseCreateKeyConflict(t *testing.T, h RunHarness) {
	t.Helper()

	mustCreate(t, h, NewRunFor("ana", "k1"))

	if _, _, err := h.Runs.Create(t.Context(), NewRunFor("ben", "k1")); !errors.Is(err, agent.ErrKeyConflict) {
		t.Fatalf("Create() by another subject = %v, want ErrKeyConflict", err)
	}
}

func caseCreateInvalid(t *testing.T, h RunHarness) {
	t.Helper()

	for name, change := range map[string]func(r *agent.NewRun){
		"no ID":      func(r *agent.NewRun) { r.ID = "" },
		"no key":     func(r *agent.NewRun) { r.Key = "" },
		"no subject": func(r *agent.NewRun) { r.Subject = "" },
		"no turns":   func(r *agent.NewRun) { r.Limits.MaxTurns = 0 },
		"no prompt":  func(r *agent.NewRun) { r.Prompt = "" },
	} {
		req := NewRunFor("ana", "k1")
		change(&req)

		if _, _, err := h.Runs.Create(t.Context(), req); !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("Create() with %s = %v, want ErrInvalid", name, err)
		}
	}
}

func caseConcurrency(t *testing.T, h RunHarness) {
	t.Helper()

	req := NewRunFor("ana", "k1")
	req.MaxActiveForSubject = 1
	mustCreate(t, h, req)

	req = NewRunFor("ana", "k2")
	req.MaxActiveForSubject = 1

	if _, _, err := h.Runs.Create(t.Context(), req); !errors.Is(err, agent.ErrConcurrencyLimit) {
		t.Fatalf("second active run = %v, want ErrConcurrencyLimit", err)
	}

	req = NewRunFor("ben", "k3")
	req.MaxActiveForSubject = 1
	mustCreate(t, h, req)
}

// noNext is the zero start request: a record that starts no operation.
func noNext() durable.StartRequest {
	return durable.StartRequest{Key: "", Kind: "", Version: 0, Input: nil}
}

// turnRecord returns the record of turn 1 with an assistant message for
// each text.
func turnRecord(id agent.RunID, texts ...string) agent.TurnRecord {
	msgs := make([]agent.Message, 0, len(texts))
	for _, text := range texts {
		msgs = append(msgs, agent.Message{Role: agent.RoleAssistant, Parts: []agent.Part{TextPart(text)}})
	}

	return agent.TurnRecord{
		Run:      id,
		Turn:     1,
		Messages: msgs,
		Progress: agent.Progress{
			Turns:     1,
			ToolCalls: 0,
			Usage:     agent.Usage{InputTokens: 0, OutputTokens: 0, CacheReadTokens: 0, CacheWriteTokens: 0},
			Elapsed:   0,
		},
	}
}

func caseRecordTurn(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	rec := turnRecord(run.ID, "first", "second")
	if err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepModel), rec, turnStart(run.ID, 2)); err != nil {
		t.Fatalf("RecordTurn() = %v", err)
	}

	entries, err := h.Runs.Transcript(t.Context(), run.ID)
	if err != nil || len(entries) != 3 || entries[1].Index != 0 || entries[2].Index != 1 ||
		entries[2].Message.Parts[0].Text != "second" {
		t.Fatalf("Transcript() = %+v, %v, want the prompt and two messages in order", entries, err)
	}

	got, err := h.Runs.Get(t.Context(), run.ID)
	if err != nil || got.Progress.Turns != 1 {
		t.Fatalf("Get() = %+v, %v, want the progress of the record", got, err)
	}

	next, _, err := h.Ops.Start(t.Context(), turnStart(run.ID, 2))
	if err != nil || next.State != durable.StatePending {
		t.Fatalf("operation of turn 2 = %+v, %v, want the pending operation that RecordTurn started", next, err)
	}
}

func caseRecordTurnLost(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	stale := op.Claim.Step(agent.StepModel)
	stale.Token++

	err := h.Runs.RecordTurn(t.Context(), stale, turnRecord(run.ID, "lost"), turnStart(run.ID, 2))
	if !errors.Is(err, durable.ErrClaimLost) {
		t.Fatalf("RecordTurn() with a stale token = %v, want ErrClaimLost", err)
	}

	if entries, err := h.Runs.Transcript(t.Context(), run.ID); err != nil || len(entries) != 1 {
		t.Fatalf("Transcript() = %+v, %v, want the prompt only", entries, err)
	}
}

func caseRecordTurnTwice(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)
	f := op.Claim.Step(agent.StepModel)

	if err := h.Runs.RecordTurn(t.Context(), f, turnRecord(run.ID, "once"), noNext()); err != nil {
		t.Fatalf("RecordTurn() = %v", err)
	}

	err := h.Runs.RecordTurn(t.Context(), f, turnRecord(run.ID, "twice"), noNext())
	if !errors.Is(err, durable.ErrStepDone) {
		t.Fatalf("second RecordTurn() = %v, want ErrStepDone", err)
	}
}

func caseUsage(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	rec := agent.UsageRecord{
		Run:   run.ID,
		Turn:  1,
		Claim: 1,
		Model: "test-model",
		Usage: agent.Usage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 50, CacheWriteTokens: 0},
		At:    time.Now(),
	}

	for range 2 {
		if err := h.Runs.RecordUsage(t.Context(), rec); err != nil {
			t.Fatalf("RecordUsage() = %v", err)
		}
	}

	rec.Claim = 2
	if err := h.Runs.RecordUsage(t.Context(), rec); err != nil {
		t.Fatalf("RecordUsage() of a second attempt = %v", err)
	}

	got, err := h.Runs.Get(t.Context(), run.ID)
	want := agent.Usage{InputTokens: 200, OutputTokens: 20, CacheReadTokens: 100, CacheWriteTokens: 0}

	if err != nil || got.Ledger != want {
		t.Fatalf("Get() ledger = %+v, %v, want %+v: one record for each attempt", got.Ledger, err, want)
	}

	rec.Run = "no-such-run"
	if err := h.Runs.RecordUsage(t.Context(), rec); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("RecordUsage() of an unknown run = %v, want ErrNotFound", err)
	}
}

func caseRequestCancel(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))

	for range 2 {
		got, err := h.Runs.RequestCancel(t.Context(), run.ID, "ben")
		if err != nil || !got.CancelRequested || got.CanceledBy != "ben" {
			t.Fatalf("RequestCancel() = %+v, %v, want a cancel by ben", got, err)
		}
	}

	if _, err := h.Runs.RequestCancel(t.Context(), "no-such-run", "ben"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("RequestCancel() of an unknown run = %v, want ErrNotFound", err)
	}
}

func runEnd(id agent.RunID, state agent.State, stop agent.StopReason) agent.RunEnd {
	return agent.RunEnd{Run: id, State: state, Stop: stop, LastError: ""}
}

func caseFinish(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	err := h.Runs.Finish(t.Context(), op.Claim.Step(agent.StepAdvance), runEnd(run.ID, agent.StateRunning, ""))
	if !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Finish() with a state that is not terminal = %v, want ErrInvalid", err)
	}

	err = h.Runs.Finish(
		t.Context(),
		op.Claim.Step(agent.StepAdvance),
		runEnd(run.ID, agent.StateSucceeded, agent.StopAnswered),
	)
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	got, err := h.Runs.Get(t.Context(), run.ID)
	if err != nil || got.State != agent.StateSucceeded || got.Stop != agent.StopAnswered || got.EndedAt.IsZero() {
		t.Fatalf("Get() = %+v, %v, want succeeded and answered", got, err)
	}

	if _, err := h.Runs.RequestCancel(t.Context(), run.ID, "ana"); !errors.Is(err, agent.ErrFinished) {
		t.Fatalf("RequestCancel() of an ended run = %v, want ErrFinished", err)
	}
}

func caseFinishAfterCancel(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	if _, err := h.Runs.RequestCancel(t.Context(), run.ID, "ana"); err != nil {
		t.Fatalf("RequestCancel() = %v", err)
	}

	err := h.Runs.Finish(
		t.Context(),
		op.Claim.Step(agent.StepAdvance),
		runEnd(run.ID, agent.StateSucceeded, agent.StopAnswered),
	)
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	if got, err := h.Runs.Get(t.Context(), run.ID); err != nil || got.State != agent.StateCanceled {
		t.Fatalf("Get() = %+v, %v, want canceled: a requested cancel wins", got, err)
	}
}

func caseList(t *testing.T, h RunHarness) {
	t.Helper()

	first := mustCreate(t, h, NewRunFor("ana", "k1"))
	mustCreate(t, h, NewRunFor("ben", "k2"))
	last := mustCreate(t, h, NewRunFor("ana", "k3"))

	got, err := h.Runs.List(t.Context(), agent.RunFilter{Subject: "ana", Namespace: "", States: nil, Limit: 0})
	if err != nil || len(got) != 2 || got[0].ID != last.ID || got[1].ID != first.ID {
		t.Fatalf("List() = %+v, %v, want the runs of ana, newest first", got, err)
	}

	got, err = h.Runs.List(t.Context(), agent.RunFilter{Subject: "", Namespace: "", States: nil, Limit: 1})
	if err != nil || len(got) != 1 {
		t.Fatalf("List() with limit 1 = %+v, %v, want one run", got, err)
	}
}
