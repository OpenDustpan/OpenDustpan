// Package secrettest is the conformance suite for secret.KeyStore and
// secret.Wrapper implementations. Every built-in adapter runs it, and custom
// adapters should too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		secrettest.Run(t, func(t *testing.T) secrettest.Store { return newMyStore(t) })
//	}
package secrettest

import (
	"bytes"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Store is every persistence port the suite exercises: namespaces and stacks
// for fixtures, secret.KeyStore under test, stack.Store.DeleteStack and
// workspace.Store.DeleteWorkspace for the delete rules.
type Store interface {
	namespace.Store
	stack.Store
	workspace.Store
	secret.KeyStore
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "create then get returns the same key", test: testRoundTrip},
		{name: "concurrent creates agree on one key", test: testConcurrentCreates},
		{name: "get of a missing key is ErrKeyNotFound", test: testMissingKey},
		{name: "create with a stale fence is ErrFenced", test: testStaleFence},
		{name: "wrapper IDs are listed", test: testWrapperIDs},
		{name: "delete of the stack keeps its key", test: testDeleteStackKeepsKey},
		{name: "delete of the workspace keeps its key", test: testDeleteWorkspaceKeepsKey},
		{name: "seal stream round trips across chunk borders", test: testSealStreamRoundTrip},
		{name: "seal stream binds the scope", test: testSealStreamBindsScope},
		{name: "tampered sealed streams fail without a leak", test: testSealStreamTamper},
		{name: "sealed state binds its state version", test: testSealStateBindsVersion},
		{name: "truncated sealed streams fail", test: testSealStreamTruncated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// now is a fixed, millisecond-precision instant: stores keep milliseconds.
func now() time.Time {
	return time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
}

// deleteEntry returns the audit entry of a stack delete in ns at the time at.
func deleteEntry(ns namespace.Name, at time.Time) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     ns,
		Subject:       "alice@example.com",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:manage",
		Object:        "pulumi:" + ns.String() + "/web/deleted",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "secrettest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func newNamespace(t *testing.T, s Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", ns.Name, err)
	}

	return ns
}

func newStack(ns namespace.Name, name string) stack.Stack {
	return stack.Stack{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Tags:        map[string]string{},
		Ref:         stack.Ref{Namespace: ns, Project: "web", Stack: stack.Name(name)},
		ID:          stack.ID(ident.NewID("stk-")),
		Version:     0,
	}
}

// createStack persists a stack and returns it with its stable ID.
func createStack(t *testing.T, s Store, ns namespace.Namespace, name string) stack.Stack {
	t.Helper()

	st := newStack(ns.Name, name)

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	return st
}

func newScope(t *testing.T, ns namespace.Name, stackID stack.ID) secret.Scope {
	t.Helper()

	scope, err := secret.NewScope(ns, stackID.String())
	if err != nil {
		t.Fatalf("new scope %s/%s: %v", ns, stackID, err)
	}

	return scope
}

func newKey(scope secret.Scope, wrapperID, ciphertext string) secret.WrappedKey {
	return secret.WrappedKey{
		CreatedAt:  now(),
		Scope:      scope,
		WrapperID:  wrapperID,
		Ciphertext: []byte(ciphertext),
	}
}

func assertKey(t *testing.T, got, want secret.WrappedKey) {
	t.Helper()

	if got.Scope != want.Scope || got.WrapperID != want.WrapperID ||
		!got.CreatedAt.Equal(want.CreatedAt) || !bytes.Equal(got.Ciphertext, want.Ciphertext) {
		t.Fatalf("key = %+v, want %+v", got, want)
	}
}

func testRoundTrip(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")
	scope := newScope(t, ns.Name, st.ID)
	want := newKey(scope, "kek-1", "wrapped-1")

	got, err := s.CreateKey(t.Context(), want, ns.Fence())
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	assertKey(t, got, want)

	stored, err := s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key: %v", err)
	}

	assertKey(t, stored, want)

	// A second create for the same scope keeps the first key.
	other := newKey(scope, "kek-2", "wrapped-2")

	again, err := s.CreateKey(t.Context(), other, ns.Fence())
	if err != nil {
		t.Fatalf("create key again: %v", err)
	}

	assertKey(t, again, want)

	stored, err = s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key after a second create: %v", err)
	}

	assertKey(t, stored, want)
}

func testConcurrentCreates(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")
	scope := newScope(t, ns.Name, st.ID)

	first := newKey(scope, "kek-1", "wrapped-first")
	second := newKey(scope, "kek-2", "wrapped-second")

	got := make([]secret.WrappedKey, 2)
	errs := make([]error, 2)

	var wg sync.WaitGroup

	for i, k := range []secret.WrappedKey{first, second} {
		wg.Go(func() {
			got[i], errs[i] = s.CreateKey(t.Context(), k, ns.Fence())
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", i, err)
		}
	}

	assertKey(t, got[1], got[0])

	stored, err := s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key after concurrent creates: %v", err)
	}

	assertKey(t, stored, got[0])
}

func testMissingKey(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")

	if _, err := s.GetKey(t.Context(), newScope(t, ns.Name, "stk-aaaaaaaaaaaaaaaaaaaa")); !errors.Is(
		err, secret.ErrKeyNotFound,
	) {
		t.Fatalf("get missing key: err = %v, want ErrKeyNotFound", err)
	}

	other := newScope(t, "ghost", st.ID)
	if _, err := s.GetKey(t.Context(), other); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("get key in a missing namespace: err = %v, want ErrKeyNotFound", err)
	}
}

