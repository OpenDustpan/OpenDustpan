package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agent/agenttest"
	"opendustpan.org/opendustpan/internal/agentsvc"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// agentFixture is one handler with the model connection service over a fresh
// SQLite store, with the audit entries it wrote.
type agentFixture struct {
	store   *sqlite.Store
	handler testHandler
	audit   *testAudit
	// service is the agent service that the handler runs: a test that needs
	// an error of the service calls it here, past no route.
	service *agentsvc.Service
}

// newAgentFixture builds a handler whose Agent service runs over a fresh
// store. authz decides the grants of the test token.
func newAgentFixture(t *testing.T, authz identity.Authorizer) agentFixture {
	t.Helper()

	st := openRouteTestStore(t)

	wrapper, err := secret.NewBuiltinWrapper([]byte(installationKey))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	sealer, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys: st, Wrapper: wrapper, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := agentsvc.New(agentsvc.Config{
		Secrets: st.AgentSecretItems(sealer), Connection: st.AgentConnections(),
		Allowlist: st.AgentEgressAllowlist(), Authorizer: authz, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("agent service: %v", err)
	}

	aud := newTestAudit()

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Agent:              svc,
		FirstAdministrator: nil,
		ProxyRules:         nil,
		Cursors:            testCursors(t),
		Reach:              noReach(),
		LocalUsers:         nil,
		Preferences:        nil,
		GitHostLinks:       nil,
		Settings:           nil,
		GitHost:            nil,
		Idempotency:        nil,
		Workers:            nil,
		InstallationKind:   "",
		StateWriteGates:    stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:                nil,
		TempAdmins:         nil,
		Authenticator:      tokenAuthenticator{},
		Authorizer:         authz,
		Logger:             slog.New(slog.DiscardHandler),
		Namespaces:         nss,
		Backups:            nil,
		Restores:           nil,
		Maintenance:        newFakeMaintenance(maintenance.StateNormal),
		Metrics:            stubMetrics{},
		Version:            "test",
		Schema:             st,
		Journal:            nil,
		Principals:         nil,
		Access:             nil,
		Tokens:             nil,
		Audit:              aud,
		OnAuditFailure:     nil,
		DeletedUnits:       nil,
		AuditChain:         nil,
		AuditSinks:         nil,
		IPAllowlists:       nil,
		Gates:              nil,
		SessionPolicy:      nil,
		SignIn:             nil,
		CheckpointEpochs:   nil,
		DenialLimiter:      nil,
		Instances:          nil,
		StoreChecks:        nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return agentFixture{store: st, handler: handler, audit: aud, service: svc}
}

// allowAgent returns the fixture with both agent actions allowed.
func allowAgent(t *testing.T) agentFixture {
	t.Helper()

	return newAgentFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
}

// serveAgent sends one request with the test token to the agent routes.
func serveAgent(t *testing.T, h testHandler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, reader)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// agentItemDoc is one secret item on the wire: metadata only, never a value.
type agentItemDoc struct {
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// agentItemListDoc is the list of the secret items of the installation.
type agentItemListDoc struct {
	Secrets []agentItemDoc `json:"secrets"`
}

// agentPendingDoc is one pending document on the wire: the document that a
// probe wrote, with the reference of the probe.
type agentPendingDoc struct {
	Probe    string          `json:"probe"`
	Document json.RawMessage `json:"document"`
	Version  int64           `json:"version"`
}

// agentConnectionDoc is the current model connection document on the wire,
// with the pending documents and their probes.
type agentConnectionDoc struct {
	Document json.RawMessage   `json:"document"`
	Version  int64             `json:"version"`
	Pending  []agentPendingDoc `json:"pending"`
}

// decodeAgentItems reads a 200 list of secret items.
func decodeAgentItems(t *testing.T, rec *httptest.ResponseRecorder) agentItemListDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc agentItemListDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode secret list %q: %v", rec.Body.String(), err)
	}

	return doc
}

