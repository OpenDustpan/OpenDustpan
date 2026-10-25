package dustpan_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// jobPools serves one pool of jobs for the namespace acme.
type jobPools struct {
	runner.PoolStore
}

func (jobPools) Pools(context.Context, namespace.Name) ([]runner.Pool, error) {
	return []runner.Pool{{
		ID: "pool-1", Namespace: "acme", Name: "ci", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Time{},
		UpdatedAt: time.Time{},
	}}, nil
}

// TestJobPlannerDefaultedValues proves that the real planner of the server and
// the rule of the job service name the values that the request did not give.
// The planner takes the only job pool of the namespace for a request with no
// pool. It never defaults the location, and it sets no profile.
func TestJobPlannerDefaultedValues(t *testing.T) {
	t.Parallel()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("ParseName = %v", err)
	}

	base, err := identity.UnitObject("opentofu", ns, "web")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	unit := base.WithUnitID("ws-1")

	tests := []struct {
		name     string
		pool     string
		wantPool string
		want     []string
	}{
		{"no pool takes the only pool", "", "pool-1", []string{job.DefaultedPool}},
		{"a given pool", "ci", "pool-1", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := job.StartRequest{
				Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: "",
				Location: job.Location{
					Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: "",
				},
				Pool: tt.pool, Edits: nil, Delivery: "", Source: "", IdempotencyKey: "", Reason: "",
				BreakGlass: nil, Trigger: nil, Channel: "", SourceIP: "", UserAgent: "", RequestID: "",
			}

			spec, err := dustpan.PlanJob(t.Context(), jobPools{PoolStore: nil}, unit, &req)
			if err != nil {
				t.Fatalf("PlanJob = %v", err)
			}

			if spec.Pool != tt.wantPool {
				t.Errorf("Pool = %q, want %q", spec.Pool, tt.wantPool)
			}

			if got := job.DefaultedValues(&req, &spec); !slices.Equal(got, tt.want) {
				t.Errorf("DefaultedValues = %v, want %v", got, tt.want)
			}
		})
	}
}
