package job_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	errDriver   = errors.New("driver: connection reset")
	errPolicy   = errors.New("policy engine is down")
	errNoRights = errors.New("grants store is down")
)

// svcStore is a job.Store that keeps what Create and Apply get. It fails the
// first creates with the errors in createErrs, in order.
type svcStore struct {
	mu         sync.Mutex
	creates    []createCall
	applies    []job.Change
	createErrs []error
	// applyErr fails every Apply.
	applyErr error
	jobs     map[job.ID]job.Job
	// keys holds the job of each idempotency key, as the store contract says.
	keys map[string]job.Job
}

type createCall struct {
	job      job.Job
	dispatch *durable.StartRequest
	fence    namespace.Fence
	entries  []audit.Entry
}

func newSvcStore() *svcStore {
	return &svcStore{
		mu:         sync.Mutex{},
		creates:    nil,
		applies:    nil,
		createErrs: nil,
		applyErr:   nil,
		jobs:       map[job.ID]job.Job{},
		keys:       map[string]job.Job{},
	}
}

//nolint:gocritic // The signature is fixed by job.Store.
func (s *svcStore) Create(
	_ context.Context, j job.Job, d *durable.StartRequest, f namespace.Fence, e []audit.Entry,
) (job.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.creates = append(s.creates, createCall{job: j, dispatch: d, fence: f, entries: e})

	if len(s.createErrs) > 0 {
		err := s.createErrs[0]
		s.createErrs = s.createErrs[1:]

		if err != nil {
			return job.Job{}, false, err
		}
	}

	// A key that names a job with another request hash fails, as the store
	// contract says; a key that names the same request gives the stored job.
	if j.IdempotencyKey != "" {
		stored, ok := s.keys[j.IdempotencyKey]
		if ok {
			if stored.RequestHash != j.RequestHash {
				return job.Job{}, false, job.ErrKeyConflict
			}

			return stored, false, nil
		}
	}

	j.Version = 1
	if d != nil {
		j.State = job.StateDispatched
	}

	s.jobs[j.ID] = j
	if j.IdempotencyKey != "" {
		s.keys[j.IdempotencyKey] = j
	}

	return j, true, nil
}

func (s *svcStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

func (s *svcStore) Apply(_ context.Context, c job.Change, _ namespace.Fence, _ []audit.Entry) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.applies = append(s.applies, c)

	if s.applyErr != nil {
		return job.Job{}, s.applyErr
	}

	j := s.jobs[c.Job]
	if c.Event == job.EventNeedApproval {
		j.State = job.StateAwaitingApproval
	}

	j.Version++
	s.jobs[c.Job] = j

	return j, nil
}

func (*svcStore) List(context.Context, job.Filter) (job.Page, error) {
	return job.Page{Jobs: nil, Damaged: nil, After: ""}, nil
}

func (*svcStore) ObserveStateVersion(context.Context, job.StateVersion, namespace.Fence) ([]job.Job, error) {
	return nil, nil
}

func (*svcStore) SetVerification(context.Context, job.ID, job.Verification, namespace.Fence, []audit.Entry,
) error {
	return nil
}

func (*svcStore) Orphans(context.Context, int) ([]job.Orphan, []job.DamagedRow, error) {
	return nil, nil, nil
}

func (*svcStore) CancelDamaged(context.Context, job.ID, namespace.Fence, []audit.Entry) error {
	return nil
}

type svcUnits struct {
	unit identity.Object
	err  error
	// other is returned in place of the unit when it is set.
	other *identity.Object
}

func (u svcUnits) Unit(_ context.Context, ref identity.Object) (identity.Object, error) {
	if u.err != nil {
		return identity.Object{}, u.err
	}

	if u.other != nil {
		return *u.other, nil
	}

	if ref.String() != u.unit.String() {
		return identity.Object{}, job.ErrNotFound
	}

	return u.unit, nil
}

type svcPlanner struct {
	mu    sync.Mutex
	calls int
	spec  func(unit identity.Object, req *job.StartRequest) job.Spec
	err   error
}

func (p *svcPlanner) Plan(_ context.Context, unit identity.Object, req *job.StartRequest) (job.Spec, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++

	if p.err != nil {
		return job.Spec{}, p.err
	}

	return p.spec(unit, req), nil
}

type svcExecutor struct {
	kind job.ExecutorKind
	err  error
}

func (e svcExecutor) Kind() job.ExecutorKind { return e.kind }

func (e svcExecutor) Phases(_ context.Context, spec job.Spec) ([]job.PhaseKind, error) {
	if e.err != nil {
		return nil, e.err
	}

	return job.PhasesFor(e.kind, spec.Operation, spec.Purpose)
}

func (svcExecutor) Abandon(context.Context, job.Job) error { return nil }

type svcDecider struct {
	mu       sync.Mutex
	decision job.Decision
	err      error
	inputs   []job.DecisionInput
}

//nolint:gocritic // The signature is fixed by job.Decider.
func (d *svcDecider) Decide(_ context.Context, in job.DecisionInput) (job.Decision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inputs = append(d.inputs, in)

	return d.decision, d.err
}

// svcAuthorizer allows the pairs of action and object in allow, and returns
// err for the pairs in fail.
type svcAuthorizer struct {
	// denied is a subject that holds no action at all.
	denied string
	allow  map[string]bool
	fail   map[string]error
	asked  []string
}

func (a *svcAuthorizer) Authorize(_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()
	a.asked = append(a.asked, key)

	if p.Subject == a.denied {
		return identity.ErrDenied
	}

	if err := a.fail[key]; err != nil {
		return err
	}

	if a.allow[key] {
		return nil
	}

	return identity.ErrDenied
}

