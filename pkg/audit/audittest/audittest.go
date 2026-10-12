// Package audittest is the conformance suite for audit.Store
// implementations. Every built-in adapter runs it, and custom adapters
// should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		audittest.Run(t, func(t *testing.T) audittest.TestStore { return ... })
//	}
package audittest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is every persistence port the suite exercises: namespaces for
// fixtures and the audit store under test.
type Store interface {
	namespace.Store
	audit.Store
}

// TestStore is one fresh, empty store with hooks that change or remove a
// stored entry behind the back of the store. The suite uses them to prove
// that Check finds a changed field and a deleted entry. Production code has
// no update or delete path: the hooks exist only for this suite.
type TestStore struct {
	Store Store
	// TamperEntry changes one stored field of the entry with seq in ns.
	// Field names one of: subject, action, object, outcome, kind.
	TamperEntry func(ctx context.Context, ns namespace.Name, seq int64, field, value string) error
	// DeleteEntry removes the entry with seq in ns.
	DeleteEntry func(ctx context.Context, ns namespace.Name, seq int64) error
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) TestStore

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s TestStore)
		name string
	}{
		{name: "append then list returns the entry with its chain", test: testAppendList},
		{name: "list is newest first with paging", test: testListPaging},
		{name: "append with a stale fence fails", test: testStaleFence},
		{name: "entries of another namespace are not listed", test: testIsolation},
		{name: "concurrent appends form one valid chain", test: testConcurrentAppends},
		{name: "check finds a changed field", test: testCheckChangedField},
		{name: "check finds a deleted entry", test: testCheckDeletedEntry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// now is a fixed instant: stores keep milliseconds.
func now() time.Time {
	return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC).UTC()
}

// newNamespace creates the namespace acme homed in eu-west at failover
// version 1.
func newNamespace(t *testing.T, s Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", ns.Name, err)
	}

	return ns
}

// newEntry returns a valid reveal entry in ns with the given subject and
// request ID.
func newEntry(ns namespace.Name, subject, requestID string, at time.Time) audit.Entry {
	return audit.Entry{
		At:         at,
		Namespace:  ns,
		Subject:    subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:decrypt",
		Object:     "pulumi:" + ns.String() + "/web/prod",
		Version:    "",
		ValuePath:  "",
		SourceIP:   "192.0.2.1",
		UserAgent:  "pulumi-cli/3.200.0",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestID,
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// appendEntry appends e with fence and fails the test on error.
func appendEntry(t *testing.T, s Store, fence namespace.Fence, e audit.Entry) audit.Entry {
	t.Helper()

	stored, err := s.Append(t.Context(), fence, e)
	if err != nil {
		t.Fatalf("append audit entry: %v", err)
	}

	return stored
}

// checkValidChain fails the test unless entries form one chain newest last:
// sequences run 1 to n, each hash matches its fields, and each previous hash
// links to its predecessor.
func checkValidChain(t *testing.T, entries []audit.Entry) {
	t.Helper()

	for i := range entries {
		checkChainLink(t, entries, i)
	}
}

// checkChainLink fails the test unless entries[i] continues the chain:
// its sequence follows its position, its previous hash links to its
// predecessor, and its hash matches its fields.
func checkChainLink(t *testing.T, entries []audit.Entry, i int) {
	t.Helper()

	e := entries[i]

	wantSeq := int64(i + 1)
	if e.Sequence != wantSeq {
		t.Fatalf("entry %d has sequence %d, want %d", i, e.Sequence, wantSeq)
	}

	var prev string
	if i > 0 {
		prev = entries[i-1].Hash
	}

	if e.PrevHash != prev {
		t.Fatalf("entry %d has previous hash %q, want %q", e.Sequence, e.PrevHash, prev)
	}

	if got := audit.ComputeHash(e, e.PrevHash); got != e.Hash {
		t.Fatalf("entry %d has hash %q, want %q", e.Sequence, e.Hash, got)
	}
}

// testAppendList proves that an append stores the entry with sequence 1, an
// empty previous hash and a matching hash, and that a list answers it with
// the total.
func testAppendList(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	stored := appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))
	checkFirstEntry(t, stored)

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	checkSingleListing(t, listing)

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != 1 {
		t.Fatalf("check = %+v, want valid with 1 entry", check)
	}
}

