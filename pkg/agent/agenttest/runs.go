package agenttest

import (
	"errors"
	"fmt"
	"slices"
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
		ID:           id,
		Key:          key,
		Subject:      subject,
		CredentialID: "token-" + subject,
		Namespace:    namespace.Name("acme"),
		Parent:       "",
		Model:        "test-model",
		Limits:       agent.DefaultLimits(),
		Prompt:       "Why did the last update of pulumi:acme/web fail?",
		First:        turnStart(id, 1),
	}
}

func turnStart(id agent.RunID, n int) durable.StartRequest {
	return durable.StartRequest{
		Key:     agent.TurnKey(id, n),
		Kind:    agent.KindTurn,
		Version: agent.TurnKindVersion,
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
		{name: "active runs of the installation", run: caseConcurrencyInstallation},
		{name: "record a turn", run: caseRecordTurn},
		{name: "record a turn with a lost claim", run: caseRecordTurnLost},
		{name: "record a step twice", run: caseRecordTurnTwice},
		{name: "usage ledger", run: caseUsage},
		{name: "cancel", run: caseRequestCancel},
		{name: "finish", run: caseFinish},
		{name: "finish after a cancel", run: caseFinishAfterCancel},
		{name: "list", run: caseList},
		{name: "list with a limit out of range", run: caseListLimit},
		{name: "reveal is sealed until the next model step", run: caseReveal},
		{name: "reveal survives a takeover", run: caseRevealTakeover},
		{name: "finish shreds the reveals", run: caseRevealFinish},
		{name: "a follow-up of a reveal keeps the mark", run: caseRevealParent},
		{name: "a reveal expires and the sweep deletes it", run: caseRevealExpired},
		{name: "a reveal needs an expiry after its time", run: caseRevealNoExpiry},
		{name: "a run whose turn failed for good is reaped", run: caseReapFailed},
		{name: "a run whose turn was canceled is reaped", run: caseReapCanceled},
		{name: "a run with a live turn is no orphan", run: caseReapActive},
		{name: "orphans with a limit out of range", run: caseOrphansLimit},
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
		Owner:        "agenttest",
		Kinds:        []durable.Kind{agent.KindTurn},
		MaxVersions:  nil,
		Lease:        testLease,
		TakeCanceled: false,
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
	req.Limits.MaxActiveForSubject = 1
	mustCreate(t, h, req)

	req = NewRunFor("ana", "k2")
	req.Limits.MaxActiveForSubject = 1

	if _, _, err := h.Runs.Create(t.Context(), req); !errors.Is(err, agent.ErrConcurrencyLimit) {
		t.Fatalf("second active run = %v, want ErrConcurrencyLimit", err)
	}

	req = NewRunFor("ben", "k3")
	req.Limits.MaxActiveForSubject = 1
	mustCreate(t, h, req)
}

func caseConcurrencyInstallation(t *testing.T, h RunHarness) {
	t.Helper()

	req := NewRunFor("ana", "k1")
	req.Limits.MaxActive = 2
	mustCreate(t, h, req)

	req = NewRunFor("ben", "k2")
	req.Limits.MaxActive = 2
	mustCreate(t, h, req)

	req = NewRunFor("cleo", "k3")
	req.Limits.MaxActive = 2

	if _, _, err := h.Runs.Create(t.Context(), req); !errors.Is(err, agent.ErrConcurrencyLimit) {
		t.Fatalf("third active run of the installation = %v, want ErrConcurrencyLimit", err)
	}

	req.Limits.MaxActive = 0
	if _, _, err := h.Runs.Create(t.Context(), req); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Create() with MaxActive 0 = %v, want ErrInvalid: 0 is never \"no limit\"", err)
	}
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
		Reveals: nil,
		Shred:   false,
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

	got, err := h.Runs.List(t.Context(), agent.RunFilter{Subject: "ana", Namespace: "", States: nil, Limit: 10})
	if err != nil || len(got) != 2 || got[0].ID != last.ID || got[1].ID != first.ID {
		t.Fatalf("List() = %+v, %v, want the runs of ana, newest first", got, err)
	}

	got, err = h.Runs.List(t.Context(), agent.RunFilter{Subject: "", Namespace: "", States: nil, Limit: 1})
	if err != nil || len(got) != 1 {
		t.Fatalf("List() with limit 1 = %+v, %v, want one run", got, err)
	}
}

