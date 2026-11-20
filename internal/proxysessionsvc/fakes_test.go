package proxysessionsvc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/internal/store/sqlite"
)

const (
	nsAcme     = namespace.Name("acme")
	nsOther    = namespace.Name("other")
	poolA      = "rp-a"
	poolB      = "rp-b"
	unitWeb    = "u-web"
	unitOther  = "u-other"
	runnerA    = "rnr-a"
	opWeb      = "op-web"
	jobWeb     = "job-web"
	claimToken = int64(7)

	// upstreamURL and the credential are the values that no error and no
	// audit entry may hold.
	upstreamURL      = "https://proxy.example.com:3128"
	upstreamUser     = "proxy-user"
	upstreamPassword = "proxy-password-0123456789"
)

var (
	// errAuditAppend is the error of an audit store that loses an entry.
	errAuditAppend = errors.New("audit append failed")
	// errNotUsed is the error of a fake method that the service never calls.
	errNotUsed = errors.New("not used")
)

// memSettings is an in-memory runner.PoolProxyStore that keeps the audit
// entries of its saves.
type memSettings struct {
	mu      sync.Mutex
	rows    map[string]runner.ProxySettingsRecord
	entries []audit.Entry
}

func newMemSettings() *memSettings {
	return &memSettings{mu: sync.Mutex{}, rows: map[string]runner.ProxySettingsRecord{}, entries: nil}
}

func (m *memSettings) ProxySettings(_ context.Context, poolID string) (runner.ProxySettingsRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.rows[poolID]
	if !ok {
		return runner.ProxySettingsRecord{}, runner.ErrNotFound
	}

	rec.Credential = bytes.Clone(rec.Credential)

	return rec, nil
}

func (m *memSettings) SetProxySettings(
	_ context.Context, _ namespace.Fence, poolID string, rec runner.ProxySettingsRecord, e audit.Entry,
) (runner.ProxySettingsRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if poolID != poolA && poolID != poolB {
		return runner.ProxySettingsRecord{}, runner.ErrNotFound
	}

	if m.rows[poolID].Version != rec.Version {
		return runner.ProxySettingsRecord{}, runner.ErrConflict
	}

	rec.Version++
	rec.Credential = bytes.Clone(rec.Credential)
	m.rows[poolID] = rec
	m.entries = append(m.entries, e)

	return rec, nil
}

// put stores rec for poolID as it is, as a restore of another row does.
func (m *memSettings) put(poolID string, rec runner.ProxySettingsRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rows[poolID] = rec
}

func (m *memSettings) auditEntries() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.entries...)
}

// memRules is an in-memory rulestore.RuleStore.
type memRules struct {
	mu   sync.Mutex
	sets map[rulestore.Scope][]byte
}

func newMemRules() *memRules {
	return &memRules{mu: sync.Mutex{}, sets: map[rulestore.Scope][]byte{}}
}

func (m *memRules) Get(_ context.Context, scope rulestore.Scope) (rulestore.Stored, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	doc, ok := m.sets[scope]
	if !ok {
		return rulestore.Stored{}, rulestore.ErrNotFound
	}

	return rulestore.Stored{
		Scope: scope, Document: bytes.Clone(doc), Digest: rulestore.DocumentDigest(doc), Version: 1, UpdatedAt: 0,
	}, nil
}

func (m *memRules) Put(
	context.Context, rulestore.Change, namespace.Fence, audit.Entry,
) (rulestore.Stored, error) {
	return rulestore.Stored{}, errNotUsed
}

func (m *memRules) Delete(context.Context, rulestore.Scope, int64, namespace.Fence, audit.Entry) error {
	return errNotUsed
}

func (m *memRules) set(scope rulestore.Scope, doc string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sets[scope] = []byte(doc)
}

// fakeOps holds the durable operations by ID.
type fakeOps struct {
	mu  sync.Mutex
	ops map[durable.ID]durable.Operation
}

func (f *fakeOps) Get(_ context.Context, id durable.ID) (durable.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	op, ok := f.ops[id]
	if !ok {
		return durable.Operation{}, durable.ErrNotFound
	}

	return op, nil
}

func (f *fakeOps) set(op durable.Operation) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ops[op.ID] = op
}

// fakeClaims answers the record of each claim with the job of its operation.
type fakeClaims struct {
	jobs map[durable.ID]string
}

func (f fakeClaims) LookupClaim(_ context.Context, c durable.Claim) (runner.ClaimRecord, error) {
	jobID, ok := f.jobs[c.Operation]
	if !ok {
		return runner.ClaimRecord{}, runner.ErrNotFound
	}

	return runner.ClaimRecord{Job: jobID, Phase: 0, ClaimedAt: time.Time{}, HasSession: true}, nil
}

// fakeJobs holds the jobs by ID.
type fakeJobs struct {
	jobs map[job.ID]job.Job
}

func (f fakeJobs) Get(_ context.Context, id job.ID) (job.Job, error) {
	j, ok := f.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

// fakeUnits knows u-web of acme and u-other of other.
type fakeUnits struct{}

func (fakeUnits) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	var e inventory.Entry

	switch unitID {
	case unitWeb:
		e.Namespace, e.Tool, e.Name, e.UnitID = nsAcme, "pulumi", "web-prod", unitWeb
		e.Labels = map[string]string{"team": "core"}
	case unitOther:
		e.Namespace, e.Tool, e.Name, e.UnitID = nsOther, "pulumi", "api-prod", unitOther
		e.Labels = map[string]string{}
	default:
		return e, inventory.ErrUnitNotFound
	}

	return e, nil
}

// memAudit keeps the appended entries.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	fail    bool
}

