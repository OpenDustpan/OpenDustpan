package runnerapi_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// replica serves a second handler over the same stores, with the dependencies
// that mutate changes. The session of s is valid on it, because the store is
// the same. It returns the URL of the replica.
func (s *sessionEnv) replica(mutate func(cfg *runnerapi.Config)) string {
	s.t.Helper()

	_, srv, _ := serve(s.t, s.store, serveOptions{
		now: s.clock.now, authz: identity.AdminOnly{}, claimer: s.claimer, recorder: s.jobs, jobs: s.jobs,
		logger: nil, mutate: mutate,
	})

	return srv.URL
}

// via returns the call c, sent to the server at url.
func via(c lcall, url string) lcall {
	c.server = url

	return c
}

// doctoredOps is the durable store with a change to the operation that Get
// returns. It makes the state in which the session layer of the store says
// that the claim is live and the operation disagrees.
type doctoredOps struct {
	durable.Store

	change func(op *durable.Operation) error
}

func (d doctoredOps) Get(ctx context.Context, id durable.ID) (durable.Operation, error) {
	op, err := d.Store.Get(ctx, id)
	if err != nil {
		return durable.Operation{}, fmt.Errorf("get: %w", err)
	}

	if changeErr := d.change(&op); changeErr != nil {
		return durable.Operation{}, changeErr
	}

	return op, nil
}

// fixedLookup is the session store with a claim lookup that finds every claim.
// With doctoredOps it removes the check of the store that also refuses a claim
// that the session does not hold, so that a case proves the check of the
// handler alone.
type fixedLookup struct {
	runner.SessionStore
}

func (fixedLookup) LookupClaim(context.Context, durable.Claim) (runner.ClaimRecord, error) {
	return runner.ClaimRecord{Job: phaseKey, Phase: 0, ClaimedAt: time.Time{}, HasSession: true}, nil
}

// TestSessionRefusesAnOperationThatDisagrees proves the checks of the handler
// on the operation of the session: the owner, the token and the state of the
// operation must be those of the session. Each case runs where the session
// layer of the store still says that the claim is live, so only these checks
// stop the request.
func TestSessionRefusesAnOperationThatDisagrees(t *testing.T) {
	t.Parallel()

	changes := []struct {
		name   string
		change func(op *durable.Operation) error
	}{
		{"another owner", func(op *durable.Operation) error { op.Claim.Owner = "rn-another"; return nil }},
		{"another claim token", func(op *durable.Operation) error { op.Claim.Token++; return nil }},
		{"an operation that is not running", func(op *durable.Operation) error {
			op.State = durable.StateSucceeded

			return nil
		}},
		{"an operation that is gone", func(*durable.Operation) error { return durable.ErrNotFound }},
	}

	for _, b := range backends() {
		for _, rt := range sessionRoutes() {
			for _, ch := range changes {
				t.Run(b.name+"/"+rt.name+"/"+ch.name, func(t *testing.T) {
					t.Parallel()

					s := newSessionEnv(t, b)
					l := s.launch()

					url := s.replica(func(cfg *runnerapi.Config) {
						cfg.Operations = doctoredOps{Store: cfg.Operations, change: ch.change}
						cfg.Handoffs = fixedLookup{SessionStore: cfg.Handoffs}
					})

					l.expect(t, "request", via(rt.build(l), url), http.StatusConflict, protocol.ErrorClaimLost)
					s.checkUntouched(t)
				})
			}
		}
	}
}

// checkUntouched fails when a request reached the recorder or finished the
// phase.
func (s *sessionEnv) checkUntouched(t *testing.T) {
	t.Helper()

	if got := s.jobs.called(); len(got) != 0 {
		t.Fatalf("a refused request reached the recorder: %v", got)
	}

	if n := s.claimer.finished.Load(); n != 0 {
		t.Fatalf("a refused request finished the phase %d times", n)
	}
}

// TestSessionFailsClosedWithoutADependency proves that a server without the
// recorder, the claimer, the durable operations or the job reader refuses the
// routes that need it with not-found, and does not panic.
func TestSessionFailsClosedWithoutADependency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(cfg *runnerapi.Config)
		// refused lists the routes that must answer not-found.
		refused []string
	}{
		{
			name:    "no recorder",
			mutate:  func(cfg *runnerapi.Config) { cfg.Recorder = nil },
			refused: []string{"step begin", "step end", "log"},
		},
		{
			name:    "no claimer",
			mutate:  func(cfg *runnerapi.Config) { cfg.Claimer = nil },
			refused: []string{"finish"},
		},
		{
			name:    "no durable operations",
			mutate:  func(cfg *runnerapi.Config) { cfg.Operations = nil },
			refused: []string{"renew", "step begin", "step end", "log", "finish"},
		},
	}

	for _, b := range backends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newSessionEnv(t, b)
				l := s.launch()
				url := s.replica(tc.mutate)

				for _, rt := range sessionRoutes() {
					if !slices.Contains(tc.refused, rt.name) {
						continue
					}

					l.expect(t, rt.name, via(rt.build(l), url), http.StatusNotFound, protocol.ErrorNotFound)
				}

				s.checkUntouched(t)
			})
		}
	}
}

