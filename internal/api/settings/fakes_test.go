package settings_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

const (
	testToken = "settings-test-token"
	ada       = "user:ada"
	// netID and appID are the unit IDs of the units net and app in acme.
	netID = "ws-1"
	appID = "ws-2"
	// otherID is the ID of a unit in the namespace other.
	otherID = "ws-9"

	connRW   = "gitconn-00000000000000000000000000000001"
	poolJobs = "rp-jobs"

	netObject = "opentofu:acme/net"
	appObject = "pulumi:acme/app"

	settingsPath    = "/api/dustpan/v1/namespaces/acme/units/" + netID + "/settings"
	suggestionsPath = settingsPath + "/suggestions"
	defaultsPath    = "/api/dustpan/v1/namespaces/acme/settings/unit-defaults"
	bulkPath        = "/api/dustpan/v1/namespaces/acme/unit-settings/bulk-put"
)

var errNoList = errors.New("no route lists objects")

// fakeNamespaces answers every namespace except "absent".
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: name, PrimaryInstallation: "test-region",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeInventory holds the units net and app in acme, and one unit in other.
type fakeInventory struct{}

func entries() []coreinventory.Entry {
	mk := func(ns namespace.Name, tool, name, id string) coreinventory.Entry {
		return coreinventory.Entry{
			LastRelease: nil,
			ManualLock:  nil, LastChanged: time.Unix(0, 0), Namespace: ns, Tool: tool, Name: name, UnitID: id,
			DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, Collection: "", Labels: nil,
		}
	}

	return []coreinventory.Entry{
		mk("acme", "opentofu", "net", netID), mk("acme", "pulumi", "app", appID), mk("other", "opentofu", "x", otherID),
	}
}

func (fakeInventory) ListUnits(_ context.Context, ns namespace.Name) ([]coreinventory.Entry, error) {
	var out []coreinventory.Entry

	all := entries()
	for i := range all {
		if all[i].Namespace == ns {
			out = append(out, all[i])
		}
	}

	return out, nil
}

func (f fakeInventory) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := f.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (fakeInventory) GetUnit(_ context.Context, unitID string) (coreinventory.Entry, error) {
	all := entries()
	for i := range all {
		if all[i].UnitID == unitID {
			return all[i], nil
		}
	}

	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

// memStore is a unitsettings.Store in memory. failWith, when set, is the error
// of every write.
type memStore struct {
	replays  idempotency.Store
	mu       sync.Mutex
	records  map[string]unitsettings.Settings
	defaults unitsettings.NamespaceDefaults
	entries  []audit.Entry
	writes   int
	failWith error
	// hold, when set, runs at the start of every Put, before the store takes its
	// lock. A test uses it to keep a write in flight.
	hold func()
	// defaultsAtPut, when set, replaces the defaults at the start of every
	// Put, as another writer that commits between the read of the handler and
	// the write. The write then returns the new defaults.
	defaultsAtPut *unitsettings.NamespaceDefaults
}

func newMemStore(records idempotency.Store) *memStore {
	return &memStore{
		replays: records,
		mu:      sync.Mutex{}, records: map[string]unitsettings.Settings{}, defaults: noDefaults(),
		entries: nil, writes: 0, failWith: nil, hold: nil, defaultsAtPut: nil,
	}
}

func (m *memStore) Get(_ context.Context, unit identity.Object) (unitsettings.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.records[unit.UnitID()]
	if !ok {
		return unitsettings.Settings{}, unitsettings.ErrNotFound
	}

	return s, nil
}

func (m *memStore) Put(
	ctx context.Context, s *unitsettings.Settings, expected int64, _ namespace.Fence, e audit.Entry,
) (unitsettings.PutResult, error) {
	if m.hold != nil {
		m.hold()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failWith != nil {
		return unitsettings.PutResult{}, m.failWith
	}

	if m.defaultsAtPut != nil {
		m.defaults = *m.defaultsAtPut
	}

	if m.records[s.Unit.UnitID()].Version != expected {
		return unitsettings.PutResult{}, unitsettings.ErrConflict
	}

	next := *s
	next.Version = expected + 1
	next.UpdatedAt = e.At

	defaults := m.defaults
	defaults.Namespace = s.Unit.Namespace()

	out := unitsettings.PutResult{Settings: next, Defaults: defaults}

	if err := m.finish(ctx, out); err != nil {
		return unitsettings.PutResult{}, err
	}

	m.records[s.Unit.UnitID()] = next
	m.entries = append(m.entries, e)
	m.writes++

	return out, nil
}

func (m *memStore) Delete(
	ctx context.Context, unit identity.Object, expected int64, _ namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failWith != nil {
		return m.failWith
	}

	old, ok := m.records[unit.UnitID()]
	if !ok {
		return unitsettings.ErrNotFound
	}

	if old.Version != expected {
		return unitsettings.ErrConflict
	}

	if err := m.finish(ctx, old); err != nil {
		return err
	}

	delete(m.records, unit.UnitID())
	m.entries = append(m.entries, e)
	m.writes++

	return nil
}

func (m *memStore) Defaults(context.Context, namespace.Name) (unitsettings.NamespaceDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d := m.defaults
	d.Namespace = "acme"

	return d, nil
}

func (m *memStore) SetDefaults(
	ctx context.Context, d unitsettings.NamespaceDefaults, expected int64, _ namespace.Fence, e audit.Entry,
) (unitsettings.NamespaceDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failWith != nil {
		return unitsettings.NamespaceDefaults{}, m.failWith
	}

	if m.defaults.Version != expected {
		return unitsettings.NamespaceDefaults{}, unitsettings.ErrConflict
	}

	d.Version = expected + 1
	d.UpdatedAt = e.At

	if err := m.finish(ctx, d); err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	m.defaults = d
	m.entries = append(m.entries, e)
	m.writes++

	return d, nil
}

// finish stores the answer of the request in ctx, as a store does in the
// transaction of its write. It runs before the write is applied.
func (m *memStore) finish(ctx context.Context, result any) error {
	c, ok := idempotency.CompletionFrom(ctx)
	if !ok {
		return nil
	}

	complete := func(token string, a idempotency.Answer) error {
		return m.replays.Complete(ctx, c.Ref, token, a)
	}

	if err := c.Finish(result, complete); err != nil {
		return fmt.Errorf("store the answer: %w", err)
	}

	return nil
}

func (m *memStore) written() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.writes
}

type fences struct{}

func (fences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}, nil
}