func caseListLimit(t *testing.T, h RunHarness) {
	t.Helper()

	mustCreate(t, h, NewRunFor("ana", "k1"))

	for _, limit := range []int{0, -1, agent.MaxRunList + 1} {
		_, err := h.Runs.List(t.Context(), agent.RunFilter{Subject: "", Namespace: "", States: nil, Limit: limit})
		if !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("List() with limit %d = %v, want ErrInvalid", limit, err)
		}
	}
}

// sealed returns a sealed reveal of turn 1 of run. The suite never reads
// the bytes: the store keeps them as they are.
func sealed(run agent.RunID) agent.SealedReveal {
	now := time.Now()

	return agent.SealedReveal{
		Run: run, Turn: 1, CallID: "call-1",
		WrappedKey: []byte("wrapped"), Content: []byte("sealed"), Values: []byte("sealed values"), At: now,
		ExpiresAt: agent.RevealExpiry(now, agent.DefaultLimits()),
	}
}

// recordReveal records the tools step of turn 1 of run with one sealed
// reveal.
func recordReveal(t *testing.T, h RunHarness, op durable.Operation, run agent.RunID) {
	t.Helper()

	rec := turnRecord(run, "masked result")
	rec.Reveals = []agent.SealedReveal{sealed(run)}

	if err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepTools), rec, noNext()); err != nil {
		t.Fatalf("RecordTurn() with a reveal = %v", err)
	}
}

// wantReveals fails t unless the run has n sealed reveals.
func wantReveals(t *testing.T, h RunHarness, run agent.RunID, n int) {
	t.Helper()

	got, err := h.Runs.Reveals(t.Context(), run)
	if err != nil || len(got) != n {
		t.Fatalf("Reveals() = %d reveals, %v, want %d", len(got), err, n)
	}

	if n > 0 && (got[0].CallID != "call-1" || string(got[0].Content) != "sealed") {
		t.Fatalf("Reveals() = %+v, want the sealed reveal as recorded", got[0])
	}
}

func caseReveal(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)
	recordReveal(t, h, op, run.ID)
	wantReveals(t, h, run.ID, 1)

	got, err := h.Runs.Get(t.Context(), run.ID)
	if err != nil || !got.ReadPlainSecrets {
		t.Fatalf("Get() = %+v, %v, want ReadPlainSecrets after a reveal", got, err)
	}

	entries := mustTranscript(t, h, run.ID)
	for i := range entries {
		if slices.ContainsFunc(entries[i].Message.Parts, func(p agent.Part) bool { return p.Text == "sealed" }) {
			t.Fatal("the transcript holds the sealed content")
		}
	}

	rec := turnRecord(run.ID, "answer")
	rec.Shred = true

	if err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepAdvance), rec, noNext()); err != nil {
		t.Fatalf("RecordTurn() that shreds = %v", err)
	}

	wantReveals(t, h, run.ID, 0)

	if got, err := h.Runs.Get(t.Context(), run.ID); err != nil || !got.ReadPlainSecrets {
		t.Fatalf("Get() = %+v, %v, want ReadPlainSecrets to stay after the shred", got, err)
	}

	if _, err := h.Runs.Reveals(t.Context(), "no-such-run"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("Reveals() of an unknown run = %v, want ErrNotFound", err)
	}
}

func caseRevealTakeover(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)
	recordReveal(t, h, op, run.ID)

	// Another instance takes the turn over with a new claim. It reads the
	// sealed reveal again, and a stale claim cannot shred it.
	if err := h.Ops.Release(t.Context(), op.Claim); err != nil {
		t.Fatalf("Release() = %v", err)
	}

	next := claimTurn(t, h)
	wantReveals(t, h, run.ID, 1)

	rec := turnRecord(run.ID, "answer")
	rec.Shred = true

	if err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepAdvance), rec, noNext()); !errors.Is(
		err, durable.ErrClaimLost,
	) {
		t.Fatalf("RecordTurn() of the old claim = %v, want ErrClaimLost", err)
	}

	wantReveals(t, h, run.ID, 1)

	if err := h.Runs.RecordTurn(t.Context(), next.Claim.Step(agent.StepAdvance), rec, noNext()); err != nil {
		t.Fatalf("RecordTurn() of the new claim = %v", err)
	}

	wantReveals(t, h, run.ID, 0)
}

