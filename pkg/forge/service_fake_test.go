package forge_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errBoom = errors.New("store failed")

// noAccount returns the zero Account of an event.
func noAccount() forge.Account {
	return forge.Account{ID: "", Login: "", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil}
}

// memStore is a forge.Store in memory. It checks versions as a SQL store does,
// and it records every audit entry that a write appends.
type memStore struct {
	mu          sync.Mutex
	conns       map[string]forge.ConnectionRecord
	reg         *forge.Registration
	assignments map[string]forge.Assignment
	audits      []audit.Entry
	// failAssignment makes GetAssignment fail with errBoom.
	failAssignment bool
	// getRegErr, when set, makes GetRegistration fail with it. getRegs counts
	// the calls of GetRegistration.
	getRegErr error
	getRegs   int
	// afterGetReg, when set, runs after each GetRegistration, outside the
	// lock. A test uses it to write between the read and the put of a caller.
	afterGetReg func()
	// putIfVersions records the ifVersion of each PutRegistration.
	putIfVersions []int64
	// receipts holds the stored receipts in order. receiptsFull makes
	// RecordEventReceipt return ErrReceiptsFull, and receiptErr makes it fail.
	// receiptCalls counts the calls.
	receipts     []forge.EventReceipt
	receiptsFull bool
	receiptErr   error
	receiptCalls int
}

func newMemStore() *memStore {
	return &memStore{
		mu: sync.Mutex{}, conns: map[string]forge.ConnectionRecord{}, reg: nil,
		assignments: map[string]forge.Assignment{}, audits: nil, failAssignment: false,
		getRegErr: nil, getRegs: 0, afterGetReg: nil, putIfVersions: nil,
		receipts: nil, receiptsFull: false, receiptErr: nil, receiptCalls: 0,
	}
}

func (m *memStore) CreateConnection(
	_ context.Context, c forge.ConnectionRecord, _ namespace.Fence, e audit.Entry,
) (forge.ConnectionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c.Default = true
	m.conns[c.ID] = c
	m.audits = append(m.audits, e)

	return c, nil
}

func (m *memStore) GetConnection(_ context.Context, ns namespace.Name, id string) (forge.ConnectionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.conns[id]
	if !ok || c.Namespace != ns {
		return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
	}

	return c, nil
}

func (m *memStore) ListConnections(_ context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []forge.ConnectionRecord

	for id := range m.conns {
		if m.conns[id].Namespace == ns {
			out = append(out, m.conns[id])
		}
	}

	return out, nil
}

//nolint:revive // argument-limit: the signature is the one of forge.Store.
func (m *memStore) DeleteConnection(
	_ context.Context, ns namespace.Name, id string, _ int64, _ namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.conns[id]; !ok || c.Namespace != ns {
		return forge.ErrConnectionNotFound
	}

	delete(m.conns, id)
	m.audits = append(m.audits, e)

	return nil
}

//nolint:revive // argument-limit: the signature is the one of forge.Store.
func (m *memStore) MakeDefault(
	_ context.Context, ns namespace.Name, id string, _ int64, _ namespace.Fence, e audit.Entry,
) (forge.ConnectionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.conns[id]
	if !ok || c.Namespace != ns {
		return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
	}

	m.audits = append(m.audits, e)

	return c, nil
}

func (m *memStore) GetRegistration(_ context.Context, kind forge.Kind) (forge.Registration, error) {
	reg, hook, err := m.readRegistration(kind)
	if hook != nil {
		hook()
	}

	return reg, err
}

func (m *memStore) PutRegistration(
	_ context.Context, r forge.Registration, ifVersion int64, e audit.Entry,
) (forge.Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.putIfVersions = append(m.putIfVersions, ifVersion)

	switch {
	case m.reg == nil && ifVersion > 0:
		return forge.Registration{}, forge.ErrNoRegistration
	case m.reg != nil && ifVersion > 0 && m.reg.Version != ifVersion:
		return forge.Registration{}, forge.ErrVersionConflict
	case m.reg != nil:
		r.HookID, r.Version = m.reg.HookID, m.reg.Version+1
	default:
		// A new registration keeps the ID and the version that it came with.
	}

	m.reg = &r
	m.audits = append(m.audits, e)

	return r, nil
}

func (m *memStore) DeleteRegistration(_ context.Context, _ forge.Kind, _ int64, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.reg == nil {
		return forge.ErrNoRegistration
	}

	if len(m.assignments) > 0 {
		return forge.ErrRegistrationInUse
	}

	m.reg = nil
	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) GetAssignment(_ context.Context, id string) (forge.Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failAssignment {
		return forge.Assignment{}, errBoom
	}

	a, ok := m.assignments[id]
	if !ok {
		return forge.Assignment{}, forge.ErrAssignmentNotFound
	}

	return a, nil
}

