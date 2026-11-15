package refusal_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errStoreDown is an Authorizer failure that is not a refusal.
var errStoreDown = errors.New("store down")

// errDiskFull is a store failure.
var errDiskFull = errors.New("disk full")

// fakeAuthorizer answers err for every check.
type fakeAuthorizer struct {
	err error
}

func (a fakeAuthorizer) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return a.err
}

// ListObjects answers an empty scope for every list.
func (fakeAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// fakeStore keeps the entries that it is given. It fails every append when
// failWith is set.
type fakeStore struct {
	entries   []audit.Entry
	failWith  error
	fenceSeen namespace.Fence
}

func (s *fakeStore) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := ctx.Err(); err != nil {
		return audit.Entry{}, fmt.Errorf("append: %w", err)
	}

	s.fenceSeen = fence

	if s.failWith != nil {
		return audit.Entry{}, s.failWith
	}

	s.entries = append(s.entries, e)

	return e, nil
}

func (s *fakeStore) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if err := ctx.Err(); err != nil {
		return audit.Entry{}, fmt.Errorf("append: %w", err)
	}

	if s.failWith != nil {
		return audit.Entry{}, s.failWith
	}

	s.entries = append(s.entries, e)

	return e, nil
}

func (*fakeStore) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: nil, Total: 0}, nil
}

func (*fakeStore) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: false}, nil
}

// newFakeStore returns an empty store that fails every append with failWith.
func newFakeStore(failWith error) *fakeStore {
	return &fakeStore{entries: nil, failWith: failWith, fenceSeen: namespace.Fence{Namespace: "", FailoverVersion: 0}}
}

// fakeNamespaces knows the namespace team at failover version 3.
type fakeNamespaces struct{}

