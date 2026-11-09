package manuallocktest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// testNowMs is the fixed clock of the suite, in Unix milliseconds.
const testNowMs = 1_700_000_000_000

// fixture is one namespace with two OpenTofu workspaces and two collections.
type fixture struct {
	s        Store
	ns       namespace.Namespace
	web      collection.UnitRef
	api      collection.UnitRef
	payments collection.Collection
	platform collection.Collection
}

func newFixture(t *testing.T, s Store) *fixture {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	f := &fixture{
		s:        s,
		ns:       ns,
		web:      createWorkspace(t, s, ns, "web"),
		api:      createWorkspace(t, s, ns, "api"),
		payments: newCollection(t, ns.Name, "payments"),
		platform: newCollection(t, ns.Name, "platform"),
	}

	for _, c := range []collection.Collection{f.payments, f.platform} {
		if err := s.CreateCollection(t.Context(), c, ns.Fence(), auditFor(ns.Fence())); err != nil {
			t.Fatalf("CreateCollection %v: %v", c.Ref, err)
		}
	}

	return f
}

// tryLock locks t as holder and returns the error of the store.
func (f *fixture) tryLock(t *testing.T, target manuallock.Target, holder, reason string) error {
	t.Helper()

	l := manuallock.Lock{Since: now(), Target: target, Holder: holder, Reason: reason}
	e := lockAudit(f.ns.Fence())
	e.Reason = reason

	return f.s.PutLock(t.Context(), l, f.ns.Fence(), e)
}

// lock locks t as holder and fails the test on an error.
func (f *fixture) lock(t *testing.T, target manuallock.Target, holder, reason string) {
	t.Helper()

	if err := f.tryLock(t, target, holder, reason); err != nil {
		t.Fatalf("PutLock %s: %v", target, err)
	}
}

// forceRemove ends the lock on target and fails the test on an error.
func (f *fixture) forceRemove(t *testing.T, target manuallock.Target) {
	t.Helper()

	if _, err := f.s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
		t.Fatalf("ForceRemoveLock %s: %v", target, err)
	}
}

// put moves unit into c, as the collection service does.
func (f *fixture) put(t *testing.T, unit collection.UnitRef, c collection.Collection) {
	t.Helper()

	var from collection.Name

	if cur, err := f.s.UnitCollection(t.Context(), unit); err == nil {
		from = cur.Name
	}

	m := collection.Move{Unit: unit, From: from, To: c.Ref, ToID: c.ID, EndsLock: true}
	if err := f.s.SetUnitCollection(t.Context(), m, f.ns.Fence(), auditFor(f.ns.Fence())); err != nil {
		t.Fatalf("SetUnitCollection %v: %v", unit, err)
	}
}

// auditFor returns a valid audit entry for a write in the namespace of fence.
func auditFor(fence namespace.Fence) audit.Entry {
	return audit.Entry{
		At:            now(),
		Namespace:     fence.Namespace,
		Subject:       "tester",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "test:write",
		Object:        fence.Namespace.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Changes:       nil,
		ObjectID:      "",
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// lockAudit returns the audit entry of a lock or an unlock.
func lockAudit(fence namespace.Fence) audit.Entry {
	e := auditFor(fence)
	e.Action = actionUnitLock

	return e
}

func newNamespace(t *testing.T, s Store, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

func newCollection(t *testing.T, ns namespace.Name, name string) collection.Collection {
	t.Helper()

	return collection.Collection{
		CreatedAt: now(),
		ID:        ident.NewID("col-"),
		Ref:       collection.Ref{Namespace: ns, Name: mustName(t, name)},
	}
}

// createWorkspace makes an OpenTofu workspace in ns and returns its inventory
// reference.
func createWorkspace(t *testing.T, s Store, ns namespace.Namespace, name string) collection.UnitRef {
	t.Helper()

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, name)},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %q: %v", name, err)
	}

	return collection.UnitRef{Namespace: ns.Name, Tool: toolOpenTofu, Name: name}
}

// newPulumiStack returns a stack of the Pulumi CLI.
func newPulumiStack(ns namespace.Name, project, name string) stack.Stack {
	return stack.Stack{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Tags:        map[string]string{},
		Ref:         stack.Ref{Namespace: ns, Project: stack.ProjectName(project), Stack: stack.Name(name)},
		ID:          stack.ID(ident.NewID("stk-")),
		Version:     0,
	}
}

func mustName(t *testing.T, s string) collection.Name {
	t.Helper()

	n, err := collection.ParseName(s)
	if err != nil {
		t.Fatalf("parse collection name %q: %v", s, err)
	}

	return n
}

func mustWorkspaceName(t *testing.T, s string) workspace.Name {
	t.Helper()

	n, err := workspace.ParseName(s)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", s, err)
	}

	return n
}

func now() time.Time {
	return time.UnixMilli(testNowMs).UTC()
}