// noDefaults are the unit defaults of a namespace that never set them.
func noDefaults() unitsettings.NamespaceDefaults {
	return unitsettings.NamespaceDefaults{Namespace: "", Pool: "", Version: 0, UpdatedAt: time.Time{}}
}

// connection is the default connection with push access of the namespace ns.
func connection(ns namespace.Name, id string) forge.ConnectionRecord {
	return forge.ConnectionRecord{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), Namespace: ns, ID: id, Kind: forge.KindGitHub,
		Auth: forge.AuthApp, Scope: forge.AppScopeInstallation, InstallationID: "1", Version: 1, Push: true,
		Default: true,
	}
}

// jobsPool is a runner pool for jobs.
func jobsPool(id string, ns namespace.Name) runner.Pool {
	return runner.Pool{
		ID:          id,
		Namespace:   ns,
		Name:        "ci",
		Purpose:     runner.PurposeJobs,
		Lease:       0,
		Concurrency: 0,
		Isolation:   nil,
		GitCredentials: runner.GitCredentials{
			Mode:   "",
			Helper: "",
		},
		CreatedAt: time.Unix(0, 0),
		UpdatedAt: time.Unix(0, 0),
	}
}

type connections struct{}

func (connections) GetConnection(_ context.Context, ns namespace.Name, id string) (forge.ConnectionRecord, error) {
	if ns == "acme" && id == connRW {
		return connection(ns, id), nil
	}

	return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
}

func (connections) ListConnections(_ context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error) {
	if ns != "acme" {
		return nil, nil
	}

	return []forge.ConnectionRecord{connection(ns, connRW)}, nil
}

type pools struct{}

func (pools) Pool(_ context.Context, id string) (runner.Pool, error) {
	if id == poolJobs {
		return jobsPool(id, "acme"), nil
	}

	return runner.Pool{}, runner.ErrNotFound
}

func (pools) Pools(_ context.Context, ns namespace.Name) ([]runner.Pool, error) {
	if ns != "acme" {
		return nil, nil
	}

	return []runner.Pool{jobsPool(poolJobs, ns)}, nil
}

