package triggers_test

import (
	"context"
	"embed"
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

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/triggers"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	testToken = "trigger-test-token"
	// limitedToken is a token that the authenticator refuses with a rate limit.
	limitedToken = "trigger-limited-token"
	// ada is the subject of the caller.
	ada = "user:ada"
	// netID and appID are the unit IDs of the units net and app in acme.
	netID = "ws-1"
	appID = "ws-2"
	// otherID is the ID of a unit in the namespace other.
	otherID = "ws-9"
)

// errNotUsed is the error of a fake method that no test calls.
var errNotUsed = errors.New("not used")

// errNoList is the error of a route that lists objects.
var errNoList = errors.New("no route lists objects")

// fakeNamespaces answers every namespace except "absent".
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Unix(0, 0),
		Name:                name,
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeInventory holds the units net and app in acme, and one unit in other.
type fakeInventory struct {
	gone map[string]bool
	// extra holds units that a test adds after the rig exists, by unit ID.
	extra map[string]coreinventory.Entry
}

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

func (f fakeInventory) ListUnits(_ context.Context, ns namespace.Name) ([]coreinventory.Entry, error) {
	var out []coreinventory.Entry

	all := f.allEntries()
	for i := range all {
		if all[i].Namespace == ns && !f.gone[all[i].UnitID] {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// QueryUnits implements coreinventory.Reader over the entries of ListUnits.
func (f fakeInventory) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	all, err := f.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return coreinventory.PageUnits(all, q), nil
}

func (f fakeInventory) GetUnit(_ context.Context, unitID string) (coreinventory.Entry, error) {
	all := f.allEntries()
	for i := range all {
		if all[i].UnitID == unitID && !f.gone[unitID] {
			return all[i], nil
		}
	}

	return coreinventory.Entry{}, coreinventory.ErrUnitNotFound
}

// allEntries returns the fixed units and the units that a test added.
func (f fakeInventory) allEntries() []coreinventory.Entry {
	all := entries()
	for id := range f.extra {
		all = append(all, f.extra[id])
	}

	return all
}

// unitsPort is the trigger.Units over fakeInventory.
type unitsPort struct{ inv fakeInventory }

func (u unitsPort) Object(ctx context.Context, unit graph.Unit) (identity.Object, error) {
	e, err := u.inv.GetUnit(ctx, unit.ID)
	if err != nil || e.Namespace != unit.Namespace || e.Tool != unit.Tool {
		return identity.Object{}, trigger.ErrUnitGone
	}

	obj, err := identity.UnitObject(e.Tool, e.Namespace, e.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", trigger.ErrUnitGone, err)
	}

	return obj.WithUnitID(e.UnitID), nil
}

func (u unitsPort) Find(ctx context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	e, err := u.inv.GetUnit(ctx, unitID)
	if err != nil || e.Namespace != ns {
		return graph.Unit{}, trigger.ErrUnitGone
	}

	return graph.Unit{Namespace: ns, Tool: e.Tool, ID: unitID}, nil
}

// fakeStore is a trigger.Store that applies the cycle rule, like an adapter.
type fakeStore struct {
	mu       sync.Mutex
	triggers map[trigger.ID]trigger.Trigger
	writes   int
}

func newStore() *fakeStore {
	return &fakeStore{mu: sync.Mutex{}, triggers: map[trigger.ID]trigger.Trigger{}, writes: 0}
}

func (s *fakeStore) Put(_ context.Context, t trigger.Trigger, _ namespace.Fence, _ audit.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, ok := s.triggers[t.ID]; ok && (old.Unit != t.Unit || old.Kind != t.Kind) {
		return fmt.Errorf("%w: a trigger keeps its unit and its kind", trigger.ErrInvalid)
	}

	if err := trigger.CheckCycle(s.sorted(), t); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	s.triggers[t.ID] = t
	s.writes++

	return nil
}

func (s *fakeStore) Get(_ context.Context, _ namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.triggers[id]
	if !ok {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	// Like an adapter, Get does not serve a row that fails its checks.
	if err := t.Validate(); err != nil {
		return trigger.Trigger{}, err
	}

	return t, nil
}

func (s *fakeStore) Stored(_ context.Context, _ namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.triggers[id]
	if !ok {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	return t, nil
}

func (s *fakeStore) List(_ context.Context, unit graph.Unit) ([]trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []trigger.Trigger

	all := s.sorted()
	for i := range all {
		if all[i].Unit == unit {
			out = append(out, all[i])
		}
	}

	return out, nil
}

func (s *fakeStore) Followers(context.Context, graph.Unit) ([]trigger.Trigger, error) {
	return nil, nil
}

func (s *fakeStore) Delete(_ context.Context, _ namespace.Name, id trigger.ID, _ namespace.Fence, _ audit.Entry,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.triggers[id]; !ok {
		return trigger.ErrNotFound
	}

	delete(s.triggers, id)

	s.writes++

	return nil
}

// NextSlot implements trigger.Store. The fake keeps no schedule clock.
func (s *fakeStore) NextSlot(_ context.Context, _ namespace.Name, _ trigger.ID) (time.Time, error) {
	return time.Time{}, nil
}

// ClaimDue implements trigger.Store. The fake keeps no schedule clock.
func (s *fakeStore) ClaimDue(_ context.Context, _ trigger.Claim, _ namespace.Fence, _ audit.Entry) error {
	return trigger.ErrNotDue
}

func (s *fakeStore) sorted() []trigger.Trigger {
	out := make([]trigger.Trigger, 0, len(s.triggers))
	for id := range s.triggers {
		out = append(out, s.triggers[id])
	}

	slices.SortFunc(out, func(a, b trigger.Trigger) int { return strings.Compare(string(a.ID), string(b.ID)) })

	return out
}

func (s *fakeStore) written() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writes
}

// plant stores t without any check.
func (s *fakeStore) plant(t trigger.Trigger) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.triggers[t.ID] = t
}

// savers answers the one error for every check, nil for an allowed saver.
type savers struct{ err error }

func (s *savers) SaverID(_ context.Context, subject string) (string, error) {
	return "prn-" + subject, nil
}

func (s *savers) CheckSave(context.Context, trigger.Trigger) error  { return s.err }
func (s *savers) CheckStart(context.Context, trigger.Trigger) error { return s.err }

// dispatcher records the manual fires.
type dispatcher struct {
	mu    sync.Mutex
	reqs  []trigger.ManualRequest
	subjs []string
	err   error
}

func (*dispatcher) Scheduled(context.Context, namespace.Name, trigger.ID, time.Time) (job.ID, error) {
	return "", errNotUsed
}

func (*dispatcher) Upstream(context.Context, trigger.Applied) ([]trigger.Outcome, error) {
	return nil, errNotUsed
}

func (d *dispatcher) Manual(_ context.Context, a job.Actor, req trigger.ManualRequest) (job.ID, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.reqs = append(d.reqs, req)
	d.subjs = append(d.subjs, a.Principal.Subject)

	if d.err != nil {
		return "", d.err
	}

	return "job-abc", nil
}

func (d *dispatcher) fires() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.reqs)
}