// createAgentItem stores name with value and returns its metadata and the raw
// bodies of the create and the list: 201 with the reference, the name and the
// time, and never the value.
func createAgentItem(t *testing.T, f agentFixture, name, value string) (agentItemDoc, []string) {
	t.Helper()

	rec := serveAgent(t, f.handler, http.MethodPost, "/api/agent/secrets",
		`{"name":`+quoteForAgent(t, name)+`,"value":`+quoteForAgent(t, value)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST secrets answers %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var doc agentItemDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode created item %q: %v", rec.Body.String(), err)
	}

	if !strings.HasPrefix(doc.Ref, "itm-") || doc.Name != name || doc.CreatedAt == "" {
		t.Fatalf("created item = %+v, want a reference, the name and a time", doc)
	}

	bodies := make([]string, 0, 2)
	bodies = append(bodies, rec.Body.String())

	listed := decodeAgentItems(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/secrets", ""))
	bodies = append(bodies, mustMarshalAgent(t, listed))

	return doc, bodies
}

// mustMarshalAgent marshals v for the value checks of the tests.
func mustMarshalAgent(t *testing.T, v agentItemListDoc) string {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal secret list: %v", err)
	}

	return string(raw)
}

// quoteForAgent quotes s as a JSON string for a request body of the tests.
func quoteForAgent(t *testing.T, s string) string {
	t.Helper()

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("quote %q: %v", s, err)
	}

	return string(raw)
}

// seedAgentConnection stores a current document without a secret through the
// store port: PUT /api/agent/connection is a later change.
func seedAgentConnection(t *testing.T, st *sqlite.Store, raw string) {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(raw))
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}

	entry := agenttest.TestEntry(audit.ServerScope, "admin", "server:manage-agent")
	conns := st.AgentConnections()

	if err := conns.PutPending(t.Context(), agent.ProbeRef("probe-1"), spec, entry); err != nil {
		t.Fatalf("put pending document: %v", err)
	}

	if err := conns.Promote(t.Context(), agent.ProbeRef("probe-1"), entry); err != nil {
		t.Fatalf("promote pending document: %v", err)
	}
}

// seedAgentPending stores a pending document without promoting it, as a
// probe would: PUT /api/agent/connection is a later change.
func seedAgentPending(t *testing.T, st *sqlite.Store, raw, probe string) {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(raw))
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}

	entry := agenttest.TestEntry(audit.ServerScope, "admin", "server:manage-agent")
	if err := st.AgentConnections().PutPending(t.Context(), agent.ProbeRef(probe), spec, entry); err != nil {
		t.Fatalf("put pending document: %v", err)
	}
}

// agentDocWithoutItem is a valid document that refers to no secret item.
const agentDocWithoutItem = `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`