func (*svcAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// svcGates is a job.GateGuard that returns the gates in enforced, or err.
type svcGates struct {
	mu       sync.Mutex
	enforced []job.EnforcedGate
	err      error
	asked    []string
}

func (g *svcGates) Enforced(_ context.Context, unit identity.Object) ([]job.EnforcedGate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.asked = append(g.asked, unit.String())

	return g.enforced, g.err
}

type svcFences struct{ err error }

func (f svcFences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	if f.err != nil {
		return namespace.Fence{}, f.err
	}

	return namespace.Fence{Namespace: ns, FailoverVersion: 3}, nil
}

type svcFreezes struct {
	list []job.Freeze
	err  error
}

func (f svcFreezes) CreateFreeze(context.Context, job.Freeze, namespace.Fence, audit.Entry) error {
	return nil
}

func (f svcFreezes) EndFreeze(context.Context, string, string, namespace.Fence, audit.Entry) error {
	return nil
}

func (f svcFreezes) ListFreezes(context.Context, namespace.Name, time.Time) ([]job.Freeze, error) {
	return f.list, f.err
}

// rig holds a service and the fakes behind it.
type rig struct {
	svc     job.Starter
	store   *svcStore
	planner *svcPlanner
	decider *svcDecider
	authz   *svcAuthorizer
	gates   *svcGates
	unit    identity.Object
	now     time.Time
}

type rigOption func(*job.ServiceConfig, *rig)

func newRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("ParseName = %v", err)
	}

	base, err := identity.UnitObject("opentofu", ns, "web")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	unit := base.WithUnitID("unt-web")
	nsObj := identity.NamespaceObject(ns)

	r := &rig{
		svc:   nil,
		store: newSvcStore(),
		planner: &svcPlanner{
			mu: sync.Mutex{}, calls: 0, err: nil,
			spec: func(u identity.Object, req *job.StartRequest) job.Spec {
				return job.Spec{
					Unit:      u,
					Operation: req.Operation,
					Purpose:   req.Purpose,
					Executor:  job.ExecutorRunner,
					Location: job.Location{
						Connection: "",
						Repository: "acme/infra",
						Ref:        "main",
						Directory:  "",
						Revision:   "",
					},
					Steps: []job.Step{
						{
							Name:             "plan",
							Role:             job.RoleTool,
							Phase:            job.PhasePlan,
							Args:             []string{"tofu", "plan"},
							Env:              nil,
							Dir:              "",
							Timeout:          0,
							AllowFailure:     false,
							ChangesResources: false,
							Outputs:          nil,
						},
					},
					Pool:      "pool-1",
					Profile:   "",
					Edits:     nil,
					Delivery:  "",
					Defaulted: nil,
				}
			},
		},
		decider: &svcDecider{
			mu: sync.Mutex{}, decision: job.Decision{Allow: true, Reason: "", Gates: nil, PolicyVersion: "v1"},
			err: nil, inputs: nil,
		},
		authz: &svcAuthorizer{
			allow: map[string]bool{
				string(job.ActionUnitRunPlan) + " " + unit.String():             true,
				string(job.ActionJobRead) + " " + unit.String():                 true,
				string(job.ActionNamespaceBreakFreeze) + " " + nsObj.String():   true,
				string(job.ActionNamespaceManageRunners) + " " + nsObj.String(): true,
				string(job.ActionUnitDefineSteps) + " " + unit.String():         true,
			},
			fail: map[string]error{}, asked: nil, denied: "bob",
		},
		gates: &svcGates{mu: sync.Mutex{}, enforced: nil, err: nil, asked: nil},
		unit:  unit,
		now:   time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
	}

	cfg := job.ServiceConfig{
		Store: r.store, Units: svcUnits{unit: unit, err: nil, other: nil}, Planner: r.planner,
		Executors:  []job.Executor{svcExecutor{kind: job.ExecutorRunner, err: nil}},
		Decider:    r.decider,
		Authorizer: r.authz,
		Gates:      r.gates,
		Fences:     svcFences{err: nil},
		Freezes:    nil, Switches: nil,
		Now:   func() time.Time { return r.now },
		NewID: func() job.ID { return job.ID("job-fixed") },
		Log:   slog.New(slog.DiscardHandler),
	}

	for _, o := range opts {
		o(&cfg, r)
	}

	svc, err := job.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService = %v", err)
	}

	r.svc = svc

	return r
}

func actorOf(subject string) job.Actor {
	return job.Actor{
		Principal: identity.Principal{
			Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: "", JobID: "",
		},
		Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{},
	}
}

func (r *rig) plan(key string) job.StartRequest {
	return job.StartRequest{
		Unit: r.unit, Operation: job.OperationPlan, Purpose: "", Executor: "", Location: emptyLocation(), Pool: "",
		Edits: nil, Delivery: "", Source: "", IdempotencyKey: key, Reason: "", BreakGlass: nil, Trigger: nil,
		Channel: audit.ChannelAPI, SourceIP: "192.0.2.7", UserAgent: "test", RequestID: "req-1",
	}
}

func TestStartPlan(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	j, created, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.plan("client-key"))
	if err != nil || !created {
		t.Fatalf("StartOnce = created %v, error %v; want a new job", created, err)
	}

	if len(r.store.creates) != 1 {
		t.Fatalf("Create calls = %d, want 1", len(r.store.creates))
	}

	c := r.store.creates[0]
	checkPlanStored(t, &c)

	if j.Source != job.SourceAPI || j.StartedBy != "ada" || len(j.Phases) != 1 || j.Phases[0].Kind != job.PhasePlan {
		t.Fatalf(
			"job = source %q, starter %q, phases %+v; want an api plan job of ada",
			j.Source,
			j.StartedBy,
			j.Phases,
		)
	}

	if len(c.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(c.entries))
	}

	checkPlanAudit(t, r, &c.entries[0])
}

// checkPlanStored checks the dispatch, the fence and the key of a stored plan job.
func checkPlanStored(t *testing.T, c *createCall) {
	t.Helper()

	if c.dispatch == nil || c.dispatch.Key != job.PhaseKey("job-fixed", 0) ||
		c.dispatch.Kind != job.RunnerKind("pool-1") ||
		c.dispatch.Version != job.PhaseVersion {
		t.Fatalf("dispatch = %+v, want the operation of phase 0 in the runner kind of the pool", c.dispatch)
	}

	if c.fence.Namespace != "acme" || c.fence.FailoverVersion != 3 {
		t.Fatalf("fence = %+v, want the fence of the namespace", c.fence)
	}

	if strings.Contains(c.job.IdempotencyKey, "client-key") || !strings.HasPrefix(c.job.IdempotencyKey, "k1:") {
		t.Fatalf("stored key = %q, want a hash that does not hold the key", c.job.IdempotencyKey)
	}
}

// checkPlanAudit checks the audit entry of the start of a plan by ada.
func checkPlanAudit(t *testing.T, r *rig, e *audit.Entry) {
	t.Helper()

	if e.Action != string(job.ActionUnitRunPlan) || e.Object != r.unit.String() || e.ObjectID != "unt-web" ||
		e.Subject != "ada" || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("audit entry = %+v, want the start of the plan by ada", e)
	}

	checkPlanAuditOrigin(t, r, e)

	if err := e.Validate(); err != nil {
		t.Fatalf("audit entry does not validate: %v", err)
	}

	checkPlanAuditChanges(t, e.Changes)
}