func (m *memAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fail {
		return audit.Entry{}, errAuditAppend
	}

	m.entries = append(m.entries, e)

	return e, nil
}

func (m *memAudit) all() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]audit.Entry(nil), m.entries...)
}

func (m *memAudit) setFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fail = fail
}

// fixture is a service over fakes and the real secret service.
type fixture struct {
	svc      *proxysessionsvc.Service
	settings *memSettings
	rules    *memRules
	ops      *fakeOps
	audit    *memAudit
	secrets  *secret.Service
}

// newFixture returns a fixture with the live claim of the job of u-web, held
// by runnerA.
func newFixture(t *testing.T) fixture {
	t.Helper()

	return newFixtureWith(t, jobWeb, job.OperationPlan)
}

// newFixtureWith is newFixture with the job of the claim and the operation of
// the job given. An empty claimJob is the claim of a sweep, which has no job.
func newFixtureWith(t *testing.T, claimJob string, operation job.Operation) fixture {
	t.Helper()

	namespaces, secrets := openSecrets(t)

	webUnit, err := identity.UnitObject("pulumi", nsAcme, "web-prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	var j job.Job

	j.ID = jobWeb
	j.Spec.Unit = webUnit.WithUnitID(unitWeb)
	j.Spec.Operation = operation
	j.Phases = make([]job.Phase, 1)
	j.Phases[0].Kind = job.PhasePlan

	f := fixture{
		svc: nil, settings: newMemSettings(), rules: newMemRules(),
		ops:   &fakeOps{mu: sync.Mutex{}, ops: map[durable.ID]durable.Operation{}},
		audit: &memAudit{mu: sync.Mutex{}, entries: nil, fail: false}, secrets: secrets,
	}

	f.ops.set(liveOp())

	f.svc, err = proxysessionsvc.New(proxysessionsvc.Config{
		Settings: f.settings, Rules: f.rules, Secrets: secrets, Namespaces: namespaces, Operations: f.ops,
		Claims: fakeClaims{jobs: map[durable.ID]string{opWeb: claimJob}},
		Jobs:   fakeJobs{jobs: map[job.ID]job.Job{jobWeb: j}}, Units: fakeUnits{}, Audit: f.audit,
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return time.UnixMilli(1_790_000_000_000) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return f
}

// liveOp returns the running operation of the job of u-web, claimed by
// runnerA.
func liveOp() durable.Operation {
	var op durable.Operation

	op.ID = opWeb
	op.State = durable.StateRunning
	op.Claim = durable.Claim{Operation: opWeb, Owner: runnerA, Token: claimToken, ExpiresAt: time.Time{}}

	return op
}

// openSecrets returns the namespace service with acme and other, and the
// secret service over a SQLite store.
func openSecrets(t *testing.T) (*namespace.Service, *secret.Service) {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, name := range []namespace.Name{nsAcme, nsOther} {
		if _, err = namespaces.Create(t.Context(), name); err != nil {
			t.Fatalf("create namespace %s: %v", name, err)
		}
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	return namespaces, secrets
}

// pool returns the pool id of the namespace ns.
func pool(id string, ns namespace.Name) runner.Pool {
	var p runner.Pool

	p.ID = id
	p.Namespace = ns
	p.Purpose = runner.PurposeJobs

	return p
}

// session returns a session of kind for the live claim of u-web.
func session(kind runner.SessionKind) runner.Session {
	var s runner.Session

	s.ID = "rss-1"
	s.Kind = kind
	s.Operation = opWeb
	s.Token = claimToken
	s.Runner = runnerA

	return s
}

// baseEntry returns the audit entry that the handler would hand to the
// service.
func baseEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "rss-1", IDPSubject: "", TokenID: "",
		Action: "", Object: "acme", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// sessionRequest returns the request of a session of kind on the pool p.
func sessionRequest(kind runner.SessionKind, p runner.Pool) *proxysessionsvc.SessionRequest {
	return &proxysessionsvc.SessionRequest{Session: session(kind), Pool: p, Entry: baseEntry()}
}

// assignmentRequest returns the assignment request of a session of kind on
// the pool p.
func assignmentRequest(kind runner.SessionKind, p runner.Pool) *proxysessionsvc.AssignmentRequest {
	return &proxysessionsvc.AssignmentRequest{SessionRequest: *sessionRequest(kind, p), ServerHost: "dustpan.example"}
}

// putRequest returns a save of the settings of p with one range, the upstream
// URL and the credential.
func putRequest(p runner.Pool) *proxysessionsvc.PutRequest {
	return &proxysessionsvc.PutRequest{
		Pool: p, AllowedRanges: []string{"10.20.0.0/16"}, UpstreamURL: upstreamURL,
		Credential: proxysessionsvc.CredentialChange{
			Present: true, Value: &proxysessionsvc.Credential{Username: upstreamUser, Password: upstreamPassword},
		},
		IfMatch: "", Entry: baseEntry(),
	}
}

// mustPut saves the settings of req and fails the test on an error.
func mustPut(t *testing.T, f fixture, req *proxysessionsvc.PutRequest) proxysessionsvc.Settings {
	t.Helper()

	got, err := f.svc.PutSettings(t.Context(), req)
	if err != nil {
		t.Fatalf("PutSettings: %v", err)
	}

	return got
}

// rulesDoc returns a document with one inspect rule of id, host and path.
func rulesDoc(id, host, path string) string {
	return `{"rules":[{"id":"` + id + `","operations":["plan"],"host":"` + host +
		`","mode":"inspect","methods":["GET"],"paths":["` + path + `"]}]}`
}
