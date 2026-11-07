package groupclaims_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/groupclaims"
)

// These tests send hostile input to one layer of the shared logic with a fake
// store, as if the layer below it had failed. The conformance suites of the
// SQL stores check the same rules through a real store.

const (
	testPerson   = "prn-person"
	testSourceID = "oidc-1"
)

// fakeStore is a groupclaims.Runner, groupclaims.Tx and
// groupclaims.MappingReads in memory. It records each write.
type fakeStore struct {
	target      groupclaims.Target
	groupSource map[string]string
	current     []groupclaims.Membership
	live        []groupclaims.Pair
	total       int
	appendErr   error
	readCalls   int
	added       []string
	ended       []string
	appended    []audit.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		target:      groupclaims.Target{Kind: principal.KindPerson, Namespace: "", BreakGlass: false, Disabled: false},
		groupSource: map[string]string{},
		current:     nil,
		live:        nil,
		total:       1,
		appendErr:   nil,
		readCalls:   0,
		added:       nil,
		ended:       nil,
		appended:    nil,
	}
}

func (f *fakeStore) Principal(context.Context, string) (groupclaims.Target, error) {
	f.readCalls++

	return f.target, nil
}

func (f *fakeStore) IsTempAdmin(context.Context, string) (bool, error) {
	f.readCalls++

	return false, nil
}

func (f *fakeStore) Mappings(
	context.Context, principal.MembershipSource, string,
) (int, []groupclaims.Pair, error) {
	f.readCalls++

	return f.total, f.live, nil
}

func (f *fakeStore) GroupSource(_ context.Context, id string) (string, bool, error) {
	source, ok := f.groupSource[id]

	return source, ok, nil
}

func (f *fakeStore) Memberships(context.Context, string) ([]groupclaims.Membership, error) {
	return f.current, nil
}

func (f *fakeStore) AddMember(_ context.Context, m principal.Membership) (bool, error) {
	f.added = append(f.added, m.GroupID)

	return true, nil
}

func (f *fakeStore) EndMember(_ context.Context, groupID, _ string, _ time.Time) (bool, error) {
	f.ended = append(f.ended, groupID)

	return true, nil
}

func (f *fakeStore) Append(_ context.Context, e audit.Entry) error {
	if f.appendErr != nil {
		return f.appendErr
	}

	f.appended = append(f.appended, e)

	return nil
}

func (f *fakeStore) InTx(_ context.Context, _ audit.Entry, fn func(groupclaims.Tx) error) error {
	return fn(f)
}

// fakeMapper is a principal.GroupMapper with a fixed answer.
type fakeMapper struct {
	out map[string][]string
	err error
}

func (m fakeMapper) MapGroups(
	context.Context, string, principal.MembershipSource, string, []string,
) (map[string][]string, error) {
	return m.out, m.err
}

func testEntry() audit.Entry {
	var e audit.Entry

	e.At = time.Date(2026, time.October, 13, 21, 9, 0, 0, time.UTC)
	e.Namespace = audit.ServerScope
	e.Subject = "sign-in"
	e.Action = "sign-in"
	e.Object = "principal:" + testPerson
	e.Channel = audit.ChannelAPI
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

func testClaims(groups ...string) principal.GroupClaims {
	return principal.GroupClaims{
		PrincipalID: testPerson, Source: principal.MembershipSourceClaim, SourceID: testSourceID, Groups: groups,
		Present: true,
	}
}

// TestMapperComparesTheBytesAgain proves the mapper keeps its own byte
// comparison when the store returns a value that differs only in case, or a
// value that was not asked.
func TestMapperComparesTheBytesAgain(t *testing.T) {
	t.Parallel()

	f := newFakeStore()
	f.live = []groupclaims.Pair{{Value: "Eng", GroupID: "g1"}, {Value: "other", GroupID: "g2"}}

	got, err := groupclaims.NewMapper(f).MapGroups(
		t.Context(), testPerson, principal.MembershipSourceClaim, testSourceID, []string{"eng"},
	)
	if err != nil {
		t.Fatalf("MapGroups: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("mapping = %v, want none for a value that differs in case or was not asked", got)
	}
}

// TestMapperBoundaryRejectsHostileValues proves the mapper refuses hostile
// input before it reads the store.
func TestMapperBoundaryRejectsHostileValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source principal.MembershipSource
		id     string
		values []string
	}{
		{"1001 values", principal.MembershipSourceClaim, testSourceID, slices.Repeat([]string{"v"}, 1001)},
		{"257 bytes", principal.MembershipSourceClaim, testSourceID, []string{strings.Repeat("a", 257)}},
		{"NUL", principal.MembershipSourceClaim, testSourceID, []string{"a\x00b"}},
		{"CR LF", principal.MembershipSourceClaim, testSourceID, []string{"a\r\nb"}},
		{"source local", principal.MembershipSourceLocal, testSourceID, []string{"v"}},
		{"empty source ID", principal.MembershipSourceClaim, "", []string{"v"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeStore()

			_, err := groupclaims.NewMapper(f).MapGroups(t.Context(), testPerson, tt.source, tt.id, tt.values)
			if !errors.Is(err, principal.ErrInvalid) {
				t.Fatalf("MapGroups error = %v, want ErrInvalid", err)
			}

			if f.readCalls != 0 {
				t.Fatalf("store reads = %d, want none before the boundary check", f.readCalls)
			}
		})
	}
}