func caseRevealFinish(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)
	recordReveal(t, h, op, run.ID)

	end := runEnd(run.ID, agent.StateFailed, agent.StopCredentialEnded)
	if err := h.Runs.Finish(t.Context(), op.Claim.Step(agent.StepAdvance), end); err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	wantReveals(t, h, run.ID, 0)
}

func mustTranscript(t *testing.T, h RunHarness, run agent.RunID) []agent.TranscriptEntry {
	t.Helper()

	entries, err := h.Runs.Transcript(t.Context(), run)
	if err != nil {
		t.Fatalf("Transcript() = %v", err)
	}

	return entries
}

func caseRevealParent(t *testing.T, h RunHarness) {
	t.Helper()

	parent := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)
	recordReveal(t, h, op, parent.ID)

	end := runEnd(parent.ID, agent.StateSucceeded, agent.StopAnswered)
	if err := h.Runs.Finish(t.Context(), op.Claim.Step(agent.StepAdvance), end); err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	req := NewRunFor("ana", "k2")
	req.Parent = parent.ID

	if child := mustCreate(t, h, req); !child.ReadPlainSecrets {
		t.Fatalf("Create() = %+v, want ReadPlainSecrets from the parent", child)
	}

	if other := mustCreate(t, h, NewRunFor("ana", "k3")); other.ReadPlainSecrets {
		t.Fatalf("Create() = %+v, want no ReadPlainSecrets for a run with no parent", other)
	}
}

func caseRevealExpired(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	// A reveal of a run that no step shredded, past the time limit of the
	// run: Reveals hides it at once, and the sweep deletes it.
	expired := sealed(run.ID)
	expired.CallID = "call-0"
	expired.At = time.Now().Add(-2 * run.Limits.MaxDuration)
	expired.ExpiresAt = agent.RevealExpiry(expired.At, run.Limits)

	rec := turnRecord(run.ID, "masked result")
	rec.Reveals = []agent.SealedReveal{expired, sealed(run.ID)}

	if err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepTools), rec, noNext()); err != nil {
		t.Fatalf("RecordTurn() with two reveals = %v", err)
	}

	wantReveals(t, h, run.ID, 1)

	swept, err := h.Runs.SweepReveals(t.Context())
	if err != nil || swept != 1 {
		t.Fatalf("SweepReveals() = %d, %v, want 1: only the expired reveal", swept, err)
	}

	wantReveals(t, h, run.ID, 1)

	if swept, err := h.Runs.SweepReveals(t.Context()); err != nil || swept != 0 {
		t.Fatalf("second SweepReveals() = %d, %v, want 0", swept, err)
	}
}

func caseRevealNoExpiry(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	op := claimTurn(t, h)

	for name, change := range map[string]func(r *agent.SealedReveal){
		"no expiry":                    func(r *agent.SealedReveal) { r.ExpiresAt = time.Time{} },
		"an expiry at the reveal time": func(r *agent.SealedReveal) { r.ExpiresAt = r.At },
	} {
		r := sealed(run.ID)
		change(&r)

		rec := turnRecord(run.ID, "masked result")
		rec.Reveals = []agent.SealedReveal{r}

		err := h.Runs.RecordTurn(t.Context(), op.Claim.Step(agent.StepTools), rec, noNext())
		if !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("RecordTurn() with a reveal with %s = %v, want ErrInvalid", name, err)
		}
	}

	wantReveals(t, h, run.ID, 0)
}

