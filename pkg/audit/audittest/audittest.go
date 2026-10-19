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
	"errors"
	"fmt"
	"strings"
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
	audit.ServerStore
}

// TestStore is one fresh, empty store with hooks that change or remove a
// stored entry behind the back of the store. The suite uses them to prove
// that Check finds a changed field and a deleted entry. Production code has
// no update or delete path: the hooks exist only for this suite.
type TestStore struct {
	Store Store
	// TamperEntry changes one stored field of the entry with seq in ns.
	// Field names a column of the entry: subject, idp_subject, token_id,
	// action, object, version, value_path, value_count, source_ip,
	// user_agent, channel, kind, outcome, request_id, object_id or changes.
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
		{name: "append to a missing namespace fails", test: testMissingNamespace},
		{name: "entries of another namespace are not listed", test: testIsolation},
		{name: "concurrent appends form one valid chain", test: testConcurrentAppends},
		{name: "check finds a changed field", test: testCheckChangedField},
		{name: "check finds a deleted entry", test: testCheckDeletedEntry},
		{name: "check pages a chain longer than one page", test: testCheckPagesLongChain},
		{name: "a nil count and a set count round-trip with a valid chain", test: testValueCountRoundTrip},
		{name: "an empty and a set object ID round-trip with a valid chain", test: testObjectIDRoundTrip},
		{name: "the server scope keeps its own chain", test: testServerScopeChain},
		{name: "the server scope refuses an entry of another scope", test: testServerScopeRefusesNamespace},
		{name: "a namespace append refuses the server scope", test: testNamespaceRefusesServerScope},
		{name: "concurrent server scope appends form one valid chain", test: testConcurrentServerAppends},
		{name: "an unknown kind is refused and change is accepted", test: testChangeKind},
		{name: "changes round-trip in canonical order with a valid chain", test: testChangesRoundTrip},
		{name: "check finds a changed value", test: testCheckChangedChanges(
			`[{"key":"database","new":"set","layer":"env"},` +
				`{"key":"log-level","old":"info","new":"warn","layer":"file","takesEffect":"next start"},` +
				`{"key":"region","new":"eu-west","layer":"file"}]`,
		)},
		{name: "check finds changes in another order", test: testCheckChangedChanges(
			`[{"key":"region","new":"eu-west","layer":"file"},` +
				`{"key":"database","new":"set","layer":"env"},` +
				`{"key":"log-level","old":"info","new":"debug","layer":"file","takesEffect":"next start"}]`,
		)},
		{name: "check finds changes that are not JSON", test: testCheckChangedChanges(`{`)},
		{name: "check finds a count set where none was", test: testCheckChangedColumn("value_count", "0")},
		{name: "check finds a changed object ID", test: testCheckChangedColumn("object_id", "stk-x")},
		{name: "check finds a changed idp subject", test: testCheckChangedColumn("idp_subject", "idp-x")},
		{name: "check finds a changed token ID", test: testCheckChangedColumn("token_id", "tok-x")},
		{name: "check finds a changed version", test: testCheckChangedColumn("version", "v9")},
		{name: "check finds a changed value path", test: testCheckChangedColumn("value_path", "secret.x")},
		{name: "check finds a changed source IP", test: testCheckChangedColumn("source_ip", "198.51.100.9")},
		{name: "check finds a changed user agent", test: testCheckChangedColumn("user_agent", "other/1.0")},
		{name: "check finds a changed channel", test: testCheckChangedColumn("channel", "startup")},
		{name: "check finds a changed kind", test: testCheckChangedColumn("kind", "change")},
		{name: "check finds a changed outcome", test: testCheckChangedColumn("outcome", "failure")},
		{name: "check finds a changed request ID", test: testCheckChangedColumn("request_id", "req-x")},
		{name: "check finds a changed reason", test: testCheckChangedColumn("reason", "reason-x")},
		{name: "an empty and a set reason round-trip with a valid chain", test: testReasonRoundTrip},
		{name: "a reason with a control character is refused", test: testReasonRefused},
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
		ID:              namespace.NewID(),
		CreatedAt:       now(),
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
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
		At:           at,
		Namespace:    ns,
		Subject:      subject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       "unit:decrypt",
		Object:       "pulumi:" + ns.String() + "/web/prod",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "pulumi-cli/3.200.0",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindReveal,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    requestID,
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
		Reason:       "",
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
// with namespace.ErrFenced and stores nothing. A driver error or a generic
// error must not pass: callers fence on the sentinel.
func testStaleFence(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion - 1}

	_, err := s.Append(t.Context(), stale, newEntry(ns.Name, "admin", "req-1", now()))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("append with a stale fence: err = %v, want %v", err, namespace.ErrFenced)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if listing.Total != 0 {
		t.Fatalf("total = %d, want 0", listing.Total)
	}
}