// checkPlanAuditOrigin checks the channel, the time and the request data of the entry.
func checkPlanAuditOrigin(t *testing.T, r *rig, e *audit.Entry) {
	t.Helper()

	if e.Channel != audit.ChannelAPI || !e.At.Equal(r.now) || e.SourceIP != "192.0.2.7" || e.UserAgent != "test" ||
		e.RequestID != "req-1" {
		t.Fatalf("audit entry = %+v, want the channel, time and request data of the start", e)
	}
}

// checkPlanAuditChanges checks the changes of the audit entry of a plan start.
func checkPlanAuditChanges(t *testing.T, changes []audit.Change) {
	t.Helper()

	got := map[string]string{}
	for _, ch := range changes {
		got[ch.Key] = ch.New
	}

	if got["job"] != "job-fixed" || got["source"] != "api" || got["operation"] != "plan" {
		t.Fatalf("audit changes = %v, want the job, the source and the operation", got)
	}
}

func TestStartSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		agent  string
		source job.SourceKind
		want   job.SourceKind
	}{
		{"default is api", "", "", job.SourceAPI},
		{"agent default", "dusty", "", job.SourceAgent},
		{"schedule", "", job.SourceSchedule, job.SourceSchedule},
		{"trigger", "", job.SourceTrigger, job.SourceTrigger},
		{"forge", "", job.SourceForge, job.SourceForge},
		{"self-service", "", job.SourceSelfService, job.SourceSelfService},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			a := actorOf("ada")
			a.Agent = tt.agent

			req := r.plan("")
			req.Source = tt.source

			j, err := r.svc.Start(t.Context(), a, req)
			if err != nil {
				t.Fatalf("Start = %v", err)
			}

			if j.Source != tt.want {
				t.Fatalf("source = %q, want %q", j.Source, tt.want)
			}

			// The policy sees the source of the start.
			if got := r.decider.inputs[0].Source; got != tt.want {
				t.Fatalf("decision input source = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartSourceRefused(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]job.SourceKind{"unknown": "cron", "upper case": "API"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)

			req := r.plan("")
			req.Source = source

			if _, err := r.svc.Start(t.Context(), actorOf("ada"), req); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("Start = %v, want %v", err, job.ErrInvalid)
			}

			if len(r.store.creates) != 0 {
				t.Fatalf("Create calls = %d, want none for a request that is not valid", len(r.store.creates))
			}
		})
	}
}

// refusal is one start that the service must refuse. Rows are positional.
type refusal struct {
	name    string
	who     string
	wantErr error
	// planned is the number of calls of the planner that the case allows.
	planned int
	mutate  func(req *job.StartRequest)
	rig     rigOption
}

// mustUnit returns the unit object of the tool opentofu, or panics.
func mustUnit(ns namespace.Name, name string) identity.Object {
	u, err := identity.UnitObject("opentofu", ns, name)
	if err != nil {
		panic(err)
	}

	return u
}

func runRefusals(t *testing.T, tests []refusal) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []rigOption
			if tt.rig != nil {
				opts = append(opts, tt.rig)
			}

			r := newRig(t, opts...)
			req := r.plan("")

			if tt.mutate != nil {
				tt.mutate(&req)
			}

			j, err := r.svc.Start(t.Context(), actorOf(tt.who), req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Start = %v, want %v", err, tt.wantErr)
			}

			checkStartRefused(t, r, &j, tt.planned)
		})
	}
}

// checkStartRefused checks that a refused start left no job and no write behind.
func checkStartRefused(t *testing.T, r *rig, j *job.Job, planned int) {
	t.Helper()

	if j.ID != "" {
		t.Fatalf("Start gave job %q with an error, want no job", j.ID)
	}

	if r.planner.calls != planned {
		t.Fatalf("planner calls = %d, want %d: a refused caller must not reach the planner", r.planner.calls, planned)
	}

	if planned == 0 && len(r.store.creates) != 0 {
		t.Fatalf("Create calls = %d, want none", len(r.store.creates))
	}

	if len(r.store.applies) != 0 {
		t.Fatalf("Apply calls = %d, want none after a refusal", len(r.store.applies))
	}
}

func TestStartRefusesBadRequests(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("k", job.MaxIdempotencyKey+1)

	runRefusals(t, []refusal{
		{"no principal", "", job.ErrInvalid, 0, nil, nil},
		{"unknown operation", "ada", job.ErrInvalid, 0, func(r *job.StartRequest) { r.Operation = "reboot" }, nil},
		{"unknown executor", "ada", job.ErrInvalid, 0, func(r *job.StartRequest) { r.Executor = "ssh" }, nil},
		{"key too long", "ada", job.ErrInvalid, 0, func(r *job.StartRequest) { r.IdempotencyKey = long }, nil},
		{
			"key with a control character", "ada", job.ErrInvalid, 0,
			func(r *job.StartRequest) { r.IdempotencyKey = "a\nb" }, nil,
		},
		{"reason with a line break", "ada", job.ErrInvalid, 0, func(r *job.StartRequest) { r.Reason = "a\nb" }, nil},
		{
			"object is not a unit", "ada", job.ErrInvalid, 0,
			func(r *job.StartRequest) { r.Unit = identity.NamespaceObject("acme") }, nil,
		},
		{
			"unit name with a control character", "ada", job.ErrInvalid, 0,
			func(r *job.StartRequest) { r.Unit = mustUnit("acme", "a\nb") }, nil,
		},
	})
}

func TestStartRefusesCallers(t *testing.T) {
	t.Parallel()

	runRefusals(t, []refusal{
		{"no run-plan action", "bob", identity.ErrDenied, 0, nil, func(_ *job.ServiceConfig, r *rig) {
			delete(r.authz.allow, string(job.ActionUnitRunPlan)+" "+r.unit.String())
		}},
		{
			"unknown unit", "ada", identity.ErrDenied, 0,
			func(r *job.StartRequest) { r.Unit = mustUnit("acme", "other") }, nil,
		},
		{"authorizer fails", "ada", errNoRights, 0, nil, func(_ *job.ServiceConfig, r *rig) {
			r.authz.fail[string(job.ActionUnitRunPlan)+" "+r.unit.String()] = errNoRights
		}},
		{"resolver fails", "ada", errDriver, 0, nil, func(cfg *job.ServiceConfig, r *rig) {
			cfg.Units = svcUnits{unit: r.unit, err: errDriver, other: nil}
		}},
		{"resolver gives another namespace", "ada", job.ErrInvalid, 0, nil, func(cfg *job.ServiceConfig, r *rig) {
			v := mustUnit("other", "web").WithUnitID("unt-x")
			cfg.Units = svcUnits{unit: r.unit, err: nil, other: &v}
		}},
		{"resolver gives no unit ID", "ada", job.ErrInvalid, 0, nil, func(cfg *job.ServiceConfig, r *rig) {
			u := mustUnit("acme", "web")
			cfg.Units = svcUnits{unit: r.unit, err: nil, other: &u}
		}},
	})
}

