package jobtokentest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/jobtoken/jobtokentest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// memFence is the failover version of a namespace in memory.
	memFence = 5
	// memIssueMs is the time of the tests, in Unix milliseconds.
	memIssueMs = 1_700_000_000_000
	// memStepTimeout is the timeout of the step of a test.
	memStepTimeout = 90 * time.Second
	// memGrace is the grace period after the step timeout.
	memGrace = 30 * time.Second
	// memLease is the time from a claim to its end.
	memLease = time.Hour
)

// memPrincipal is one principal record in memory.
type memPrincipal struct {
	id        string
	principal identity.Principal
	disabled  bool
	deleted   bool
}

// memClaim is the claim of one durable operation in memory.
type memClaim struct {
	state    durable.State
	token    int64
	leaseEnd time.Time
}

// memNamespace is one namespace in memory: its stable ID and its current
// name.
type memNamespace struct {
	id   string
	name namespace.Name
}

// memToken is one token row in memory. It keeps the namespace by its stable
// ID, as an adapter keeps a foreign key.
type memToken struct {
	token       jobtoken.Token
	namespaceID string
}

// memWorld is a jobtoken.Store in memory. It proves that the cases hold for
// a correct adapter.
type memWorld struct {
	mu         sync.Mutex
	fences     map[namespace.Name]int64
	namespaces map[string]*memNamespace
	nsByName   map[namespace.Name]string
	units      map[string]identity.Object
	principals map[string]*memPrincipal
	// bySubject maps the subject of a principal to the ID of its record.
	bySubject map[string]string
	jobs      map[job.ID]*job.Job
	claims    map[durable.ID]*memClaim
	tokens    map[jobtoken.Digest]memToken
	audit     map[namespace.Name][]audit.Entry
}

var _ jobtoken.Store = (*memWorld)(nil)

func newMemWorld() *memWorld {
	return &memWorld{
		mu:         sync.Mutex{},
		fences:     map[namespace.Name]int64{},
		namespaces: map[string]*memNamespace{},
		nsByName:   map[namespace.Name]string{},
		units:      map[string]identity.Object{},
		principals: map[string]*memPrincipal{},
		bySubject:  map[string]string{},
		jobs:       map[job.ID]*job.Job{},
		claims:     map[durable.ID]*memClaim{},
		tokens:     map[jobtoken.Digest]memToken{},
		audit:      map[namespace.Name][]audit.Entry{},
	}
}

// Issue implements jobtoken.Store.
func (m *memWorld) Issue(_ context.Context, tok jobtoken.Token, fence namespace.Fence, e audit.Entry) error {
	if err := tok.Validate(); err != nil {
		return err
	}

	if err := checkEntry(fence, e); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.issueLocked(tok, fence, e)
}

// GetByDigest implements jobtoken.Store.
func (m *memWorld) GetByDigest(_ context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var mch jobtoken.Match

	row, ok := m.tokens[d]
	if !ok {
		return mch, jobtoken.ErrNotFound
	}

	ns, ok := m.namespaces[row.namespaceID]
	if !ok {
		return mch, jobtoken.ErrNotFound
	}

	tok := row.token
	if ns.name != tok.Unit.Namespace() {
		return mch, jobtoken.ErrNotFound
	}

	starter, ok := m.principals[tok.StarterID]
	if !ok || starter.deleted {
		return mch, jobtoken.ErrNotFound
	}

	j, ok := m.jobs[tok.JobID]
	if !ok {
		return mch, jobtoken.ErrNotFound
	}

	claim, ok := m.claims[tok.ClaimOperation]
	if !ok {
		return mch, jobtoken.ErrNotFound
	}

	mch.Token = tok
	mch.Claim.OperationState = claim.state
	mch.Claim.OperationClaimToken = claim.token
	mch.Claim.LeaseEndsAt = claim.leaseEnd
	mch.Claim.JobID = j.ID
	mch.Claim.JobState = j.State
	mch.Claim.JobOperation = currentOperation(j)
	mch.Claim.JobUnitID = j.Spec.Unit.UnitID()

	jobStarterID, ok := m.bySubject[j.StartedBy]
	if !ok {
		return mch, jobtoken.ErrNotFound
	}

	mch.Claim.JobStarterID = jobStarterID
	mch.Claim.Starter = starter.principal
	mch.Claim.StarterRecordID = starter.id
	mch.Claim.StarterEnabled = !starter.disabled

	return mch, nil
}