// TestSecretRoutes_NeverAnswerValue proves no secret route answers a value:
// the create, list and replace answers hold the reference, the name and the
// time only, and a delete leaves no item.
func TestSecretRoutes_NeverAnswerValue(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)
	first, second := "route-test-value-1", "route-test-value-2"

	created, bodies := createAgentItem(t, f, "api-key", first)

	listed := decodeAgentItems(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/secrets", ""))
	if len(listed.Secrets) != 1 || listed.Secrets[0] != created {
		t.Fatalf("secret list = %+v, want the created item %+v", listed.Secrets, created)
	}

	bodies = append(bodies, mustMarshalAgent(t, listed))

	rec := serveAgent(t, f.handler, http.MethodPut, "/api/agent/secrets/"+created.Ref,
		`{"value":`+quoteForAgent(t, second)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT secret answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	after := decodeAgentItems(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/secrets", ""))
	if len(after.Secrets) != 1 || after.Secrets[0].Ref != created.Ref {
		t.Fatalf("secret list after replace = %+v, want the item", after.Secrets)
	}

	bodies = append(bodies, mustMarshalAgent(t, after))

	remove := serveAgent(t, f.handler, http.MethodDelete, "/api/agent/secrets/"+created.Ref, "")
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE secret answers %d, want 204: %s", remove.Code, remove.Body.String())
	}

	if body := remove.Body.String(); body != "" {
		t.Fatalf("DELETE body = %q, want none", body)
	}

	empty := decodeAgentItems(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/secrets", ""))
	if len(empty.Secrets) != 0 {
		t.Fatalf("secret list after delete = %+v, want none", empty.Secrets)
	}

	for _, body := range append(bodies, rec.Body.String()) {
		for _, value := range []string{first, second} {
			if strings.Contains(body, value) {
				t.Fatalf("an answer holds the secret value: %s", body)
			}
		}
	}
}

// TestConnectionRoutes_ReadDelete proves GET answers 404 with the
// not-connected code when no operator wrote a document, 200 with the document
// and its version after the store holds one, and DELETE removes it in one
// transaction: GET and DELETE answer 404 after it.
func TestConnectionRoutes_ReadDelete(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)

	none := serveAgent(t, f.handler, http.MethodGet, "/api/agent/connection", "")
	if none.Code != http.StatusNotFound {
		t.Fatalf("GET connection answers %d, want 404: %s", none.Code, none.Body.String())
	}

	if got := decodeErrorBody(t, none).ErrorCode; got != "agent-connection.not-connected" {
		t.Fatalf("GET connection errorCode = %q, want agent-connection.not-connected", got)
	}

	seedAgentConnection(t, f.store, agentDocWithoutItem)

	wantSeededConnection(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/connection", ""))

	remove := serveAgent(t, f.handler, http.MethodDelete, "/api/agent/connection", "")
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE connection answers %d, want 204: %s", remove.Code, remove.Body.String())
	}

	if body := remove.Body.String(); body != "" {
		t.Fatalf("DELETE body = %q, want none", body)
	}

	gone := serveAgent(t, f.handler, http.MethodGet, "/api/agent/connection", "")
	if gone.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE answers %d, want 404: %s", gone.Code, gone.Body.String())
	}

	again := serveAgent(t, f.handler, http.MethodDelete, "/api/agent/connection", "")
	if again.Code != http.StatusNotFound {
		t.Fatalf("DELETE after DELETE answers %d, want 404: %s", again.Code, again.Body.String())
	}
}

// wantSeededConnection checks that rec answers 200 with the seeded
// document, version 1 and no pending document.
func wantSeededConnection(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("GET connection answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var doc agentConnectionDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode connection %q: %v", rec.Body.String(), err)
	}

	if doc.Version != 1 {
		t.Fatalf("connection version = %d, want 1", doc.Version)
	}

	if len(doc.Pending) != 0 {
		t.Fatalf("connection pending = %+v, want none", doc.Pending)
	}

	var raw map[string]any
	if err := json.Unmarshal(doc.Document, &raw); err != nil {
		t.Fatalf("decode document %q: %v", doc.Document, err)
	}

	if raw["model"] != "example-model" {
		t.Fatalf("connection model = %v, want example-model", raw["model"])
	}
}

// TestConnectionRoutes_AnswersPending proves GET answers the pending
// documents with their probes, next to the current document, and an empty
// list when none is pending.
func TestConnectionRoutes_AnswersPending(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)

	seedAgentConnection(t, f.store, agentDocWithoutItem)
	seedAgentPending(t, f.store, agentDocWithoutItem, "probe-pending-route")

	rec := serveAgent(t, f.handler, http.MethodGet, "/api/agent/connection", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET connection answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var doc agentConnectionDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode connection %q: %v", rec.Body.String(), err)
	}

	if len(doc.Pending) != 1 {
		t.Fatalf("connection pending = %+v, want the one pending document", doc.Pending)
	}

	if doc.Pending[0].Probe != "probe-pending-route" || doc.Pending[0].Version != 1 {
		t.Fatalf("pending document = %+v, want the probe and version 1", doc.Pending[0])
	}

	var raw map[string]any
	if err := json.Unmarshal(doc.Pending[0].Document, &raw); err != nil {
		t.Fatalf("decode pending document %q: %v", doc.Pending[0].Document, err)
	}

	if raw["model"] != "example-model" {
		t.Fatalf("pending model = %v, want example-model", raw["model"])
	}

	// The promote of the probe removes its pending document.
	entry := agenttest.TestEntry(audit.ServerScope, "admin", "server:manage-agent")
	probe := agent.ProbeRef("probe-pending-route")
	if err := f.store.AgentConnections().Promote(t.Context(), probe, entry); err != nil {
		t.Fatalf("promote pending document: %v", err)
	}

	rec = serveAgent(t, f.handler, http.MethodGet, "/api/agent/connection", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET connection after promote answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode connection %q: %v", rec.Body.String(), err)
	}

	if len(doc.Pending) != 0 {
		t.Fatalf("connection pending after promote = %+v, want none", doc.Pending)
	}
}