// TestOpenSessionFailsClosedWithoutADependency proves that an exchange on a
// server without the job reader or the durable operations answers not-found
// and does not use the handoff token: the same token still opens a session on
// a server that has them.
func TestOpenSessionFailsClosedWithoutADependency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(cfg *runnerapi.Config)
	}{
		{"no job reader", func(cfg *runnerapi.Config) { cfg.Jobs = nil }},
		{"no durable operations", func(cfg *runnerapi.Config) { cfg.Operations = nil }},
	}

	for _, b := range backends() {
		for _, tc := range tests {
			t.Run(b.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				s := newSessionEnv(t, b)
				url := s.replica(tc.mutate)
				x := s.newExchange(s.runner.handoff(s.work.Operation))

				status, body := s.exchangeAt(url, x)
				if status != http.StatusNotFound || errorCode(t, body) != protocol.ErrorNotFound {
					t.Fatalf("exchange = %d %s, want 404 not-found", status, body)
				}

				x.nonce = newNonce(t)

				if status, body = s.sendExchange(x); status != http.StatusOK {
					t.Fatalf("exchange with the same token on a full server = %d %s, want 200", status, body)
				}
			})
		}
	}
}

// fakeSweeps claims the sweeps of a pool through the durable store.
type fakeSweeps struct {
	ops durable.Store
}

var _ runner.SweepClaimer = fakeSweeps{ops: nil}

func (f fakeSweeps) Claim(ctx context.Context, req durable.ClaimRequest) (runner.SweepClaim, error) {
	op, err := f.ops.Claim(ctx, req)
	if err != nil {
		return runner.SweepClaim{}, fmt.Errorf("claim: %w", err)
	}

	return runner.SweepClaim{
		Claim: op.Claim, Fence: namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		Work: runner.Work{
			Operation: string(op.ID), Token: op.Claim.Token, ExpiresAt: op.Claim.ExpiresAt, Lease: req.Lease, Job: "",
			Phase: 0, Sweep: "sweep-1", Image: "", ServiceAccount: "", Deadline: time.Minute, Grace: time.Minute,
		},
	}, nil
}

func (fakeSweeps) Renew(context.Context, durable.Claim, namespace.Fence, time.Duration) (durable.Claim, error) {
	return durable.Claim{}, errUnused
}

func (fakeSweeps) Release(context.Context, durable.Claim, namespace.Fence) error { return errUnused }

func (fakeSweeps) Report(context.Context, durable.Claim, namespace.Fence, io.Reader) error {
	return errUnused
}

// newSweepEnv joins a runner to a pool for sweeps and claims one sweep for it.
func newSweepEnv(t *testing.T, b backend) *sessionEnv {
	t.Helper()

	jobs := newSessionJobs()
	logs := &syncBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
	claimer := &sessionClaimer{opsClaimer: nil, finished: atomic.Int64{}, outcome: atomic.Pointer[job.Outcome]{}}

	e := newEnvServing(t, b.open, func(s stores, o *serveOptions) {
		claimer.opsClaimer = &opsClaimer{ops: s.operations, released: atomic.Int64{}}
		o.claimer, o.recorder, o.jobs = claimer, jobs, jobs
		o.mutate = func(cfg *runnerapi.Config) { cfg.Sweeps = fakeSweeps{ops: s.operations} }
	})

	status, body := e.admin(http.MethodPost, "/api/runners/acme/pools",
		map[string]any{"name": "scan", "purpose": "sweeps"})
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decode(t, body, &pool)

	r := e.newRunner("scanner-1")
	if status, body = r.join(pool.ID, e.joinToken(pool.ID)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	_, _, err := e.store.operations.Start(t.Context(), durable.StartRequest{
		Key: "sweep-1", Kind: runner.SweepKind(pool.ID), Version: 0, Input: nil,
	})
	if err != nil {
		t.Fatalf("start sweep: %v", err)
	}

	work := r.poll(2 * time.Second)
	if work == nil || work.Sweep != "sweep-1" {
		t.Fatalf("poll = %+v, want the sweep", work)
	}

	return &sessionEnv{env: e, jobs: jobs, claimer: claimer, logs: logs, pool: pool.ID, runner: r, work: work}
}

// TestSweepSessionHasNoSteps proves the check of the sweep claim: a session of
// a sweep renews its claim, and every route that writes the record of a job
// phase refuses it before the recorder or the claimer.
func TestSweepSessionHasNoSteps(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		for _, rt := range sessionRoutes() {
			t.Run(b.name+"/"+rt.name, func(t *testing.T) {
				t.Parallel()

				s := newSweepEnv(t, b)
				l := s.launchSweep()

				if rt.name == "renew" {
					l.expect(t, "renew", rt.build(l), http.StatusOK, "")

					return
				}

				l.expect(t, rt.name, rt.build(l), http.StatusBadRequest, protocol.ErrorInvalid)
				s.checkUntouched(t)
			})
		}
	}
}

// TestSweepExchangeIsRefused proves that the exchange of a sweep claim does
// not answer an assignment.
func TestSweepExchangeIsRefused(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			s := newSweepEnv(t, b)
			x := s.newExchange(s.runner.handoff(s.work.Operation))

			status, body := s.sendExchange(x)
			if status != http.StatusBadRequest || errorCode(t, body) != protocol.ErrorInvalid {
				t.Fatalf("exchange of a sweep = %d %s, want 400 invalid", status, body)
			}
		})
	}
}

// launchSweep opens the session of the claim in the store, as the exchange
// does, because the exchange of a sweep is refused.
func (s *sessionEnv) launchSweep() *launcher {
	s.t.Helper()

	x := s.newExchange(s.runner.handoff(s.work.Operation))

	sess, err := s.store.runners.OpenSession(s.t.Context(), sha256Hex(x.token),
		runner.Key{Public: x.pub, CreatedAt: time.Time{}}, time.Now())
	if err != nil {
		s.t.Fatalf("open session: %v", err)
	}

	return &launcher{e: s, id: sess.ID, pub: x.pub, priv: x.priv}
}