// issueLocked writes tok and its audit entry. The caller holds the lock.
func (m *memWorld) issueLocked(tok jobtoken.Token, fence namespace.Fence, e audit.Entry) error {
	namespaceID, current := m.namespaceOf(tok.Namespace, fence)
	if !current {
		return namespace.ErrFenced
	}

	if !tok.ExpiresAt.After(e.At) {
		return fmt.Errorf("%w: the expiry is not after the issue time", jobtoken.ErrInvalid)
	}

	if _, ok := m.tokens[tok.Digest]; ok {
		return jobtoken.ErrAlreadyExists
	}

	j, ok := m.jobs[tok.JobID]
	if !ok {
		return jobtoken.ErrNotFound
	}

	claim, ok := m.claims[tok.ClaimOperation]
	if !ok {
		return jobtoken.ErrNotFound
	}

	starter, ok := m.principals[tok.StarterID]
	if !ok || starter.deleted || starter.disabled {
		return jobtoken.ErrNotLive
	}

	if m.bySubject[j.StartedBy] != tok.StarterID {
		return fmt.Errorf("%w: the starter is not the job starter", jobtoken.ErrInvalid)
	}

	if err := checkClaimAndJob(j, claim, tok, e.At); err != nil {
		return err
	}

	m.tokens[tok.Digest] = memToken{token: tok, namespaceID: namespaceID}
	m.audit[tok.Namespace] = append(m.audit[tok.Namespace], e)

	return nil
}

// namespaceOf returns the stable ID of the namespace ns when the fence is
// current. The second result is false when the fence is stale, names another
// namespace, or names no namespace.
func (m *memWorld) namespaceOf(ns namespace.Name, fence namespace.Fence) (string, bool) {
	if fence.Namespace != ns || fence.FailoverVersion != m.fences[ns] {
		return "", false
	}

	id, ok := m.nsByName[ns]

	return id, ok
}

// checkEntry refuses an audit entry that fails validation or that names
// another namespace than the fence.
func checkEntry(fence namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: audit entry: %w", audit.ErrInvalidEntry, err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names another namespace", audit.ErrInvalidEntry)
	}

	return nil
}

// checkClaimAndJob refuses a token of a claim or job that is not live.
func checkClaimAndJob(j *job.Job, claim *memClaim, tok jobtoken.Token, at time.Time) error {
	if claim.state != durable.StateRunning || claim.token != tok.ClaimToken || !claim.leaseEnd.After(at) {
		return jobtoken.ErrNotLive
	}

	if !jobtoken.JobStateLive(j.State) || currentOperation(j) != tok.ClaimOperation ||
		j.Spec.Unit.UnitID() != tok.Unit.UnitID() {
		return jobtoken.ErrNotLive
	}

	return nil
}

// currentOperation returns the current operation of a job.
func currentOperation(j *job.Job) durable.ID {
	if j.Current < 0 || j.Current >= len(j.Phases) {
		return ""
	}

	return durable.ID(j.Phases[j.Current].Operation)
}

func (m *memWorld) harness() jobtokentest.Harness {
	return jobtokentest.Harness{
		Store:           m,
		Namespace:       m.makeNamespace,
		DeleteNamespace: m.deleteNamespace,
		RenameNamespace: m.renameNamespace,
		Unit:            m.makeUnit,
		Principal:       m.makePrincipal,
		Disable:         m.disable,
		Delete:          m.deletePrincipal,
		Rename:          m.rename,
		Job:             m.makeJob,
		Takeover:        m.takeover,
		SetJobState:     m.setJobState,
		EndClaim:        m.endClaim,
		ReleaseClaim:    m.releaseClaim,
		EndJob:          m.endJob,
		MoveJob:         m.moveJob,
		WriteRaw:        m.writeRaw,
		Audit:           m.entries,
	}
}

func (m *memWorld) makeNamespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	ns, err := namespace.ParseName(name)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.fences[ns]; !ok {
		id := ident.NewID("ns-")
		m.fences[ns] = memFence
		m.namespaces[id] = &memNamespace{id: id, name: ns}
		m.nsByName[ns] = id
		m.audit[ns] = nil
	}

	return namespace.Fence{Namespace: ns, FailoverVersion: memFence}
}

