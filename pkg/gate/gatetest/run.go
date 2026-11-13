package gatetest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Placement is the optional part of a Domain that puts units in collections.
// A case that needs a collection or a unit runs only with a Domain that
// implements it, and is skipped otherwise.
type Placement interface {
	// CreateCollection creates the collection name in ns.
	CreateCollection(ctx context.Context, ns namespace.Name, name collection.Name) error
	// CreateUnit creates the unit name of tool in ns, and puts it in the
	// collection in, or in none when in is empty.
	CreateUnit(ctx context.Context, ns namespace.Name, tool, name string, in collection.Name) error
}

// Auditor is the optional part of a Domain that reads the audit entries of a
// scope, oldest first. A case that counts entries runs only with a Domain
// that implements it, and skips the count otherwise.
type Auditor interface {
	// Entries returns the audit entries of ns, where ns is audit.ServerScope
	// for the entries of the installation, oldest first.
	Entries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error)
}

// RunStore executes the named cases of StoreCases against harnesses from
// newHarness. It runs each case whose rules a store holds across scopes, so
// a store that keeps the rules of one scope only (see RunObjects) does not
// run it. RunnableCases lists the names. A case that needs a Placement or an
// Auditor that the Domain does not implement is skipped or counts nothing.
func RunStore(t *testing.T, newHarness Factory) {
	t.Helper()

	runners := storeRunners()

	cases := StoreCases()

	for i := range cases {
		name := cases[i].Name

		run, ok := runners[name]
		if !ok {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run(t, newHarness)
		})
	}
}

