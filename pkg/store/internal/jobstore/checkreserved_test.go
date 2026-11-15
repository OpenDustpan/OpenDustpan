package jobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// reserveTx is a jobstore.Tx that answers the plan lookup from one row. Only
// Plan runs: checkReserved reads no other row.
type reserveTx struct {
	jobstore.Tx

	row jobstore.PlanRow
	ok  bool
}

// Plan implements jobstore.ArtifactTx.
func (f *reserveTx) Plan(context.Context, namespace.Name, string) (jobstore.PlanRow, bool, error) {
	return f.row, f.ok, nil
}

// TestCheckReservedBindsTheReserve proves that checkReserved refuses the fill
// of a reserve by another phase, another operation, another token, and by a
// row that has no binding at all.
func TestCheckReservedBindsTheReserve(t *testing.T) {
	t.Parallel()

	claim := job.PhaseClaim{
		Job: "job-1", Phase: 0,
		Claim: durable.Claim{Operation: "dop-1", Owner: "runner-1", Token: 7, ExpiresAt: time.Time{}},
		Fence: namespace.Fence{Namespace: "acme", FailoverVersion: 1},
	}

	body := func(binding string) string {
		return `{"id":"pln-1","itemKey":"ik-1","stateVersion":"sv-1","parts":[],` +
			`"createdAt":"2026-10-21T09:00:00Z","expiresAt":"2027-10-21T09:00:00Z",` +
			`"redactedAt":"0001-01-01T00:00:00Z"` + binding + `}`
	}

	cases := []struct {
		name    string
		binding string
		ok      bool
	}{
		{
			name:    "the phase, the operation and the token of the claim",
			binding: `,"reservePhase":0,"reserveOperation":"dop-1","reserveToken":7`, ok: true,
		},
		{
			name:    "another phase",
			binding: `,"reservePhase":1,"reserveOperation":"dop-1","reserveToken":7`, ok: false,
		},
		{
			name:    "another operation",
			binding: `,"reservePhase":0,"reserveOperation":"dop-2","reserveToken":7`, ok: false,
		},
		{
			name:    "another token",
			binding: `,"reservePhase":0,"reserveOperation":"dop-1","reserveToken":8`, ok: false,
		},
		{name: "no binding", binding: "", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tx := &reserveTx{
				Tx:  nil,
				row: jobstore.PlanRow{ID: "pln-1", Job: "job-1", Body: body(tc.binding), CreatedMs: 0},
				ok:  true,
			}
			p := job.PlanRef{
				ID: "pln-1", ItemKey: "ik-1", StateVersion: "", Parts: nil,
				CreatedAt: time.Time{}, ExpiresAt: time.Time{}, RedactedAt: time.Time{},
			}
			j := fullJob(t)

			reserved, err := jobstore.CheckReservedPlan(t.Context(), tx, &j, &claim, &p)
			if !tc.ok {
				if !errors.Is(err, job.ErrConflict) {
					t.Fatalf("CheckReservedPlan = %v, want ErrConflict", err)
				}

				return
			}

			if err != nil || reserved == nil || reserved.ID != "pln-1" {
				t.Fatalf("CheckReservedPlan = %+v, %v; want the reserved plan", reserved, err)
			}
		})
	}
}