// deleteNamespace deletes the namespace ns and its fence.
func (m *memWorld) deleteNamespace(t *testing.T, ns namespace.Name) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	id, ok := m.nsByName[ns]
	if !ok {
		t.Fatalf("namespace %s does not exist", ns)
	}

	delete(m.nsByName, ns)
	delete(m.namespaces, id)
	delete(m.fences, ns)
	delete(m.audit, ns)
}

// renameNamespace changes the name of namespace ns to newName. The stable ID
// of the namespace stays.
func (m *memWorld) renameNamespace(t *testing.T, ns namespace.Name, newName string) {
	t.Helper()

	renamed, err := namespace.ParseName(newName)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id, ok := m.nsByName[ns]
	if !ok {
		t.Fatalf("namespace %s does not exist", ns)
	}

	if _, ok := m.nsByName[renamed]; ok {
		t.Fatalf("namespace %s already exists", renamed)
	}

	record, found := m.namespaces[id]
	if !found || record == nil {
		t.Fatalf("namespace %s has no record", ns)
	}

	record.name = renamed

	delete(m.nsByName, ns)
	m.nsByName[renamed] = id

	if version, ok := m.fences[ns]; ok {
		delete(m.fences, ns)
		m.fences[renamed] = version
	}

	if entries, ok := m.audit[ns]; ok {
		delete(m.audit, ns)
		m.audit[renamed] = entries
	}
}

func (m *memWorld) makeUnit(t *testing.T, nsName, tool, name string) identity.Object {
	t.Helper()

	ns, err := namespace.ParseName(nsName)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatal(err)
	}

	id := ident.NewID("unt-")
	unit := ref.WithUnitID(id)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.fences[ns]; !ok {
		t.Fatalf("namespace %s does not exist", nsName)
	}

	m.units[id] = unit

	return unit
}

func (m *memWorld) makePrincipal(t *testing.T, subject string) jobtokentest.Starter {
	t.Helper()

	id := ident.NewID("prn-")

	var p identity.Principal

	p.Subject = subject
	p.DisplayName = subject
	p.Kind = identity.KindPerson

	m.mu.Lock()
	defer m.mu.Unlock()

	m.principals[id] = &memPrincipal{id: id, principal: p, disabled: false, deleted: false}
	m.bySubject[subject] = id

	return jobtokentest.Starter{ID: id, Principal: p}
}

func (m *memWorld) disable(t *testing.T, s jobtokentest.Starter) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.principals[s.ID]
	if !ok {
		t.Fatalf("principal %s does not exist", s.ID)
	}

	p.disabled = true
}

func (m *memWorld) deletePrincipal(t *testing.T, s jobtokentest.Starter) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.principals[s.ID]
	if !ok {
		t.Fatalf("principal %s does not exist", s.ID)
	}

	p.deleted = true
	delete(m.bySubject, p.principal.Subject)
}

func (m *memWorld) makeJob(
	t *testing.T, unit identity.Object, starter jobtokentest.Starter, op jobtokentest.Operation,
) job.Job {
	t.Helper()

	var phase job.Phase

	phase.Operation = string(op.ID)

	var j job.Job

	j.ID = job.ID(ident.NewID(job.IDPrefix))
	j.Spec.Unit = unit
	j.State = job.StateRunning
	j.Phases = []job.Phase{phase}
	j.Current = 0
	j.StartedBy = starter.Principal.Subject

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.units[unit.UnitID()]; !ok {
		t.Fatalf("unit %s does not exist", unit.UnitID())
	}

	m.jobs[j.ID] = &j
	m.claims[op.ID] = &memClaim{state: op.State, token: op.Token, leaseEnd: op.LeaseEnd}

	return j
}

func (m *memWorld) takeover(t *testing.T, j job.ID) time.Time {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	claim := m.claimOf(t, j)
	claim.token++
	claim.leaseEnd = claim.leaseEnd.Add(memLease)

	return claim.leaseEnd
}

// rename changes the name of unit to newName. The stable unit ID stays.
func (m *memWorld) rename(t *testing.T, unit identity.Object, newName string) identity.Object {
	t.Helper()

	ref, err := identity.UnitObject(unit.Tool(), unit.Namespace(), newName)
	if err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.units[unit.UnitID()]; !ok {
		t.Fatalf("unit %s does not exist", unit.UnitID())
	}

	renamed := ref.WithUnitID(unit.UnitID())
	m.units[unit.UnitID()] = renamed

	return renamed
}