// RunnableCases returns the names of the cases that RunStore executes.
func RunnableCases() []string {
	names := make([]string, 0, len(storeRunners()))
	for name := range storeRunners() {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// runner executes one case. It asks the factory for each fresh harness that
// it needs.
type runner func(t *testing.T, f Factory)

// storeRunners returns the runner of each executable case, by name.
func storeRunners() map[string]runner {
	out := map[string]runner{}

	for _, m := range []map[string]runner{
		objectRunners(), ruleRunners(), unitRunners(), settingsRunners(), recordRunners(),
	} {
		maps.Copy(out, m)
	}

	return out
}

// env is the namespaces acme and globex of one case, and the optional
// placement of the units.
type env struct {
	t      *testing.T
	h      Harness
	acme   namespace.Fence
	globex namespace.Fence
	fx     fixtures
	// placed is true when the collections and the units of the fixtures
	// exist.
	placed bool
}

func newEnv(t *testing.T, h Harness) *env {
	t.Helper()

	e := &env{t: t, h: h, acme: zeroFence(), globex: zeroFence(), fx: newFixtures(), placed: false}

	var err error

	if e.acme, err = h.Domain.CreateNamespace(t.Context(), "acme"); err != nil {
		t.Fatalf("CreateNamespace(acme) = %v", err)
	}

	if e.globex, err = h.Domain.CreateNamespace(t.Context(), "globex"); err != nil {
		t.Fatalf("CreateNamespace(globex) = %v", err)
	}

	// The configuration of a unit keys on the stable ID of the unit, so a
	// store that resolves units holds none for a unit that does not exist.
	// Each case gets the units of the fixtures when the Domain can make them.
	if _, ok := h.Domain.(Placement); ok {
		e.place()
	}

	return e
}

// place makes the collections prod and staging of acme, the unit web/prod of
// pulumi in prod and the unit net of opentofu in no collection, and skips the
// case when the Domain cannot place units. A second call does nothing.
func (e *env) place() {
	e.t.Helper()

	p, ok := e.h.Domain.(Placement)
	if !ok {
		e.t.Skip("the Domain does not implement Placement")
	}

	if e.placed {
		return
	}

	e.placed = true

	ctx := e.t.Context()

	for _, name := range []collection.Name{"prod", "staging"} {
		if err := p.CreateCollection(ctx, "acme", name); err != nil {
			e.t.Fatalf("CreateCollection(%s) = %v", name, err)
		}
	}

	units := []struct {
		ns   namespace.Name
		tool string
		name string
		in   collection.Name
	}{
		{ns: "acme", tool: "pulumi", name: "web/prod", in: "prod"},
		{ns: "acme", tool: "opentofu", name: "net", in: ""},
		{ns: "globex", tool: "opentofu", name: "net", in: ""},
	}

	for _, u := range units {
		if err := p.CreateUnit(ctx, u.ns, u.tool, u.name, u.in); err != nil {
			e.t.Fatalf("CreateUnit(%s/%s) = %v", u.ns, u.name, err)
		}
	}
}

// fence returns the fence that a write at scope takes.
func (e *env) fence(scope gate.Scope) namespace.Fence {
	switch scope.Namespace() {
	case "acme":
		return e.acme
	case "globex":
		return e.globex
	default:
		return zeroFence()
	}
}

// entry returns a valid audit entry for a write at scope.
func (e *env) entry(scope gate.Scope) audit.Entry {
	if scope.Level() == gate.LevelInstallation {
		return serverEntry()
	}

	return entry(scope.Namespace())
}

// reasonEntry returns entry(scope) with the reason of a write that makes a
// final gate weaker.
func (e *env) reasonEntry(scope gate.Scope, reason string) audit.Entry {
	en := e.entry(scope)
	en.Changes = []audit.Change{{Key: gate.ReasonKey, Old: nil, New: reason, Layer: "", TakesEffect: ""}}

	return en
}

// serverEntry returns a valid audit entry in audit.ServerScope.
func serverEntry() audit.Entry {
	en := entry(audit.ServerScope)
	en.Action = "installation:manage-gates"
	en.Object = "installation"

	return en
}

// entries returns the audit entries of ns, and nil when the Domain is no
// Auditor. The second result is false then.
func (e *env) entries(ns namespace.Name) ([]audit.Entry, bool) {
	e.t.Helper()

	a, ok := e.h.Domain.(Auditor)
	if !ok {
		return nil, false
	}

	got, err := a.Entries(e.t.Context(), ns)
	if err != nil {
		e.t.Fatalf("Entries(%s) = %v", ns, err)
	}

	return got, true
}

// wantEntries fails the test when the audit entries of ns are not n.
func (e *env) wantEntries(ns namespace.Name, n int) {
	e.t.Helper()

	if got, ok := e.entries(ns); ok && len(got) != n {
		e.t.Fatalf("audit entries of %s = %d, want %d", ns, len(got), n)
	}
}

// wantErrText fails the test when err does not wrap want or when its text
// does not hold each of the substrings.
func wantErrText(t *testing.T, call string, err, want error, parts ...string) {
	t.Helper()

	wantErr(t, call, err, want)

	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("%s = %q, want the text %q", call, err, p)
		}
	}
}

// mustNil fails the test on err.
func mustNil(t *testing.T, call string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s = %v", call, err)
	}
}

// kindOps is the port of one kind of gate.Objects, with the type erased, so
// a case that holds for each kind is written once.
type kindOps interface {
	kind() Kind
	// put stores a sample object and returns its version.
	put(e *env, scope gate.Scope, id gate.ID, salt string, prev int64) (int64, error)
	// putWith is put with an explicit fence and entry.
	putWith(e *env, w *write) (int64, error)
	get(e *env, scope gate.Scope, id gate.ID) (gate.ObjectRef, error)
	list(e *env, scope gate.Scope) ([]gate.ObjectRef, error)
	listEveryScope(e *env) ([]gate.ObjectRef, error)
	del(e *env, ref gate.ObjectRef, fence namespace.Fence, en audit.Entry) error
	limit() int
}

// ops is kindOps for the kind T.
type ops[T gate.Object] struct {
	k    Kind
	objs gate.Objects[T]
}

