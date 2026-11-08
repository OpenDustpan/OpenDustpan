package savedview_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

const (
	ownerAna = "prn-anaanaanaanaanaana1"
	ownerBob = "prn-bobbobbobbobbobbob22"
)

var errStore = errors.New("store failed")

// memStore is a savedview.Store in memory. It records the fence and the audit
// entry of each write.
type memStore struct {
	mu     sync.Mutex
	views  map[savedview.ID]savedview.View
	fences []namespace.Fence
	audits []audit.Entry
	// failIn names a namespace whose cleanup fails.
	failIn namespace.Name
}

func newMemStore() *memStore {
	return &memStore{
		mu: sync.Mutex{}, views: map[savedview.ID]savedview.View{}, fences: nil, audits: nil, failIn: "",
	}
}

func (m *memStore) Create(_ context.Context, v savedview.View, f namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.views[v.ID] = v
	m.fences = append(m.fences, f)
	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) Get(_ context.Context, ns namespace.Name, id savedview.ID, owner string) (savedview.View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.views[id]
	if !ok || v.Owner != owner || v.Namespace != ns {
		return savedview.View{}, savedview.ErrNotFound
	}

	return v, nil
}

func (m *memStore) List(_ context.Context, ns namespace.Name, owner string) ([]savedview.View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []savedview.View

	for id := range m.views {
		if m.views[id].Owner == owner && m.views[id].Namespace == ns {
			out = append(out, m.views[id])
		}
	}

	slices.SortFunc(out, func(a, b savedview.View) int { return strings.Compare(a.Name, b.Name) })

	return out, nil
}

//nolint:revive // argument-limit: the signature is the one of savedview.Store.
func (m *memStore) Delete(
	_ context.Context, ns namespace.Name, id savedview.ID, owner string, f namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.views[id]
	if !ok || v.Owner != owner || v.Namespace != ns {
		return savedview.ErrNotFound
	}

	delete(m.views, id)

	m.fences = append(m.fences, f)
	m.audits = append(m.audits, e)

	return nil
}

func (m *memStore) DeleteOwned(
	_ context.Context, ns namespace.Name, owner string, f namespace.Fence, e audit.Entry,
) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ns == m.failIn {
		return 0, errStore
	}

	m.fences = append(m.fences, f)

	removed := 0

	for id := range m.views {
		if m.views[id].Owner != owner || m.views[id].Namespace != ns {
			continue
		}

		delete(m.views, id)

		entry := e
		entry.ObjectID = string(id)
		m.audits = append(m.audits, entry)
		removed++
	}

	return removed, nil
}

// memNamespaces holds namespaces with their own failover versions.
type memNamespaces struct {
	list []namespace.Namespace
}

func (m memNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	for _, n := range m.list {
		if n.Name == name {
			return n, nil
		}
	}

	return namespace.Namespace{}, namespace.ErrNotFound
}

func (m memNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return m.list, nil
}

func testNamespaces() memNamespaces {
	mk := func(name namespace.Name, version int64) namespace.Namespace {
		return namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: time.UnixMilli(1), Name: name, PrimaryInstallation: "eu-west",
			FailoverVersion: version, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
	}

	return memNamespaces{list: []namespace.Namespace{mk("acme", 3), mk("beta", 7), mk("gamma", 11)}}
}