func TestStartRefusesWhenPortsFail(t *testing.T) {
	t.Parallel()

	runRefusals(t, []refusal{
		{"planner refuses", "ada", job.ErrUnsupported, 1, nil, func(_ *job.ServiceConfig, r *rig) {
			r.planner.err = job.ErrUnsupported
		}},
		{"planner changes the operation", "ada", job.ErrInvalid, 1, nil, func(_ *job.ServiceConfig, r *rig) {
			r.planner.spec = func(u identity.Object, _ *job.StartRequest) job.Spec {
				return job.Spec{
					Unit: u, Operation: job.OperationDestroy, Purpose: "", Executor: job.ExecutorRunner,
					Location: emptyLocation(), Steps: nil, Pool: "p", Profile: "", Edits: nil, Delivery: "",
					Defaulted: nil,
				}
			}
		}},
		{"executor refuses the spec", "ada", job.ErrInvalid, 1, nil, func(cfg *job.ServiceConfig, _ *rig) {
			cfg.Executors = []job.Executor{svcExecutor{kind: job.ExecutorRunner, err: job.ErrInvalid}}
		}},
		{"no executor of the kind", "ada", job.ErrUnsupported, 1, nil, func(cfg *job.ServiceConfig, _ *rig) {
			cfg.Executors = []job.Executor{svcExecutor{kind: job.ExecutorWriteBack, err: nil}}
		}},
		{"policy denies", "ada", job.ErrPolicyDenied, 1, nil, func(_ *job.ServiceConfig, r *rig) {
			r.decider.decision = job.Decision{
				Allow:         false,
				Reason:        "plans are off today",
				Gates:         nil,
				PolicyVersion: "v1",
			}
		}},
		{"policy fails", "ada", errPolicy, 1, nil, func(_ *job.ServiceConfig, r *rig) {
			r.decider.err = errPolicy
			// A decision that allows must not count when the policy failed.
			r.decider.decision = job.Decision{Allow: true, Reason: "", Gates: nil, PolicyVersion: ""}
		}},
		{"fence fails", "ada", namespace.ErrNotFound, 1, nil, func(cfg *job.ServiceConfig, _ *rig) {
			cfg.Fences = svcFences{err: namespace.ErrNotFound}
		}},
		{"store refuses a key", "ada", job.ErrKeyConflict, 1, nil, storeFails(job.ErrKeyConflict)},
		{"store above a limit", "ada", job.ErrLimit, 1, nil, storeFails(job.ErrLimit)},
		{"store fails", "ada", errDriver, 1, nil, storeFails(errDriver)},
		{"stale fence", "ada", namespace.ErrFenced, 1, nil, storeFails(namespace.ErrFenced)},
	})
}

// storeFails returns a rig option that makes the first create of the store fail with err.
func storeFails(err error) rigOption {
	return func(_ *job.ServiceConfig, r *rig) { r.store.createErrs = []error{err} }
}

// emptyLocation returns a location with no field set.
func emptyLocation() job.Location {
	return job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
}

// TestUnknownUnitLooksLikeDenied proves that a caller cannot tell a unit that
// does not exist from a unit that it may not use.
func TestUnknownUnitLooksLikeDenied(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	other, err := identity.UnitObject("opentofu", "acme", "other")
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	missing := r.plan("")
	missing.Unit = other

	_, errMissing := r.svc.Start(t.Context(), actorOf("ada"), missing)

	delete(r.authz.allow, string(job.ActionUnitRunPlan)+" "+r.unit.String())

	_, errDenied := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))

	if !errors.Is(errMissing, identity.ErrDenied) || !errors.Is(errDenied, identity.ErrDenied) {
		t.Fatalf("errors = %v and %v, want identity.ErrDenied for both", errMissing, errDenied)
	}

	if strings.Contains(errMissing.Error(), "other") {
		t.Fatalf("error %q names the unit that the caller asked for", errMissing)
	}
}

// startRefusalCase is one case of TestStartRefusalCodes.
type startRefusalCase struct {
	name string
	who  string
	// jobToken makes the caller the job token of a job. The rig grants the
	// start action to every subject but its denied one, so the refusal does
	// not come from a missing permission.
	jobToken bool
	// wantErr is the sentinel of the refusal.
	wantErr error
	// notErr is the sentinel that the refusal must not wrap. A refusal that
	// wraps identity.ErrDenied answers access.denied, because a denial wins
	// over the other refusals.
	notErr error
	// wantAsked is false for a refusal before the authorizer runs.
	wantAsked bool
}

// TestStartRefusalCodes proves that a job token with the start grants is
// refused as a policy refusal and not as a denial, and that a principal
// without the start grant is denied.
func TestStartRefusalCodes(t *testing.T) {
	t.Parallel()

	tests := []startRefusalCase{
		{
			name: "a job token with the start grants", who: "ada", jobToken: true, wantErr: job.ErrPolicyDenied,
			notErr: identity.ErrDenied, wantAsked: false,
		},
		{
			name: "a principal without the start grant", who: "bob", jobToken: false, wantErr: identity.ErrDenied,
			notErr: job.ErrPolicyDenied, wantAsked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkStartRefusal(t, tt)
		})
	}
}

// checkStartRefusal starts a job for one case of TestStartRefusalCodes.
func checkStartRefusal(t *testing.T, tt startRefusalCase) {
	t.Helper()

	r := newRig(t)
	a := actorOf(tt.who)

	if tt.jobToken {
		a.Principal.Credential = identity.CredentialJobToken
	}

	_, err := r.svc.Start(t.Context(), a, r.plan(""))
	if !errors.Is(err, tt.wantErr) || errors.Is(err, tt.notErr) {
		t.Fatalf("Start = %v, want %v and not %v", err, tt.wantErr, tt.notErr)
	}

	if asked := len(r.authz.asked) > 0; asked != tt.wantAsked {
		t.Fatalf("authorizer asked = %v, want %v", asked, tt.wantAsked)
	}

	if len(r.store.creates) != 0 {
		t.Fatalf("Create calls = %d, want none", len(r.store.creates))
	}
}