func (m *memStore) ListAssignments(context.Context) ([]forge.Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]forge.Assignment, 0, len(m.assignments))
	for _, a := range m.assignments {
		out = append(out, a)
	}

	// Digit strings without a leading zero order as numbers by length, then by
	// text.
	slices.SortFunc(out, func(a, b forge.Assignment) int {
		return cmp.Or(
			cmp.Compare(len(a.InstallationID), len(b.InstallationID)),
			strings.Compare(a.InstallationID, b.InstallationID),
		)
	})

	return out, nil
}

func (m *memStore) PutAssignment(
	_ context.Context, a forge.Assignment, _ int64, e audit.Entry,
) (forge.Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.assignments[a.InstallationID]; ok {
		a.Version = old.Version + 1
	}

	m.assignments[a.InstallationID] = a
	m.audits = append(m.audits, e)

	return a, nil
}

func (m *memStore) DeleteAssignment(_ context.Context, id string, _ int64, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.assignments[id]; !ok {
		return forge.ErrAssignmentNotFound
	}

	delete(m.assignments, id)
	m.audits = append(m.audits, e)

	return nil
}

// memNamespaces is a forge.Namespaces with one namespace per name.
type memNamespaces map[namespace.Name]namespace.Namespace

func newNamespaces(names ...namespace.Name) memNamespaces {
	out := memNamespaces{}

	for _, n := range names {
		out[n] = namespace.Namespace{
			ID:                  namespace.NewID(),
			CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
			Name:                n,
			PrimaryInstallation: "eu-west",
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		}
	}

	return out
}

func (m memNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	n, ok := m[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return n, nil
}

// WritableHere is GetNamespace for a namespace whose primary installation is
// this one, "eu-west", and namespace.ErrNotHome for any other.
func (m memNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	n, err := m.GetNamespace(ctx, name)
	if err != nil {
		return namespace.Namespace{}, err
	}

	if n.PrimaryInstallation != "eu-west" {
		return namespace.Namespace{}, namespace.ErrNotHome
	}

	return n, nil
}

// memAudit is an audit.Store that keeps what it appends.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *memAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

func (a *memAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, More: false}, nil
}

func (a *memAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

func (a *memAudit) all() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.entries)
}

// memSealer is a forge.SecretSealer that reverses the bytes, so a sealed value
// differs from its plaintext, and counts its calls.
type memSealer struct {
	mu    sync.Mutex
	seals int
	opens int
}

func reverse(b []byte) []byte {
	out := slices.Clone(b)
	slices.Reverse(out)

	return out
}

// SealBound keeps the binding in the sealed value, as the AEAD does with its
// associated data: OpenBound opens the value only with the same binding.
func (m *memSealer) SealBound(_ context.Context, _ string, binding, plaintext []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seals++

	return append([]byte("sealed:"+string(binding)+":"), reverse(plaintext)...), nil
}

func (m *memSealer) OpenBound(_ context.Context, _ string, binding, ciphertext []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.opens++

	rest, ok := cutPrefix(ciphertext, "sealed:"+string(binding)+":")
	if !ok {
		return nil, errBoom
	}

	return reverse(rest), nil
}

func cutPrefix(b []byte, prefix string) ([]byte, bool) {
	if len(b) < len(prefix) || string(b[:len(prefix)]) != prefix {
		return nil, false
	}

	return b[len(prefix):], true
}

// recorder is a forge.Opener that records what it was asked to open and
// returns a fake Forge.
type recorder struct {
	mu       sync.Mutex
	opened   []openCall
	account  forge.Account
	caps     forge.Capabilities
	checkErr error
	openErr  error
	// events is the verifier that ParseEvent runs. A test sets it to nil to
	// show that the service fails closed.
	events forge.Events
}

type openCall struct {
	Conn forge.Connection
	Cred forge.Credential
}

func (r *recorder) Kind() forge.Kind { return forge.KindGitHub }

// errNoVerifier is the error of a recorder with no verifier.
var errNoVerifier = errors.New("the recorder has no verifier")

// ParseEvent runs the verifier of r. A recorder with no verifier returns an
// error that the port does not name, so the service fails closed.
func (r *recorder) ParseEvent(ctx context.Context, req forge.EventRequest, secrets [][]byte) (forge.Event, error) {
	if r.events == nil {
		return forge.Event{}, errNoVerifier
	}

	return r.events.ParseEvent(ctx, req, secrets)
}

//nolint:ireturn // The port is the interface: the adapter type stays hidden.
func (r *recorder) Open(_ context.Context, c forge.Connection, cred forge.Credential) (forge.Forge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.openErr != nil {
		return nil, r.openErr
	}

	r.opened = append(r.opened, openCall{Conn: c, Cred: cred})

	return fakeForge{Forge: nil, rec: r}, nil
}