// testMissingNamespace proves that an append to a missing namespace fails
// with namespace.ErrNotFound and stores nothing.
func testMissingNamespace(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	missing := namespace.Name("ghost")
	fence := namespace.Fence{Namespace: missing, FailoverVersion: 1}

	entry := newEntry(missing, "admin", "req-1", now())

	_, err := s.Append(t.Context(), fence, entry)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("append to a missing namespace: err = %v, want %v", err, namespace.ErrNotFound)
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
		ID:              namespace.NewID(),
		CreatedAt:       now(),
		Name:            "other",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
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

// testCheckPagesLongChain proves that Check walks a chain longer than one
// check page in bounded pages: it appends past audit.CheckPageSize entries
// and the check still answers valid with every entry checked. A walk that
// reads the whole chain in one query holds every row at once, so this case
// pins the paged path: a page boundary that drops or repeats an entry fails
// it.
func testCheckPagesLongChain(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	const total = 2*audit.CheckPageSize + 5

	for i := 1; i <= total; i++ {
		appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", fmt.Sprintf("req-%d", i), now()))
	}

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != total {
		t.Fatalf("check = %+v, want valid with %d entries", check, total)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if listing.Total != total {
		t.Fatalf("total = %d, want %d", listing.Total, total)
	}
}

// testValueCountRoundTrip proves that an entry with a nil count and an
// entry with a set count round-trip through Append and List, and that the
// chain check passes on a chain that holds both: the hash covers the
// count field of each entry.
func testValueCountRoundTrip(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))

	count := int64(2)
	withCount := newEntry(ns.Name, "admin", "req-2", now())
	withCount.Kind = audit.KindToolOperation
	withCount.ValueCount = &count
	stored := appendEntry(t, s, ns.Fence(), withCount)

	if stored.ValueCount == nil || *stored.ValueCount != count {
		t.Fatalf("stored value count = %+v, want %d", stored.ValueCount, count)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if listing.Total != 2 || len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v, want 2 entries", listing)
	}

	// The list answers newest first: the entry with the set count, then
	// the entry with a nil count.
	if got := listing.Entries[0].ValueCount; got == nil || *got != count {
		t.Fatalf("newest value count = %+v, want %d", got, count)
	}

	if got := listing.Entries[1].ValueCount; got != nil {
		t.Fatalf("oldest value count = %+v, want nil", got)
	}

	ordered := []audit.Entry{listing.Entries[1], listing.Entries[0]}
	checkValidChain(t, ordered)

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != 2 {
		t.Fatalf("check = %+v, want valid with 2 entries", check)
	}
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

// testCheckChangedColumn answers a test that proves Check reports the
// sequence of an entry whose column field changed to value behind the back
// of the store.
func testCheckChangedColumn(field, value string) func(t *testing.T, ts TestStore) {
	return func(t *testing.T, ts TestStore) {
		t.Helper()

		if ts.TamperEntry == nil {
			t.Fatal("suite needs TamperEntry to prove the chain check")
		}

		s := ts.Store
		ns := newNamespace(t, s)

		for i := 1; i <= 3; i++ {
			appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", fmt.Sprintf("req-%d", i), now()))
		}

		if err := ts.TamperEntry(t.Context(), ns.Name, 2, field, value); err != nil {
			t.Fatalf("change %s of entry 2: %v", field, err)
		}

		check, err := s.Check(t.Context(), ns.Name)
		if err != nil {
			t.Fatalf("check audit chain: %v", err)
		}

		if check.Valid || check.BadSequence != 2 {
			t.Fatalf("check after a change of %s = %+v, want bad sequence 2", field, check)
		}
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

// newServerEntry returns a valid change entry in ServerScope with the given
// subject and request ID.
func newServerEntry(subject, requestID string) audit.Entry {
	e := newEntry(audit.ServerScope, subject, requestID, now())
	e.Action = "server:manage-principals"
	e.Object = "principal:prn-1"
	e.Kind = audit.KindChange

	return e
}

// testServerScopeChain proves that the server scope numbers its entries
// from 1 in a chain of its own, apart from the chain of a namespace, and
// that List and Check read it.
func testServerScopeChain(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-ns", now()))

	for i := range 3 {
		stored, err := s.AppendServer(t.Context(), newServerEntry("admin", fmt.Sprintf("req-%d", i)))
		if err != nil {
			t.Fatalf("append server entry: %v", err)
		}

		if stored.Sequence != int64(i+1) || stored.Namespace != audit.ServerScope || stored.Kind != audit.KindChange {
			t.Fatalf("stored server entry = %+v, want sequence %d in %s", stored, i+1, audit.ServerScope)
		}
	}

	checkServerScopeReads(t, s, ns.Name)
}

// checkServerScopeReads proves that List and Check read the three entries
// of the server scope, and that the namespace ns lists only its own entry.
func checkServerScopeReads(t *testing.T, s Store, ns namespace.Name) {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	if listing.Total != 3 || len(listing.Entries) != 3 {
		t.Fatalf("server listing = %d of %d, want 3", len(listing.Entries), listing.Total)
	}

	checkStoredChain(t, listing)

	check, err := s.Check(t.Context(), audit.ServerScope)
	if err != nil {
		t.Fatalf("check server chain: %v", err)
	}

	if !check.Valid || check.Checked != 3 {
		t.Fatalf("server check = %+v, want valid with 3 entries", check)
	}

	nsListing, err := s.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil || nsListing.Total != 1 {
		t.Fatalf("namespace listing = %+v, %v; want only its own entry", nsListing, err)
	}
}

// testServerScopeRefusesNamespace proves that AppendServer refuses an entry
// that names a namespace and an entry that fails validation.
func testServerScopeRefusesNamespace(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	_, err := s.AppendServer(t.Context(), newEntry(ns.Name, "admin", "req-1", now()))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("server append of a namespace entry = %v, want ErrInvalidEntry", err)
	}

	bad := newServerEntry("", "req-2")

	_, err = s.AppendServer(t.Context(), bad)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("server append with no subject = %v, want ErrInvalidEntry", err)
	}
}

// testNamespaceRefusesServerScope proves that Append never writes the
// server scope: no namespace has its name, so no fence names it.
func testNamespaceRefusesServerScope(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	fence := namespace.Fence{Namespace: audit.ServerScope, FailoverVersion: 1}

	_, err := s.Append(t.Context(), fence, newServerEntry("admin", "req-1"))
	if err == nil {
		t.Fatal("append through a fence that names the server scope succeeded, want an error")
	}
}

// testConcurrentServerAppends proves that appends of the server scope that
// run at the same time form one sequence and one valid chain.
func testConcurrentServerAppends(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store

	const n = 16

	var wg sync.WaitGroup

	errs := make(chan error, n)

	for i := range n {
		wg.Go(func() {
			_, err := s.AppendServer(
				context.Background(),
				newServerEntry(fmt.Sprintf("user-%d", i), fmt.Sprintf("req-%d", i)),
			)
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent server append: %v", err)
		}
	}

	check, err := s.Check(t.Context(), audit.ServerScope)
	if err != nil {
		t.Fatalf("check server chain: %v", err)
	}

	if !check.Valid || check.Checked != n {
		t.Fatalf("server check = %+v, want valid with %d entries", check, n)
	}

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: n})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	checkSequencesUnique(t, listing, n)
	checkStoredChain(t, listing)
}

