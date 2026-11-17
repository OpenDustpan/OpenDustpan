package job_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestApprovalEntryAgentInBlock proves that the verdict entry of an agent
// holds the agent in the agent block and no agent change key. A rejection is
// the one verdict that an agent records: an agent cannot approve.
func TestApprovalEntryAgentInBlock(t *testing.T) {
	t.Parallel()

	r := approveRig(t, openPolicy())
	r.seed(nil)

	if _, err := r.svc.Approve(t.Context(), agentActor("carol", "dusty"), approveID, job.VerdictReject,
		noBinding(), "stopped"); err != nil {
		t.Fatalf("Approve = %v", err)
	}

	if len(r.store.applies) != 1 || len(r.store.applyEntries) != 1 || len(r.store.applyEntries[0]) != 1 {
		t.Fatalf("applies = %d, entries = %v, want one change with one entry", len(r.store.applies),
			r.store.applyEntries)
	}

	e := r.store.applyEntries[0][0]

	if e.Origin == nil || e.Origin.Run() != "dusty" || e.Origin.Session() != "dusty" {
		t.Fatalf("origin = %+v, want the agent block of dusty", e.Origin)
	}

	for _, ch := range e.Changes {
		if ch.Key == "agent" {
			t.Fatalf("changes hold the agent key: %+v", e.Changes)
		}
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