// TestConnectionRoutes_Formats proves GET answers the wire formats that a
// connection document can name now.
func TestConnectionRoutes_Formats(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)

	rec := serveAgent(t, f.handler, http.MethodGet, "/api/agent/formats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET formats answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// The answer is an object with one key, not a bare array: the API
	// description names each response schema, and the client generator
	// accepts a named object only.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode formats %q: %v", rec.Body.String(), err)
	}

	if len(body) != 1 {
		t.Fatalf("formats answer has %d keys, want one key formats: %s", len(body), rec.Body.String())
	}

	var formats []string
	if err := json.Unmarshal(body["formats"], &formats); err != nil {
		t.Fatalf("decode formats key %q: %v", rec.Body.String(), err)
	}

	if strings.Join(formats, ",") != "chat-completions,messages" {
		t.Fatalf("formats = %v, want chat-completions and messages", formats)
	}
}

// TestConnectionRoutes_OversizeBody proves a body over the 32 KiB limit of a
// route answers 413 and changes nothing.
func TestConnectionRoutes_OversizeBody(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)
	big := `{"name":"big","value":"` + strings.Repeat("x", 33*1024) + `"}`

	create := serveAgent(t, f.handler, http.MethodPost, "/api/agent/secrets", big)
	if create.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize POST secrets answers %d, want 413: %s", create.Code, create.Body.String())
	}

	allowlist := `{"hosts":[],"cidrs":[` + strings.Repeat(`{"cidr":"10.0.0.0/8","port":443,"http":false},`, 1024) + `]}`
	change := serveAgent(t, f.handler, http.MethodPut, "/api/server/egress-allowlist", allowlist)
	if change.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize PUT allowlist answers %d, want 413: %s", change.Code, change.Body.String())
	}

	listed := decodeAgentItems(t, serveAgent(t, f.handler, http.MethodGet, "/api/agent/secrets", ""))
	if len(listed.Secrets) != 0 {
		t.Fatalf("secret list after an oversize create = %+v, want none", listed.Secrets)
	}
}

// agentRouteCall is one request of the forbidden and fail-closed tables.
type agentRouteCall struct {
	method string
	target string
	body   string
}

// agentRouteTable lists every route of the change with a valid body.
func agentRouteTable() []agentRouteCall {
	return []agentRouteCall{
		{method: http.MethodPost, target: "/api/agent/secrets", body: `{"name":"api-key","value":"x"}`},
		{method: http.MethodGet, target: "/api/agent/secrets", body: ""},
		{method: http.MethodPut, target: "/api/agent/secrets/itm-abc123", body: `{"value":"x"}`},
		{method: http.MethodDelete, target: "/api/agent/secrets/itm-abc123", body: ""},
		{method: http.MethodGet, target: "/api/agent/connection", body: ""},
		{method: http.MethodDelete, target: "/api/agent/connection", body: ""},
		{method: http.MethodGet, target: "/api/agent/formats", body: ""},
		{method: http.MethodGet, target: "/api/server/egress-allowlist", body: ""},
		{method: http.MethodPut, target: "/api/server/egress-allowlist", body: `{"hosts":[],"cidrs":[]}`},
	}
}