// testChangeKind proves that the store keeps the kind change and refuses a
// kind that the audit log does not know.
func testChangeKind(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	e := newEntry(ns.Name, "admin", "req-1", now())
	e.Kind = audit.KindChange

	stored := appendEntry(t, s, ns.Fence(), e)
	if stored.Kind != audit.KindChange {
		t.Fatalf("stored kind = %q, want %q", stored.Kind, audit.KindChange)
	}

	e.Kind = "rename"

	if _, err := s.Append(t.Context(), ns.Fence(), e); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("append with an unknown kind = %v, want ErrInvalidEntry", err)
	}
}

// testObjectIDRoundTrip appends an entry without an object ID and one with
// it: both list back as written, and the chain stays valid, so the hash
// covers the object ID field of each entry.
func testObjectIDRoundTrip(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))

	withID := newEntry(ns.Name, "admin", "req-2", now())
	withID.Kind = audit.KindChange
	withID.ObjectID = "stk-0123"

	if stored := appendEntry(t, s, ns.Fence(), withID); stored.ObjectID != withID.ObjectID {
		t.Fatalf("stored object ID = %q, want %q", stored.ObjectID, withID.ObjectID)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 2 || listing.Entries[0].ObjectID != withID.ObjectID ||
		listing.Entries[1].ObjectID != "" {
		t.Fatalf("listing = %+v, want the set ID newest and an empty ID oldest", listing.Entries)
	}

	checkValidChain(t, []audit.Entry{listing.Entries[1], listing.Entries[0]})

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != 2 {
		t.Fatalf("check = %+v, want 2 valid entries", check)
	}
}