func (fakeNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != "team" {
		return namespace.Namespace{}, fmt.Errorf("%w: %s", namespace.ErrNotFound, name)
	}

	return namespace.Namespace{
		CreatedAt:           time.Time{},
		ID:                  "",
		Name:                name,
		PrimaryInstallation: "",
		FailoverVersion:     3,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// refusalCase is one check of the table: what the Authorizer answers, who
// asks, on what object, and what the audit store then holds.
type refusalCase struct {
	name       string
	authz      error
	subject    string
	obj        identity.Object
	store      *fakeStore
	wantErr    error
	wantEntry  bool
	wantFailed bool
	// inServerScope is true when the entry lands in the installation scope
	// although the object names a namespace.
	inServerScope bool
}

func TestAuthorizeRecordsRefusals(t *testing.T) {
	t.Parallel()

	cases := []refusalCase{
		{
			name:          "refused write writes a denied entry in the namespace",
			authz:         identity.ErrDenied,
			subject:       "bo@example.com",
			obj:           identity.NamespaceObject("team"),
			store:         newFakeStore(nil),
			wantErr:       identity.ErrDenied,
			wantEntry:     true,
			wantFailed:    false,
			inServerScope: false,
		},
		{
			name:          "allowed action writes nothing",
			authz:         nil,
			subject:       "ann@example.com",
			obj:           identity.NamespaceObject("team"),
			store:         newFakeStore(nil),
			wantErr:       nil,
			wantEntry:     false,
			wantFailed:    false,
			inServerScope: false,
		},
		{
			name:          "error that is not a refusal writes an entry",
			authz:         errStoreDown,
			subject:       "bo@example.com",
			obj:           identity.NamespaceObject("team"),
			store:         newFakeStore(nil),
			wantErr:       errStoreDown,
			wantEntry:     true,
			wantFailed:    false,
			inServerScope: false,
		},
		{
			name:          "guest writes nothing",
			authz:         identity.ErrDenied,
			subject:       "",
			obj:           identity.NamespaceObject("team"),
			store:         newFakeStore(nil),
			wantErr:       identity.ErrDenied,
			wantEntry:     false,
			wantFailed:    false,
			inServerScope: false,
		},
		{
			name:          "failed append keeps the refusal and counts the failure",
			authz:         identity.ErrDenied,
			subject:       "bo@example.com",
			obj:           identity.NamespaceObject("team"),
			store:         newFakeStore(errDiskFull),
			wantErr:       identity.ErrDenied,
			wantEntry:     false,
			wantFailed:    true,
			inServerScope: false,
		},
		{
			name:          "unknown namespace writes the entry in the installation scope",
			authz:         identity.ErrDenied,
			subject:       "bo@example.com",
			obj:           identity.NamespaceObject("gone"),
			store:         newFakeStore(nil),
			wantErr:       identity.ErrDenied,
			wantEntry:     true,
			wantFailed:    false,
			inServerScope: true,
		},
		{
			name:          "installation object writes the server scope",
			authz:         identity.ErrDenied,
			subject:       "bo@example.com",
			obj:           identity.ServerObject(),
			store:         newFakeStore(nil),
			wantErr:       identity.ErrDenied,
			wantEntry:     true,
			wantFailed:    false,
			inServerScope: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRefusal(t, tc)
		})
	}
}

// TestNoStoreIsRefused proves that New fails closed: a Recorder without an
// audit store would let a refusal pass with no entry.
func TestNoStoreIsRefused(t *testing.T) {
	t.Parallel()

	rec, err := refusal.New(fakeAuthorizer{err: identity.ErrDenied}, nil, fakeNamespaces{},
		slog.New(slog.DiscardHandler), nil)
	if !errors.Is(err, refusal.ErrNoStore) {
		t.Fatalf("New answers %v, want %v", err, refusal.ErrNoStore)
	}

	if rec != nil {
		t.Fatal("New answers a recorder with no store")
	}
}

// checkRefusal runs one case through a Recorder and checks the answer, the
// entry and the counted failures.
func checkRefusal(t *testing.T, tc refusalCase) {
	t.Helper()

	failures := 0
	rec, err := refusal.New(fakeAuthorizer{err: tc.authz}, tc.store, fakeNamespaces{},
		slog.New(slog.DiscardHandler), func() { failures++ })
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/stacks",
		strings.NewReader(`{"secret":"hunter2"}`))
	req.Header.Set("User-Agent", "pulumi/3")

	err = rec.Authorize(t.Context(), req,
		identity.Principal{
			Subject: tc.subject, TokenID: "tok-1", DisplayName: "", Admin: false, TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       "",
			Credential: "",
			JobID:      "",
		}, identity.ActionUnitWrite, tc.obj)

	if !errors.Is(err, tc.wantErr) {
		t.Fatalf("Authorize answers %v, want %v", err, tc.wantErr)
	}

	wantFailures := 0
	if tc.wantFailed {
		wantFailures = 1
	}

	if failures != wantFailures {
		t.Fatalf("counted %d failures, want %d", failures, wantFailures)
	}

	if !tc.wantEntry {
		if len(tc.store.entries) != 0 {
			t.Fatalf("wrote %d entries, want none", len(tc.store.entries))
		}

		return
	}

	if len(tc.store.entries) != 1 {
		t.Fatalf("wrote %d entries, want 1", len(tc.store.entries))
	}

	checkEntry(t, tc, tc.store.entries[0])

	if !tc.obj.IsServer() && !tc.inServerScope && tc.store.fenceSeen.FailoverVersion != 3 {
		t.Fatalf("append fence %+v, want the namespace fence", tc.store.fenceSeen)
	}
}

// checkEntry checks the fields of the entry of one refusal.
func checkEntry(t *testing.T, tc refusalCase, e audit.Entry) {
	t.Helper()

	if e.Outcome != audit.OutcomeDenied || e.Kind != audit.KindChange {
		t.Fatalf("entry outcome %q kind %q, want denied and change", e.Outcome, e.Kind)
	}

	if e.Subject != tc.subject || e.Action != string(identity.ActionUnitWrite) || e.UserAgent != "pulumi/3" {
		t.Fatalf("entry = %+v, want the subject, the action and the user agent of the request", e)
	}

	wantScope := tc.obj.Namespace()
	if tc.obj.IsServer() || tc.inServerScope {
		wantScope = audit.ServerScope
	}

	if e.Namespace != wantScope {
		t.Fatalf("entry scope %q, want %q", e.Namespace, wantScope)
	}
}