// TestAgentRoutes_Forbidden proves a principal without server:manage-agent
// gets 403 on each secret, connection and format route.
func TestAgentRoutes_Forbidden(t *testing.T) {
	t.Parallel()

	denied := map[identity.Action]bool{identity.ActionServerManageAgent: true}

	for _, call := range agentRouteTable() {
		if strings.HasPrefix(call.target, "/api/server/") {
			continue
		}

		t.Run(call.method+" "+call.target, func(t *testing.T) {
			t.Parallel()

			f := newAgentFixture(t, selectiveAuthorizer{deny: denied})

			if rec := serveAgent(t, f.handler, call.method, call.target, call.body); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s answers %d, want 403: %s", call.method, call.target, rec.Code, rec.Body.String())
			}

			// The denied route writes its audit entry: the server audits
			// each call that reaches it.
			if n := len(f.audit.entries); n != 1 {
				t.Fatalf("%s %s wrote %d audit entries, want the one denial", call.method, call.target, n)
			}

			if got := f.audit.entries[0]; got.Outcome != audit.OutcomeDenied {
				t.Fatalf("%s %s entry outcome = %q, want denied", call.method, call.target, got.Outcome)
			}
		})
	}
}

// TestEgressAllowlistRoutes_Forbidden proves a principal without
// server:manage-settings gets 403 on the read and the change of the allowlist.
func TestEgressAllowlistRoutes_Forbidden(t *testing.T) {
	t.Parallel()

	denied := map[identity.Action]bool{identity.ActionServerManageSettings: true}

	for _, call := range []agentRouteCall{
		{method: http.MethodGet, target: "/api/server/egress-allowlist", body: ""},
		{method: http.MethodPut, target: "/api/server/egress-allowlist", body: `{"hosts":[],"cidrs":[]}`},
	} {
		t.Run(call.method, func(t *testing.T) {
			t.Parallel()

			f := newAgentFixture(t, selectiveAuthorizer{deny: denied})

			if rec := serveAgent(t, f.handler, call.method, call.target, call.body); rec.Code != http.StatusForbidden {
				t.Fatalf("%s allowlist answers %d, want 403: %s", call.method, rec.Code, rec.Body.String())
			}

			// The denied route writes its audit entry: the server audits
			// each call that reaches it.
			if n := len(f.audit.entries); n != 1 {
				t.Fatalf("%s allowlist wrote %d audit entries, want the one denial", call.method, n)
			}

			if got := f.audit.entries[0]; got.Outcome != audit.OutcomeDenied {
				t.Fatalf("%s allowlist entry outcome = %q, want denied", call.method, got.Outcome)
			}
		})
	}
}

// TestEgressAllowlistRoutes_ReadWrite proves GET answers the empty allowlist
// when none was stored, and PUT stores the list and answers it.
func TestEgressAllowlistRoutes_ReadWrite(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)

	rec := serveAgent(t, f.handler, http.MethodGet, "/api/server/egress-allowlist", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET allowlist answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if strings.TrimSpace(rec.Body.String()) != `{"hosts":[],"cidrs":[]}` {
		t.Fatalf("GET allowlist = %s, want empty lists, never null", rec.Body.String())
	}

	change := serveAgent(t, f.handler, http.MethodPut, "/api/server/egress-allowlist",
		`{"hosts":["models.example.com"],"cidrs":[{"cidr":"10.20.0.0/16","port":443,"http":false}]}`)
	if change.Code != http.StatusOK {
		t.Fatalf("PUT allowlist answers %d, want 200: %s", change.Code, change.Body.String())
	}

	if !strings.Contains(change.Body.String(), "models.example.com") ||
		!strings.Contains(change.Body.String(), "10.20.0.0/16") {
		t.Fatalf("PUT allowlist = %s, want the stored list", change.Body.String())
	}
}

// TestAuditEntries_NoValue proves no audit entry of a secret item change or
// an allowlist change holds a secret value.
func TestAuditEntries_NoValue(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)
	value := "route-test-value-9"

	createAgentItem(t, f, "api-key", value)

	change := serveAgent(t, f.handler, http.MethodPut, "/api/server/egress-allowlist",
		`{"hosts":["models.example.com"],"cidrs":[]}`)
	if change.Code != http.StatusOK {
		t.Fatalf("PUT allowlist answers %d, want 200: %s", change.Code, change.Body.String())
	}

	entries := listServerEntries(t, f)

	if len(entries) < 2 {
		t.Fatalf("audit entries = %d, want a secret item entry and an allowlist entry", len(entries))
	}

	for i := range entries {
		if strings.Contains(fmt.Sprintf("%+v", entries[i]), value) {
			t.Fatalf("entry %d holds the secret value: %+v", i, entries[i])
		}
	}
}