// testReasonRoundTrip appends an entry without a reason and one with a reason
// of 500 code points: both list back as written, and the chain stays valid,
// so the hash covers the reason field of each entry.
func testReasonRoundTrip(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	appendEntry(t, s, ns.Fence(), newEntry(ns.Name, "admin", "req-1", now()))

	withReason := newEntry(ns.Name, "admin", "req-2", now())
	withReason.Kind = audit.KindChange
	withReason.Reason = strings.Repeat("é", audit.MaxReasonLen)

	if stored := appendEntry(t, s, ns.Fence(), withReason); stored.Reason != withReason.Reason {
		t.Fatalf("stored reason has %d bytes, want %d", len(stored.Reason), len(withReason.Reason))
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 2 || listing.Entries[0].Reason != withReason.Reason || listing.Entries[1].Reason != "" {
		t.Fatalf("listing = %+v, want the set reason newest and an empty reason oldest", listing.Entries)
	}

	checkValidChain(t, []audit.Entry{listing.Entries[1], listing.Entries[0]})

	check, err := s.Check(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("check audit chain: %v", err)
	}

	if !check.Valid || check.Checked != 2 {
		t.Fatalf("check = %+v, want 2 valid entries", check)
	}
}

// testReasonRefused proves that an append refuses a reason with a control
// character, and that the refusal writes nothing.
func testReasonRefused(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store
	ns := newNamespace(t, s)

	e := newEntry(ns.Name, "admin", "req-1", now())
	e.Reason = "first\nsecond"

	if _, err := s.Append(t.Context(), ns.Fence(), e); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("append with a line break in the reason: error = %v, want ErrInvalidEntry", err)
	}

	listing, err := s.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil || listing.Total != 0 {
		t.Fatalf("list after a refused append = %+v, %v; want no entry", listing, err)
	}
}