func newService(t *testing.T, store *memStore) *savedview.Service {
	t.Helper()

	svc, err := savedview.NewService(savedview.Config{
		Store: store, Namespaces: testNamespaces(), Now: func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

func person(subject string) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: "", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson, Credential: "",
		JobID:   "",
		JobUnit: identity.Object{},
	}
}

// template is the audit entry that the request fills.
func template() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

func callerOf(owner string, p identity.Principal) *savedview.Caller {
	return &savedview.Caller{Owner: owner, Principal: p, By: template()}
}

func noQuery() savedview.Query {
	return savedview.Query{Labels: nil, References: nil}
}

func TestNewServiceNeedsItsPorts(t *testing.T) {
	t.Parallel()

	_, err := savedview.NewService(savedview.Config{Store: nil, Namespaces: testNamespaces(), Now: nil})
	if !errors.Is(err, savedview.ErrMissingDependency) {
		t.Fatalf("no store = %v, want ErrMissingDependency", err)
	}

	_, err = savedview.NewService(savedview.Config{Store: newMemStore(), Namespaces: nil, Now: nil})
	if !errors.Is(err, savedview.ErrMissingDependency) {
		t.Fatalf("no namespaces = %v, want ErrMissingDependency", err)
	}
}

func TestNewIDIsValidAndUnique(t *testing.T) {
	t.Parallel()

	seen := map[savedview.ID]bool{}

	for range 200 {
		id := savedview.NewID()
		if err := id.Validate(); err != nil {
			t.Fatalf("NewID() = %q: %v", id, err)
		}

		if seen[id] {
			t.Fatalf("NewID() repeated %q", id)
		}

		seen[id] = true
	}
}

func TestServiceCreate(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)
	q := savedview.Query{Labels: []savedview.Label{{Key: "team", Value: "payments"}}, References: nil}

	v, err := svc.Create(t.Context(), callerOf(ownerAna, person("ana")), "acme", "Reorg of the payments team", q)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if v.Owner != ownerAna || v.Namespace != "acme" || v.CreatedAt.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("view = %+v", v)
	}

	if len(store.fences) != 1 || store.fences[0] != (namespace.Fence{Namespace: "acme", FailoverVersion: 3}) {
		t.Fatalf("fences = %+v, want the fence of acme", store.fences)
	}

	checkCreateEntry(t, store.audits[0], &v)
}

// checkCreateEntry checks the audit entry of a create: the view ID, the person,
// and no name.
func checkCreateEntry(t *testing.T, e audit.Entry, v *savedview.View) {
	t.Helper()

	if e.Action != "view:create" || e.ObjectID != string(v.ID) || e.Object != "acme" || e.Subject != "ana" ||
		e.TokenID != "tok-1" || e.Namespace != "acme" || e.PrincipalKind != audit.PrincipalKindPerson ||
		e.SourceIP != "192.0.2.1" || e.Outcome != audit.OutcomeSuccess || e.Validate() != nil {
		t.Fatalf("audit entry = %+v", e)
	}

	// The name can be private text: it is not in the entry.
	if strings.Contains(fmt.Sprintf("%+v", e), "Reorg") {
		t.Fatalf("the audit entry holds the name: %+v", e)
	}
}

func withKind(p identity.Principal, k identity.Kind) identity.Principal {
	p.Kind = k

	return p
}

func withJob(p identity.Principal) identity.Principal {
	p.JobID = "job-1"

	return p
}

func TestServiceCreateRefusals(t *testing.T) {
	t.Parallel()

	ana := person("ana")

	tests := []struct {
		name   string
		caller *savedview.Caller
		ns     namespace.Name
		view   string
		want   error
	}{
		{
			"service",
			callerOf(ownerAna, withKind(person("ci"), identity.KindService)),
			"acme",
			"x",
			savedview.ErrNotPerson,
		},
		{
			"workload",
			callerOf(ownerAna, withKind(person("pod"), identity.KindWorkload)),
			"acme",
			"x",
			savedview.ErrNotPerson,
		},
		{"no kind", callerOf(ownerAna, withKind(person("root"), "")), "acme", "x", savedview.ErrNotPerson},
		{"unknown kind", callerOf(ownerAna, withKind(ana, "robot")), "acme", "x", savedview.ErrNotPerson},
		{"a job", callerOf(ownerAna, withJob(ana)), "acme", "x", savedview.ErrNotPerson},
		{"owner is a subject", callerOf("ana", ana), "acme", "x", savedview.ErrNotPerson},
		{"no owner", callerOf("", ana), "acme", "x", savedview.ErrNotPerson},
		{"owner with a pattern", callerOf("prn-%", ana), "acme", "x", savedview.ErrNotPerson},
		{"no caller", nil, "acme", "x", savedview.ErrNotPerson},
		{"invalid name", callerOf(ownerAna, ana), "acme", "", savedview.ErrInvalid},
		{"unknown namespace", callerOf(ownerAna, ana), "nowhere", "x", namespace.ErrNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			svc := newService(t, store)

			_, err := svc.Create(t.Context(), tc.caller, tc.ns, tc.view, noQuery())
			if !errors.Is(err, tc.want) {
				t.Fatalf("create = %v, want %v", err, tc.want)
			}

			if len(store.views) != 0 || len(store.audits) != 0 {
				t.Fatalf("a refused create stored %d views and %d entries", len(store.views), len(store.audits))
			}
		})
	}
}

