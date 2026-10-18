// Package stackhistorytest is the conformance suite for stack.BlameStore.
// Every built-in store runs it, and a custom store should too:
//
//	func TestStackBlameConformance(t *testing.T) {
//		t.Parallel()
//		stackhistorytest.Run(t, func(t *testing.T) stackhistorytest.Store { return newMyStore(t) })
//	}
//
// The cases of stack.StateAtReader need the update tables, so they are part
// of the store suite of the update cycle.
package stackhistorytest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Store is every persistence port the suite exercises: namespaces for the
// fence, and stack.BlameStore under test.
type Store interface {
	namespace.Store
	stack.BlameStore
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
		{name: "a new stack has the cursor 0 and no row", test: testEmpty},
		{name: "rows of a version read back ordered by URN", test: testVersionChanges},
		{name: "a record moves the cursor, also with no row", test: testCursor},
		{name: "a record at or below the cursor changes nothing", test: testRepeat},
		{name: "resource history answers newest first in pages", test: testResourceHistory},
		{name: "rows of another stack stay apart", test: testOtherStack},
		{name: "a stale fence refuses a record", test: testFenced},
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

// fenceVersion is the failover version of the namespace of the suite.
const fenceVersion = 2

// makeNamespace creates the namespace "acme" and returns its fence.
func makeNamespace(t *testing.T, s Store) namespace.Fence {
	t.Helper()

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt: suiteTime(), Name: "acme", HomeRegion: "local", FailoverVersion: fenceVersion,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespace.Fence{Namespace: "acme", FailoverVersion: fenceVersion}
}

// row returns a resource change of the stack id.
func row(id stack.ID, version int, urn string, kind stack.ResourceChangeKind) stack.ResourceChange {
	return stack.ResourceChange{
		At:           suiteTime().Add(time.Duration(version) * time.Minute),
		Stack:        id,
		Update:       stack.UpdateID("upd-" + strconv.Itoa(version)),
		URN:          urn,
		Kind:         kind,
		UpdateKind:   "update",
		By:           history.Actor{Subject: "ana", TokenID: "tok-1", JobID: "job-1"},
		Version:      version,
		ChangedPaths: 1,
	}
}

// blame returns the blame of the update version of the stack id.
func blame(id stack.ID, version int, changes ...stack.ResourceChange) stack.VersionBlame {
	return stack.VersionBlame{Stack: id, Changes: changes, Version: version}
}

// mustRecord records b.
func mustRecord(t *testing.T, s Store, f namespace.Fence, b stack.VersionBlame) {
	t.Helper()

	if err := s.RecordResourceChanges(t.Context(), "acme", b, f); err != nil {
		t.Fatalf("record version %d: %v", b.Version, err)
	}
}

// urns returns the URNs of rows.
func urns(rows []stack.ResourceChange) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		r := rows[i]
		out = append(out, r.URN)
	}

	return out
}

const (
	urnBucket = "urn:pulumi:prod::web::aws:s3/bucket:Bucket::assets"
	urnRole   = "urn:pulumi:prod::web::aws:iam/role:Role::app"
	urnQueue  = "urn:pulumi:prod::web::aws:sqs/queue:Queue::jobs"
)

func testEmpty(t *testing.T, s Store) {
	t.Helper()
	makeNamespace(t, s)

	cursor, err := s.BlameCursor(t.Context(), "stk-one")
	if err != nil || cursor != 0 {
		t.Errorf("cursor = %d, %v, want 0", cursor, err)
	}

	rows, err := s.VersionChanges(t.Context(), "stk-one", 1)
	if err != nil || len(rows) != 0 {
		t.Errorf("rows = %v, %v, want none", rows, err)
	}
}