// checkFirstEntry fails the test unless stored is the first entry of its
// namespace: sequence 1, an empty previous hash and a matching hash.
func checkFirstEntry(t *testing.T, stored audit.Entry) {
	t.Helper()

	if stored.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", stored.Sequence)
	}

	if stored.PrevHash != "" {
		t.Fatalf("previous hash = %q, want empty", stored.PrevHash)
	}

	if got := audit.ComputeHash(stored, ""); got != stored.Hash {
		t.Fatalf("hash = %q, want %q", stored.Hash, got)
	}
}

// checkSingleListing fails the test unless listing holds exactly the
// appended reveal entry with its total.
func checkSingleListing(t *testing.T, listing audit.Listing) {
	t.Helper()

	if listing.Total != 1 {
		t.Fatalf("total = %d, want 1", listing.Total)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(listing.Entries))
	}

	got := listing.Entries[0]
	if got.Subject != "admin" || got.RequestID != "req-1" || got.Kind != audit.KindReveal {
		t.Fatalf("listed entry = %+v, want the appended entry", got)
	}
}

// testListPaging appends five entries and proves that the list answers them
// newest first across pages of two.
func testListPaging(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	for i := 1; i <= 5; i++ {
		appendEntry(t, s, ns.Fence(), newEntry(ns.Name, fmt.Sprintf("user-%d", i), fmt.Sprintf("req-%d", i), now()))
	}

	checkPagingFirst(t, s, ns.Name)
	checkPagingRest(t, s, ns.Name)
}

// checkPagingFirst proves the first page holds the two newest entries with
// the total.
func checkPagingFirst(t *testing.T, s Store, ns namespace.Name) {
	t.Helper()

	first, err := s.List(t.Context(), ns, audit.Page{Number: 1, Size: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}

	if first.Total != 5 {
		t.Fatalf("total = %d, want 5", first.Total)
	}

	if len(first.Entries) != 2 || first.Entries[0].Subject != "user-5" || first.Entries[1].Subject != "user-4" {
		t.Fatalf("page 1 = %+v, want user-5 then user-4", first.Entries)
	}
}

// checkPagingRest proves the second page holds the next two entries and the
// third page holds the oldest one.
func checkPagingRest(t *testing.T, s Store, ns namespace.Name) {
	t.Helper()

	second, err := s.List(t.Context(), ns, audit.Page{Number: 2, Size: 2})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}

	if len(second.Entries) != 2 || second.Entries[0].Subject != "user-3" || second.Entries[1].Subject != "user-2" {
		t.Fatalf("page 2 = %+v, want user-3 then user-2", second.Entries)
	}

	third, err := s.List(t.Context(), ns, audit.Page{Number: 3, Size: 2})
	if err != nil {
		t.Fatalf("list page 3: %v", err)
	}

	if len(third.Entries) != 1 || third.Entries[0].Subject != "user-1" {
		t.Fatalf("page 3 = %+v, want user-1", third.Entries)
	}
}

// testStaleFence proves that an append with an old failover version fails
// and stores nothing.
func testStaleFence(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion - 1}

	if _, err := s.Append(t.Context(), stale, newEntry(ns.Name, "admin", "req-1", now())); err == nil {
		t.Fatal("append with a stale fence succeeded, want a fence error")
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if listing.Total != 0 {
		t.Fatalf("total = %d, want 0", listing.Total)
	}
}

