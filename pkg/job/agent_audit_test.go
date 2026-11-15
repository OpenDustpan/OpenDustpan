package job_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
)

// startedByAgent starts a job as ada, with the agent ref, on r and returns
// the audit entry of the start.
func startedByAgent(t *testing.T, r *rig, ref job.AgentRef) audit.Entry {
	t.Helper()

	a := actorOf("ada")
	a.Agent, a.Mode = ref, job.AgentModeApproveFirst

	if _, err := r.svc.Start(t.Context(), a, r.plan("")); err != nil {
		t.Fatalf("Start = %v", err)
	}

	if len(r.store.creates) != 1 || len(r.store.creates[0].entries) != 1 {
		t.Fatalf("creates = %+v, want one create with one entry", r.store.creates)
	}

	return r.store.creates[0].entries[0]
}

// TestAuditAgentBlockHasNoAgentField proves that the start entry of an agent
// holds the agent block and no agent change key.
func TestAuditAgentBlockHasNoAgentField(t *testing.T) {
	t.Parallel()

	e := startedByAgent(t, newRig(t), "dusty")

	if e.Origin == nil || e.Origin.Run() != "dusty" || e.Origin.Session() != "dusty" {
		t.Fatalf("origin = %+v, want the agent block of dusty", e.Origin)
	}

	for _, ch := range e.Changes {
		if ch.Key == audit.LegacyAgentChangeKey {
			t.Fatalf("changes hold the agent key: %+v", e.Changes)
		}
	}

	if got := e.AgentOrigin(); got != e.Origin {
		t.Fatalf("AgentOrigin = %+v, want the block", got)
	}
}

// TestAuditHashStableWithoutAgent proves that an entry with no agent keeps
// its hash: the start entry of a person holds no block, and a fixed entry
// with no block hashes to the value that it had before the block existed.
func TestAuditHashStableWithoutAgent(t *testing.T) {
	t.Parallel()

	e := startedByAgent(t, newRig(t), "")
	if e.Origin != nil || e.HasAgentBlock() {
		t.Fatalf("origin = %+v, want no block for a start with no agent", e.Origin)
	}

	fixed := audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: "acme", Subject: "ada", IDPSubject: "",
		TokenID: "", Action: "unit:run", Object: "pulumi:acme/web", Version: "", ValuePath: "",
		ValueCount: nil, Changes: []audit.Change{{Key: "job", Old: nil, New: "j1", Layer: "", TakesEffect: ""}},
		SourceIP: "192.0.2.7", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-1", ObjectID: "", PrevHash: "", Hash: "", Sequence: 1,
		FenceVersion: 1, PrincipalKind: "person", JobID: "", Reason: "", Origin: nil,
	}

	const want = "57d8ab14bfc59f91cf0ab4a5f39b9adc697e3cf59511a9749e0d808ed1a9dbba"

	if got := audit.ComputeHash(fixed, ""); got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