func testVersionChanges(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	want := []stack.ResourceChange{
		row("stk-one", 1, urnBucket, stack.ResourceCreated),
		row("stk-one", 1, urnRole, stack.ResourceCreated),
	}
	mustRecord(t, s, f, blame("stk-one", 1, want...))

	got, err := s.VersionChanges(t.Context(), "stk-one", 1)
	if err != nil {
		t.Fatalf("version changes: %v", err)
	}

	if !slices.Equal(urns(got), []string{urnRole, urnBucket}) {
		t.Fatalf("URNs = %v, want ordered by URN", urns(got))
	}

	if got[0].By != want[1].By || got[0].Update != want[1].Update || got[0].Kind != stack.ResourceCreated ||
		!got[0].At.Equal(want[1].At) || got[0].ChangedPaths != 1 {
		t.Errorf("row = %+v, want %+v", got[0], want[1])
	}
}

func testCursor(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("stk-one", 1, row("stk-one", 1, urnBucket, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-one", 3))

	if cursor, err := s.BlameCursor(t.Context(), "stk-one"); err != nil || cursor != 3 {
		t.Errorf("cursor = %d, %v, want 3", cursor, err)
	}
}

func testRepeat(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("stk-one", 2, row("stk-one", 2, urnBucket, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-one", 2, row("stk-one", 2, urnBucket, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-one", 1, row("stk-one", 1, urnRole, stack.ResourceCreated)))

	if rows, err := s.VersionChanges(t.Context(), "stk-one", 2); err != nil || len(rows) != 1 {
		t.Errorf("rows of version 2 after a repeat = %d, %v, want 1", len(rows), err)
	}

	if rows, err := s.VersionChanges(t.Context(), "stk-one", 1); err != nil || len(rows) != 0 {
		t.Errorf("rows of version 1 below the cursor = %d, %v, want none", len(rows), err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "stk-one"); err != nil || cursor != 2 {
		t.Errorf("cursor = %d, %v, want 2", cursor, err)
	}
}

func testResourceHistory(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("stk-one", 1, row("stk-one", 1, urnBucket, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-one", 2, row("stk-one", 2, urnQueue, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-one", 3, row("stk-one", 3, urnBucket, stack.ResourceUpdated)))
	mustRecord(t, s, f, blame("stk-one", 4, row("stk-one", 4, urnBucket, stack.ResourceReplaced)))
	mustRecord(t, s, f, blame("stk-one", 5, row("stk-one", 5, urnBucket, stack.ResourceDeleted)))

	first, err := s.ResourceHistory(t.Context(), "stk-one", urnBucket, 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d rows, %v, want 2", len(first), err)
	}

	rest, err := s.ResourceHistory(t.Context(), "stk-one", urnBucket, first[len(first)-1].Version, 10)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	items := slices.Concat(first, rest)
	kinds := make([]stack.ResourceChangeKind, 0, len(items))
	for i := range items {
		r := items[i]
		kinds = append(kinds, r.Kind)
	}

	want := []stack.ResourceChangeKind{
		stack.ResourceDeleted,
		stack.ResourceReplaced,
		stack.ResourceUpdated,
		stack.ResourceCreated,
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("history = %v, want %v", kinds, want)
	}
}

func testOtherStack(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	mustRecord(t, s, f, blame("stk-one", 1, row("stk-one", 1, urnBucket, stack.ResourceCreated)))
	mustRecord(t, s, f, blame("stk-two", 4, row("stk-two", 4, urnBucket, stack.ResourceCreated)))

	if rows, err := s.ResourceHistory(t.Context(), "stk-one", urnBucket, 0, 10); err != nil || len(rows) != 1 {
		t.Errorf("history of stk-one = %d rows, %v, want 1", len(rows), err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "stk-one"); err != nil || cursor != 1 {
		t.Errorf("cursor of stk-one = %d, %v, want 1", cursor, err)
	}
}

func testFenced(t *testing.T, s Store) {
	t.Helper()
	f := makeNamespace(t, s)
	stale := namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion - 1}

	err := s.RecordResourceChanges(
		t.Context(),
		"acme",
		blame("stk-one", 1, row("stk-one", 1, urnBucket, stack.ResourceCreated)),
		stale,
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("record with a stale fence err = %v, want ErrFenced", err)
	}

	if cursor, err := s.BlameCursor(t.Context(), "stk-one"); err != nil || cursor != 0 {
		t.Errorf("a fenced record moved the cursor to %d, %v", cursor, err)
	}
}