// listServerEntries returns the audit entries of the installation scope,
// oldest first. Successful writes append their entries to the store, while
// the denial recorder of the handler writes refusals to the test audit.
func listServerEntries(t *testing.T, f agentFixture) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for before := int64(0); ; {
		listing, err := f.store.List(
			t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: before},
		)
		if err != nil {
			t.Fatalf("list audit entries: %v", err)
		}

		out = append(out, listing.Entries...)

		if n := len(listing.Entries); n > 0 {
			before = listing.Entries[n-1].Sequence
		}

		if !listing.More {
			break
		}
	}

	return out
}

// agentFailCase is one hostile input of the fail-closed table.
type agentFailCase struct {
	name     string
	method   string
	target   string
	body     string
	want     int
	wantCode string
	// denied marks the case whose refusal is the missing grant: the
	// authorizer denies the action before the handler runs.
	denied   bool
	seedUsed bool
}

// agentFailTable lists the hostile inputs: each is denied with its status
// and code, and each refusal writes one denied audit entry.
func agentFailTable() []agentFailCase {
	return []agentFailCase{
		{
			name: "secret reference with no form", method: http.MethodPut,
			target: "/api/agent/secrets/not-a-ref", body: `{"value":"x"}`,
			want:     http.StatusBadRequest,
			wantCode: "agent-connection.secret-invalid",
			denied:   false, seedUsed: false,
		},
		{
			name: "unknown secret reference", method: http.MethodPut,
			target: "/api/agent/secrets/itm-unknown000", body: `{"value":"x"}`,
			want:     http.StatusNotFound,
			wantCode: "agent-connection.secret-not-found",
			denied:   false, seedUsed: false,
		},
		{
			name: "secret item in use", method: http.MethodDelete,
			target: "/api/agent/secrets/", body: "",
			want:     http.StatusConflict,
			wantCode: "agent-connection.secret-in-use",
			denied:   false, seedUsed: true,
		},
		{
			name: "unknown field in secret body", method: http.MethodPost,
			target: "/api/agent/secrets", body: `{"name":"api-key","value":"x","extra":1}`,
			want:     http.StatusBadRequest,
			wantCode: "agent-connection.secret-invalid",
			denied:   false, seedUsed: false,
		},
		{
			name: "allowlist without cidrs", method: http.MethodPut,
			target: "/api/server/egress-allowlist", body: `{"hosts":[]}`,
			want:     http.StatusBadRequest,
			wantCode: "agent-connection.allowlist-invalid",
			denied:   false, seedUsed: false,
		},
		{
			name: "allowlist with a denied range", method: http.MethodPut,
			target:   "/api/server/egress-allowlist",
			body:     `{"hosts":[],"cidrs":[{"cidr":"127.0.0.0/8","port":443,"http":false}]}`,
			want:     http.StatusBadRequest,
			wantCode: "agent-connection.allowlist-invalid",
			denied:   false, seedUsed: false,
		},
		{
			name: "allowlist rule with host bits", method: http.MethodPut,
			target:   "/api/server/egress-allowlist",
			body:     `{"hosts":[],"cidrs":[{"cidr":"10.20.5.0/16","port":443,"http":false}]}`,
			want:     http.StatusBadRequest,
			wantCode: "agent-connection.allowlist-invalid",
			denied:   false, seedUsed: false,
		},
		{
			name: "connection of a new installation", method: http.MethodGet,
			target: "/api/agent/connection", body: "",
			want:     http.StatusNotFound,
			wantCode: "agent-connection.not-connected",
			denied:   false, seedUsed: false,
		},
		{
			name: "principal without the grant", method: http.MethodGet,
			target: "/api/agent/secrets", body: "",
			want:     http.StatusForbidden,
			wantCode: "access.denied",
			denied:   true, seedUsed: false,
		},
	}
}