// setJobState sets the state of the job with the ID id.
func (m *memWorld) setJobState(t *testing.T, id job.ID, state job.State) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.jobs[id]
	if !ok {
		t.Fatalf("job %s does not exist", id)
	}

	stored.State = state
}

func (m *memWorld) endClaim(t *testing.T, j job.ID) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.claimOf(t, j).state = durable.StateSucceeded
}

func (m *memWorld) releaseClaim(t *testing.T, j job.ID) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	claim := m.claimOf(t, j)
	claim.state = durable.StatePending
	claim.token = 0
	claim.leaseEnd = time.Time{}
}

func (m *memWorld) endJob(t *testing.T, j job.ID) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.jobs[j]
	if !ok {
		t.Fatalf("job %s does not exist", j)
	}

	stored.State = job.StateSucceeded
}

func (m *memWorld) moveJob(t *testing.T, j job.ID, op durable.ID) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.jobs[j]
	if !ok {
		t.Fatalf("job %s does not exist", j)
	}

	var phase job.Phase

	phase.Operation = string(op)
	stored.Phases = append(stored.Phases, phase)
	stored.Current = len(stored.Phases) - 1
}

func (m *memWorld) writeRaw(t *testing.T, tok jobtoken.Token) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tokens[tok.Digest] = memToken{token: tok, namespaceID: m.nsByName[tok.Namespace]}
}

func (m *memWorld) entries(t *testing.T, ns namespace.Name) []audit.Entry {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.audit[ns]...)
}

// claimOf returns the claim of the current operation of the job with the ID
// id. The caller holds the lock.
func (m *memWorld) claimOf(t *testing.T, id job.ID) *memClaim {
	t.Helper()

	j, ok := m.jobs[id]
	if !ok {
		t.Fatalf("job %s does not exist", id)
	}

	claim, ok := m.claims[currentOperation(j)]
	if !ok {
		t.Fatalf("claim of job %s does not exist", id)
	}

	return claim
}

// setLease sets the end of the lease of the operation id. It is a hook of the
// restored-data cases.
func (m *memWorld) setLease(t *testing.T, id durable.ID, at time.Time) {
	t.Helper()

	m.mu.Lock()
	defer m.mu.Unlock()

	claim, ok := m.claims[id]
	if !ok {
		t.Fatalf("operation %s does not exist", id)
	}

	claim.leaseEnd = at
}

// TestSuiteOverAMemoryStore runs the conformance suite on the reference
// adapter in memory.
func TestSuiteOverAMemoryStore(t *testing.T) {
	t.Parallel()

	jobtokentest.Run(t, func(t *testing.T) jobtokentest.Harness {
		t.Helper()

		return newMemWorld().harness()
	})
}

// TestCaseNamesAreUnique proves that the suite names each case once.
func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, name := range jobtokentest.CaseNames() {
		if seen[name] {
			t.Fatalf("case %q is in the suite twice", name)
		}

		seen[name] = true
	}

	if len(seen) == 0 {
		t.Fatal("the suite has no case")
	}
}

// TestReadRefusesAJobStarterSubjectWithoutAPrincipal proves that the store
// resolves the subject of the job starter and refuses a subject with no
// principal.
func TestReadRefusesAJobStarterSubjectWithoutAPrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	tok, _ := liveRaw(t, f, f.alice, memExpiry())
	f.h.WriteRaw(t, tok)

	f.world.mu.Lock()
	j, ok := f.world.jobs[f.job.ID]
	if ok && j != nil {
		j.StartedBy = "nobody"
	}
	f.world.mu.Unlock()

	if !ok || j == nil {
		t.Fatal("the fixture has no job")
	}

	if _, err := f.h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest with a starter subject that has no principal = %v, want ErrNotFound", err)
	}
}

// fixture is one world for an acceptance test of the service.
type fixture struct {
	world *memWorld
	h     jobtokentest.Harness
	svc   *jobtoken.Service
	fence namespace.Fence
	unitA identity.Object
	unitB identity.Object
	alice jobtokentest.Starter
	bob   jobtokentest.Starter
	op    jobtokentest.Operation
	job   job.Job
}

// refusalCase is one refusal of Authenticate: its name and the setup that
// writes its world.
type refusalCase struct {
	name  string
	setup func(t *testing.T, f *fixture) (string, jobtoken.Token)
}

