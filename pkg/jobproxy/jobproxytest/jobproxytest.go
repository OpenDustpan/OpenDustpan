// Package jobproxytest is the conformance suite of the RuleStore port of
// package rulestore. A new adapter runs RunRuleStore with a Factory.
//
// Each case states one rule of the port.
package jobproxytest

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audit returns the
// audit entries of a namespace, oldest first.
type Harness struct {
	Store     rulestore.RuleStore
	Namespace func(t *testing.T, name string) namespace.Fence
	Audit     func(t *testing.T, ns string) []audit.Entry
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	cases := cases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// RunRuleStore runs every case of the suite.
func RunRuleStore(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newHarness(t))
		})
	}
}

// Values of the cases.
const (
	nsAcme  = "acme"
	nsOther = "other"
	unitA   = "u-1"
	unitB   = "u-2"
	actor   = "alice"
	action  = "unit:manage-proxy-rules"
	// staleVersion is a version that no case stores.
	staleVersion = 7
	// writesOfTheAuditCase is the number of writes of writesAppendAudit.
	writesOfTheAuditCase = 3
	// atMs is the time of the audit entries of the cases.
	atMs = 1_790_000_000_000
)

// docA and docB are two valid proxy rule set documents.
func docA() []byte {
	return []byte(`{"rules":[{"id":"api","operations":["plan"],"host":"api.example.com","mode":"inspect",` +
		`"methods":["GET"],"paths":["/api/**"]}]}`)
}

func docB() []byte {
	return []byte(`{"rules":[{"id":"registry","operations":["plan"],"host":"registry.example.com","mode":"tunnel"}]}`)
}

// at is the time of the audit entries of the cases.
func at() time.Time {
	return time.UnixMilli(atMs)
}

// entry returns an audit entry of ns.
func entry(ns string) audit.Entry {
	return audit.Entry{
		At: at(), Namespace: namespace.Name(ns), Subject: actor, IDPSubject: "", TokenID: "", Action: action,
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: "", Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

// unit returns the scope of the unit id in ns.
func unit(ns, id string) rulestore.Scope {
	return rulestore.Scope{Namespace: namespace.Name(ns), UnitID: id}
}

// change returns the write of doc at scope with expected.
func change(scope rulestore.Scope, doc []byte, expected int64) rulestore.Change {
	return rulestore.Change{Scope: scope, Document: doc, Expected: expected}
}

// nsScope returns the scope of the namespace ns.
func nsScope(ns string) rulestore.Scope {
	return rulestore.Scope{Namespace: namespace.Name(ns), UnitID: ""}
}

// put stores doc at scope with expected and fails the test on an error.
func put(t *testing.T, h Harness, scope rulestore.Scope, doc []byte, expected int64) rulestore.Stored {
	t.Helper()

	fence := h.Namespace(t, string(scope.Namespace))

	stored, err := h.Store.Put(t.Context(), change(scope, doc, expected), fence, entry(string(scope.Namespace)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	return stored
}

// wantNotFound fails the test unless scope has no proxy rule set.
func wantNotFound(t *testing.T, h Harness, scope rulestore.Scope) {
	t.Helper()

	if _, err := h.Store.Get(t.Context(), scope); !errors.Is(err, rulestore.ErrNotFound) {
		t.Fatalf("Get: got %v, want ErrNotFound", err)
	}
}

// cases lists the rules of the RuleStore port. Each name states one rule.
func cases() []suiteCase {
	return []suiteCase{
		{"put then get returns the document, its digest and version 1", putThenGet},
		{"a second put at the read version gives version 2", putTwice},
		{"put with version 0 of a scope with a set returns conflict", putCreateConflict},
		{"put with a stale version returns conflict and changes nothing", putStaleConflict},
		{"put with a stale fence returns fenced and changes nothing", putStaleFence},
		{"put with the fence of another namespace returns fenced", putOtherFence},
		{"put of a document that fails parse returns invalid and changes nothing", putInvalidDocument},
		{"put of an invalid scope returns invalid", putInvalidScope},
		{"put with an entry of another namespace returns invalid", putOtherEntry},
		{"each write appends one audit entry", writesAppendAudit},
		{"get of a scope with no set returns not found", missingScope},
		{"a unit scope does not return the set of its namespace", unitIsNotNamespace},
		{"a scope of one namespace does not return a set of another", namespaceIsolation},
		{"delete removes the set", deleteRemoves},
		{"delete of a scope with no set returns not found", deleteMissing},
		{"delete with a stale version or fence changes nothing", deleteStale},
		{"a change of the returned document does not change the stored one", storedIsACopy},
	}
}

func putThenGet(t *testing.T, h Harness) {
	t.Helper()

	stored := put(t, h, unit(nsAcme, unitA), docA(), 0)

	got, err := h.Store.Get(t.Context(), unit(nsAcme, unitA))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	switch {
	case !bytes.Equal(got.Document, docA()):
		t.Fatalf("Document %q, want %q", got.Document, docA())
	case got.Digest != rulestore.DocumentDigest(docA()):
		t.Fatalf("Digest %q, want %q", got.Digest, rulestore.DocumentDigest(docA()))
	case got.Version != 1 || stored.Version != 1:
		t.Fatalf("Version %d and %d, want 1", got.Version, stored.Version)
	case got.UpdatedAt != at().UnixMilli():
		t.Fatalf("UpdatedAt %d, want %d", got.UpdatedAt, at().UnixMilli())
	case got.Scope != unit(nsAcme, unitA):
		t.Fatalf("Scope %+v, want %+v", got.Scope, unit(nsAcme, unitA))
	default:
		// The stored set is the one that the case wrote.
	}
}

func putTwice(t *testing.T, h Harness) {
	t.Helper()

	first := put(t, h, nsScope(nsAcme), docA(), 0)
	second := put(t, h, nsScope(nsAcme), docB(), first.Version)

	if second.Version != 2 || second.Digest != rulestore.DocumentDigest(docB()) {
		t.Fatalf("second put: version %d, digest %q", second.Version, second.Digest)
	}
}

func putCreateConflict(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, unit(nsAcme, unitA), docA(), 0)

	fence := h.Namespace(t, nsAcme)

	_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), docB(), 0), fence, entry(nsAcme))
	if !errors.Is(err, rulestore.ErrConflict) {
		t.Fatalf("Put: got %v, want ErrConflict", err)
	}
}

