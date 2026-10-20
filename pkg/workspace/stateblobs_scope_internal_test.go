package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	// siblingWS is a second workspace of the namespace acmeID.
	siblingWS = ID("ws-2")
	// siblingBlob is the key of a blob of siblingWS. The workspace testWS must
	// never delete it.
	siblingBlob = blob.Key("namespaces/ns-acmeacmeacmeacmeacme/tofu/ws-2/sv-9/a1")
	ownBlob     = blob.Key("namespaces/ns-acmeacmeacmeacmeacme/tofu/ws-1/sv-1/a1")
)

// noFence returns a fence that the fake state store ignores.
func noFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

// versionOf returns a state version with the ID id that names the blob key.
func versionOf(id StateVersionID, key blob.Key) StateVersion {
	var sv StateVersion

	sv.ID = id
	sv.Content = key

	return sv
}

// scopeStates is a state store that answers only what the blob paths use.
type scopeStates struct {
	StateStore

	superseded blob.Key
	discarded  atomic.Int32
}

func (s *scopeStates) RecordUploadAttempt(
	context.Context, StateVersionID, blob.Key, namespace.Fence,
) (blob.Key, error) {
	return s.superseded, nil
}

func (s *scopeStates) DiscardStateVersion(context.Context, StateVersionID, namespace.Fence) error {
	s.discarded.Add(1)

	return nil
}

// scopeServices is a Service over blobs in memory, with a recorder that writes
// real audit entries, as the server wires it.
type scopeServices struct {
	svc     *Service
	store   *callCountingStore
	states  *scopeStates
	entries *serverEntries
	ws      Workspace
}

// newScopeService returns a service for the workspace testWS of acmeID, with a
// blob of testWS and a blob of siblingWS in the store.
func newScopeService(t *testing.T) *scopeServices {
	t.Helper()

	store := &callCountingStore{mu: sync.Mutex{}, objects: map[blob.Key][]byte{}, calls: 0}
	entries := &serverEntries{got: nil, err: nil}
	at := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)

	rec, err := secret.NewAuditOutOfScope(entries, audit.ChannelAPI, StateBlobFamily(), func() time.Time { return at })
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}

	for _, key := range []blob.Key{ownBlob, siblingBlob} {
		store.objects[key] = []byte("state")
	}

	states := &scopeStates{StateStore: nil, superseded: "", discarded: atomic.Int32{}}
	svc := new(Service)
	svc.blobs = &stateBlobs{store: store, refusals: rec}
	svc.states = states
	svc.now = func() time.Time { return at }

	var ws Workspace

	ws.ID = testWS
	ws.NamespaceID = acmeID
	ws.Ref = Ref{Namespace: "acme", Name: "one"}

	return &scopeServices{svc: svc, store: store, states: states, entries: entries, ws: ws}
}

// checkRefusedAndKept checks the result of a hostile call: the error is
// blob.ErrOutOfScope, the blob of the sibling workspace is still there, and the
// audit log holds one security entry.
func (e *scopeServices) checkRefusedAndKept(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, blob.ErrOutOfScope) {
		t.Fatalf("err = %v, want blob.ErrOutOfScope", err)
	}

	if _, ok := e.store.objects[siblingBlob]; !ok {
		t.Error("the blob of the other workspace was deleted")
	}

	if len(e.entries.got) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(e.entries.got))
	}

	if got := e.entries.got[0]; got.Kind != audit.KindSecurity || got.Outcome != audit.OutcomeDenied ||
		got.Namespace != audit.ServerScope || got.Object != acmeID.String() {
		t.Errorf("audit entry = %+v, want a denied security entry in @server for the namespace", got)
	}

	for i := range e.entries.got {
		if reason := e.entries.got[i].Reason; strings.Contains(reason, "namespaces/") ||
			strings.Contains(reason, string(siblingWS)) {
			t.Errorf("reason %q holds a key", reason)
		}
	}
}

// TestRowOfOneWorkspaceCannotDeleteABlobOfAnother sends, to each path that
// deletes a blob the row names, a row of the workspace testWS that names a blob
// of the workspace siblingWS in the same namespace. Each path must refuse
// with blob.ErrOutOfScope, delete nothing, and write one security entry.
func TestRowOfOneWorkspaceCannotDeleteABlobOfAnother(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(e *scopeServices) error
	}{
		{"noteAttempt, the superseded key", func(e *scopeServices) error {
			e.states.superseded = siblingBlob

			return e.svc.noteAttempt(context.Background(), e.ws, "sv-1", ownBlob, noFence())
		}},
		{"the expiry sweep", func(e *scopeServices) error {
			expired := []StateVersion{versionOf("sv-9", siblingBlob)}

			return e.svc.deleteExpiredBlobs(context.Background(), e.ws, expired)
		}},
		{"dropFence", func(e *scopeServices) error {
			return e.svc.dropFence(
				context.Background(), e.ws, versionOf("sv-9", siblingBlob), noFence(),
			)
		}},
		{"refusedRestore, a rejection", func(e *scopeServices) error {
			return e.svc.refusedRestore(
				context.Background(), e.ws, versionOf("sv-9", siblingBlob),
				noFence(), ErrStaleSerial,
			)
		}},
		{"refusedRestore, a duplicate", func(e *scopeServices) error {
			return e.svc.refusedRestore(
				context.Background(), e.ws, versionOf("sv-9", siblingBlob),
				noFence(), ErrAlreadyRestored,
			)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newScopeService(t)
			callsBefore := e.store.callCount()

			e.checkRefusedAndKept(t, tc.call(e))

			if calls := e.store.callCount() - callsBefore; calls != 0 {
				t.Errorf("calls that reached the blob store = %d, want 0", calls)
			}
		})
	}
}