// wantOrphans fails t unless Orphans answers the runs ids, in order.
func wantOrphans(t *testing.T, h RunHarness, ids ...agent.RunID) {
	t.Helper()

	got, err := h.Runs.Orphans(t.Context(), orphanLimit)
	if err != nil {
		t.Fatalf("Orphans() = %v", err)
	}

	gotIDs := make([]agent.RunID, 0, len(got))
	for i := range got {
		gotIDs = append(gotIDs, got[i].ID)
	}

	if !slices.Equal(gotIDs, ids) {
		t.Fatalf("Orphans() = %v, want %v", gotIDs, ids)
	}
}

// orphanLimit is the limit of the Orphans calls of the suite.
const orphanLimit = 10

func caseReapFailed(t *testing.T, h RunHarness) {
	t.Helper()

	req := NewRunFor("ana", "k1")
	req.Limits.MaxActiveForSubject = 1
	run := mustCreate(t, h, req)
	op := claimTurn(t, h)
	recordReveal(t, h, op, run.ID)

	// The durable runner fails the turn for good: at its failure limit or
	// its expiry limit, both end with a final failure. No step ends the run.
	failure := durable.Failure{Message: "the model step failed", Final: true, RetryAfter: 0}
	if err := h.Ops.Fail(t.Context(), op.Claim, failure); err != nil {
		t.Fatalf("Fail() = %v", err)
	}

	wantOrphans(t, h, run.ID)

	got, err := h.Runs.Reap(t.Context(), run.ID)
	if err != nil || got.State != agent.StateFailed || got.Stop != agent.StopTurnFailed ||
		got.LastError != agent.ErrTurnFailed.Error() {
		t.Fatalf("Reap() = %+v, %v, want a failed run with the stop turn-failed", got, err)
	}

	wantReveals(t, h, run.ID, 0)
	wantOrphans(t, h)

	if _, err := h.Runs.Reap(t.Context(), run.ID); !errors.Is(err, agent.ErrFinished) {
		t.Fatalf("second Reap() = %v, want ErrFinished", err)
	}

	// The run freed its place in the limit of active runs.
	next := NewRunFor("ana", "k2")
	next.Limits.MaxActiveForSubject = 1
	mustCreate(t, h, next)
}

func caseReapCanceled(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))

	if _, err := h.Runs.RequestCancel(t.Context(), run.ID, "ana"); err != nil {
		t.Fatalf("RequestCancel() = %v", err)
	}

	// The turn operation is still pending, so its cancel ends it at once,
	// and no step of the turn runs to end the run.
	op, _, err := h.Ops.Start(t.Context(), turnStart(run.ID, 1))
	if err != nil {
		t.Fatalf("Start() of the existing turn = %v", err)
	}

	if cerr := h.Ops.Cancel(t.Context(), op.ID); cerr != nil {
		t.Fatalf("Cancel() of the turn = %v", cerr)
	}

	wantOrphans(t, h, run.ID)

	got, err := h.Runs.Reap(t.Context(), run.ID)
	if err != nil || got.State != agent.StateCanceled || got.Stop != agent.StopCanceled {
		t.Fatalf("Reap() = %+v, %v, want a canceled run", got, err)
	}
}

func caseReapActive(t *testing.T, h RunHarness) {
	t.Helper()

	run := mustCreate(t, h, NewRunFor("ana", "k1"))
	wantOrphans(t, h)

	if _, err := h.Runs.Reap(t.Context(), run.ID); !errors.Is(err, agent.ErrTurnActive) {
		t.Fatalf("Reap() of a run with a pending turn = %v, want ErrTurnActive", err)
	}

	claimTurn(t, h)

	if _, err := h.Runs.Reap(t.Context(), run.ID); !errors.Is(err, agent.ErrTurnActive) {
		t.Fatalf("Reap() of a run with a running turn = %v, want ErrTurnActive", err)
	}

	if _, err := h.Runs.Reap(t.Context(), "no-such-run"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("Reap() of an unknown run = %v, want ErrNotFound", err)
	}
}

func caseOrphansLimit(t *testing.T, h RunHarness) {
	t.Helper()

	for _, limit := range []int{0, agent.MaxRunList + 1} {
		if _, err := h.Runs.Orphans(t.Context(), limit); !errors.Is(err, agent.ErrInvalid) {
			t.Fatalf("Orphans(%d) = %v, want ErrInvalid", limit, err)
		}
	}
}