func memNow() time.Time {
	return time.UnixMilli(memIssueMs).UTC()
}

func memExpiry() time.Time {
	return memNow().Add(memStepTimeout + memGrace)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	world := newMemWorld()
	h := world.harness()

	var f fixture

	f.world = world
	f.h = h
	f.svc = jobtoken.NewService(world, memNow)

	f.fence = h.Namespace(t, "acme")
	f.unitA = h.Unit(t, "acme", "pulumi", "web/prod")
	f.unitB = h.Unit(t, "acme", "pulumi", "web/staging")
	f.alice = h.Principal(t, "alice")
	f.bob = h.Principal(t, "bob")
	f.op = jobtokentest.Operation{
		ID: durable.ID("op-a"), State: durable.StateRunning, Token: 1, LeaseEnd: memNow().Add(memLease),
	}
	f.job = h.Job(t, f.unitA, f.alice, f.op)

	return &f
}

// memEntry returns a valid audit entry of an issue in ns.
func memEntry(ns namespace.Name) audit.Entry {
	var e audit.Entry

	e.At = memNow()
	e.Namespace = ns
	e.Subject = "alice"
	e.Action = "job:issue"
	e.Object = ns.String()
	e.Channel = audit.ChannelAPI
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

// rawSpec describes the raw token that rawToken builds.
type rawSpec struct {
	unit      identity.Object
	starter   jobtokentest.Starter
	jobID     job.ID
	operation jobtokentest.Operation
	expiresAt time.Time
}

// rawToken returns a token and its secret for the specification.
func rawToken(t *testing.T, spec *rawSpec) (jobtoken.Token, string) {
	t.Helper()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	var tok jobtoken.Token

	tok.Digest = jobtoken.DigestOf(secret)
	tok.StarterID = spec.starter.ID
	tok.JobID = spec.jobID
	tok.Namespace = spec.unit.Namespace()
	tok.Unit = spec.unit
	tok.ClaimOperation = spec.operation.ID
	tok.ClaimToken = spec.operation.Token
	tok.ExpiresAt = spec.expiresAt
	tok.CreatedAt = memNow()

	return tok, secret.Reveal()
}

// liveRaw returns a raw token of unitA with the live expiry.
func liveRaw(t *testing.T, f *fixture, starter jobtokentest.Starter, expiresAt time.Time) (jobtoken.Token, string) {
	t.Helper()

	spec := rawSpec{unit: f.unitA, starter: starter, jobID: f.job.ID, operation: f.op, expiresAt: expiresAt}

	return rawToken(t, &spec)
}

// issueRequest returns the request of a live issue of the fixture.
func issueRequest(
	t *testing.T,
	f *fixture,
	starter jobtokentest.Starter,
	op jobtokentest.Operation,
) *jobtoken.IssueRequest {
	t.Helper()

	var claim durable.Claim

	claim.Operation = op.ID
	claim.Token = op.Token
	claim.ExpiresAt = op.LeaseEnd

	var req jobtoken.IssueRequest

	req.StarterID = starter.ID
	req.StarterSubject = starter.Principal.Subject
	req.Job = f.job
	req.Job.Spec.Steps = []job.Step{{
		Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"},
		Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: true, Outputs: nil,
	}}
	req.Claim = claim
	req.ExpiresAt = memExpiry()
	req.Fence = f.fence
	req.Entry = memEntry(f.fence.Namespace)

	return &req
}

// wantUnauthenticated proves that svc refuses secret and that the error holds
// none of the values of the record.
func wantUnauthenticated(t *testing.T, svc *jobtoken.Service, secret string, tok jobtoken.Token) {
	t.Helper()

	p, unit, err := svc.Authenticate(t.Context(), secret)

	var zero identity.Principal

	if p != zero {
		t.Fatal("Authenticate returned a principal with an error")
	}

	var zeroObject identity.Object

	if unit != zeroObject {
		t.Fatal("Authenticate returned an object with an error")
	}

	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Authenticate = %v, want ErrUnauthenticated", err)
	}

	text := err.Error()
	for _, value := range []string{secret, tok.Digest.String(), string(tok.JobID), tok.Unit.UnitName(), tok.Namespace.String()} {
		if value != "" && strings.Contains(text, value) {
			t.Fatalf("error %q holds %q", text, value)
		}
	}
}

