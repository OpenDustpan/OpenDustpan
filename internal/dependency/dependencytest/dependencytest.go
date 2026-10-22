// Package dependencytest is the conformance suite for dependency.Store.
// Every built-in store runs it, and a custom store should too:
//
//	func TestDependencyConformance(t *testing.T) {
//		t.Parallel()
//		dependencytest.Run(t, func(t *testing.T) dependencytest.Store { return newMyStore(t) })
//	}
package dependencytest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/dependency"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is every persistence port the suite exercises: namespaces for the
// fence, and dependency.Store under test.
type Store interface {
	namespace.Store
	dependency.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "a new edge reads back as written", test: testRecordNew},
		{name: "a second write keeps the first time and adds the reads", test: testRecordAgain},
		{name: "an ambiguous write marks the stored edge", test: testRecordAmbiguous},
		{name: "a principal edge and a unit edge are two edges", test: testPrincipalAndUnit},
		{name: "dependents answer newest first and leave out old edges", test: testDependentsSince},
		{name: "sources answer the edges of a dependent unit", test: testSources},
		{
			name: "drop deletes the edges of a purged unit in its namespace and keeps those of another namespace",
			test: testDropUnit,
		},
		{name: "an invalid edge is refused", test: testInvalidEdge},
		{name: "a stale fence refuses record and drop", test: testFenced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// suiteTime() returns the fixed time of the suite.
func suiteTime() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// fenceVersion is the failover version of the namespaces of the suite.
const fenceVersion = 2

// makeNamespace creates the namespace name and returns its fence.
func makeNamespace(t *testing.T, s Store, name namespace.Name) namespace.Fence {
	t.Helper()

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: suiteTime(), Name: name, HomeRegion: "local", FailoverVersion: fenceVersion,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return namespace.Fence{Namespace: name, FailoverVersion: fenceVersion}
}

// unit returns a unit of tool in ns.
func unit(ns namespace.Name, tool, id string) history.Unit {
	return history.Unit{Namespace: ns, Tool: tool, ID: id}
}

// lockEdge returns a lock edge from dependent to source, seen at at.
func lockEdge(source, dependent history.Unit, at time.Time) dependency.Edge {
	return dependency.Edge{
		FirstSeen: at, LastSeen: at, Dependent: &dependent, Source: source, Principal: "ci",
		Kind: dependency.KindOutputRead, Evidence: dependency.EvidenceLock, Reads: 1, Ambiguous: false,
	}
}

// principalEdge returns a principal edge to source, seen at at.
func principalEdge(source history.Unit, at time.Time) dependency.Edge {
	return dependency.Edge{
		FirstSeen: at, LastSeen: at, Dependent: nil, Source: source, Principal: "ana",
		Kind: dependency.KindOutputRead, Evidence: dependency.EvidencePrincipal, Reads: 1, Ambiguous: false,
	}
}

// mustRecord records e.
func mustRecord(t *testing.T, s Store, e dependency.Edge, fence namespace.Fence) {
	t.Helper()

	if err := s.RecordEdge(t.Context(), e, fence); err != nil {
		t.Fatalf("record edge: %v", err)
	}
}

// dependents reads the dependents of source since the zero time.
func dependents(t *testing.T, s Store, source history.Unit) []dependency.Edge {
	t.Helper()

	got, err := s.Dependents(t.Context(), source, time.Time{})
	if err != nil {
		t.Fatalf("dependents: %v", err)
	}

	return got
}

func testRecordNew(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")
	e := lockEdge(src, unit("acme", "pulumi", "stk-app"), suiteTime())
	mustRecord(t, s, e, fence)

	got := dependents(t, s, src)
	if len(got) != 1 || !dependency.SameEdge(got[0], e) || got[0].Reads != 1 || !got[0].FirstSeen.Equal(suiteTime()) {
		t.Errorf("dependents = %+v, want the edge %+v", got, e)
	}
}

func testRecordAgain(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")
	dep := unit("acme", "pulumi", "stk-app")
	mustRecord(t, s, lockEdge(src, dep, suiteTime()), fence)
	later := suiteTime().Add(2 * dependency.RecordInterval)
	mustRecord(t, s, lockEdge(src, dep, later), fence)

	got := dependents(t, s, src)
	if len(got) != 1 {
		t.Fatalf("dependents = %d edges, want 1", len(got))
	}

	if !got[0].FirstSeen.Equal(suiteTime()) || !got[0].LastSeen.Equal(later) || got[0].Reads != 2 {
		t.Errorf("edge after two writes = %+v, want first %v, last %v, 2 reads", got[0], suiteTime(), later)
	}
}

func testRecordAmbiguous(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")
	dep := unit("acme", "pulumi", "stk-app")
	mustRecord(t, s, lockEdge(src, dep, suiteTime()), fence)

	again := lockEdge(src, dep, suiteTime().Add(dependency.RecordInterval))
	again.Ambiguous = true
	mustRecord(t, s, again, fence)

	if got := dependents(t, s, src); len(got) != 1 || !got[0].Ambiguous {
		t.Errorf("edge after an ambiguous write = %+v, want ambiguous", got)
	}
}

func testPrincipalAndUnit(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")
	mustRecord(t, s, lockEdge(src, unit("acme", "pulumi", "stk-app"), suiteTime()), fence)
	mustRecord(t, s, principalEdge(src, suiteTime()), fence)

	if got := dependents(t, s, src); len(got) != 2 {
		t.Errorf("dependents = %d edges, want 2", len(got))
	}
}

func testDependentsSince(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")
	old := lockEdge(src, unit("acme", "pulumi", "stk-old"), suiteTime())
	mid := lockEdge(src, unit("acme", "pulumi", "stk-mid"), suiteTime().Add(24*time.Hour))
	recent := principalEdge(src, suiteTime().Add(48*time.Hour))

	items := []dependency.Edge{old, mid, recent}
	for i := range items {
		e := items[i]
		mustRecord(t, s, e, fence)
	}

	mustRecord(
		t,
		s,
		lockEdge(unit("acme", "opentofu", "ws-other"), unit("acme", "pulumi", "stk-x"), suiteTime()),
		fence,
	)

	got, err := s.Dependents(t.Context(), src, suiteTime().Add(time.Hour))
	if err != nil {
		t.Fatalf("dependents: %v", err)
	}

	if len(got) != 2 || !dependency.SameEdge(got[0], recent) || !dependency.SameEdge(got[1], mid) {
		t.Errorf("dependents since = %+v, want the recent edge, then the middle edge", got)
	}
}

func testSources(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	dep := unit("acme", "pulumi", "stk-app")
	mustRecord(t, s, lockEdge(unit("acme", "opentofu", "ws-net"), dep, suiteTime()), fence)
	mustRecord(t, s, lockEdge(unit("acme", "opentofu", "ws-dns"), dep, suiteTime().Add(time.Hour)), fence)
	mustRecord(t, s, lockEdge(unit("acme", "opentofu", "ws-dns"), unit("acme", "pulumi", "stk-y"), suiteTime()), fence)

	got, err := s.Sources(t.Context(), dep, time.Time{})
	if err != nil {
		t.Fatalf("sources: %v", err)
	}

	if len(got) != 2 || got[0].Source.ID != "ws-dns" || got[1].Source.ID != "ws-net" {
		t.Errorf("sources = %+v, want ws-dns, then ws-net", got)
	}
}

func testDropUnit(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	other := makeNamespace(t, s, "beta")
	gone := unit("acme", "pulumi", "stk-gone")
	net := unit("acme", "opentofu", "ws-net")
	far := unit("beta", "opentofu", "ws-far")

	mustRecord(t, s, lockEdge(gone, unit("acme", "pulumi", "stk-app"), suiteTime()), fence)
	mustRecord(t, s, lockEdge(net, gone, suiteTime()), fence)
	mustRecord(t, s, lockEdge(far, gone, suiteTime()), other)

	if err := s.DropUnit(t.Context(), gone, fence); err != nil {
		t.Fatalf("drop: %v", err)
	}

	if got := dependents(t, s, gone); len(got) != 0 {
		t.Errorf("dependents after drop = %+v, want none", got)
	}

	if got := dependents(t, s, net); len(got) != 0 {
		t.Errorf("edges of the same namespace to the purged unit = %+v, want none", got)
	}

	if got := dependents(t, s, far); len(got) != 1 {
		t.Errorf("edges of another namespace to the purged unit = %d, want 1 kept", len(got))
	}

	if err := s.DropUnit(t.Context(), gone, fence); err != nil {
		t.Errorf("second drop: %v", err)
	}
}

func testInvalidEdge(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	src := unit("acme", "opentofu", "ws-net")

	self := lockEdge(src, src, suiteTime())
	noUnit := lockEdge(src, src, suiteTime())
	noUnit.Dependent = nil

	items := map[string]dependency.Edge{"a unit on itself": self, "a lock edge with no unit": noUnit}
	for name := range items {
		e := items[name]
		if err := s.RecordEdge(t.Context(), e, fence); !errors.Is(err, dependency.ErrInvalidEdge) {
			t.Errorf("%s: err = %v, want ErrInvalidEdge", name, err)
		}
	}
}

func testFenced(t *testing.T, s Store) {
	t.Helper()
	fence := makeNamespace(t, s, "acme")
	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}
	src := unit("acme", "opentofu", "ws-net")

	if err := s.RecordEdge(t.Context(), principalEdge(src, suiteTime()), stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("record with a stale fence err = %v, want ErrFenced", err)
	}

	if got := dependents(t, s, src); len(got) != 0 {
		t.Errorf("a fenced record left %d edges", len(got))
	}

	mustRecord(t, s, principalEdge(src, suiteTime()), fence)

	if err := s.DropUnit(t.Context(), src, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("drop with a stale fence err = %v, want ErrFenced", err)
	}
}