func TestStartGateOnFirstPhase(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(_ *job.ServiceConfig, r *rig) {
		r.decider.decision = job.Decision{
			Allow:         true,
			Reason:        "",
			PolicyVersion: "v1",
			Gates: map[job.PhaseKind]job.Gate{
				job.PhasePlan: {
					Type:      "",
					Approvals: 1,
					Groups:    nil,
					GroupIDs:  nil,
					AllowSelf: false,
					Timeout:   0,
					BindPlan:  false,
				},
			},
		}
	})

	j, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	if r.store.creates[0].dispatch != nil {
		t.Fatal("Create got a dispatch for a phase that waits for an approval")
	}

	// The first state is stored by the create itself: no second transaction.
	if len(r.store.applies) != 0 || r.store.creates[0].job.State != job.StateAwaitingApproval {
		t.Fatalf("applies = %+v, created state %s; want no change and awaiting-approval in the create",
			r.store.applies, r.store.creates[0].job.State)
	}

	if j.State != job.StateAwaitingApproval || len(j.Phases[0].Gates) != 1 ||
		j.Phases[0].Gates[0].Type != job.GateApproval {
		t.Fatalf("job = state %s, gates %+v; want awaiting-approval with the approval gate", j.State, j.Phases[0].Gates)
	}
}

func TestStartQueuesWhenHeld(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantWait job.WaitReason
	}{
		{"unit busy", job.ErrUnitBusy, job.WaitUnitBusy},
		{"manual lock", job.ErrLocked, job.WaitLocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, storeFails(tt.err))

			j, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))
			if err != nil {
				t.Fatalf("Start = %v", err)
			}

			checkQueued(t, r, &j, tt.wantWait)
		})
	}
}

// checkQueued checks that the service stored j twice: dispatched first, then queued.
func checkQueued(t *testing.T, r *rig, j *job.Job, wantWait job.WaitReason) {
	t.Helper()

	if len(r.store.creates) != 2 || r.store.creates[0].dispatch == nil || r.store.creates[1].dispatch != nil {
		t.Fatalf("creates = %d, want a dispatch first and a queued job second", len(r.store.creates))
	}

	if j.Wait != wantWait || j.State == job.StateDispatched {
		t.Fatalf("job = state %s, wait %q; want queued with wait %q", j.State, j.Wait, wantWait)
	}

	// The queued job is the same job: same ID, same key, same entry.
	if r.store.creates[1].job.ID != r.store.creates[0].job.ID || len(r.store.creates[1].entries) != 1 {
		t.Fatal("the second create must store the same job with its audit entry")
	}
}

func TestStartRefusesWhenFrozen(t *testing.T) {
	t.Parallel()

	r := newRig(t, storeFails(job.ErrFrozen))

	if _, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan("")); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("Start = %v, want %v", err, job.ErrFrozen)
	}
}

func TestStartIdempotency(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	if _, _, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.plan("same")); err != nil {
		t.Fatalf("first StartOnce = %v", err)
	}

	_, created, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.plan("same"))
	if err != nil || created {
		t.Fatalf("second StartOnce = created %v, error %v; want the first job again", created, err)
	}

	if _, err := r.svc.Start(t.Context(), actorOf("bob"), r.plan("same")); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Start by bob = %v, want denied (bob has no right)", err)
	}

	r.authz.denied = ""

	if _, _, err := r.svc.StartOnce(t.Context(), actorOf("bob"), r.plan("same")); err != nil {
		t.Fatalf("StartOnce by bob = %v", err)
	}

	keys := map[string]string{}
	for _, c := range r.store.creates {
		keys[c.job.StartedBy] = c.job.IdempotencyKey
	}

	if keys["ada"] == "" || keys["ada"] == keys["bob"] {
		t.Fatalf("stored keys = %v, want one key for each principal, so that bob can never read the job of ada", keys)
	}

	none := r.plan("")
	if _, _, err := r.svc.StartOnce(t.Context(), actorOf("ada"), none); err != nil {
		t.Fatalf("StartOnce with no key = %v", err)
	}

	if last := r.store.creates[len(r.store.creates)-1].job.IdempotencyKey; last != "" {
		t.Fatalf("stored key = %q, want none for a start with no key", last)
	}
}

// TestStartKeyConflictForAnotherRequest proves that two starts with one key
// and one principal are the same request only when their edits and their
// resolved source kind are the same. The second start of another request
// fails with ErrKeyConflict instead of replaying the first job.
func TestStartKeyConflictForAnotherRequest(t *testing.T) {
	t.Parallel()

	editsOne := []job.FileEdit{
		{Path: "a", Keys: map[string][]byte{"b": []byte("c")}},
		{Path: "d", Keys: map[string][]byte{}},
	}
	editsTwo := []job.FileEdit{
		{Path: "a", Keys: map[string][]byte{}},
		{Path: "b", Keys: map[string][]byte{"c": []byte("d")}},
	}

	tests := []struct {
		name   string
		first  func(*job.StartRequest)
		second func(*job.StartRequest)
	}{
		{
			"another edit structure",
			func(req *job.StartRequest) { req.Edits = editsOne },
			func(req *job.StartRequest) { req.Edits = editsTwo },
		},
		{
			"another source kind",
			func(req *job.StartRequest) { req.Source = job.SourceAPI },
			func(req *job.StartRequest) { req.Source = job.SourceSelfService },
		},
		{
			// Without the trigger marker, the trigger fields "p" and "0"
			// read as one edit at path "p" with no keys.
			"a trigger against an edit",
			func(req *job.StartRequest) { req.Trigger = &job.TriggerRef{ID: "p", Saver: "0"} },
			func(req *job.StartRequest) { req.Edits = []job.FileEdit{{Path: "p", Keys: map[string][]byte{}}} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)

			first := r.plan("same-key")
			tt.first(&first)

			if _, err := r.svc.Start(t.Context(), actorOf("ada"), first); err != nil {
				t.Fatalf("first Start = %v", err)
			}

			second := r.plan("same-key")
			tt.second(&second)

			if _, err := r.svc.Start(t.Context(), actorOf("ada"), second); !errors.Is(err, job.ErrKeyConflict) {
				t.Fatalf("second Start = %v, want %v", err, job.ErrKeyConflict)
			}
		})
	}
}

func TestStartAgent(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	a := actorOf("ada")
	a.Agent, a.Mode = "dusty", job.AgentModeApproveFirst

	j, err := r.svc.Start(t.Context(), a, r.plan(""))
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	// A runner plan changes nothing, so it needs no confirmation in any mode.
	if j.Agent != "dusty" || j.AgentMode != job.AgentModeApproveFirst || len(j.Phases[0].Gates) != 0 {
		t.Fatalf("job = agent %q, mode %q, gates %+v; want the agent and no gate", j.Agent, j.AgentMode,
			j.Phases[0].Gates)
	}

	if r.decider.inputs[0].Agent != "dusty" || r.decider.inputs[0].AgentMode != job.AgentModeApproveFirst {
		t.Fatalf("decision input = %+v, want the agent and its mode", r.decider.inputs[0])
	}
}