func runRefusals(t *testing.T, cases []refusalCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			secret, tok := tc.setup(t, f)
			wantUnauthenticated(t, f.svc, secret, tok)
		})
	}
}

// TestAuthenticateRefusesEscalation proves that a token cannot reach another
// unit, another operation or another principal.
func TestAuthenticateRefusesEscalation(t *testing.T) {
	t.Parallel()

	runRefusals(t, escalationCases())

	t.Run("issue refuses a starter that is not the job starter", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		tok, _ := liveRaw(t, f, f.bob, memExpiry())

		err := f.h.Store.Issue(t.Context(), tok, f.fence, memEntry(f.fence.Namespace))
		if !errors.Is(err, jobtoken.ErrInvalid) {
			t.Fatalf("Issue with another starter = %v, want ErrInvalid", err)
		}

		if _, err := f.h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
			t.Fatalf("GetByDigest after the refused issue = %v, want ErrNotFound", err)
		}
	})
}

func escalationCases() []refusalCase {
	return []refusalCase{
		{name: "a raw record for another unit", setup: setupUnitMismatch},
		{name: "a token for a job that moved to another operation", setup: setupMovedJob},
		{name: "a raw record whose starter is another principal", setup: setupStarterMismatch},
	}
}

func setupUnitMismatch(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := rawToken(
		t,
		&rawSpec{unit: f.unitB, starter: f.alice, jobID: f.job.ID, operation: f.op, expiresAt: memExpiry()},
	)
	f.h.WriteRaw(t, tok)

	return secret, tok
}

func setupMovedJob(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	secret, err := f.svc.Issue(t.Context(), issueRequest(t, f, f.alice, f.op))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	f.h.MoveJob(t, f.job.ID, "op-b")

	m, err := f.h.Store.GetByDigest(t.Context(), jobtoken.DigestOf(secret))
	if err != nil {
		t.Fatalf("GetByDigest: %v", err)
	}

	return secret.Reveal(), m.Token
}

func setupStarterMismatch(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.bob, memExpiry())
	f.h.WriteRaw(t, tok)

	return secret, tok
}

// TestAuthenticateRefusesRestoredData proves that a restored row that the
// service would refuse is refused at each request.
func TestAuthenticateRefusesRestoredData(t *testing.T) {
	t.Parallel()

	runRefusals(t, restoredCases())
}

func restoredCases() []refusalCase {
	return []refusalCase{
		{name: "a raw record with a passed hard expiry", setup: setupExpired},
		{name: "a raw record with the claim token of another claim", setup: setupClaimToken},
		{name: "a raw record with an ended claim lease", setup: setupLeaseEnded},
		{name: "a raw record with a released claim", setup: setupReleased},
		{name: "a raw record with a disabled starter", setup: setupDisabledStarter},
		{name: "a raw record with a deleted starter", setup: setupDeletedStarter},
		{name: "a raw record on an ended job", setup: setupEndedJob},
	}
}

func setupExpired(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memNow())
	tok.CreatedAt = memNow().Add(-time.Minute)
	f.h.WriteRaw(t, tok)

	return secret, tok
}

func setupClaimToken(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	tok.ClaimToken = f.op.Token + 1
	f.h.WriteRaw(t, tok)

	return secret, tok
}

func setupLeaseEnded(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	f.world.setLease(t, f.op.ID, memNow())
	f.h.WriteRaw(t, tok)

	return secret, tok
}

func setupReleased(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	f.h.WriteRaw(t, tok)
	f.h.ReleaseClaim(t, f.job.ID)

	return secret, tok
}

func setupDisabledStarter(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	f.h.WriteRaw(t, tok)
	f.h.Disable(t, f.alice)

	return secret, tok
}

func setupDeletedStarter(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	f.h.WriteRaw(t, tok)
	f.h.Delete(t, f.alice)

	return secret, tok
}

func setupEndedJob(t *testing.T, f *fixture) (string, jobtoken.Token) {
	t.Helper()

	tok, secret := liveRaw(t, f, f.alice, memExpiry())
	f.h.WriteRaw(t, tok)
	f.h.EndJob(t, f.job.ID)

	return secret, tok
}

// TestAuthenticateRefusalText proves that a refusal names none of the values
// of the record and wraps identity.ErrUnauthenticated.
func TestAuthenticateRefusalText(t *testing.T) {
	t.Parallel()

	all := append(escalationCases(), restoredCases()...)

	runRefusals(t, all)
}
