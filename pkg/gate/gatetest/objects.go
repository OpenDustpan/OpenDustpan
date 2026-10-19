package gatetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Store  gate.Store
	Domain Domain
}

// Domain makes the rows that the gate store needs and that other ports own.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// RunObjects executes the rules of gate.Objects[T] that hold for one scope
// against harnesses from newHarness: the version rules, the scope of Get
// and Delete, the fence, the limit of the scope and the audit entry. The
// rules across scopes are the named cases of StoreCases.
func RunObjects[T gate.Object](t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, f objFixture[T])
	}{
		{"a new object has version 1", testCreate[T]},
		{"a create of an ID that exists is ErrConflict", testCreateExists[T]},
		{"a replace with a stale version is ErrConflict", testReplaceStale[T]},
		{"a replace gives the next version", testReplace[T]},
		{"a replace of an unknown ID is ErrNotFound", testReplaceUnknown[T]},
		{"another scope does not see the object", testOtherScope[T]},
		{"a delete removes the object", testDelete[T]},
		{"a stale fence changes nothing", testStaleFence[T]},
		{"a fence of another scope is ErrInvalid", testWrongFence[T]},
		{"a new object above the limit of the scope is ErrLimit", testLimit[T]},
		{"an entry that fails validation is audit.ErrInvalidEntry", testBadEntry[T]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, setUpObjects[T](t, newHarness(t)))
		})
	}
}

// objFixture is the store, the port of one kind, and the scopes and the
// fences of acme and globex for one case.
type objFixture[T gate.Object] struct {
	h           Harness
	objs        gate.Objects[T]
	acme        namespace.Fence
	globex      namespace.Fence
	acmeScope   gate.Scope
	globexScope gate.Scope
}

func setUpObjects[T gate.Object](t *testing.T, h Harness) objFixture[T] {
	t.Helper()

	f := objFixture[T]{
		h: h, objs: objectsOf[T](t, h.Store), acme: zeroFence(), globex: zeroFence(),
		acmeScope: gate.NamespaceScope("acme"), globexScope: gate.NamespaceScope("globex"),
	}

	var err error

	if f.acme, err = h.Domain.CreateNamespace(t.Context(), "acme"); err != nil {
		t.Fatalf("CreateNamespace(acme) = %v", err)
	}

	if f.globex, err = h.Domain.CreateNamespace(t.Context(), "globex"); err != nil {
		t.Fatalf("CreateNamespace(globex) = %v", err)
	}

	return f
}

// zeroFence returns the zero fence, which the installation takes.
func zeroFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

// objectsOf returns the port of the kind T of s.
func objectsOf[T gate.Object](t *testing.T, s gate.Store) gate.Objects[T] {
	t.Helper()

	var port any

	var zero T

	switch any(zero).(type) {
	case gate.Gate:
		port = s.Gates()
	case gate.AccessLimit:
		port = s.AccessLimits()
	case gate.ApprovalRule:
		port = s.ApprovalRules()
	case gate.OwnershipRule:
		port = s.OwnershipRules()
	case gate.PathSet:
		port = s.PathSets()
	default:
	}

	objs, ok := port.(gate.Objects[T])
	if !ok {
		t.Fatalf("the store has no port for %T", zero)
	}

	return objs
}

