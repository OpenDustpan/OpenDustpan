package runnerapi_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// noRefusals drops each out-of-scope refusal and each mismatch. The real
// recorder of these cases never refuses a key.
type noRefusals struct{}

func (noRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

func (noRefusals) RecordMismatch(context.Context, namespace.ID, string) error {
	return nil
}

// commitSpy is the real plan recorder with a record of the error of each
// CommitPlan call, so that a case can show which path of the recorder
// answered.
type commitSpy struct {
	job.PlanRecorder

	mu   sync.Mutex
	errs []error
}

func (s *commitSpy) CommitPlan(ctx context.Context, c job.PhaseClaim, parts []job.PlanCommitPart) (job.PlanRef, error) {
	ref, err := s.PlanRecorder.CommitPlan(ctx, c, parts)

	s.mu.Lock()
	s.errs = append(s.errs, err)
	s.mu.Unlock()

	if err != nil {
		return job.PlanRef{}, fmt.Errorf("commit plan: %w", err)
	}

	return ref, nil
}

// commitErrors returns the error of each CommitPlan call, in order.
func (s *commitSpy) commitErrors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.errs)
}

// newDurablePlans returns the real plan recorder over the SQLite store st,
// with a blob store in a temporary directory.
func newDurablePlans(t *testing.T, st *sqlite.Store) *job.DurableRecorder {
	t.Helper()

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	// A fixed key of zeros: the case needs no secrecy, only a valid key.
	wrapper, err := secret.NewBuiltinWrapper(make([]byte, 32))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	itemBlobs, err := secret.NewItemKeyBlobs(blobs, noRefusals{}, noRefusals{})
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: st, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: st, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("item sealer: %v", err)
	}

	artifacts, err := job.NewArtifactBlobs(blobs, noRefusals{})
	if err != nil {
		t.Fatalf("artifact blobs: %v", err)
	}

	rec, err := job.NewRecorder(job.RecorderConfig{
		Store: st.Jobs(), Jobs: st.Jobs(), Blobs: artifacts, Sealer: sealer, Namespaces: st,
		Retention: history.DefaultRetentionSettings(), Now: time.Now, Log: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	return rec
}

// durablePlanEnv is a session environment over SQLite whose claim is the plan
// phase of a real dispatched job, with the real job claimer, the real job
// store as the job reader and the real plan recorder behind a commitSpy.
type durablePlanEnv struct {
	*sessionEnv

	plans *commitSpy
	job   job.Job
}

// newDurablePlanEnv opens the store, joins a runner, dispatches a plan job and
// claims its plan phase.
func newDurablePlanEnv(t *testing.T) *durablePlanEnv {
	t.Helper()

	spy := &commitSpy{PlanRecorder: nil, mu: sync.Mutex{}, errs: nil}

	e := newEnvServing(t, openSQLite, func(s stores, o *serveOptions) {
		st, ok := s.namespaces.(*sqlite.Store)
		if !ok {
			t.Fatal("the test store is not a SQLite store")
		}

		spy.PlanRecorder = newDurablePlans(t, st)
		o.claimer = job.NewClaimer(s.jobs, s.operations, s.namespaces, jobtest.NoGates{})
		o.jobs = s.jobs
		o.mutate = func(cfg *runnerapi.Config) { cfg.Plans = spy }
	})

	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	j := dispatchPlan(t, e, pool, unitOf(t, e.store, e.fence, "plans"))

	work := r.poll(2 * time.Second)
	if work == nil || work.Job != string(j.ID) || work.Phase != 0 {
		t.Fatalf("poll = %+v, want phase 0 of job %s", work, j.ID)
	}

	s := &sessionEnv{env: e, jobs: nil, claimer: nil, logs: nil, pool: pool, runner: r, work: work}

	return &durablePlanEnv{sessionEnv: s, plans: spy, job: j}
}

// storedPlanParts reads the job of s from the store and returns the parts of
// the plan of its plan phase, or nil when the phase has no plan.
func (s *durablePlanEnv) storedPlanParts(t *testing.T) []job.PlanCommitPart {
	t.Helper()

	j, err := s.store.jobs.Get(t.Context(), s.job.ID)
	if err != nil {
		t.Fatalf("get job %s: %v", s.job.ID, err)
	}

	p := j.Phases[0].Plan
	if p == nil {
		return nil
	}

	parts := make([]job.PlanCommitPart, 0, len(p.Parts))
	for _, part := range p.Parts {
		parts = append(parts, job.PlanCommitPart{Kind: part.Kind, Size: part.Size, SHA256: part.SHA256})
	}

	return parts
}

// checkRealRecommitConflict fails t unless the commits of s reached the real
// recorder three times: two commits that passed and a third that the
// recorder refused with ErrConflict on the path of a phase that has a plan
// with other parts.
func checkRealRecommitConflict(t *testing.T, s *durablePlanEnv) {
	t.Helper()

	errs := s.plans.commitErrors()
	if len(errs) != 3 || errs[0] != nil || errs[1] != nil {
		t.Fatalf("recorder commit errors = %v, want nil, nil and a conflict", errs)
	}

	if !errors.Is(errs[2], job.ErrConflict) ||
		!strings.Contains(errs[2].Error(), "has other parts than the commit names") {
		t.Fatalf("third commit error = %v, want the conflict of a plan with other parts", errs[2])
	}
}