// freezeRig returns a rig whose service reads the freezes from fz.
func freezeRig(t *testing.T, fz job.FreezeStore, opts ...rigOption) *rig {
	t.Helper()

	opts = append(opts, func(cfg *job.ServiceConfig, _ *rig) { cfg.Freezes = fz })

	return newRig(t, opts...)
}

// dropBreakFreeze removes the break-freeze action of the caller.
func dropBreakFreeze(_ *job.ServiceConfig, r *rig) {
	delete(r.authz.allow, string(job.ActionNamespaceBreakFreeze)+" "+identity.NamespaceObject("acme").String())
}

func activeFreeze() job.Freeze {
	return job.Freeze{
		ID: "frz-1", Namespace: "acme", Reason: "incident", StartsAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		EndsAt: time.Time{}, CreatedBy: "ops", EndedBy: "",
	}
}

func futureFreeze() job.Freeze {
	return job.Freeze{
		ID: "frz-2", Namespace: "acme", Reason: "later", StartsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		EndsAt: time.Time{}, CreatedBy: "ops", EndedBy: "",
	}
}

func TestStartBreakGlass(t *testing.T) {
	t.Parallel()

	r := freezeRig(t, svcFreezes{list: []job.Freeze{futureFreeze(), activeFreeze()}, err: nil})

	req := r.plan("")
	req.BreakGlass = &job.BreakGlass{Reason: "outage", Freeze: "frz-spoofed"}

	j, err := r.svc.Start(t.Context(), actorOf("ada"), req)
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	if j.BreakGlass == nil || j.BreakGlass.Freeze != "frz-1" || j.BreakGlass.Reason != "outage" {
		t.Fatalf("break-glass = %+v, want the active freeze frz-1 and the reason, never the caller's freeze ID",
			j.BreakGlass)
	}

	e := r.store.creates[0].entries[0]
	if e.Kind != audit.KindOverride || e.Reason != "outage" {
		t.Fatalf("audit entry = kind %s, reason %q; want an override with the reason", e.Kind, e.Reason)
	}
}

func TestStartBreakGlassRefused(t *testing.T) {
	t.Parallel()

	activeOnly := svcFreezes{list: []job.Freeze{activeFreeze()}, err: nil}

	tests := []struct {
		name    string
		freezes job.FreezeStore
		agent   string
		reason  string
		noRight bool
		wantErr error
	}{
		{
			"no active freeze",
			svcFreezes{list: []job.Freeze{futureFreeze()}, err: nil},
			"",
			"outage",
			false,
			job.ErrInvalid,
		},
		{"no freeze store", nil, "", "outage", false, job.ErrInvalid},
		{"freeze store fails", svcFreezes{list: nil, err: errDriver}, "", "outage", false, errDriver},
		{"no reason", activeOnly, "", "", false, job.ErrInvalid},
		{"reason with a line break", activeOnly, "", "a\nb", false, job.ErrInvalid},
		{"agent", activeOnly, "dusty", "outage", false, job.ErrAgentDenied},
		{"no break-freeze action", activeOnly, "", "outage", true, identity.ErrDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []rigOption
			if tt.noRight {
				opts = append(opts, dropBreakFreeze)
			}

			r := freezeRig(t, tt.freezes, opts...)

			a := actorOf("ada")
			a.Agent = tt.agent

			req := r.plan("")
			req.BreakGlass = &job.BreakGlass{Reason: tt.reason, Freeze: ""}

			if _, err := r.svc.Start(t.Context(), a, req); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Start = %v, want %v", err, tt.wantErr)
			}

			if len(r.store.creates) != 0 {
				t.Fatalf("Create calls = %d, want none", len(r.store.creates))
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	started, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))
	if err != nil {
		t.Fatalf("Start = %v", err)
	}

	tests := []struct {
		name    string
		id      job.ID
		deny    bool
		fail    error
		wantErr error
	}{
		{"reads", started.ID, false, nil, nil},
		{"unknown job", "job-unknown", false, nil, job.ErrNotFound},
		{"not a job ID", "unt-web", false, nil, job.ErrNotFound},
		{"no job:read", started.ID, true, nil, job.ErrNotFound},
		{"authorizer fails", started.ID, false, errNoRights, errNoRights},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr := newRig(t)
			if _, err := rr.svc.Start(t.Context(), actorOf("ada"), rr.plan("")); err != nil {
				t.Fatalf("Start = %v", err)
			}

			key := string(job.ActionJobRead) + " " + rr.unit.String()
			if tt.deny {
				delete(rr.authz.allow, key)
			}

			if tt.fail != nil {
				rr.authz.fail[key] = tt.fail
			}

			got, err := rr.svc.Get(t.Context(), actorOf("cy"), tt.id)
			checkGet(t, &got, err, tt.wantErr, started.ID)
		})
	}
}

// checkGet checks the result of a Get against the wanted error.
func checkGet(t *testing.T, got *job.Job, err, wantErr error, want job.ID) {
	t.Helper()

	if !errors.Is(err, wantErr) {
		t.Fatalf("Get = %v, want %v", err, wantErr)
	}

	if wantErr == nil && got.ID != want {
		t.Fatalf("Get = %q, want %q", got.ID, want)
	}

	if wantErr != nil && got.ID != "" {
		t.Fatalf("Get gave job %q with an error", got.ID)
	}
}

func TestMethodsOfALaterVersion(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	a := actorOf("ada")

	if _, err := r.svc.Cancel(t.Context(), a, "job-1", ""); !errors.Is(err, job.ErrUnsupported) {
		t.Errorf("Cancel = %v, want ErrUnsupported", err)
	}

	if _, err := r.svc.Approve(t.Context(), a, "job-1", job.VerdictApprove, emptyBinding(), ""); !errors.Is(
		err, job.ErrUnsupported,
	) {
		t.Errorf("Approve = %v, want ErrUnsupported", err)
	}

	if _, err := r.svc.Confirm(t.Context(), a, "job-1", job.VerdictApprove, emptyBinding()); !errors.Is(
		err, job.ErrUnsupported,
	) {
		t.Errorf("Confirm = %v, want ErrUnsupported", err)
	}

	if _, err := r.svc.RedactLog(t.Context(), a, "job-1", "v1", nil, ""); !errors.Is(err, job.ErrUnsupported) {
		t.Errorf("RedactLog = %v, want ErrUnsupported", err)
	}
}