type fences struct{}

func (fences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	return namespace.Fence{Namespace: ns, FailoverVersion: 1}, nil
}

// tokenAuthenticator signs in the principal ada for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token == limitedToken {
		return identity.Principal{}, identity.ErrRateLimited
	}

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

func (a *recordingAuthorizer) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.checks)
}

// rig holds a handler and the fakes behind it.
type rig struct {
	h      *triggers.Handler
	store  *fakeStore
	savers *savers
	disp   *dispatcher
	inv    fakeInventory
}

func newRig(t *testing.T, authz identity.Authorizer) *rig {
	t.Helper()

	return newRigLogged(t, authz, slog.New(slog.DiscardHandler))
}

// newRigLogged returns a rig whose handler writes its log lines to logger.
func newRigLogged(t *testing.T, authz identity.Authorizer, logger *slog.Logger) *rig {
	t.Helper()

	return newRigWorkers(t, authz, logger, nil)
}

// newRigWorkers returns a rig whose handler asks workers before a fire.
func newRigWorkers(t *testing.T, authz identity.Authorizer, logger *slog.Logger, workers admit.Source) *rig {
	t.Helper()

	return newRigGuarded(t, authz, logger, workers, guardOptions{
		records: &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}, limiter: nil,
	})
}

// guardOptions sets the audit store and the limiter of the refusals of a rig.
type guardOptions struct {
	records audit.Store
	limiter *denylimit.Limiter
}

// newRigGuarded is newRigWorkers with the audit store and the limiter of the
// refusals.
func newRigGuarded(
	t *testing.T, authz identity.Authorizer, logger *slog.Logger, workers admit.Source, guard guardOptions,
) *rig {
	t.Helper()

	r := &rig{
		h:      nil,
		store:  newStore(),
		savers: &savers{err: nil},
		disp: &dispatcher{
			mu:    sync.Mutex{},
			reqs:  nil,
			subjs: nil,
			err:   nil,
		},
		inv: fakeInventory{gone: map[string]bool{}, extra: map[string]coreinventory.Entry{}},
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	n := 0

	manager, err := trigger.NewManager(trigger.ManagerConfig{
		Store: r.store, Dispatcher: r.disp, Authorizer: authz, Savers: r.savers, Units: unitsPort{inv: r.inv},
		Fences: fences{}, Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
		NewID: func() trigger.ID {
			n++

			return trigger.ID(fmt.Sprintf("trg-%d", n))
		},
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}

	r.h, err = triggers.NewHandler(triggers.Config{
		Namespaces:     namespaces,
		Units:          r.inv,
		Store:          r.store,
		Manager:        manager,
		Workers:        workers,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		Audit:          guard.records,
		OnAuditFailure: nil,
		DenialLimiter:  guard.limiter,
		Sessions:       nil,
		Logger:         logger,
	})
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

// holdsAll returns the checks of a caller that holds every action on unit net
// and unit app, and reads the namespace.
func holdsAll() []string {
	out := make([]string, 0, 7)
	out = append(out, "namespace:read acme")

	for _, obj := range []string{"opentofu:acme/net", "pulumi:acme/app"} {
		for _, a := range []string{"unit:read", "unit:manage", "unit:run-plan"} {
			out = append(out, a+" "+obj)
		}
	}

	return out
}

const (
	basePath    = "/api/dustpan/v1/namespaces/acme/units/" + netID + "/triggers"
	scheduleDoc = `{"kind":"schedule","frequency":{"preset":"daily"}}`
)

func unitNet() graph.Unit { return graphUnit("opentofu", netID) }

func graphUnit(tool, id string) graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: tool, ID: id}
}

func noUnit() graph.Unit { return graph.Unit{Namespace: "", Tool: "", ID: ""} }

// recordingAudit keeps the entries that the handler appends.
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

// denied returns the entries that the handler kept.
func (a *recordingAudit) denied() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.entries)
}