// sample returns a valid object of the kind T with id at scope. salt changes
// a field, so two samples with one ID can differ.
func sample[T gate.Object](t *testing.T, scope gate.Scope, id gate.ID, salt string) T {
	t.Helper()

	var zero T

	var obj any

	approvers := gate.Approvers{Groups: []string{"sre"}, Users: nil, UnitOwners: false}

	switch any(zero).(type) {
	case gate.Gate:
		obj = gate.Gate{
			Scope: scope, ID: id, Version: 0, DecisionPoint: gate.PointChangePlanned,
			Condition: "context.plan.available && context.plan.destroy > 0", Unless: "",
			Outcome: gate.OutcomeDeny, Message: "no destroy " + salt, Mode: gate.ModeEvaluate,
			OnError: gate.OnErrorFail, Final: false,
			CheckedWith: gate.CheckedWith{Engine: "test/1", Schema: gate.SchemaVersion{
				Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0,
			}},
			State: "", Reads: nil,
		}
	case gate.AccessLimit:
		obj = gate.AccessLimit{
			Scope: scope, ID: id, Version: 0,
			Policy: `forbid (principal, action, resource) when { context.principal.kind == "` + salt + `" };`,
		}
	case gate.ApprovalRule:
		obj = gate.ApprovalRule{
			Scope: scope, ID: id, Version: 0, Approvers: approvers, Count: len(salt)%2 + 1,
			AuthorMayApprove: false, FreshAuth: gate.NoFreshAuth(), Timeout: 0,
		}
	case gate.OwnershipRule:
		obj = gate.OwnershipRule{
			Scope: scope, ID: id, Version: 0, Mode: gate.ModeEvaluate, Paths: []string{"modules/" + salt + "/**"},
			When: "", Approvers: approvers, Count: 1, FreshAuth: gate.NoFreshAuth(), Reads: nil,
		}
	case gate.PathSet:
		obj = gate.PathSet{Scope: scope, ID: id, Version: 0, Globs: []string{"modules/" + salt + "/**"}}
	default:
	}

	out, ok := obj.(T)
	if !ok {
		t.Fatalf("no sample for %T", zero)
	}

	return out
}

// limitOf returns the most objects of the kind T in one scope.
func limitOf[T gate.Object]() int {
	var zero T

	switch any(zero).(type) {
	case gate.Gate:
		return gate.MaxGatesPerScope
	case gate.AccessLimit:
		return gate.MaxAccessLimitsPerScope
	case gate.ApprovalRule:
		return gate.MaxApprovalRulesPerScope
	case gate.OwnershipRule:
		return gate.MaxOwnershipRulesPerScope
	default:
		return gate.MaxPathSetsPerScope
	}
}

// entry returns a valid audit entry in ns.
func entry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_760_000_000_000), Namespace: ns, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-gates", Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// put stores obj and fails the test on an error.
func (f objFixture[T]) put(t *testing.T, obj T, prev int64) int64 {
	t.Helper()

	v, err := f.objs.Put(t.Context(), obj, prev, f.acme, entry("acme"))
	if err != nil {
		t.Fatalf("Put(%s, prev %d) = %v", describe(obj.Ref()), prev, err)
	}

	return v
}

// mustGet returns the object id of scope and fails the test on an error.
func (f objFixture[T]) mustGet(t *testing.T, scope gate.Scope, id gate.ID) T {
	t.Helper()

	got, err := f.objs.Get(t.Context(), scope, id)
	if err != nil {
		t.Fatalf("Get(%s/%s) = %v", scope, id, err)
	}

	return got
}

// wantErr fails the test when err does not wrap want.
func wantErr(t *testing.T, call string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", call, err, want)
	}
}

// ref returns the reference of version v of the object "a" of scope, which
// the cases write.
func ref(scope gate.Scope, v int64) gate.ObjectRef {
	return gate.ObjectRef{Scope: scope, ID: "a", Version: v}
}

func describe(r gate.ObjectRef) string {
	return fmt.Sprintf("%s/%s@%d", r.Scope, r.ID, r.Version)
}

func testCreate[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	if v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0); v != 1 {
		t.Fatalf("Put(new) = version %d, want 1", v)
	}

	if got := f.mustGet(t, f.acmeScope, "a").Ref(); got.Version != 1 || got.Scope != f.acmeScope || got.ID != "a" {
		t.Fatalf("Get() = %s, want f.acmeScope/a@1", describe(got))
	}
}

func testCreateExists[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)

	_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "a", "two"), 0, f.acme, entry("acme"))
	wantErr(t, "Put(prev 0, exists)", err, gate.ErrConflict)

	if got := f.mustGet(t, f.acmeScope, "a").Ref(); got.Version != 1 {
		t.Fatalf("Get() after the refused put = %s, want version 1", describe(got))
	}
}

func testReplaceStale[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)
	f.put(t, sample[T](t, f.acmeScope, "a", "two"), v)

	_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "a", "three"), v, f.acme, entry("acme"))
	wantErr(t, "Put(stale prev)", err, gate.ErrConflict)
}