func TestNewServiceRefusesAPartialConfig(t *testing.T) {
	t.Parallel()

	full := func(r *rig) job.ServiceConfig {
		return job.ServiceConfig{
			Store:   r.store,
			Units:   svcUnits{unit: r.unit, err: nil, other: nil},
			Planner: r.planner,
			Executors: []job.Executor{
				svcExecutor{kind: job.ExecutorRunner, err: nil},
			},
			Decider:    r.decider,
			Authorizer: r.authz,
			Gates:      r.gates,
			Fences:     svcFences{err: nil},
			Freezes:    nil,
			Switches:   nil,
			Now:        nil,
			NewID:      nil,
			Log:        slog.New(slog.DiscardHandler),
		}
	}

	tests := []struct {
		name   string
		mutate func(*job.ServiceConfig)
	}{
		{name: "no store", mutate: func(c *job.ServiceConfig) { c.Store = nil }},
		{name: "no units", mutate: func(c *job.ServiceConfig) { c.Units = nil }},
		{name: "no planner", mutate: func(c *job.ServiceConfig) { c.Planner = nil }},
		{name: "no decider", mutate: func(c *job.ServiceConfig) { c.Decider = nil }},
		{name: "no authorizer", mutate: func(c *job.ServiceConfig) { c.Authorizer = nil }},
		{name: "no gates", mutate: func(c *job.ServiceConfig) { c.Gates = nil }},
		{name: "no fences", mutate: func(c *job.ServiceConfig) { c.Fences = nil }},
		{name: "no logger", mutate: func(c *job.ServiceConfig) { c.Log = nil }},
		{name: "no executor", mutate: func(c *job.ServiceConfig) { c.Executors = nil }},
		{name: "a nil executor", mutate: func(c *job.ServiceConfig) { c.Executors = []job.Executor{nil} }},
		{name: "an unknown kind", mutate: func(c *job.ServiceConfig) {
			c.Executors = []job.Executor{svcExecutor{kind: "ssh", err: nil}}
		}},
		{name: "two executors of one kind", mutate: func(c *job.ServiceConfig) {
			c.Executors = []job.Executor{
				svcExecutor{kind: job.ExecutorRunner, err: nil},
				svcExecutor{kind: job.ExecutorRunner, err: nil},
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := full(newRig(t))
			tt.mutate(&cfg)

			if _, err := job.NewService(cfg); !errors.Is(err, job.ErrInvalid) {
				t.Fatalf("NewService = %v, want ErrInvalid", err)
			}
		})
	}
}

// emptyBinding returns a plan binding with no field set.
func emptyBinding() job.PlanBinding {
	return job.PlanBinding{Plan: "", DisplaySHA256: "", ApplySHA256: "", StateVersion: ""}
}

// gateCase is one case of TestStartGateGuard.
type gateCase struct {
	name      string
	enforced  []job.EnforcedGate
	guardErr  error
	wantErr   error
	wantNamed []string
}

func gateCases() []gateCase {
	stateWrite := job.EnforcedGate{Scope: "unit/acme/opentofu/web", ID: "no-plans", Point: "state.write"}
	propose := job.EnforcedGate{Scope: "namespace/acme", ID: "review-first", Point: "change.propose"}

	return []gateCase{
		{"no gate", nil, nil, nil, nil},
		{
			"an enforced gate at state.write",
			[]job.EnforcedGate{stateWrite},
			nil, job.ErrGateEnforced,
			[]string{"no-plans", "state.write"},
		},
		{
			"an enforced gate at change.propose",
			[]job.EnforcedGate{propose},
			nil, job.ErrGateEnforced,
			[]string{"review-first", "change.propose"},
		},
		{
			"two enforced gates",
			[]job.EnforcedGate{stateWrite, propose},
			nil, job.ErrGateEnforced,
			[]string{"no-plans", "review-first"},
		},
		{"the guard cannot answer", nil, errDriver, job.ErrGateEnforced, nil},
	}
}

func TestStartGateGuard(t *testing.T) {
	t.Parallel()

	for _, tt := range gateCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			r.gates.enforced, r.gates.err = tt.enforced, tt.guardErr

			j, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))
			if tt.wantErr == nil {
				if err != nil || j.ID == "" || len(r.store.creates) != 1 {
					t.Fatalf("Start = %v, creates %d; want a job", err, len(r.store.creates))
				}

				return
			}

			checkGateRefusal(t, r, err, &tt)
		})
	}
}

// checkGateRefusal checks a start that a gate refused: the error, no job, the
// names of the gates, and no detail of the guard.
func checkGateRefusal(t *testing.T, r *rig, err error, tt *gateCase) {
	t.Helper()

	if !errors.Is(err, tt.wantErr) {
		t.Fatalf("Start = %v, want %v", err, tt.wantErr)
	}

	if len(r.store.creates) != 0 {
		t.Fatalf("Create calls = %d, want none: a covered unit starts no job", len(r.store.creates))
	}

	for _, want := range tt.wantNamed {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	if tt.guardErr != nil && strings.Contains(err.Error(), "driver") {
		t.Errorf("error %q holds the detail of the guard", err)
	}
}

// TestStartGateGuardComesAfterTheActions proves that a caller without the
// start action learns nothing about the gates of the unit.
func TestStartGateGuardComesAfterTheActions(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.gates.enforced = []job.EnforcedGate{{Scope: "installation", ID: "secret-gate", Point: "state.write"}}

	_, err := r.svc.Start(t.Context(), actorOf("bob"), r.plan(""))
	if !errors.Is(err, identity.ErrDenied) || errors.Is(err, job.ErrGateEnforced) {
		t.Fatalf("Start by bob = %v, want denied with no word of the gate", err)
	}

	if len(r.gates.asked) != 0 {
		t.Fatalf("the guard was asked %d times for a caller without rights", len(r.gates.asked))
	}
}

// TestStartIsAtomicAtTheFirstGate proves failure injection at the second
// transaction: the create stores awaiting-approval, so a failing Apply
// changes nothing, and a failing create leaves no job.
func TestStartIsAtomicAtTheFirstGate(t *testing.T) {
	t.Parallel()

	gated := func(_ *job.ServiceConfig, r *rig) {
		r.decider.decision = job.Decision{
			Allow: true, Reason: "", PolicyVersion: "v1",
			Gates: map[job.PhaseKind]job.Gate{
				job.PhasePlan: {
					Type:      "",
					Approvals: 1,
					Groups:    nil,
					GroupIDs:  nil,
					AllowSelf: false,
					Timeout:   0,
					BindPlan:  false,
				},
			},
		}
	}

	t.Run("a failing second transaction cannot strand the job", func(t *testing.T) {
		t.Parallel()

		r := newRig(t, gated)
		r.store.applyErr = errDriver

		j, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan("k"))
		if err != nil || j.State != job.StateAwaitingApproval {
			t.Fatalf("Start = state %s, error %v; want awaiting-approval though Apply fails", j.State, err)
		}

		if len(r.store.applies) != 0 {
			t.Fatalf("applies = %d, want none", len(r.store.applies))
		}
	})

	t.Run("a failing create leaves no job, and the retry stores one", func(t *testing.T) {
		t.Parallel()

		r := newRig(t, gated)
		r.store.createErrs = []error{errDriver}

		if _, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan("k")); err == nil {
			t.Fatal("Start with a failing create = nil, want an error")
		}

		if len(r.store.jobs) != 0 {
			t.Fatalf("stored jobs = %d, want none after a failed create", len(r.store.jobs))
		}

		j, created, err := r.svc.StartOnce(t.Context(), actorOf("ada"), r.plan("k"))
		if err != nil || !created || j.State != job.StateAwaitingApproval {
			t.Fatalf("retry = state %s, created %v, error %v; want a new awaiting-approval job", j.State, created, err)
		}
	})
}

