package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// sessionPolicyDoc is the session policy on the wire.
type sessionPolicyDoc struct {
	UpdatedAt             *string `json:"updatedAt"`
	IdleSeconds           int64   `json:"idleSeconds"`
	MaxLengthSeconds      int64   `json:"maxLengthSeconds"`
	TokenFreshnessSeconds int64   `json:"tokenFreshnessSeconds"`
	TokenFreshnessDefault bool    `json:"tokenFreshnessDefault"`
}

// newSessionPolicyHandler builds a Handler over a fresh SQLite store whose
// session policy routes run with authz. The store keeps the audit log of
// @server.
func newSessionPolicyHandler(t *testing.T, authz identity.Authorizer) (*sqlite.Store, testHandler) {
	t.Helper()

	st := openRouteTestStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policy service: %v", err)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
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
		Gates:            nil,
		SessionPolicy:    policies,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return st, handler
}

// serveSessionPolicy sends one request to the session policy route with
// the test token.
func serveSessionPolicy(t *testing.T, h testHandler, method, body string) *httptest.ResponseRecorder {
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

	// A session policy change needs a console session with a recent sign-in.
	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	serve := func(method, body string) *httptest.ResponseRecorder {
		return e.sendWithSession(t, method, sess, "/api/server/session-policy", body)
	}

	defaults := decodeSessionPolicy(t, serve(http.MethodGet, ""))
	wantIdle(t, defaults, 900)

	if defaults.UpdatedAt != nil {
		t.Fatalf("default updatedAt = %s, want null", *defaults.UpdatedAt)
	}

	rec := serve(http.MethodPatch, `{"idleSeconds":1800}`)

	got := decodeSessionPolicy(t, rec)
	wantIdle(t, got, 1800)

	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	again := decodeSessionPolicy(t, serve(http.MethodGet, ""))
	wantIdle(t, again, 1800)

	if got.UpdatedAt == nil || again.UpdatedAt == nil || *again.UpdatedAt != *got.UpdatedAt {
		t.Fatalf("updatedAt = %v after the change and %v after a read, want the same time", got.UpdatedAt,
			again.UpdatedAt)
	}

	entries := sessionPolicyEntries(t, e.store)
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

// withoutSessionID returns changes without the sessionId change that the
// entry of a console session holds.
func withoutSessionID(changes []audit.Change) []audit.Change {
	return slices.DeleteFunc(slices.Clone(changes), func(c audit.Change) bool { return c.Key == "sessionId" })
}

// wantIdleChangeEntry fails unless e is the success entry of a change of
// the idle limit from 900 to 1800 seconds.
func wantIdleChangeEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != string(identity.ActionServerManageSignIn) || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry = %+v, want a success of %s", e, identity.ActionServerManageSignIn)
	}

	// A console session adds a sessionId change to the entry.
	changes := withoutSessionID(e.Changes)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", e.Changes)
	}

	c := changes[0]
	if c.Key != "idleSeconds" || c.Old == nil || *c.Old != "900" || c.New != "1800" {
		t.Fatalf("change = %+v, want idleSeconds from 900 to 1800", c)
	}
}

func TestSessionPolicyTokenFreshness(t *testing.T) {
	t.Parallel()

	// A session policy change needs a console session with a recent sign-in.
	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	serve := func(method, body string) *httptest.ResponseRecorder {
		return e.sendWithSession(t, method, sess, "/api/server/session-policy", body)
	}

	defaults := decodeSessionPolicy(t, serve(http.MethodGet, ""))
	if defaults.TokenFreshnessSeconds != 900 || !defaults.TokenFreshnessDefault {
		t.Fatalf("default window = %d (default %t), want 900 from the server default", defaults.TokenFreshnessSeconds,
			defaults.TokenFreshnessDefault)
	}

	got := decodeSessionPolicy(t, serve(http.MethodPatch, `{"tokenFreshnessSeconds":0}`))
	if got.TokenFreshnessSeconds != 0 || got.TokenFreshnessDefault || got.IdleSeconds != 900 {
		t.Fatalf("window after a change to 0 = %+v, want 0 that an administrator set", got)
	}

	entries := sessionPolicyEntries(t, e.store)
	if len(entries) != 1 || len(withoutSessionID(entries[0].Changes)) != 1 ||
		withoutSessionID(entries[0].Changes)[0].Key != "tokenFreshnessSeconds" ||
		withoutSessionID(entries[0].Changes)[0].New != "0" {
		t.Fatalf("entries = %+v, want one change of tokenFreshnessSeconds to 0", entries)
	}
}

func TestSessionPolicyRefusesBadChanges(t *testing.T) {
	t.Parallel()

	// A session policy change needs a console session with a recent sign-in.
	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	serve := func(method, body string) *httptest.ResponseRecorder {
		return e.sendWithSession(t, method, sess, "/api/server/session-policy", body)
	}

	bodies := []string{
		`{}`, `{"other":1}`, `{"idleSeconds":-1}`, `{"idleSeconds":60}`, `{"maxLengthSeconds":600}`,
		`{"maxLengthSeconds":604801}`, `{"maxLengthSeconds":9223372036854775807}`, `not json`,
		`{"tokenFreshnessSeconds":30}`, `{"tokenFreshnessSeconds":604801}`, `{"tokenFreshnessSeconds":-1}`,
	}

	for _, body := range bodies {
		rec := serve(http.MethodPatch, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}

	after := decodeSessionPolicy(t, serve(http.MethodGet, ""))
	wantIdle(t, after, 900)

	if after.UpdatedAt != nil {
		t.Fatalf("updatedAt after refused changes = %s, want null", *after.UpdatedAt)
	}

	for _, e := range sessionPolicyEntries(t, e.store) {
		if e.Outcome != audit.OutcomeFailure || e.Action != string(identity.ActionServerManageSignIn) {
			t.Fatalf("entry = %+v, want only failures of %s", e, identity.ActionServerManageSignIn)
		}
	}

	if n := len(sessionPolicyEntries(t, e.store)); n != len(bodies) {
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