func (r *recorder) calls() []openCall {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.opened)
}

// fakeForge is the Forge that recorder returns. Check answers what the recorder
// holds. Every other method fails the test if it runs.
type fakeForge struct {
	forge.Forge

	rec *recorder
}

func (f fakeForge) Capabilities() forge.Capabilities { return f.rec.caps }

func (f fakeForge) Check(context.Context) (forge.Account, error) {
	return f.rec.account, f.rec.checkErr
}

// testCheckURL refuses an address that is not the one that the tests use.
func testCheckURL(raw string) error {
	if raw != "https://api.github.com" && raw != "https://ghe.example.com/api/v3" {
		return errBoom
	}

	return nil
}

// callerEntry is the template that the HTTP adapter gives the service.
func callerEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: audit.PrincipalKindPerson,
		JobID: "", Reason: "",
		Origin: nil,
	}
}

type rig struct {
	svc     *forge.Service
	store   *memStore
	audit   *memAudit
	sealer  *memSealer
	opener  *recorder
	ns      memNamespaces
	refused []string
	mu      sync.Mutex
	// clock is the time of the service, in milliseconds. Advance moves it.
	clock atomic.Int64
}

// advance moves the clock of the service by d.
func (r *rig) advance(d time.Duration) {
	r.clock.Add(d.Milliseconds())
}

func newRig(t *testing.T) *rig {
	t.Helper()

	r := &rig{
		clock: atomic.Int64{},
		svc:   nil, store: newMemStore(), audit: &memAudit{mu: sync.Mutex{}, entries: nil},
		sealer: &memSealer{mu: sync.Mutex{}, seals: 0, opens: 0},
		opener: &recorder{
			mu:       sync.Mutex{},
			opened:   nil,
			account:  forge.Account{ID: "99", Login: "acme-org", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil},
			caps:     nil,
			checkErr: nil,
			openErr:  nil,
			events:   newFakeEvents(),
		},
		refused: nil, mu: sync.Mutex{}, ns: newNamespaces("acme", "beta"),
	}

	r.clock.Store(1_700_000_000_000)

	svc, err := forge.NewService(forge.Config{
		Store: r.store, Namespaces: r.ns, Secrets: r.sealer, Audit: r.audit,
		Openers: []forge.Opener{r.opener}, CheckURL: testCheckURL, OnAuditFailure: nil, CheckLimits: nil,
		OnRefused: func(reason string) {
			r.mu.Lock()
			defer r.mu.Unlock()

			r.refused = append(r.refused, reason)
		},
		Logger: nil,
		Now:    func() time.Time { return time.UnixMilli(r.clock.Load()) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	r.svc = svc

	return r
}

func (r *rig) refusals() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.refused)
}

// mustName returns the namespace name s for a test.
func mustName(s string) namespace.Name {
	return namespace.Name(s)
}

// register registers the app with the canary key and event secret.
func (r *rig) register(t *testing.T) forge.RegistrationView {
	t.Helper()

	view, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, registrationInput(), 0)
	if err != nil {
		t.Fatalf("put registration: %v", err)
	}

	return view
}

// assign gives the installation to the namespace.
func (r *rig) assign(t *testing.T, id string, ns namespace.Name) {
	t.Helper()

	if _, err := r.svc.AssignInstallation(t.Context(), callerEntry(), id, ns, 0); err != nil {
		t.Fatalf("assign %s to %s: %v", id, ns, err)
	}
}

// connect makes a connection of ns to the installation id.
func (r *rig) connect(t *testing.T, ns namespace.Name, id string) forge.ConnectionView {
	t.Helper()

	conn, err := r.svc.CreateConnection(t.Context(), callerEntry(), ns, connectionInput(id))
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	return conn
}

// ready registers the app, assigns installation 101 to acme and makes one
// connection. It returns the connection.
func ready(t *testing.T, r *rig) forge.ConnectionView {
	t.Helper()

	r.register(t)
	r.assign(t, "101", "acme")

	return r.connect(t, "acme", "101")
}

const (
	keyCanary    = "KEYCANARY-private-key-bytes"
	secretCanary = "EVENTCANARY-0123456789abcdef0123456789"
	apiURL       = "https://api.github.com"
	appID        = "12345"
)

func registrationInput() forge.RegistrationInput {
	return forge.RegistrationInput{
		AppID: appID, BaseURL: apiURL, PrivateKey: []byte(keyCanary), EventSecret: []byte(secretCanary),
	}
}

func connectionInput(installationID string) forge.ConnectionInput {
	return forge.ConnectionInput{
		Kind: forge.KindGitHub, Auth: forge.AuthApp, InstallationID: installationID, Push: false,
	}
}

