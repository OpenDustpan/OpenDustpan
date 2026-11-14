package sqlite_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/audit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// hotpathNoRefusals drops every out-of-scope refusal. The benchmarks never
// refuse a key, so the recorder needs no real recorder of refusals.
type hotpathNoRefusals struct{}

func (hotpathNoRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

func (hotpathNoRefusals) RecordMismatch(context.Context, namespace.ID, string) error {
	return nil
}

// openHotpathStore returns a fresh SQLite store in a temporary directory with
// the namespace acme, and the fence of that namespace.
func openHotpathStore(b *testing.B) (*sqlite.Store, namespace.Fence) {
	b.Helper()

	ctx := b.Context()

	s, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	fence, err := (&jobDomain{s: s}).CreateNamespace(ctx, "acme")
	if err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	return s, fence
}

// hotpathPlanJob returns a queued runner job of the plan operation on unit,
// with one step called plan. Its phases come from the default phases of the
// operation, as a dispatch makes them.
func hotpathPlanJob(b *testing.B, unit identity.Object) *job.Job {
	b.Helper()

	phases, err := job.DefaultPhases(job.ExecutorRunner, job.OperationPlan)
	if err != nil {
		b.Fatalf("default phases: %v", err)
	}

	step := job.Step{
		Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil, Dir: "",
		Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
	}

	j := job.Job{
		ID: job.ID(ident.NewID(job.IDPrefix)), NamespaceID: "", State: job.StateQueued, Wait: job.WaitNone,
		Reason: "", Current: 0, StartedBy: "al", Source: job.SourceAPI, Agent: "", AgentMode: "", BreakGlass: nil,
		Delivery: nil, Closed: nil, Verification: job.VerificationNone, IdempotencyKey: "", RequestHash: "",
		Trigger: nil,
		Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{}, StateChangedAt: time.Time{},
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: job.PurposeNone, Executor: job.ExecutorRunner,
			Location: job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
			Steps:    []job.Step{step}, Pool: "rpl-default", Profile: "prf-default", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
		Phases: make([]job.Phase, len(phases)),
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// queueHotpathJob stores j as a queued job with its first phase at version 1.
func queueHotpathJob(b *testing.B, s *sqlite.Store, fence namespace.Fence, j *job.Job) {
	b.Helper()

	start := &durable.StartRequest{
		Key:     job.PhaseKey(j.ID, 0),
		Kind:    job.RunnerKind(j.Spec.Pool),
		Version: 1,
		Input:   nil,
	}

	if _, created, err := s.Jobs().Create(b.Context(), *j, start, fence, nil); err != nil || !created {
		b.Fatalf("create job = created %v, %v", created, err)
	}
}

// BenchmarkRecorderPutPlan measures one DurableRecorder.PutPlan over the
// SQLite store, with a display part and an apply part of 4 KiB each. A call
// reserves the plan row and its item key in one transaction, seals the parts
// into the blob store and then stores the plan record. A phase takes one plan
// per claim, because a second plan under the same claim reads a stale state
// version and is refused. Each iteration therefore queues a job and claims its
// first phase outside the timer, and only PutPlan is timed.
func BenchmarkRecorderPutPlan(b *testing.B) {
	b.ReportAllocs()

	s, fence := openHotpathStore(b)
	ctx := b.Context()
	domain := &jobDomain{s: s}

	claimer := job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s, jobtest.NoGates{})

	// A fixed key of zeros: the benchmark needs no secrecy, only a valid key.
	wrapper, err := secret.NewBuiltinWrapper(make([]byte, 32))
	if err != nil {
		b.Fatalf("wrapper: %v", err)
	}

	blobs, err := blobfs.Open(b.TempDir())
	if err != nil {
		b.Fatalf("open blob store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			b.Errorf("close blob store: %v", closeErr)
		}
	})

	itemBlobs, err := secret.NewItemKeyBlobs(blobs, hotpathNoRefusals{}, hotpathNoRefusals{})
	if err != nil {
		b.Fatalf("item key blobs: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: s, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: s, Now: time.Now,
	})
	if err != nil {
		b.Fatalf("item sealer: %v", err)
	}

	artifacts, err := job.NewArtifactBlobs(blobs, hotpathNoRefusals{})
	if err != nil {
		b.Fatalf("artifact blobs: %v", err)
	}

	rec, err := job.NewRecorder(job.RecorderConfig{
		Store: s.Jobs(), Jobs: s.Jobs(), Blobs: artifacts, Sealer: sealer, Namespaces: s,
		Retention: history.DefaultRetentionSettings(), Now: time.Now,
		Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		b.Fatalf("recorder: %v", err)
	}

	display := bytes.Repeat([]byte("d"), 4<<10)
	apply := bytes.Repeat([]byte("a"), 4<<10)

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		b.StopTimer()

		unit, err := domain.CreateUnit(ctx, fence, "opentofu", fmt.Sprintf("plan-%d", i))
		if err != nil {
			b.Fatalf("create unit: %v", err)
		}

		j := hotpathPlanJob(b, unit)
		queueHotpathJob(b, s, fence, j)

		a, claimErr := claimer.Claim(ctx, job.ClaimRequest{
			Owner: "runner-1", Kinds: []durable.Kind{job.RunnerKind(j.Spec.Pool)}, MaxVersions: nil, Lease: time.Hour,
		})
		if claimErr != nil {
			b.Fatalf("claim: %v", claimErr)
		}

		parts := []job.PlanUpload{
			{Kind: job.OutputPlanDisplay, Body: bytes.NewReader(display)},
			{Kind: job.OutputPlanApply, Body: bytes.NewReader(apply)},
		}

		b.StartTimer()

		if _, putErr := rec.PutPlan(ctx, a.Claim, parts); putErr != nil {
			b.Fatalf("PutPlan: %v", putErr)
		}
	}
}

// BenchmarkRunnerClaim measures one claim of a queued phase through the job
// claimer over the SQLite store. A runner poll reaches this path: the claimer
// calls the durable store, which runs the claim transaction under the write
// lock. Each iteration queues one job on its own unit outside the timer, so
// every claim takes an operation that is due.
func BenchmarkRunnerClaim(b *testing.B) {
	b.ReportAllocs()

	s, fence := openHotpathStore(b)
	ctx := b.Context()
	domain := &jobDomain{s: s}

	claimer := job.NewClaimer(s.Jobs(), s.DurableOperationsPort(), s, jobtest.NoGates{})
	req := job.ClaimRequest{
		Owner: "runner-1", Kinds: []durable.Kind{job.RunnerKind("rpl-default")}, MaxVersions: nil, Lease: time.Hour,
	}

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		b.StopTimer()

		unit, err := domain.CreateUnit(ctx, fence, "opentofu", fmt.Sprintf("network-%d", i))
		if err != nil {
			b.Fatalf("create unit: %v", err)
		}

		queueHotpathJob(b, s, fence, hotpathPlanJob(b, unit))

		b.StartTimer()

		if _, claimErr := claimer.Claim(ctx, req); claimErr != nil {
			b.Fatalf("Claim: %v", claimErr)
		}
	}
}

// BenchmarkAuditAppend measures one audit.Store.Append over the SQLite store:
// the entry validation, the read of the last hash of the namespace, and the
// chained write in one transaction with the fence check.
func BenchmarkAuditAppend(b *testing.B) {
	b.ReportAllocs()

	s, fence := openHotpathStore(b)
	ctx := b.Context()

	e := audit.Entry{
		At:            time.UnixMilli(1_700_000_000_000).UTC(),
		Namespace:     fence.Namespace,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "stack.update",
		Object:        "stack/acme/web",
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
		PrincipalKind: audit.PrincipalKindPerson,
		JobID:         "",
		Reason:        "",
	}

	b.ResetTimer()

	for b.Loop() {
		if _, err := s.Append(ctx, fence, e); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}
}