func TestServiceReadsAndDeletesRefuseWhoIsNotAPerson(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	v, err := svc.Create(t.Context(), callerOf(ownerAna, person("ana")), "acme", "x", noQuery())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	bad := callerOf(ownerAna, withKind(person("ana"), "robot"))

	if _, err = svc.Get(t.Context(), bad, "acme", v.ID); !errors.Is(err, savedview.ErrNotPerson) {
		t.Fatalf("get = %v, want ErrNotPerson", err)
	}

	if _, err = svc.List(t.Context(), bad, "acme"); !errors.Is(err, savedview.ErrNotPerson) {
		t.Fatalf("list = %v, want ErrNotPerson", err)
	}

	if err = svc.Delete(t.Context(), bad, "acme", v.ID); !errors.Is(err, savedview.ErrNotPerson) {
		t.Fatalf("delete = %v, want ErrNotPerson", err)
	}

	if len(store.views) != 1 {
		t.Fatalf("views = %d, want the view to stay", len(store.views))
	}
}

func TestServiceGetAndList(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())
	ana, bob := callerOf(ownerAna, person("ana")), callerOf(ownerBob, person("bob"))

	v, err := svc.Create(t.Context(), ana, "acme", "x", noQuery())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got, getErr := svc.Get(t.Context(), ana, "acme", v.ID); getErr != nil || got.ID != v.ID {
		t.Fatalf("get = %+v, %v", got, getErr)
	}

	if _, getErr := svc.Get(t.Context(), bob, "acme", v.ID); !errors.Is(getErr, savedview.ErrNotFound) {
		t.Fatalf("get as another owner = %v, want ErrNotFound", getErr)
	}

	list, err := svc.List(t.Context(), ana, "acme")
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}

	if other, listErr := svc.List(t.Context(), bob, "acme"); listErr != nil || len(other) != 0 {
		t.Fatalf("list as another owner = %v, %v", other, listErr)
	}
}

func TestServiceRefusesIDsThatAreNotViewIDs(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())
	ana := callerOf(ownerAna, person("ana"))

	for _, id := range []savedview.ID{"", "view-x", "../etc", savedview.ID(strings.Repeat("a", 5000))} {
		if _, err := svc.Get(t.Context(), ana, "acme", id); !errors.Is(err, savedview.ErrNotFound) {
			t.Fatalf("get %q = %v, want ErrNotFound", id, err)
		}

		if err := svc.Delete(t.Context(), ana, "acme", id); !errors.Is(err, savedview.ErrNotFound) {
			t.Fatalf("delete %q = %v, want ErrNotFound", id, err)
		}
	}
}

func TestServiceDelete(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)
	ana, bob := callerOf(ownerAna, person("ana")), callerOf(ownerBob, person("bob"))

	v, err := svc.Create(t.Context(), ana, "acme", "x", noQuery())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if delErr := svc.Delete(t.Context(), bob, "acme", v.ID); !errors.Is(delErr, savedview.ErrNotFound) {
		t.Fatalf("delete as another owner = %v, want ErrNotFound", delErr)
	}

	if delErr := svc.Delete(t.Context(), ana, "nowhere", v.ID); !errors.Is(delErr, namespace.ErrNotFound) {
		t.Fatalf("delete in an unknown namespace = %v, want ErrNotFound of the namespace", delErr)
	}

	if delErr := svc.Delete(t.Context(), ana, "acme", v.ID); delErr != nil {
		t.Fatalf("delete: %v", delErr)
	}

	last := store.audits[len(store.audits)-1]
	if last.Action != "view:delete" || last.ObjectID != string(v.ID) {
		t.Fatalf("audit entry of the delete = %+v", last)
	}
}