// TestRowOfOneWorkspaceKeepsItsOwnBlobsDeletable proves that the check does not
// stop a delete in the directory of the workspace: each path deletes an own
// blob and writes no entry.
func TestRowOfOneWorkspaceKeepsItsOwnBlobsDeletable(t *testing.T) {
	t.Parallel()

	e := newScopeService(t)
	e.states.superseded = ownBlob

	next := blob.Key("namespaces/ns-acmeacmeacmeacmeacme/tofu/ws-1/sv-1/a2")

	err := e.svc.noteAttempt(context.Background(), e.ws, "sv-1", next, noFence())
	if err != nil {
		t.Fatalf("noteAttempt: %v", err)
	}

	if _, ok := e.store.objects[ownBlob]; ok {
		t.Error("the superseded blob of the workspace stayed")
	}

	if _, ok := e.store.objects[siblingBlob]; !ok {
		t.Error("the blob of the other workspace was deleted")
	}

	if len(e.entries.got) != 0 {
		t.Errorf("audit entries = %d, want 0", len(e.entries.got))
	}
}

// countingNamespaces is a namespace store that counts every read.
type countingNamespaces struct {
	reads atomic.Int32
}

func (s *countingNamespaces) CreateNamespace(context.Context, namespace.Namespace) error {
	return errNotUsed
}

func (s *countingNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	s.reads.Add(1)

	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: acmeID, Name: name, HomeRegion: "local",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}, nil
}

func (s *countingNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return errNotUsed
}

func (s *countingNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	s.reads.Add(1)

	return nil, nil
}

func (s *countingNamespaces) Ping(context.Context) error { return nil }

// TestStateBlobCallsReadNoNamespace proves that no call of the state blob code
// reads a namespace: the workspace row carries the namespace ID. Only the
// blob purge, which runs after the row is gone, reads the namespace once.
func TestStateBlobCallsReadNoNamespace(t *testing.T) {
	t.Parallel()

	nsStore := &countingNamespaces{reads: atomic.Int32{}}

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	e := newScopeService(t)
	e.svc.namespaces = namespaces

	ctx := context.Background()
	w := e.ws
	fence := noFence()

	key, err := e.svc.blobs.attemptKey(w.NamespaceID, w.ID, "sv-1")
	if err != nil {
		t.Fatalf("attemptKey: %v", err)
	}

	e.states.superseded = ownBlob

	calls := []struct {
		name string
		call func() error
	}{
		{"put", func() error { return e.svc.blobs.put(ctx, w.NamespaceID, w.ID, key, strings.NewReader("x")) }},
		{"get", func() error {
			r, getErr := e.svc.blobs.get(ctx, w.NamespaceID, w.ID, key)
			if getErr != nil {
				return getErr
			}

			return r.Close()
		}},
		{"list", func() error { _, listErr := e.svc.blobs.list(ctx, w.NamespaceID, w.ID); return listErr }},
		{"delete", func() error { return e.svc.blobs.delete(ctx, w.NamespaceID, w.ID, key) }},
		{"deleteRetried", func() error { return e.svc.blobs.deleteRetried(ctx, w.NamespaceID, w.ID, key) }},
		{"noteAttempt", func() error { return e.svc.noteAttempt(ctx, w, "sv-1", key, fence) }},
		{"deleteExpiredBlobs", func() error {
			return e.svc.deleteExpiredBlobs(ctx, w, []StateVersion{versionOf("sv-1", key)})
		}},
		{"dropFence", func() error { return e.svc.dropFence(ctx, w, versionOf("sv-1", key), fence) }},
		{"refusedRestore", func() error {
			return e.svc.refusedRestore(ctx, w, versionOf("sv-1", key), fence, ErrStaleSerial)
		}},
	}

	for _, tc := range calls {
		before := nsStore.reads.Load()

		if callErr := tc.call(); callErr != nil && !errors.Is(callErr, ErrStaleSerial) {
			t.Fatalf("%s: %v", tc.name, callErr)
		}

		if n := nsStore.reads.Load() - before; n != 0 {
			t.Errorf("%s read the namespace %d times, want 0", tc.name, n)
		}
	}

	before := nsStore.reads.Load()

	if purgeErr := e.svc.DeletePurgedBlobs(ctx, "acme", w.ID); purgeErr != nil {
		t.Fatalf("DeletePurgedBlobs: %v", purgeErr)
	}

	if n := nsStore.reads.Load() - before; n != 1 {
		t.Errorf("DeletePurgedBlobs read the namespace %d times, want 1", n)
	}
}
