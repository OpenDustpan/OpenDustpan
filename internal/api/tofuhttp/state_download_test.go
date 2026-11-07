package tofuhttp_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const downloadState = `{"version":4,"serial":1}`

// stateUnits is a fakeUnits that holds one state version.
type stateUnits struct {
	*fakeUnits
}

// Current answers the state version.
func (stateUnits) Current(context.Context, workspace.ID) (workspace.StateVersion, error) {
	var sv workspace.StateVersion

	sv.ID = "sv-1"

	return sv, nil
}

// Open answers the state content.
func (stateUnits) Open(context.Context, workspace.StateVersion) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(downloadState)), nil
}

// recordingAudit keeps the appended entries. A set err fails every append.
type recordingAudit struct {
	audit.Store

	err     error
	entries []audit.Entry
	mu      sync.Mutex
}

// Append records the entry, or answers err.
func (r *recordingAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return audit.Entry{}, r.err
	}

	r.entries = append(r.entries, e)

	return e, nil
}

// kindAuth signs in the test token as the principal p.
type kindAuth struct{ p identity.Principal }

// AuthenticateToken answers p for the admin token.
func (k kindAuth) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != adminToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return k.p, nil
}

// downloadPrincipal returns a test principal with a kind, a credential and a
// job ID.
func downloadPrincipal(kind identity.Kind, credential identity.Credential, jobID string) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "alice",
		DisplayName:    "",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           kind,
		Credential:     credential,
		JobID:          jobID,
	}
}

func newDownloadHandler(t *testing.T, p identity.Principal, store audit.Store) http.Handler {
	t.Helper()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  kindAuth{p: p},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     stateUnits{fakeUnits: newFakeUnits(true)},
		Namespaces:     fakeNamespaces{},
		Audit:          store,
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return h
}

// TestStateReadKindFollowsThePrincipal proves that a read of the state writes
// the kind reveal for a person and tool-operation for any other caller,
// whatever channel the client declares, records the principal kind and the job
// ID, and answers no-store.
func TestStateReadKindFollowsThePrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		principal identity.Principal
		channel   string
		wantKind  audit.Kind
		wantChan  audit.Channel
		wantPKind string
		wantJobID string
	}{
		{
			"unknown kind counts as a person",
			downloadPrincipal("", "", ""),
			"api",
			audit.KindReveal,
			audit.ChannelAPI,
			"",
			"",
		},
		{
			"person with a personal token", downloadPrincipal(identity.KindPerson, identity.CredentialAccessToken, ""),
			"api", audit.KindReveal, audit.ChannelAPI, "person", "",
		},
		{
			"person with a session", downloadPrincipal(identity.KindPerson, identity.CredentialSession, ""),
			"console", audit.KindReveal, audit.ChannelConsole, "person", "",
		},
		{
			"service token", downloadPrincipal(identity.KindService, identity.CredentialAccessToken, ""),
			"api", audit.KindToolOperation, audit.ChannelAPI, "service", "",
		},
		{
			"service token that declares the console",
			downloadPrincipal(identity.KindService, identity.CredentialAccessToken, ""),
			"console",
			audit.KindToolOperation,
			audit.ChannelConsole,
			"service",
			"",
		},
		{
			"workload with a trust token", downloadPrincipal(identity.KindWorkload, identity.CredentialTrustToken, ""),
			"api", audit.KindToolOperation, audit.ChannelAPI, "workload", "",
		},
		{
			"person with a job token", downloadPrincipal(identity.KindPerson, identity.CredentialJobToken, "job-7"),
			"api", audit.KindToolOperation, audit.ChannelAPI, "person", "job-7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingAudit{Store: nil, err: nil, entries: nil, mu: sync.Mutex{}}
			h := newDownloadHandler(t, tc.principal, store)

			headers := bearer(adminToken)
			headers["Dustpan-Channel"] = tc.channel

			rec := do(h, http.MethodGet, unitPath, "", headers)
			if rec.Code != http.StatusOK || rec.Body.String() != downloadState {
				t.Fatalf("read answers %d %q, want 200 and the state", rec.Code, rec.Body.String())
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}

			checkReadEntry(t, store, readWant{
				kind: tc.wantKind, channel: tc.wantChan, principalKind: tc.wantPKind, jobID: tc.wantJobID,
			})
		})
	}
}