// testIsolation proves that entries of one namespace never appear in the
// list or the check of another namespace.
func testIsolation(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	other := namespace.Namespace{
		CreatedAt:       now(),
		Name:            "other",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("create namespace %q: %v", other.Name, err)
	}

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))

	listing, err := s.List(t.Context(), other.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries of the other namespace: %v", err)
	}

	if listing.Total != 0 || len(listing.Entries) != 0 {
		t.Fatalf("other namespace lists %+v, want empty", listing)
	}

	check, err := s.Check(t.Context(), other.Name)
	if err != nil {
		t.Fatalf("check the other namespace: %v", err)
	}

	if !check.Valid || check.Checked != 0 {
		t.Fatalf("check of the other namespace = %+v, want valid with 0 entries", check)
	}
}

// testConcurrentAppends appends 16 entries from goroutines and proves that
// they form one valid chain: every sequence appears exactly once.
func testConcurrentAppends(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	const n = 16

	var wg sync.WaitGroup

	errs := make(chan error, n)

	for i := range n {
		wg.Go(func() {
			_, err := s.Append(
				context.Background(),
				ns.Fence(),
				newEntry(ns.Name, fmt.Sprintf("user-%d", i), fmt.Sprintf("req-%d", i), now()),
			)
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != n {
		t.Fatalf("check = %+v, want valid with %d entries", check, n)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: n})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	checkSequencesUnique(t, listing, n)
	checkStoredChain(t, listing)
}

// checkSequencesUnique fails the test unless listing holds each sequence
// from 1 to n exactly once.
func checkSequencesUnique(t *testing.T, listing audit.Listing, n int) {
	t.Helper()

	seen := make(map[int64]bool, n)

	for i := range listing.Entries {
		seq := listing.Entries[i].Sequence
		if seq < 1 || seq > int64(n) || seen[seq] {
			t.Fatalf("duplicate or out-of-range sequence %d in %+v", seq, listing.Entries)
		}

		seen[seq] = true
	}
}

// checkStoredChain fails the test unless the listed entries form one valid
// chain: it orders them oldest first and checks every link.
func checkStoredChain(t *testing.T, listing audit.Listing) {
	t.Helper()

	ordered := make([]audit.Entry, len(listing.Entries))
	for i := range listing.Entries {
		ordered[i] = listing.Entries[len(listing.Entries)-1-i]
	}

	checkValidChain(t, ordered)
}

// testCheckChangedField proves that Check reports the sequence of an entry
// whose field changed behind the back of the store.
func testCheckChangedField(t *testing.T, ts TestStore) {
	t.Helper()

	if ts.TamperEntry == nil {
		t.Fatal("suite needs TamperEntry to prove the chain check")
	}

	s := ts.Store
	ns := newNamespace(t, s)

	for i := 1; i <= 3; i++ {
		appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", fmt.Sprintf("req-%d", i), now()))
	}

	if err := ts.TamperEntry(t.Context(), ns.Name, 2, "subject", "mallory"); err != nil {
		t.Fatalf("change entry 2: %v", err)
	}

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if check.Valid {
		t.Fatal("check is valid after a field changed, want invalid")
	}

	if check.BadSequence != 2 {
		t.Fatalf("bad sequence = %d, want 2", check.BadSequence)
	}
}

// testCheckDeletedEntry proves that Check reports the sequence after a
// deleted entry: the link of the next entry points at a missing hash.
func testCheckDeletedEntry(t *testing.T, ts TestStore) {
	t.Helper()

	if ts.DeleteEntry == nil {
		t.Fatal("suite needs DeleteEntry to prove the chain check")
	}

	s := ts.Store
	ns := newNamespace(t, s)

	for i := 1; i <= 3; i++ {
		appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", fmt.Sprintf("req-%d", i), now()))
	}

	if err := ts.DeleteEntry(t.Context(), ns.Name, 2); err != nil {
		t.Fatalf("delete entry 2: %v", err)
	}

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if check.Valid {
		t.Fatal("check is valid after a deletion, want invalid")
	}

	if check.BadSequence != 3 {
		t.Fatalf("bad sequence = %d, want 3", check.BadSequence)
	}
}