// TestApplierRefusesAGroupThatTheMapperReturns proves the applier checks the
// group again at the point of use.
func TestApplierRefusesAGroupThatTheMapperReturns(t *testing.T) {
	t.Parallel()

	f := newFakeStore()
	f.groupSource["g-directory"] = string(principal.SourceSCIM)
	f.groupSource["g-local"] = string(principal.SourceLocal)

	mapper := fakeMapper{out: map[string][]string{"v": {"g-unknown", "g-directory", "g-local"}}, err: nil}

	res, err := groupclaims.NewApplier(f, mapper).ApplyGroupClaims(t.Context(), testEntry(), testClaims("v"))
	if err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	if !slices.Equal(res.Refused, []string{"g-directory", "g-unknown"}) ||
		!slices.Equal(res.Added, []string{"g-local"}) {
		t.Fatalf("result = %+v, want two refused groups and the local group added", res)
	}

	if !slices.Equal(f.added, []string{"g-local"}) {
		t.Fatalf("added = %v, want only the local group", f.added)
	}
}

// TestApplierEndsOnlyItsOwnMemberships proves the removal loop checks the
// source and the source ID of each row, and the pin, when the store returns
// every membership of the principal.
func TestApplierEndsOnlyItsOwnMemberships(t *testing.T) {
	t.Parallel()

	f := newFakeStore()
	f.current = []groupclaims.Membership{
		{
			GroupID: "g-other-id", Source: principal.MembershipSourceClaim, SourceID: "oidc-2",
			State: principal.MembershipStateActive, Pinned: false,
		},
		{
			GroupID: "g-local", Source: principal.MembershipSourceLocal, SourceID: "",
			State: principal.MembershipStateActive, Pinned: false,
		},
		{
			GroupID: "g-scim", Source: principal.MembershipSourceSCIM, SourceID: testSourceID,
			State: principal.MembershipStateActive, Pinned: false,
		},
		{
			GroupID: "g-pinned", Source: principal.MembershipSourceClaim, SourceID: testSourceID,
			State: principal.MembershipStateActive, Pinned: true,
		},
		{
			GroupID: "g-own", Source: principal.MembershipSourceClaim, SourceID: testSourceID,
			State: principal.MembershipStateActive, Pinned: false,
		},
	}

	mapper := fakeMapper{out: map[string][]string{}, err: nil}

	res, err := groupclaims.NewApplier(f, mapper).ApplyGroupClaims(t.Context(), testEntry(), testClaims())
	if err != nil {
		t.Fatalf("ApplyGroupClaims: %v", err)
	}

	if !slices.Equal(f.ended, []string{"g-own"}) || !slices.Equal(res.Removed, []string{"g-own"}) {
		t.Fatalf("ended = %v, removed = %v, want only g-own", f.ended, res.Removed)
	}
}

// TestApplierChecksThePrincipalAgainInTheTransaction proves a principal that
// is disabled between the first read and the transaction gets no membership.
func TestApplierChecksThePrincipalAgainInTheTransaction(t *testing.T) {
	t.Parallel()

	outside := newFakeStore()
	inside := newFakeStore()
	inside.target.Disabled = true
	inside.groupSource["g1"] = string(principal.SourceLocal)

	runner := splitRunner{Reads: outside, tx: inside}
	mapper := fakeMapper{out: map[string][]string{"v": {"g1"}}, err: nil}

	_, err := groupclaims.NewApplier(runner, mapper).ApplyGroupClaims(t.Context(), testEntry(), testClaims("v"))
	if !errors.Is(err, principal.ErrDisabled) {
		t.Fatalf("ApplyGroupClaims error = %v, want ErrDisabled", err)
	}

	if len(inside.added) != 0 {
		t.Fatalf("added = %v, want none", inside.added)
	}
}

// splitRunner reads outside a transaction with one store and runs the
// transaction on another.
type splitRunner struct {
	groupclaims.Reads

	tx *fakeStore
}

func (s splitRunner) InTx(_ context.Context, _ audit.Entry, fn func(groupclaims.Tx) error) error {
	return fn(s.tx)
}

// TestApplierReturnsAnAuditFailure proves a failed audit append is an error,
// so the runner rolls the transaction back.
func TestApplierReturnsAnAuditFailure(t *testing.T) {
	t.Parallel()

	f := newFakeStore()
	f.appendErr = namespace.ErrUnavailable
	f.groupSource["g1"] = string(principal.SourceLocal)

	mapper := fakeMapper{out: map[string][]string{"v": {"g1"}}, err: nil}

	_, err := groupclaims.NewApplier(f, mapper).ApplyGroupClaims(t.Context(), testEntry(), testClaims("v"))
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ApplyGroupClaims error = %v, want the error of the audit append", err)
	}
}