func (o ops[T]) kind() Kind { return o.k }

func (o ops[T]) limit() int { return limitOf[T]() }

func (o ops[T]) put(e *env, scope gate.Scope, id gate.ID, salt string, prev int64) (int64, error) {
	return o.putWith(e, &write{
		scope: scope, id: id, salt: salt, prev: prev, fence: e.fence(scope), entry: e.entry(scope),
	})
}

// write is one Put of a sample object.
type write struct {
	scope gate.Scope
	id    gate.ID
	salt  string
	prev  int64
	fence namespace.Fence
	entry audit.Entry
}

func (o ops[T]) putWith(e *env, w *write) (int64, error) {
	v, err := o.objs.Put(e.t.Context(), sample[T](e.t, w.scope, w.id, w.salt), w.prev, w.fence, w.entry)
	if err != nil {
		return 0, fmt.Errorf("put: %w", err)
	}

	return v, nil
}

func (o ops[T]) get(e *env, scope gate.Scope, id gate.ID) (gate.ObjectRef, error) {
	obj, err := o.objs.Get(e.t.Context(), scope, id)
	if err != nil {
		return gate.ObjectRef{}, fmt.Errorf("get: %w", err)
	}

	return obj.Ref(), nil
}

func (o ops[T]) list(e *env, scope gate.Scope) ([]gate.ObjectRef, error) {
	objs, err := o.objs.List(e.t.Context(), scope)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	out := make([]gate.ObjectRef, len(objs))
	for i, obj := range objs {
		out[i] = obj.Ref()
	}

	return out, nil
}

func (o ops[T]) listEveryScope(e *env) ([]gate.ObjectRef, error) {
	objs, err := o.objs.ListEveryScope(e.t.Context())
	if err != nil {
		return nil, fmt.Errorf("list every scope: %w", err)
	}

	out := make([]gate.ObjectRef, len(objs))
	for i, obj := range objs {
		out[i] = obj.Ref()
	}

	return out, nil
}

func (o ops[T]) del(e *env, ref gate.ObjectRef, fence namespace.Fence, en audit.Entry) error {
	if err := o.objs.Delete(e.t.Context(), ref, fence, en); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}

// allKinds returns the ports of each kind of s.
func allKinds(t *testing.T, s gate.Store) []kindOps {
	t.Helper()

	return []kindOps{
		ops[gate.Gate]{k: KindGate, objs: objectsOf[gate.Gate](t, s)},
		ops[gate.AccessLimit]{k: KindAccessLimit, objs: objectsOf[gate.AccessLimit](t, s)},
		ops[gate.ApprovalRule]{k: KindApprovalRule, objs: objectsOf[gate.ApprovalRule](t, s)},
		ops[gate.OwnershipRule]{k: KindOwnershipRule, objs: objectsOf[gate.OwnershipRule](t, s)},
		ops[gate.PathSet]{k: KindPathSet, objs: objectsOf[gate.PathSet](t, s)},
	}
}

// once returns a runner that runs fn on a fresh environment.
func once(fn func(t *testing.T, e *env)) runner {
	return func(t *testing.T, f Factory) {
		t.Helper()

		fn(t, newEnv(t, f(t)))
	}
}

// eachKind returns a runner that runs fn on a fresh environment for each
// kind.
func eachKind(fn func(t *testing.T, e *env, k kindOps)) runner {
	return func(t *testing.T, f Factory) {
		t.Helper()

		for _, kind := range []Kind{KindGate, KindAccessLimit, KindApprovalRule, KindOwnershipRule, KindPathSet} {
			t.Run(string(kind), func(t *testing.T) {
				t.Parallel()

				h := f(t)
				i := slices.IndexFunc(allKinds(t, h.Store), func(k kindOps) bool { return k.kind() == kind })
				fn(t, newEnv(t, h), allKinds(t, h.Store)[i])
			})
		}
	}
}