func testStaleFence(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")
	scope := newScope(t, ns.Name, st.ID)
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if _, err := s.CreateKey(t.Context(), newKey(scope, "kek-1", "wrapped-1"), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("create key with stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetKey(t.Context(), scope); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("fenced key was written: err = %v, want ErrKeyNotFound", err)
	}

	missing := secret.WrappedKey{
		CreatedAt:  now(),
		Scope:      newScope(t, "ghost", st.ID),
		WrapperID:  "kek-1",
		Ciphertext: []byte("wrapped"),
	}
	_, err := s.CreateKey(
		t.Context(), missing, namespace.Fence{Namespace: "ghost", FailoverVersion: 1},
	)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("create key in a missing namespace: err = %v, want namespace.ErrNotFound", err)
	}

	want := newKey(scope, "kek-1", "wrapped-1")

	if _, err = s.CreateKey(t.Context(), want, ns.Fence()); err != nil {
		t.Fatalf("create key: %v", err)
	}

	if _, err = s.CreateKey(t.Context(), newKey(scope, "kek-2", "wrapped-2"), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("overwrite key with stale fence: err = %v, want ErrFenced", err)
	}

	stored, err := s.GetKey(t.Context(), scope)
	if err != nil {
		t.Fatalf("get key after a fenced overwrite: %v", err)
	}

	assertKey(t, stored, want)
}

func testWrapperIDs(t *testing.T, s Store) {
	t.Helper()

	empty, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs of an empty store: %v", err)
	}

	if len(empty) != 0 {
		t.Fatalf("wrapper IDs of an empty store = %v, want none", empty)
	}

	ns := newNamespace(t, s)

	scopes := make([]secret.Scope, 0, 3)
	for _, name := range []string{"prod", "dev", "staging"} {
		st := createStack(t, s, ns, name)
		scopes = append(scopes, newScope(t, ns.Name, st.ID))
	}

	keys := []secret.WrappedKey{
		newKey(scopes[0], "kek-2", "wrapped-1"),
		newKey(scopes[1], "kek-1", "wrapped-2"),
		newKey(scopes[2], "kek-1", "wrapped-3"),
	}

	for i, k := range keys {
		if _, err = s.CreateKey(t.Context(), k, ns.Fence()); err != nil {
			t.Fatalf("create key %d: %v", i, err)
		}
	}

	got, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs: %v", err)
	}

	if !slices.Equal(got, []string{"kek-1", "kek-2"}) {
		t.Fatalf("wrapper IDs = %v, want [kek-1 kek-2]", got)
	}
}

// testDeleteStackKeepsKey checks that a soft delete keeps the data key of the
// stack, so a restore can read its secrets again (ADR 47, decision 3). Only
// the final delete removes the key (ADR 23, decision 5).
func testDeleteStackKeepsKey(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)
	st := createStack(t, s, ns, "prod")
	scope := newScope(t, ns.Name, st.ID)

	if _, err := s.CreateKey(t.Context(), newKey(scope, "kek-1", "wrapped-1"), ns.Fence()); err != nil {
		t.Fatalf("create key: %v", err)
	}

	at := time.UnixMilli(1_760_000_000_000).UTC()
	err := s.DeleteStack(
		t.Context(),
		st.Ref,
		ns.Fence(),
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(ns.Name, at), PurgeAfter: at.Add(inventory.DeletedRetention)},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if _, err = s.GetKey(t.Context(), scope); err != nil {
		t.Fatalf("get key after a soft delete: %v", err)
	}

	got, err := s.ListWrapperIDs(t.Context())
	if err != nil {
		t.Fatalf("list wrapper IDs after delete: %v", err)
	}

	if len(got) != 1 || got[0] != "kek-1" {
		t.Fatalf("wrapper IDs after delete = %v, want [kek-1]", got)
	}

	if _, err := s.GetStack(t.Context(), st.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get deleted stack: err = %v, want ErrNotFound", err)
	}
}

// testDeleteWorkspaceKeepsKey proves a soft delete of a workspace keeps its
// data key, so a restore can open its sealed state again (ADR 47, decision
// 3). Only the final delete removes the key (ADR 23, decision 5).
func testDeleteWorkspaceKeepsKey(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s)

	name, nameErr := workspace.ParseName("network")
	if nameErr != nil {
		t.Fatalf("parse workspace name: %v", nameErr)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: name},
		ID:          workspace.ID(ident.NewID("ws-")),
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	scope, err := secret.NewScope(ns.Name, w.ID.String())
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	if _, err = s.CreateKey(t.Context(), newKey(scope, "kek-1", "wrapped-1"), ns.Fence()); err != nil {
		t.Fatalf("create key: %v", err)
	}

	at := time.UnixMilli(1_760_000_000_000).UTC()
	err = s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name, at), PurgeAfter: at.Add(inventory.DeletedRetention)},
	)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	if _, err := s.GetKey(t.Context(), scope); err != nil {
		t.Fatalf("get key after a soft delete: %v", err)
	}

	if _, err := s.GetWorkspace(t.Context(), w.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get deleted workspace: err = %v, want ErrNotFound", err)
	}
}