// registration reads the stored registration.
func (r *rig) registration(t *testing.T) forge.Registration {
	t.Helper()

	reg, err := r.store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get registration: %v", err)
	}

	return reg
}

// ConfirmRotation clears the old fields. It returns ErrNoRotation with no
// window and checks the version as a SQL store does.
func (m *memStore) ConfirmRotation(
	_ context.Context, kind forge.Kind, ifVersion int64, e audit.Entry,
) (forge.Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case m.reg == nil || m.reg.Kind != kind:
		return forge.Registration{}, forge.ErrNoRegistration
	case ifVersion > 0 && m.reg.Version != ifVersion:
		return forge.Registration{}, forge.ErrVersionConflict
	case m.reg.OldEventSecretEndsAt.IsZero() && m.reg.OldEventSecretSetID == "" && len(m.reg.OldSealedEventSecret) == 0:
		return forge.Registration{}, forge.ErrNoRotation
	}

	m.reg.OldEventSecretSetID, m.reg.OldSealedEventSecret, m.reg.OldEventSecretEndsAt = "", nil, time.Time{}
	m.reg.Version++
	m.audits = append(m.audits, e)

	return *m.reg, nil
}

// RecordEventReceipt keeps the receipt under both keys, the delivery ID and
// the body digest, as the SQL stores do. The bound of 100000 rows is the rule
// of the store: the fake returns ErrReceiptsFull from a flag.
func (m *memStore) RecordEventReceipt(_ context.Context, r forge.EventReceipt) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.receiptCalls++

	switch {
	case m.receiptErr != nil:
		return false, m.receiptErr
	case m.receiptsFull:
		return false, forge.ErrReceiptsFull
	}

	for i := range m.receipts {
		if m.receipts[i].DeliveryID == r.DeliveryID || m.receipts[i].BodyDigest == r.BodyDigest {
			return false, nil
		}
	}

	m.receipts = append(m.receipts, r)

	if r.Audit != nil {
		m.audits = append(m.audits, *r.Audit)
	}

	return true, nil
}

// fakeEvents is a forge.Events. A request matches a secret when its header
// "Sig" holds the secret. The event of a match comes from the headers
// "Delivery", "Kind" and "Install", or from result when it is set. Secrets
// shorter than MinEventSecretBytes never match, as in the real verifier.
type fakeEvents struct {
	mu    sync.Mutex
	calls [][][]byte
	// result, when set, replaces the event that the headers give; resultErr is
	// returned for a matching request.
	result    *forge.Event
	resultErr error
}

func newFakeEvents() *fakeEvents {
	return &fakeEvents{mu: sync.Mutex{}, calls: nil, result: nil, resultErr: nil}
}

func (f *fakeEvents) ParseEvent(_ context.Context, r forge.EventRequest, secrets [][]byte) (forge.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, slices.Clone(secrets))

	sig := ""
	if v := r.Header["Sig"]; len(v) == 1 {
		sig = v[0]
	}

	matched := false

	for _, s := range secrets {
		if len(s) >= forge.MinEventSecretBytes && string(s) == sig {
			matched = true
		}
	}

	if !matched {
		return forge.Event{}, forge.ErrSignature
	}

	if f.resultErr != nil {
		return forge.Event{}, f.resultErr
	}

	if f.result != nil {
		return *f.result, nil
	}

	first := func(key, def string) string {
		if v := r.Header[key]; len(v) == 1 {
			return v[0]
		}

		return def
	}

	ev := forge.Event{
		DeliveryID: first("Delivery", "delivery-1"), Kind: forge.EventKind(first("Kind", string(forge.EventReview))),
		Repo: "acme/app", ReviewID: "7", InstallationID: first("Install", "101"), Sender: noAccount(),
	}
	if ev.Kind == forge.EventPing {
		ev.Repo, ev.ReviewID, ev.InstallationID = "", "", ""
	}

	return ev, nil
}

// secretsOfCalls returns the secrets that each call got, as strings.
func (f *fakeEvents) secretsOfCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([][]string, 0, len(f.calls))

	for _, c := range f.calls {
		row := make([]string, 0, len(c))
		for _, s := range c {
			row = append(row, string(s))
		}

		out = append(out, row)
	}

	return out
}

func (m *memStore) readRegistration(kind forge.Kind) (forge.Registration, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.getRegs++

	switch {
	case m.getRegErr != nil:
		return forge.Registration{}, nil, m.getRegErr
	case m.reg == nil || m.reg.Kind != kind:
		return forge.Registration{}, nil, forge.ErrNoRegistration
	}

	return *m.reg, m.afterGetReg, nil
}

// setRegistration replaces the stored registration without the checks of the
// service, as a hostile writer of the database does.
func (m *memStore) setRegistration(r forge.Registration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reg = &r
}