func TestStartRequestHash(t *testing.T) {
	t.Parallel()

	hashOf := func(t *testing.T, mutate func(*job.StartRequest, *rig)) string {
		t.Helper()

		r := newRig(t)
		req := r.plan("k")
		req.Reason = "first reason"

		if mutate != nil {
			mutate(&req, r)
		}

		j, err := r.svc.Start(t.Context(), actorOf("ada"), req)
		if err != nil {
			t.Fatalf("Start = %v", err)
		}

		return j.RequestHash
	}

	base := hashOf(t, nil)
	if !strings.HasPrefix(base, "r1:") || base != hashOf(t, nil) {
		t.Fatalf("hash = %q, want a stable r1: hash for one request", base)
	}

	tests := []struct {
		name   string
		mutate func(*job.StartRequest, *rig)
	}{
		{"another reason", func(q *job.StartRequest, _ *rig) { q.Reason = "second reason" }},
		{"another pool name", func(q *job.StartRequest, _ *rig) { q.Pool = "other" }},
		{"another location", func(q *job.StartRequest, _ *rig) { q.Location.Ref = "dev" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := hashOf(t, tt.mutate); got == base {
				t.Fatalf("hash with %s = hash of the base request", tt.name)
			}
		})
	}

	r := newRig(t)
	if j, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan("")); err != nil || j.RequestHash != "" {
		t.Fatalf("Start with no key = hash %q, error %v; want no hash", j.RequestHash, err)
	}
}

// fieldCase is one case of TestStartChecksTheActionsOfThePoolAndTheLocation.
type fieldCase struct {
	name     string
	pool     string
	location job.Location
	revoke   identity.Action
	wantErr  error
}

func fieldCases() []fieldCase {
	repo := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "", Revision: ""}
	none := emptyLocation()

	return []fieldCase{
		{"neither field", "", none, "", nil},
		{"a pool with the action", "ci", none, "", nil},
		{"a location with the action", "", repo, "", nil},
		{"both with both actions", "ci", repo, "", nil},
		{"a pool without namespace:manage-runners", "ci", none, job.ActionNamespaceManageRunners, identity.ErrDenied},
		{"a location without unit:define-steps", "", repo, job.ActionUnitDefineSteps, identity.ErrDenied},
		{"both without namespace:manage-runners", "ci", repo, job.ActionNamespaceManageRunners, identity.ErrDenied},
		{"both without unit:define-steps", "ci", repo, job.ActionUnitDefineSteps, identity.ErrDenied},
		{"no fields without the action of the pool", "", none, job.ActionNamespaceManageRunners, nil},
	}
}

// TestStartChecksTheActionsOfThePoolAndTheLocation proves a request that names
// a pool needs namespace:manage-runners, and a request that names a code
// location needs unit:define-steps. The planner never runs for a caller
// without them, and no job is stored.
func TestStartChecksTheActionsOfThePoolAndTheLocation(t *testing.T) {
	t.Parallel()

	for _, tt := range fieldCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)

			for key := range r.authz.allow {
				if tt.revoke != "" && strings.HasPrefix(key, string(tt.revoke)+" ") {
					delete(r.authz.allow, key)
				}
			}

			req := r.plan("")
			req.Pool, req.Location = tt.pool, tt.location

			_, err := r.svc.Start(t.Context(), actorOf("ada"), req)
			checkFieldResult(t, r, err, &tt)
		})
	}
}

// checkFieldResult checks the error, the planner calls and the stored jobs of
// a start that names a pool or a location.
func checkFieldResult(t *testing.T, r *rig, err error, tt *fieldCase) {
	t.Helper()

	if tt.wantErr == nil && err != nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
		t.Fatalf("Start = %v, want %v", err, tt.wantErr)
	}

	refused := tt.wantErr != nil
	if (r.planner.calls > 0) == refused {
		t.Fatalf("planner calls = %d for error %v: the planner runs only for a caller with the actions",
			r.planner.calls, tt.wantErr)
	}

	if stored := len(r.store.creates); refused == (stored == 1) {
		t.Fatalf("Create calls = %d for error %v", stored, tt.wantErr)
	}
}

// TestStartRefusesWhenThePlannerRefuses proves a refusal of the planner
// (zero pools, two pools, a unit with no code location, a connection of
// another namespace) stores no job and asks for no gate.
func TestStartRefusesWhenThePlannerRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want error
	}{
		{"zero pools", fmt.Errorf("%w: no pool", job.ErrUnsupported), job.ErrUnsupported},
		{"two pools", fmt.Errorf("%w: two pools", job.ErrUnsupported), job.ErrUnsupported},
		{"no code location", fmt.Errorf("%w: no location", job.ErrUnsupported), job.ErrUnsupported},
		{"a connection of another namespace", fmt.Errorf("%w: connection", identity.ErrDenied), identity.ErrDenied},
		{"a pool of another namespace", fmt.Errorf("%w: no pool", job.ErrInvalid), job.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t)
			r.planner.err = tt.err

			_, err := r.svc.Start(t.Context(), actorOf("ada"), r.plan(""))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Start = %v, want %v", err, tt.want)
			}

			if len(r.store.creates) != 0 || len(r.gates.asked) != 0 {
				t.Fatalf("creates %d, gate checks %d; want none", len(r.store.creates), len(r.gates.asked))
			}
		})
	}
}