func TestServiceRemoveOwner(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)
	ana, bob := callerOf(ownerAna, person("ana")), callerOf(ownerBob, person("bob"))
	want := make([]savedview.ID, 0, 3)

	for i, ns := range []namespace.Name{"acme", "acme", "gamma"} {
		v, err := svc.Create(t.Context(), ana, ns, fmt.Sprintf("view %d", i), noQuery())
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		want = append(want, v.ID)
	}

	keep, err := svc.Create(t.Context(), bob, "acme", "keep", noQuery())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store.fences, store.audits = nil, nil

	removed, err := svc.RemoveOwner(t.Context(), ownerAna)
	if err != nil || removed != 3 {
		t.Fatalf("RemoveOwner = %d, %v; want 3", removed, err)
	}

	checkCleanupRecords(t, store, want)

	if _, err = svc.Get(t.Context(), bob, "acme", keep.ID); err != nil {
		t.Fatalf("view of another owner after the cleanup: %v", err)
	}

	// A second run finds nothing.
	if removed, err = svc.RemoveOwner(t.Context(), ownerAna); err != nil || removed != 0 {
		t.Fatalf("second RemoveOwner = %d, %v; want 0", removed, err)
	}
}

// checkCleanupRecords checks that each namespace ran with its own fence, in the
// order of the list, and that the cleanup wrote one entry for each view, from
// the server, with the ID only.
func checkCleanupRecords(t *testing.T, store *memStore, want []savedview.ID) {
	t.Helper()

	wantFences := []namespace.Fence{
		{Namespace: "acme", FailoverVersion: 3},
		{Namespace: "beta", FailoverVersion: 7},
		{Namespace: "gamma", FailoverVersion: 11},
	}
	if !slices.Equal(store.fences, wantFences) {
		t.Fatalf("fences = %+v, want %+v", store.fences, wantFences)
	}

	if len(store.audits) != len(want) {
		t.Fatalf("%d audit entries, want %d", len(store.audits), len(want))
	}

	for i := range store.audits {
		e := &store.audits[i]
		if e.Action != "view:delete" || e.Subject != "@server" || !slices.Contains(want, savedview.ID(e.ObjectID)) ||
			len(e.Changes) != 0 || e.Validate() != nil {
			t.Fatalf("audit entry = %+v", *e)
		}
	}
}

func TestServiceRemoveOwnerContinuesAfterAFailure(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)
	ana := callerOf(ownerAna, person("ana"))

	for _, ns := range []namespace.Name{"acme", "beta", "gamma"} {
		if _, err := svc.Create(t.Context(), ana, ns, "x", noQuery()); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	store.failIn = "beta"

	removed, err := svc.RemoveOwner(t.Context(), ownerAna)
	if !errors.Is(err, errStore) || removed != 2 {
		t.Fatalf("RemoveOwner = %d, %v; want 2 and the store error", removed, err)
	}

	// The retry finishes the namespace that failed.
	store.failIn = ""

	if removed, err = svc.RemoveOwner(t.Context(), ownerAna); err != nil || removed != 1 {
		t.Fatalf("retry = %d, %v; want 1", removed, err)
	}
}

func TestServiceRemoveOwnerRefusesAnOwnerThatIsNotAPrincipalID(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	if _, err := svc.Create(t.Context(), callerOf(ownerAna, person("ana")), "acme", "x", noQuery()); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, owner := range []string{"", "ana", "prn-", "prn-%", "%", "prn-aaa aaa", strings.Repeat("p", 100)} {
		removed, err := svc.RemoveOwner(t.Context(), owner)
		if !errors.Is(err, savedview.ErrInvalid) || removed != 0 {
			t.Errorf("RemoveOwner(%q) = %d, %v; want 0 and ErrInvalid", owner, removed, err)
		}
	}

	if len(store.views) != 1 {
		t.Fatalf("views = %d, want the view to stay", len(store.views))
	}
}