func testReplace[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)

	if next := f.put(t, sample[T](t, f.acmeScope, "a", "two"), v); next != v+1 {
		t.Fatalf("Put(replace) = version %d, want %d", next, v+1)
	}
}

func testReplaceUnknown[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "a", "one"), 1, f.acme, entry("acme"))
	wantErr(t, "Put(prev 1, unknown)", err, gate.ErrNotFound)
}

func testOtherScope[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)

	_, err := f.objs.Get(t.Context(), f.globexScope, "a")
	wantErr(t, "Get(other scope)", err, gate.ErrNotFound)

	err = f.objs.Delete(t.Context(), ref(f.globexScope, v), f.globex, entry("globex"))
	wantErr(t, "Delete(other scope)", err, gate.ErrNotFound)

	f.mustGet(t, f.acmeScope, "a")
}

func testDelete[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)

	err := f.objs.Delete(t.Context(), ref(f.acmeScope, v+1), f.acme, entry("acme"))
	wantErr(t, "Delete(stale prev)", err, gate.ErrConflict)

	if derr := f.objs.Delete(t.Context(), ref(f.acmeScope, v), f.acme, entry("acme")); derr != nil {
		t.Fatalf("Delete() = %v", derr)
	}

	_, err = f.objs.Get(t.Context(), f.acmeScope, "a")
	wantErr(t, "Get(after delete)", err, gate.ErrNotFound)

	err = f.objs.Delete(t.Context(), ref(f.acmeScope, v), f.acme, entry("acme"))
	wantErr(t, "Delete(unknown)", err, gate.ErrNotFound)
}

func testStaleFence[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	v := f.put(t, sample[T](t, f.acmeScope, "a", "one"), 0)
	stale := f.acme

	if _, err := f.h.Domain.BumpFence(t.Context(), f.acme); err != nil {
		t.Fatalf("BumpFence() = %v", err)
	}

	_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "b", "one"), 0, stale, entry("acme"))
	wantErr(t, "Put(stale fence)", err, namespace.ErrFenced)

	err = f.objs.Delete(t.Context(), ref(f.acmeScope, v), stale, entry("acme"))
	wantErr(t, "Delete(stale fence)", err, namespace.ErrFenced)

	_, err = f.objs.Get(t.Context(), f.acmeScope, "b")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)
	f.mustGet(t, f.acmeScope, "a")
}

func testWrongFence[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	for _, fence := range []namespace.Fence{f.globex, {Namespace: "", FailoverVersion: 0}} {
		_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "a", "one"), 0, fence, entry("acme"))
		wantErr(t, fmt.Sprintf("Put(fence %+v)", fence), err, gate.ErrInvalid)
	}

	_, err := f.objs.Get(t.Context(), f.acmeScope, "a")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)
}

func testLimit[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	limit := limitOf[T]()

	for i := range limit {
		f.put(t, sample[T](t, f.acmeScope, gate.ID(fmt.Sprintf("o%d", i)), "one"), 0)
	}

	_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "over", "one"), 0, f.acme, entry("acme"))
	wantErr(t, "Put(above the limit)", err, gate.ErrLimit)

	f.put(t, sample[T](t, f.acmeScope, "o0", "two"), 1)

	other := sample[T](t, f.globexScope, "over", "one")
	if _, err := f.objs.Put(t.Context(), other, 0, f.globex, entry("globex")); err != nil {
		t.Fatalf("Put(another scope at the limit of acme) = %v", err)
	}
}

func testBadEntry[T gate.Object](t *testing.T, f objFixture[T]) {
	t.Helper()

	invalid := entry("acme")
	invalid.Subject = ""

	entries := []audit.Entry{entry("globex"), invalid}
	for i := range entries {
		_, err := f.objs.Put(t.Context(), sample[T](t, f.acmeScope, "a", "one"), 0, f.acme, entries[i])
		wantErr(t, "Put(entry of "+entries[i].Namespace.String()+")", err, audit.ErrInvalidEntry)
	}

	_, err := f.objs.Get(t.Context(), f.acmeScope, "a")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)
}
