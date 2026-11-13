package job_test

import (
	"fmt"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// TestStartRecordsTheDefaultedValues proves that a job names the pool, the
// code location and the profile that the request did not give.
func TestStartRecordsTheDefaultedValues(t *testing.T) {
	t.Parallel()

	named := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: ""}
	revisionOnly := job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: "abcdef0"}

	tests := []struct {
		name    string
		pool    string
		loc     job.Location
		profile string
		// plannerList is a list that the planner sets by itself.
		plannerList []string
		// plannerSource is the source that the planner sets by itself.
		plannerSource job.LocationSource
		want          []string
		wantSource    job.LocationSource
	}{
		{"names pool and location", "pool-1", named, "", nil, "", nil, job.LocationSourceRequest},
		{"names no pool", "", named, "", nil, "", []string{"pool"}, job.LocationSourceRequest},
		{"names neither", "", emptyLocation(), "", nil, "", []string{"pool", "location"}, job.LocationSourceUnit},
		{
			"the planner names the namespace and the request gives no location",
			"", emptyLocation(), "", nil, job.LocationSourceNamespace,
			[]string{"pool", "location"},
			job.LocationSourceNamespace,
		},
		{
			"the request location wins over the namespace of the planner",
			"", named, "", nil, job.LocationSourceNamespace,
			[]string{"pool"},
			job.LocationSourceRequest,
		},
		{
			"names only a revision", "", revisionOnly, "", nil, "",
			[]string{"pool"},
			job.LocationSourceRequest,
		},
		{
			"a profile from the unit settings", "pool-1", named, "prf-1", nil, "",
			[]string{"profile"},
			job.LocationSourceRequest,
		},
		{
			"all three", "", emptyLocation(), "prf-1", nil, "",
			[]string{"pool", "location", "profile"},
			job.LocationSourceUnit,
		},
		{
			"the service ignores a list of the planner", "pool-1", named, "",
			[]string{"pool"},
			"",
			nil,
			job.LocationSourceRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			r.planner.spec = func(u identity.Object, req *job.StartRequest) job.Spec {
				return job.Spec{
					Unit: u, Operation: req.Operation, Purpose: req.Purpose, Executor: job.ExecutorRunner,
					Location: named, LocationSource: tt.plannerSource, Steps: nil, Pool: "pool-1",
					Profile: tt.profile, Edits: nil, Delivery: "", Defaulted: tt.plannerList,
				}
			}

			req := r.plan("")
			req.Pool, req.Location = tt.pool, tt.loc

			j, _, err := r.svc.StartOnce(t.Context(), actorOf("ada"), req)
			if err != nil {
				t.Fatalf("StartOnce = %v", err)
			}

			stored, err := r.store.Get(t.Context(), j.ID)
			if err != nil {
				t.Fatalf("Get = %v", err)
			}

			if !slices.Equal(stored.Spec.Defaulted, tt.want) {
				t.Fatalf("Spec.Defaulted = %v, want %v", stored.Spec.Defaulted, tt.want)
			}

			if stored.Spec.LocationSource != tt.wantSource {
				t.Fatalf("Spec.LocationSource = %q, want %q", stored.Spec.LocationSource, tt.wantSource)
			}
		})
	}
}

// TestDefaultedStaysAfterTheSettingsChange proves that the spec is fixed at
// start: a later plan with other values changes neither a value nor the list
// of the job that exists.
func TestDefaultedStaysAfterTheSettingsChange(t *testing.T) {
	t.Parallel()

	n := 0
	r := newRig(t, func(c *job.ServiceConfig, _ *rig) {
		c.NewID = func() job.ID {
			n++

			return job.ID(fmt.Sprintf("job-%d", n))
		}
	})

	first, _, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.plan(""))
	if err != nil {
		t.Fatalf("StartOnce = %v", err)
	}

	r.planner.spec = func(u identity.Object, req *job.StartRequest) job.Spec {
		return job.Spec{
			Unit: u, Operation: req.Operation, Purpose: req.Purpose, Executor: job.ExecutorRunner,
			Location: req.Location, LocationSource: "", Steps: nil, Pool: "pool-2", Profile: "prf-2", Edits: nil,
			Delivery: "", Defaulted: nil,
		}
	}

	req := r.plan("")
	req.Pool = "pool-2"

	_, _, err = r.svc.StartOnce(t.Context(), actorOf("ada"), req)
	if err != nil {
		t.Fatalf("second StartOnce = %v", err)
	}

	got, err := r.store.Get(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	if got.Spec.Pool != "pool-1" || !slices.Equal(got.Spec.Defaulted, []string{"pool", "location"}) {
		t.Fatalf("job = pool %q, defaulted %v; want pool-1, [pool location]", got.Spec.Pool, got.Spec.Defaulted)
	}
}