// newChangesEntry answers a config-file entry of ADR 63 with three changes
// in no particular order: one sensitive value, one plain value and one with
// an old value.
func newChangesEntry(requestID string) audit.Entry {
	old := "info"

	e := newEntry(audit.ServerScope, "ins-aaaaaaaaaaaaaaaaaaaa", requestID, now())
	e.Action = "server:manage-settings"
	e.Object = audit.ServerScope.String()
	e.Kind = audit.KindChange
	e.Channel = audit.ChannelConfigFile
	e.Changes = []audit.Change{
		{Key: "region", Old: nil, New: "eu-west", Layer: "file", TakesEffect: ""},
		{Key: "database", Old: nil, New: "set", Layer: "env", TakesEffect: ""},
		{Key: "log-level", Old: &old, New: "debug", Layer: "file", TakesEffect: "next start"},
	}

	return e
}

// testChangesRoundTrip proves the changes of an entry read back sorted by
// key with their values, that an entry without changes reads back nil, and
// that the chain checks as valid.
func testChangesRoundTrip(t *testing.T, ts TestStore) {
	t.Helper()

	s := ts.Store

	if _, err := s.AppendServer(t.Context(), newServerEntry("admin", "req-1")); err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	if _, err := s.AppendServer(t.Context(), newChangesEntry("req-2")); err != nil {
		t.Fatalf("append changes entry: %v", err)
	}

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	if len(listing.Entries) != 2 {
		t.Fatalf("listing = %+v, want 2 entries", listing)
	}

	if got := listing.Entries[1].Changes; got != nil {
		t.Fatalf("changes of the entry without changes = %+v, want nil", got)
	}

	checkChangesFixture(t, listing.Entries[0].Changes)

	check, err := s.Check(t.Context(), audit.ServerScope)
	if err != nil {
		t.Fatalf("check server chain: %v", err)
	}

	if !check.Valid || check.Checked != 2 {
		t.Fatalf("check = %+v, want valid with 2 entries", check)
	}
}

// checkChangesFixture proves got holds the changes of newChangesEntry,
// sorted by key.
func checkChangesFixture(t *testing.T, got []audit.Change) {
	t.Helper()

	want := []string{"database=set@env", "log-level=debug@file(old info, next start)", "region=eu-west@file"}

	if len(got) != len(want) {
		t.Fatalf("changes = %+v, want %v", got, want)
	}

	for i, setting := range got {
		text := setting.Key + "=" + setting.New + "@" + setting.Layer
		if setting.Old != nil {
			text += "(old " + *setting.Old + ", " + setting.TakesEffect + ")"
		}

		if text != want[i] {
			t.Errorf("setting %d = %s, want %s", i, text, want[i])
		}
	}
}

// testCheckChangedChanges answers a test that proves Check finds the
// changes of an entry changed to tampered behind the back of the store, and
// that List still answers the entry.
func testCheckChangedChanges(tampered string) func(t *testing.T, ts TestStore) {
	return func(t *testing.T, ts TestStore) {
		t.Helper()

		if ts.TamperEntry == nil {
			t.Skip("the store has no tamper hook")
		}

		s := ts.Store

		stored, err := s.AppendServer(t.Context(), newChangesEntry("req-1"))
		if err != nil {
			t.Fatalf("append changes entry: %v", err)
		}

		tamperErr := ts.TamperEntry(t.Context(), audit.ServerScope, stored.Sequence, "changes", tampered)
		if tamperErr != nil {
			t.Fatalf("tamper changes: %v", tamperErr)
		}

		check, err := s.Check(t.Context(), audit.ServerScope)
		if err != nil {
			t.Fatalf("check server chain: %v", err)
		}

		if check.Valid || check.BadSequence != stored.Sequence {
			t.Fatalf("check = %+v, want bad sequence %d", check, stored.Sequence)
		}

		checkListAnswers(t, s, stored.Sequence)
	}
}

// checkListAnswers proves a list of the server scope still answers the one
// entry with seq after a tamper: only the check reports it.
func checkListAnswers(t *testing.T, s Store, seq int64) {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list after the tamper: %v", err)
	}

	if len(listing.Entries) != 1 || listing.Entries[0].Sequence != seq {
		t.Fatalf("list after the tamper = %+v, want the entry", listing)
	}
}