// readWant names the fields of the entry that a state read must leave.
type readWant struct {
	kind          audit.Kind
	channel       audit.Channel
	principalKind string
	jobID         string
}

// checkReadEntry fails the test unless store holds exactly one entry of a
// state read, with the wanted kind, channel, principal kind and job ID.
func checkReadEntry(t *testing.T, store *recordingAudit, want readWant) {
	t.Helper()

	if len(store.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(store.entries))
	}

	e := store.entries[0]
	if e.Kind != want.kind || e.Channel != want.channel ||
		e.PrincipalKind != want.principalKind || e.JobID != want.jobID {
		t.Fatalf("kind, channel, principal kind, job ID = %q, %q, %q, %q; want %q, %q, %q, %q",
			e.Kind, e.Channel, e.PrincipalKind, e.JobID, want.kind, want.channel, want.principalKind, want.jobID)
	}

	if e.Action != string(identity.ActionUnitReadSecrets) || e.Object != "opentofu:acme/net" || e.Version != "sv-1" {
		t.Fatalf("entry = %+v, want unit:read-secrets on opentofu:acme/net at sv-1", e)
	}
}

// TestStateReadAnswersNoContentWhenTheEntryIsLost proves that a read whose
// audit entry cannot be written, here because the namespace is fenced, answers
// 500 with no state and counts the failure.
func TestStateReadAnswersNoContentWhenTheEntryIsLost(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{
		Store: nil, err: fmt.Errorf("append: %w", namespace.ErrFenced), entries: nil, mu: sync.Mutex{},
	}

	var failures int

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  kindAuth{p: downloadPrincipal(identity.KindPerson, identity.CredentialAccessToken, "")},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     stateUnits{fakeUnits: newFakeUnits(true)},
		Namespaces:     fakeNamespaces{},
		Audit:          store,
		Gate:           nil,
		OnAuditFailure: func() { failures++ },
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, unitPath, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != "internal error" {
		t.Fatalf("read answers %d %q, want 500 with no state", rec.Code, rec.Body.String())
	}

	if failures != 1 {
		t.Fatalf("failure count = %d, want 1", failures)
	}
}

// TestStateReadNeedsTheSecretAction proves that a caller who may read the unit
// but not its secret values gets 403 and no entry of a read, and that a caller who
// holds both actions reads the state.
func TestStateReadNeedsTheSecretAction(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, err: nil, entries: nil, mu: sync.Mutex{}}
	h := newDownloadHandler(t, downloadPrincipal(identity.KindPerson, identity.CredentialAccessToken, ""), store)

	viewer := downloadPrincipal(identity.KindPerson, identity.CredentialAccessToken, "")
	viewer.Subject, viewer.Admin = "bob", false
	viewerHandler, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  kindAuth{p: viewer},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     stateUnits{fakeUnits: newFakeUnits(true)},
		Namespaces:     fakeNamespaces{},
		Audit:          store,
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	rec := do(viewerHandler, http.MethodGet, unitPath, "", bearer(adminToken))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "serial") {
		t.Fatalf("viewer read answers %d %q, want 403 with no state", rec.Code, rec.Body.String())
	}

	for _, e := range store.entries {
		if e.Outcome == audit.OutcomeSuccess {
			t.Fatalf("viewer read left the entry %+v, want none that succeeded", e)
		}
	}

	if rec = do(h, http.MethodGet, unitPath, "", bearer(adminToken)); rec.Code != http.StatusOK {
		t.Fatalf("read with both actions answers %d, want 200", rec.Code)
	}
}