func putStaleConflict(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, unit(nsAcme, unitA), docA(), 0)

	fence := h.Namespace(t, nsAcme)

	_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), docB(), staleVersion), fence, entry(nsAcme))
	if !errors.Is(err, rulestore.ErrConflict) {
		t.Fatalf("Put: got %v, want ErrConflict", err)
	}

	wantFirstVersion(t, h, unit(nsAcme, unitA), docA())
}

func putStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	fence.FailoverVersion++

	_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), docA(), 0), fence, entry(nsAcme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Put: got %v, want namespace.ErrFenced", err)
	}

	wantNotFound(t, h, unit(nsAcme, unitA))

	if n := len(h.Audit(t, nsAcme)); n != 0 {
		t.Fatalf("a failed put appended %d audit entries", n)
	}
}

func putOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	fence := h.Namespace(t, nsOther)

	_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), docA(), 0), fence, entry(nsAcme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Put: got %v, want namespace.ErrFenced", err)
	}

	wantNotFound(t, h, unit(nsAcme, unitA))
}

func putInvalidDocument(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	// The handler parses the document too. This case sends a document that
	// the parser refuses straight to the store, as if that check had failed.
	hostile := [][]byte{
		[]byte(`{"rules":[],"rules":[]}`),
		[]byte(`{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect",` +
			`"methods":["GET"],"paths":["/a/../b"]}]}`),
		[]byte(`not json`),
	}

	for _, doc := range hostile {
		_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), doc, 0), fence, entry(nsAcme))
		if !errors.Is(err, jobproxy.ErrInvalid) {
			t.Fatalf("Put of %q: got %v, want ErrInvalid", doc, err)
		}
	}

	wantNotFound(t, h, unit(nsAcme, unitA))

	if n := len(h.Audit(t, nsAcme)); n != 0 {
		t.Fatalf("a failed put appended %d audit entries", n)
	}
}

func putInvalidScope(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	for _, scope := range []rulestore.Scope{unit("", unitA), unit(nsAcme, "u/1"), unit(nsAcme, "u 1")} {
		_, err := h.Store.Put(t.Context(), change(scope, docA(), 0), fence, entry(nsAcme))
		if !errors.Is(err, jobproxy.ErrInvalid) {
			t.Fatalf("Put at %+v: got %v, want ErrInvalid", scope, err)
		}
	}
}

func putOtherEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	_, err := h.Store.Put(t.Context(), change(unit(nsAcme, unitA), docA(), 0), fence, entry(nsOther))
	if !errors.Is(err, jobproxy.ErrInvalid) {
		t.Fatalf("Put: got %v, want ErrInvalid", err)
	}

	wantNotFound(t, h, unit(nsAcme, unitA))
}

func writesAppendAudit(t *testing.T, h Harness) {
	t.Helper()

	first := put(t, h, unit(nsAcme, unitA), docA(), 0)
	second := put(t, h, unit(nsAcme, unitA), docB(), first.Version)

	fence := h.Namespace(t, nsAcme)
	if err := h.Store.Delete(t.Context(), unit(nsAcme, unitA), second.Version, fence, entry(nsAcme)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	entries := h.Audit(t, nsAcme)
	if len(entries) != writesOfTheAuditCase {
		t.Fatalf("got %d audit entries, want %d", len(entries), writesOfTheAuditCase)
	}

	for i := range entries {
		e := &entries[i]
		if e.Action != action || e.Subject != actor {
			t.Fatalf("audit entry %+v is not the entry of the write", e)
		}
	}
}

func missingScope(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	wantNotFound(t, h, unit(nsAcme, unitA))
	wantNotFound(t, h, nsScope(nsAcme))
}

func unitIsNotNamespace(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, nsScope(nsAcme), docA(), 0)
	wantNotFound(t, h, unit(nsAcme, unitA))

	put(t, h, unit(nsAcme, unitB), docB(), 0)
	wantFirstVersion(t, h, nsScope(nsAcme), docA())
}

func namespaceIsolation(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, unit(nsAcme, unitA), docA(), 0)
	h.Namespace(t, nsOther)

	wantNotFound(t, h, unit(nsOther, unitA))
	wantNotFound(t, h, nsScope(nsOther))
}

func deleteRemoves(t *testing.T, h Harness) {
	t.Helper()

	stored := put(t, h, unit(nsAcme, unitA), docA(), 0)
	fence := h.Namespace(t, nsAcme)

	if err := h.Store.Delete(t.Context(), unit(nsAcme, unitA), stored.Version, fence, entry(nsAcme)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	wantNotFound(t, h, unit(nsAcme, unitA))
}

func deleteMissing(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	err := h.Store.Delete(t.Context(), unit(nsAcme, unitA), 1, fence, entry(nsAcme))
	if !errors.Is(err, rulestore.ErrNotFound) {
		t.Fatalf("Delete: got %v, want ErrNotFound", err)
	}
}

func deleteStale(t *testing.T, h Harness) {
	t.Helper()

	stored := put(t, h, unit(nsAcme, unitA), docA(), 0)
	fence := h.Namespace(t, nsAcme)

	err := h.Store.Delete(t.Context(), unit(nsAcme, unitA), stored.Version+1, fence, entry(nsAcme))
	if !errors.Is(err, rulestore.ErrConflict) {
		t.Fatalf("Delete: got %v, want ErrConflict", err)
	}

	stale := fence
	stale.FailoverVersion++

	err = h.Store.Delete(t.Context(), unit(nsAcme, unitA), stored.Version, stale, entry(nsAcme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Delete: got %v, want namespace.ErrFenced", err)
	}

	wantFirstVersion(t, h, unit(nsAcme, unitA), docA())

	if n := len(h.Audit(t, nsAcme)); n != 1 {
		t.Fatalf("got %d audit entries, want the 1 of the put", n)
	}
}

func storedIsACopy(t *testing.T, h Harness) {
	t.Helper()

	doc := docA()
	put(t, h, unit(nsAcme, unitA), doc, 0)
	doc[0] = 'X'

	got, err := h.Store.Get(t.Context(), unit(nsAcme, unitA))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got.Document[0] = 'Y'

	wantFirstVersion(t, h, unit(nsAcme, unitA), docA())
}

// wantFirstVersion fails the test unless scope holds doc at version 1.
func wantFirstVersion(t *testing.T, h Harness, scope rulestore.Scope, doc []byte) {
	t.Helper()

	const version = 1

	t.Helper()

	got, err := h.Store.Get(t.Context(), scope)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !bytes.Equal(got.Document, doc) || got.Version != version {
		t.Fatalf("Get: document %q at version %d, want %q at version %d", got.Document, got.Version, doc, version)
	}
}
