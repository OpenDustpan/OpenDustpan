package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// sessionPolicyDoc is the session policy on the wire.
type sessionPolicyDoc struct {
	UpdatedAt        *string `json:"updatedAt"`
	IdleSeconds      int64   `json:"idleSeconds"`
	MaxLengthSeconds int64   `json:"maxLengthSeconds"`
}

// newSessionPolicyHandler builds a Handler over a fresh SQLite store whose
// session policy routes run with authz. The store keeps the audit log of
// @server.
func newSessionPolicyHandler(t *testing.T, authz identity.Authorizer) (*sqlite.Store, *apiserver.Handler) {
	t.Helper()

	st := openRouteTestStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policy service: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:    tokenAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       nss,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            st,
		OnAuditFailure:   nil,
		IPAllowlists:     nil,
		SessionPolicy:    policies,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return st, handler
}

// serveSessionPolicy sends one request to the session policy route with
// the test token.
func serveSessionPolicy(t *testing.T, h *apiserver.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, "/api/server/session-policy", reader)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// decodeSessionPolicy reads a 200 answer as the session policy.
func decodeSessionPolicy(t *testing.T, rec *httptest.ResponseRecorder) sessionPolicyDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc sessionPolicyDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode session policy %q: %v", rec.Body.String(), err)
	}

	return doc
}

// sessionPolicyEntries returns the audit entries of @server whose object is
// the session policy.
func sessionPolicyEntries(t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Object == session.PolicyRef {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

func TestSessionPolicyReadAndChange(t *testing.T) {
	t.Parallel()

	st, h := newSessionPolicyHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	defaults := decodeSessionPolicy(t, serveSessionPolicy(t, h, http.MethodGet, ""))
	wantIdle(t, defaults, 900)

	if defaults.UpdatedAt != nil {
		t.Fatalf("default updatedAt = %s, want null", *defaults.UpdatedAt)
	}

	rec := serveSessionPolicy(t, h, http.MethodPatch, `{"idleSeconds":1800}`)

	got := decodeSessionPolicy(t, rec)
	wantIdle(t, got, 1800)

	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	again := decodeSessionPolicy(t, serveSessionPolicy(t, h, http.MethodGet, ""))
	wantIdle(t, again, 1800)

	if got.UpdatedAt == nil || again.UpdatedAt == nil || *again.UpdatedAt != *got.UpdatedAt {
		t.Fatalf("updatedAt = %v after the change and %v after a read, want the same time", got.UpdatedAt,
			again.UpdatedAt)
	}

	entries := sessionPolicyEntries(t, st)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1", len(entries))
	}

	wantIdleChangeEntry(t, entries[0])
}

// wantIdle fails unless doc holds the idle limit idle and the default
// maximum length of 43200 seconds.
func wantIdle(t *testing.T, doc sessionPolicyDoc, idle int64) {
	t.Helper()

	if doc.IdleSeconds != idle || doc.MaxLengthSeconds != 43200 {
		t.Fatalf("policy = %+v, want %d and 43200", doc, idle)
	}
}

// wantIdleChangeEntry fails unless e is the success entry of a change of
// the idle limit from 900 to 1800 seconds.
func wantIdleChangeEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != string(identity.ActionServerManageSignIn) || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry = %+v, want a success of %s", e, identity.ActionServerManageSignIn)
	}

	if len(e.Changes) != 1 {
		t.Fatalf("changes = %+v, want one", e.Changes)
	}

	c := e.Changes[0]
	if c.Key != "idleSeconds" || c.Old == nil || *c.Old != "900" || c.New != "1800" {
		t.Fatalf("change = %+v, want idleSeconds from 900 to 1800", c)
	}
}

func TestSessionPolicyRefusesBadChanges(t *testing.T) {
	t.Parallel()

	st, h := newSessionPolicyHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	bodies := []string{
		`{}`, `{"other":1}`, `{"idleSeconds":-1}`, `{"idleSeconds":60}`, `{"maxLengthSeconds":600}`,
		`{"maxLengthSeconds":604801}`, `{"maxLengthSeconds":9223372036854775807}`, `not json`,
	}

	for _, body := range bodies {
		rec := serveSessionPolicy(t, h, http.MethodPatch, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}

	after := decodeSessionPolicy(t, serveSessionPolicy(t, h, http.MethodGet, ""))
	wantIdle(t, after, 900)

	if after.UpdatedAt != nil {
		t.Fatalf("updatedAt after refused changes = %s, want null", *after.UpdatedAt)
	}

	for _, e := range sessionPolicyEntries(t, st) {
		if e.Outcome != audit.OutcomeFailure || e.Action != string(identity.ActionServerManageSignIn) {
			t.Fatalf("entry = %+v, want only failures of %s", e, identity.ActionServerManageSignIn)
		}
	}

	if n := len(sessionPolicyEntries(t, st)); n != len(bodies) {
		t.Fatalf("%d entries, want one failure for each of the %d refused changes", n, len(bodies))
	}
}

func TestSessionPolicyChangeNeedsManageSignIn(t *testing.T) {
	t.Parallel()

	_, h := newSessionPolicyHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{
		identity.ActionServerManageSignIn: true,
	}})

	if rec := serveSessionPolicy(t, h, http.MethodGet, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 with %s: %s", rec.Code, identity.ActionServerReadTokens, rec.Body.String())
	}

	if rec := serveSessionPolicy(t, h, http.MethodPatch, `{"idleSeconds":1800}`); rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH = %d, want 403 without %s: %s", rec.Code, identity.ActionServerManageSignIn, rec.Body.String())
	}
}