// seedUsedSecret stores a current document that refers to the created item,
// so its delete answers 409 while the document stays.
func seedUsedSecret(t *testing.T, f agentFixture, ref string) {
	t.Helper()

	raw := `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "x-api-key", "prefix": "", "secret": "` + ref + `" },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`
	seedAgentConnection(t, f.store, raw)
}

// checkAgentFailCase serves one hostile input and checks its denial and its
// audit entry.
func checkAgentFailCase(t *testing.T, tc agentFailCase) {
	t.Helper()

	var f agentFixture
	if tc.denied {
		f = newAgentFixture(t, selectiveAuthorizer{deny: map[identity.Action]bool{
			identity.ActionServerManageAgent: true,
		}})
	} else {
		f = allowAgent(t)
	}

	target := tc.target

	if tc.seedUsed {
		created, _ := createAgentItem(t, f, "api-key", "route-test-value-4")
		seedUsedSecret(t, f, created.Ref)

		target += created.Ref
	}

	before := len(f.audit.entries)

	rec := serveAgent(t, f.handler, tc.method, target, tc.body)
	if rec.Code != tc.want {
		t.Fatalf("%s answers %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body.String())
	}

	if got := decodeErrorBody(t, rec).ErrorCode; got != tc.wantCode {
		t.Fatalf("%s errorCode = %q, want %q: %s", tc.name, got, tc.wantCode, rec.Body.String())
	}

	// The server audits each call that reaches it: the refusal writes one
	// denied entry, at the authorize step or in the handler.
	if len(f.audit.entries) != before+1 {
		t.Fatalf("%s wrote %d audit entries, want the one denial", tc.name, len(f.audit.entries)-before)
	}

	if got := f.audit.entries[len(f.audit.entries)-1]; got.Outcome != audit.OutcomeDenied {
		t.Fatalf("%s entry outcome = %q, want denied", tc.name, got.Outcome)
	}
}

// TestFailClosed_Table proves each hostile input is denied with its status
// and code, and a refusal writes an audit entry.
func TestFailClosed_Table(t *testing.T) {
	t.Parallel()

	for _, tc := range agentFailTable() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkAgentFailCase(t, tc)
		})
	}
}

// TestSecretServicePath_Refused sends the hostile item of the review, a
// secret item whose value is empty, straight to the service of the agent
// routes, past no check of a route. The service refuses it with
// agent.ErrInvalid, the routes answer 400 for that error, and the answer
// records the denied audit entry, as each refusal of a route does.
func TestSecretServicePath_Refused(t *testing.T) {
	t.Parallel()

	f := allowAgent(t)

	p := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "Admin",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
	base := agenttest.TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	_, err := f.service.CreateSecret(t.Context(), p, base, "api-key", nil)
	if !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("CreateSecret() = %v, want agent.ErrInvalid", err)
	}

	status, message := apiserver.ClassifyAgentForTest(err)
	if status != http.StatusBadRequest {
		t.Fatalf("classifyAgent() = %d, %q, want 400", status, message)
	}

	if strings.Contains(message, "api-key") {
		t.Fatalf("classifyAgent() message %q holds a request value", message)
	}

	// The refusal that the routes write for this error is a bad request, and
	// it records the denial with the refusal text, never the request value.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/agent/secrets", http.NoBody)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()

	before := len(f.audit.entries)

	f.handler.WriteAgentRefusalForTest(rec, req, p, identity.ActionServerManageAgent, err)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("refusal answers %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	if got := decodeErrorBody(t, rec).ErrorCode; got != "agent-connection.secret-invalid" {
		t.Fatalf("refusal errorCode = %q, want agent-connection.secret-invalid", got)
	}

	if len(f.audit.entries) != before+1 {
		t.Fatalf("refusal wrote %d audit entries, want the one denial", len(f.audit.entries)-before)
	}

	last := f.audit.entries[len(f.audit.entries)-1]

	if last.Outcome != audit.OutcomeDenied {
		t.Fatalf("denial outcome = %q, want denied", last.Outcome)
	}

	if strings.Contains(fmt.Sprintf("%+v", last), "api-key") {
		t.Fatalf("denial entry %+v holds a request value", last)
	}
}