// suggester gives one suggestion for any unit.
type suggester struct{ err error }

func (s suggester) Suggest(context.Context, identity.Object) ([]unitsettingssvc.Suggestion, error) {
	if s.err != nil {
		return nil, s.err
	}

	return []unitsettingssvc.Suggestion{{
		Repository: "acme/infra", Commit: "0123456789abcdef", Source: "pulumi-update",
		ObservedAt: time.Unix(1_700_000_000, 0),
	}}, nil
}

// tokenAuthenticator signs in the principal ada for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != testToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: ada, DisplayName: "", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}, nil
}

// recordingAuthorizer allows the checks that holds names, as "<action> <object>",
// and records every check in order.
type recordingAuthorizer struct {
	holds  map[string]bool
	checks []string
	mu     sync.Mutex
}

func newAuthorizer(holds ...string) *recordingAuthorizer {
	a := &recordingAuthorizer{holds: map[string]bool{}, checks: nil, mu: sync.Mutex{}}
	for _, h := range holds {
		a.holds[h] = true
	}

	return a
}

func (a *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = append(a.checks, key)

	if !a.holds[key] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, key)
	}

	return nil
}

func (*recordingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoList
}

// revoke removes a check from holds, so the next check of it is denied.
func (a *recordingAuthorizer) revoke(check string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.holds, check)
}

func (a *recordingAuthorizer) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.checks)
}

// recordingAudit keeps the entries that the guard appends.
type recordingAudit struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

// AppendServer keeps an entry of the installation scope, so that a test counts
// the refusals of a namespace that does not exist.
func (a *recordingAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

func (a *recordingAudit) denied() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.entries)
}

// rig holds a handler and the fakes behind it.
type rig struct {
	h       *settings.Handler
	records *idempotencytest.Memory
	store   *memStore
	audit   *recordingAudit
	authz   *recordingAuthorizer
}

// fullAccess lists the checks of a caller that holds every action on the unit
// net and on the namespace acme.
func fullAccess() []string {
	return []string{
		"namespace:read acme", "namespace:admin acme",
		"namespace:use-git-host-connection acme",
		"unit:read " + netObject, "unit:manage " + netObject, "unit:define-steps " + netObject,
		"unit:read " + appObject, "unit:manage " + appObject, "unit:define-steps " + appObject,
	}
}

func newRig(t *testing.T, holds ...string) *rig {
	t.Helper()

	return newRigWith(t, suggester{err: nil}, holds...)
}

func newRigWith(t *testing.T, sg unitsettingssvc.Suggester, holds ...string) *rig {
	t.Helper()

	return newRigFull(t, sg, rigOptions{withoutReplayRecords: false}, holds...)
}

// rigOptions changes how newRigFull builds the rig.
type rigOptions struct {
	// withoutReplayRecords builds the handler with no store of replay records,
	// as a server that was started without one.
	withoutReplayRecords bool
}

// newRigFull builds the rig.
func newRigFull(t *testing.T, sg unitsettingssvc.Suggester, opts rigOptions, holds ...string) *rig {
	t.Helper()

	records := idempotencytest.NewMemory()

	r := &rig{
		h:       nil,
		records: records,
		store:   newMemStore(records),
		audit:   &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
		authz:   newAuthorizer(holds...),
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: r.store, Fences: fences{}, Connections: connections{}, Pools: pools{},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	idem, err := idempotent.New(idempotent.Config{
		Store: records, Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	cfg := settings.Config{
		Idempotency: idem, Records: records,
		Namespaces: namespaces, Units: fakeInventory{}, Service: svc, Suggestions: sg,
		Authenticator: tokenAuthenticator{}, Authorizer: r.authz, Audit: r.audit, OnAuditFailure: nil,
		DenialLimiter: nil, Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	}
	if opts.withoutReplayRecords {
		cfg.Idempotency, cfg.Records = nil, nil
	}

	r.h, err = settings.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return r
}

// request is one call of a test.
type request struct {
	method, path, token, body string
	header                    map[string]string
}

func serve(t *testing.T, h http.Handler, r request) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, strings.NewReader(r.body))
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	for k, v := range r.header {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// call sends an authenticated request.
func (r *rig) call(t *testing.T, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	return serve(t, r.h, request{method: method, path: path, token: testToken, body: body, header: header})
}