// TestAuditLegacyChangeKeyDualRead proves that an old start entry with the
// agent change key reads and its hash covers the key, and that the block wins
// when an entry holds both.
func TestAuditLegacyChangeKeyDualRead(t *testing.T) {
	t.Parallel()

	legacy := audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: "acme", Subject: "ada", IDPSubject: "",
		TokenID: "", Action: "unit:run", Object: "pulumi:acme/web", Version: "", ValuePath: "",
		ValueCount: nil, Changes: []audit.Change{
			{Key: "job", Old: nil, New: "j1", Layer: "", TakesEffect: ""},
			{Key: audit.LegacyAgentChangeKey, Old: nil, New: "old-run", Layer: "", TakesEffect: ""},
		},
		SourceIP: "192.0.2.7", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-1", ObjectID: "", PrevHash: "", Hash: "", Sequence: 1,
		FenceVersion: 1, PrincipalKind: "person", JobID: "", Reason: "", Origin: nil,
	}

	if got := legacy.AgentOrigin(); got == nil || got.Run() != "old-run" || got.Session() != "old-run" {
		t.Fatalf("legacy AgentOrigin = %+v, want the legacy value", got)
	}

	// The hash covers the legacy key, so a changed value breaks the chain.
	changed := legacy
	changed.Changes = []audit.Change{legacy.Changes[0], {
		Key: audit.LegacyAgentChangeKey, Old: nil, New: "mallory", Layer: "", TakesEffect: "",
	}}

	if audit.ComputeHash(legacy, "") == audit.ComputeHash(changed, "") {
		t.Fatal("hash is unchanged after the legacy agent value changed")
	}

	block, err := audit.NewOrigin(audit.OriginArgs{
		Run: "new-run", Session: "new-run", Tool: "", ArgsDigest: "", BreakGlass: false, SecretStage: "",
		DelegationID: "",
	})
	if err != nil {
		t.Fatalf("new origin: %v", err)
	}

	both := legacy
	both.Origin = block

	if got := both.AgentOrigin(); got != block {
		t.Fatalf("both shapes: AgentOrigin = %+v, want the block", got)
	}

	// A legacy value that a block cannot hold never reaches a reader.
	hostile := legacy
	hostile.Changes = []audit.Change{{
		Key: audit.LegacyAgentChangeKey, Old: nil, New: "run\nforged", Layer: "", TakesEffect: "",
	}}

	if got := hostile.AgentOrigin(); got != nil {
		t.Fatalf("hostile legacy AgentOrigin = %+v, want nil", got)
	}
}

// TestStartJobMalformedAgentRefRefused proves, at the service layer, that a
// start with a malformed agent ref is refused with ErrInvalid before the
// policy sees it, and that the store gets no create and no entry.
func TestStartJobMalformedAgentRefRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		ref  job.AgentRef
	}{
		{"line break", "run-1\nforged"},
		{"bidirectional override", job.AgentRef("run-1" + string(rune(0x202e)) + "forged")},
		{"zero width space", job.AgentRef("run-1" + string(rune(0x200b)) + "forged")},
		{"invalid UTF-8", "run-\xff\xfe"},
		{"too long", job.AgentRef(strings.Repeat("a", audit.MaxAgentFieldLen+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			a := actorOf("ada")
			a.Agent, a.Mode = tc.ref, job.AgentModeApproveFirst

			if _, err := r.svc.Start(t.Context(), a, r.plan("")); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("Start = %v, want %v", err, job.ErrInvalid)
			}

			if len(r.store.creates) != 0 || len(r.store.jobs) != 0 {
				t.Fatalf("creates = %d, jobs = %d, want none", len(r.store.creates), len(r.store.jobs))
			}

			if len(r.decider.inputs) != 0 {
				t.Fatalf("decisions = %d, want 0: a refused ref reaches no policy", len(r.decider.inputs))
			}
		})
	}
}

// TestStartJobBlockWriteFailureRollsBack proves, at the port adapter layer,
// that the job and the entry with its agent block go to the store in one
// create, and that a failed write fails the start and leaves no job.
func TestStartJobBlockWriteFailureRollsBack(t *testing.T) {
	t.Parallel()

	r := newRig(t, storeFails(audit.ErrInvalidEntry))
	a := actorOf("ada")
	a.Agent, a.Mode = "dusty", job.AgentModeApproveFirst

	if _, err := r.svc.Start(t.Context(), a, r.plan("")); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Start = %v, want %v", err, audit.ErrInvalidEntry)
	}

	if len(r.store.creates) != 1 {
		t.Fatalf("creates = %d, want 1: the job and its entry go in one create", len(r.store.creates))
	}

	c := r.store.creates[0]
	if len(c.entries) != 1 || c.entries[0].Origin == nil || c.entries[0].Origin.Run() != "dusty" {
		t.Fatalf("entries = %+v, want the one start entry with its block", c.entries)
	}

	if len(r.store.jobs) != 0 || len(r.store.applies) != 0 {
		t.Fatalf("jobs = %d, applies = %d, want none after a failed write", len(r.store.jobs), len(r.store.applies))
	}
}
