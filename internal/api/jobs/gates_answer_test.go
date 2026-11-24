package jobs_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
)

// gatesDoc is the part of the answer of a job that the test reads.
type gatesDoc struct {
	Phases []gatesPhase `json:"phases"`
}

type gatesPhase struct {
	Gates     []gatesGate     `json:"gates"`
	Approvals []gatesApproval `json:"approvals"`
}

type gatesGate struct {
	Name      string `json:"name"`
	Approvals int    `json:"approvals"`
	Filled    int    `json:"filled"`
}

type gatesApproval struct {
	For []string `json:"for"`
}

// TestGetJobListsTheGatesWithTheirFilledSlots proves that the answer names each
// gate, counts the slots that the matching fills, and shows the gates of each
// approval. One person who holds both gates fills one slot of one gate.
func TestGetJobListsTheGatesWithTheirFilledSlots(t *testing.T) {
	t.Parallel()

	approval := func(subject string, names ...string) job.Approval {
		return job.Approval{
			Gate: job.GateApproval, For: names, Subject: subject, Agent: "", Verdict: job.VerdictApprove,
			Comment: "", Plan: nil, At: time.Unix(1, 0).UTC(), Auth: "", AuthAt: time.Time{},
		}
	}

	stored := runningJob(t, testJob)
	stored.Phases[0].Gates = []job.Gate{
		{
			Type: job.GateApproval, Name: "default/namespace/acme", Approvals: 2, Groups: []string{"platform"},
			GroupIDs: []string{"grp-a"}, AllowSelf: false, Timeout: 0, BindPlan: false,
			Users:      nil,
			UserIDs:    nil,
			UnitOwners: false,
			Scope:      gate.Scope{},
		},
		{
			Type: job.GateApproval, Name: "default", Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false,
			Timeout: 0, BindPlan: false,
			Users:      nil,
			UserIDs:    nil,
			UnitOwners: false,
			Scope:      gate.Scope{},
		},
	}
	stored.Phases[0].Approvals = []job.Approval{
		approval("bo", "default/namespace/acme", "default"),
		approval("cy", "default/namespace/acme", "default"),
	}

	handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(stored))
	rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}

	var doc gatesDoc

	err := json.Unmarshal(rec.Body.Bytes(), &doc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	gates := doc.Phases[0].Gates
	if len(gates) != 2 || gates[0].Name != "default/namespace/acme" || gates[1].Name != "default" {
		t.Fatalf("gates = %+v, want the two named gates in order", gates)
	}

	// Two people fill two of the three slots: bo and cy take the two slots of
	// the first gate, or one of them takes the second gate. The total is 2.
	if filled := gates[0].Filled + gates[1].Filled; filled != 2 || gates[0].Filled > 2 || gates[1].Filled > 1 {
		t.Fatalf("filled = %d and %d, want 2 slots in all", gates[0].Filled, gates[1].Filled)
	}

	if !slices.Equal(doc.Phases[0].Approvals[0].For, []string{"default/namespace/acme", "default"}) {
		t.Fatalf("approval For = %v, want both gates", doc.Phases[0].Approvals[0].For)
	}
}
