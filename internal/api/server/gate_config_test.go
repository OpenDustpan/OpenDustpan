package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	gateBase     = "/api/dustpan/v1/namespaces/"
	gateDefault  = "/approval-default"
	gateRulesURL = "/approval-rules"
)

// gateAuthorizer allows an action only on the namespace that the test names
// for it. An action that is not in the map is denied.
type gateAuthorizer struct {
	allow map[identity.Action]namespace.Name
}

func (a gateAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	if want, ok := a.allow[action]; ok && want == obj.Namespace() {
		return nil
	}

	return identity.ErrDenied
}

func (gateAuthorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// adminGates allows both gate actions on acme and beta.
func adminGates() gateAuthorizer {
	return gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceReadGates:   "acme",
		identity.ActionNamespaceManageGates: "acme",
	}}
}

// adminGatesAndInstallation allows the two gate actions on acme and the two
// installation gate actions on the installation.
func adminGatesAndInstallation() gateAuthorizer {
	return gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceReadGates:      "acme",
		identity.ActionNamespaceManageGates:    "acme",
		identity.ActionInstallationReadGates:   "",
		identity.ActionInstallationManageGates: "",
	}}
}

// gateEnv is a handler over a fresh store with acme and beta, and a console
// session with a fresh sign-in. It sends each write as that session: an
// access token never gives fresh authentication. It sends each read with an
// access token. The raw handler stays for tests of the token refusal.
type gateEnv struct {
	env     signInEnv
	session *http.Cookie
	csrf    string
}

// ServeHTTP sends a PUT or a DELETE as the session and any other request
// with its own credential.
func (g gateEnv) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		r.Header.Del("Authorization")
		r.AddCookie(g.session)
		r.Header.Set(apiserver.CSRFHeader, g.csrf)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}

	g.env.handler.ServeHTTP(w, r)
}

// newGateEnv builds a gateEnv that authorizes with authz.
func newGateEnv(t *testing.T, authz identity.Authorizer) gateEnv {
	t.Helper()

	return newGateEnvWith(t, authz, false)
}

// newGateEnvWith builds a gateEnv that authorizes with authz. With noEngine, the
// gate config service has no policy engine.
func newGateEnvWith(t *testing.T, authz identity.Authorizer, noEngine bool) gateEnv {
	t.Helper()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate:        true,
		idempotency:       false,
		failTempAdminRead: false,
		noPolicyRoutes:    false,
		noGateEngine:      noEngine,
		stored:            nil,
		authz:             authz,
		hostile:           nil,
		passwords:         nil,
		namespaces:        []namespace.Name{"acme", "beta"},
		failPolicyRead:    nil,
	})
	sess := e.freshSignIn(t)

	createGateUnits(t, e.store)

	// The session read needs an action that the test authorizer may deny, so
	// the CSRF value comes from the session secret.
	secret, err := session.ParseSecret(sess.Value)
	if err != nil {
		t.Fatalf("parse session secret: %v", err)
	}

	return gateEnv{env: e, session: sess, csrf: session.CSRFValue(secret)}
}

// createGateUnits makes the units of the unit scopes of the tests in acme:
// the Pulumi stack web/prod and the OpenTofu workspace net. The gates of a
// unit belong to the unit, so a write at the scope of a unit needs it.
func createGateUnits(t *testing.T, st *sqlite.Store) {
	t.Helper()

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	now := time.Now()

	err = st.CreateStack(t.Context(), stack.Stack{
		NamespaceID: "", CreatedAt: now, UpdatedAt: now, Tags: map[string]string{}, Ref: ref,
		ID: stack.ID("stk-gate-web-prod"), Version: 0,
	}, ns.Fence())
	if err != nil {
		t.Fatalf("create stack: %v", err)
	}

	createGateWorkspace(t, st, "ws-gate-net", "net")
}

// createGateWorkspace makes the OpenTofu workspace name of acme with the ID id.
func createGateWorkspace(t *testing.T, st *sqlite.Store, id workspace.ID, name workspace.Name) {
	t.Helper()

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	now := time.Now()

	err = st.CreateWorkspace(t.Context(), workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: "acme", Name: name},
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}, ns.Fence())
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
}

// gateTestEntry returns an audit entry of acme for the setup writes of a test.
func gateTestEntry(action string) audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: "acme", Subject: "admin", IDPSubject: "", TokenID: "", Action: action,
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Reason: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Origin: nil,
	}
}

// newGateHandler builds a gateEnv over a fresh store with acme and beta.
func newGateHandler(t *testing.T, authz identity.Authorizer) (*sqlite.Store, gateEnv) {
	t.Helper()

	g := newGateEnv(t, authz)

	return g.env.store, g
}

// newGateHandlerWithoutStore builds a handler that mounts no gate route.
func newGateHandlerWithoutStore(t *testing.T, authz identity.Authorizer) testHandler {
	t.Helper()

	st, nss := openIPAllowFixture(t)

	return gateHandler(t, st, nss, authz, nil)
}

// gateHandler builds a handler over st and nss that serves the gate routes
// from port, or none when port is nil.
func gateHandler(
	t *testing.T, st *sqlite.Store, nss *namespace.Service, authz identity.Authorizer, port apiserver.GateConfig,
) testHandler {
	t.Helper()

	handler, err := newCheckedHandler(t, &apiserver.Config{
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
		Audit:              st,
		OnAuditFailure:     nil,
		IPAllowlists:       nil,
		Gates:              port,
		SessionPolicy:      nil,
		DeletedUnits:       nil,
		AuditChain:         nil,
		AuditSinks:         nil,
		SignIn:             nil,
		CheckpointEpochs:   nil,
		DenialLimiter:      nil,
		Instances:          nil,
		StoreChecks:        nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// gateCall is one request of a gate configuration test.
type gateCall struct {
	method  string
	target  string
	body    string
	ifMatch string
}

func serveGate(t *testing.T, h http.Handler, c gateCall) *httptest.ResponseRecorder {
	t.Helper()

	var body io.Reader = http.NoBody
	if c.body != "" {
		body = strings.NewReader(c.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, body)
	req.RemoteAddr = ipTestRemote
	req.Header.Set("Authorization", "Bearer test-token")

	if c.ifMatch != "" {
		req.Header.Set("If-Match", c.ifMatch)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

// defaultDoc and ruleDoc are the answers of the routes.
type approversDoc struct {
	Groups     []string `json:"groups"`
	Users      []string `json:"users"`
	UnitOwners bool     `json:"unitOwners"`
}

type defaultDoc struct {
	Count            int          `json:"count"`
	FreshAuthSeconds *int         `json:"freshAuthSeconds"`
	Approvers        approversDoc `json:"approvers"`
	Version          int64        `json:"version"`
}

type ruleDoc struct {
	ID               string       `json:"id"`
	Count            int          `json:"count"`
	AuthorMayApprove bool         `json:"authorMayApprove"`
	TimeoutSeconds   *int64       `json:"timeoutSeconds"`
	Approvers        approversDoc `json:"approvers"`
	Version          int64        `json:"version"`
}

func decodeGateJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()

	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

const platformDefault = `{"count":2,"approvers":{"groups":["platform"],"users":[],"unitOwners":false}}`

// TestApprovalDefaultLifecycle follows one approval default through create,
// read, replace, delete and a second delete.
func TestApprovalDefaultLifecycle(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	target := gateBase + "acme" + gateDefault

	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), http.StatusNotFound)

	rec := serveGate(t, h, gateCall{http.MethodPut, target, platformDefault, ""})
	wantStatus(t, rec, http.StatusOK)

	var created defaultDoc

	decodeGateJSON(t, rec, &created)

	if created.Count != 2 || created.Version != 1 || strings.Join(created.Approvers.Groups, ",") != "platform" {
		t.Fatalf("created default = %+v, want count 2, version 1, group platform", created)
	}

	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", got)
	}

	rec = serveGate(t, h, gateCall{http.MethodPut, target, `{"count":3,"approvers":{}}`, ""})
	wantStatus(t, rec, http.StatusOK)

	var replaced defaultDoc

	decodeGateJSON(t, rec, &replaced)

	if replaced.Count != 3 || replaced.Version != 2 || replaced.Approvers.Groups == nil {
		t.Fatalf("replaced default = %+v, want count 3, version 2, empty (not null) groups", replaced)
	}

	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, target, "", ""}), http.StatusNoContent)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), http.StatusNotFound)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, target, "", ""}), http.StatusNoContent)
}

// TestApprovalDefaultVersionPrecondition proves that If-Match protects a
// default from a lost update, and that a bad header is refused.
func TestApprovalDefaultVersionPrecondition(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	target := gateBase + "acme" + gateDefault

	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodPut, target, platformDefault, ""}),
		http.StatusOK,
	)

	tests := []struct {
		name    string
		call    gateCall
		want    int
		version int64
	}{
		{
			"stale put",
			gateCall{http.MethodPut, target, platformDefault, `"7"`},
			http.StatusConflict,
			1,
		},
		{"stale delete", gateCall{http.MethodDelete, target, "", `"7"`}, http.StatusConflict, 1},
		{
			"not a version",
			gateCall{http.MethodPut, target, platformDefault, `abc`},
			http.StatusBadRequest,
			1,
		},
		{"zero version", gateCall{http.MethodDelete, target, "", `"0"`}, http.StatusBadRequest, 1},
		{
			"current put",
			gateCall{http.MethodPut, target, platformDefault, `"1"`},
			http.StatusOK,
			2,
		},
	}

	for _, tt := range tests {
		wantStatus(t, serveGate(t, h, tt.call), tt.want)

		var got defaultDoc

		decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), &got)

		if got.Version != tt.version {
			t.Fatalf("%s: stored version = %d, want %d", tt.name, got.Version, tt.version)
		}
	}
}

// TestApprovalDefaultIfMatchCodes proves that an If-Match header that is not
// one version in quotes answers its own code, and that a bad body with a
// good header still answers the body code.
func TestApprovalDefaultIfMatchCodes(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	target := gateBase + "acme" + gateDefault

	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodPut, target, platformDefault, ""}),
		http.StatusOK,
	)

	tests := []struct {
		name string
		call gateCall
		code string
		msg  string
	}{
		{
			"not a number",
			gateCall{http.MethodPut, target, platformDefault, `abc`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"quoted word",
			gateCall{http.MethodPut, target, platformDefault, `"zzz"`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"zero version",
			gateCall{http.MethodPut, target, platformDefault, `"0"`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"negative version",
			gateCall{http.MethodPut, target, platformDefault, `"-3"`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"quoted empty",
			gateCall{http.MethodPut, target, platformDefault, `""`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"delete not a number",
			gateCall{http.MethodDelete, target, "", `abc`},
			errcode.RequestIfMatchInvalid,
			ifMatchInvalidMessage,
		},
		{
			"bad body, good header",
			gateCall{http.MethodPut, target, `not json`, `"1"`},
			errcode.RequestBodyInvalid,
			"invalid JSON body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serveGate(t, h, tt.call)

			wantStatus(t, rec, http.StatusBadRequest)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, rec.Body.Bytes(),
				errcodetest.Want{Code: tt.code, Args: nil, RequestBody: nil})
			wantMessage(t, rec.Body.Bytes(), tt.msg)
		})
	}

	var got defaultDoc

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), &got)

	if got.Version != 1 {
		t.Fatalf("stored version = %d, want 1: a refused write changed the default", got.Version)
	}
}

// ifMatchInvalidMessage is the message of an answer to an If-Match header that
// is not one version in quotes.
const ifMatchInvalidMessage = `If-Match must be one stored version in quotes, for example "1"`

// wantMessage fails the test unless the message of the answer body is want.
// The code check does not read the message, so each test that expects one
// reads it here.
func wantMessage(t *testing.T, body []byte, want string) {
	t.Helper()

	var got struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, body)
	}

	if got.Message != want {
		t.Fatalf("message = %q, want %q: %s", got.Message, want, body)
	}
}

// TestApprovalRuleIfMatchCodes proves that a bad If-Match header on the PUT and
// the DELETE of an approval rule answers the If-Match code and its message, and
// that the refused writes leave the rule at its version.
func TestApprovalRuleIfMatchCodes(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	rule := gateBase + "acme" + gateRulesURL + "/ops"
	ruleBody := `{"count":1,"approvers":{"groups":["ops"]}}`

	if rec := serveGate(t, h, gateCall{http.MethodPut, rule, ruleBody, ""}); rec.Code >= http.StatusBadRequest {
		t.Fatalf("create the rule: %d: %s", rec.Code, rec.Body.String())
	}

	tests := []struct {
		name string
		call gateCall
	}{
		{"put not a number", gateCall{http.MethodPut, rule, ruleBody, `abc`}},
		{"put quoted word", gateCall{http.MethodPut, rule, ruleBody, `"zzz"`}},
		{"put zero version", gateCall{http.MethodPut, rule, ruleBody, `"0"`}},
		{"delete not a number", gateCall{http.MethodDelete, rule, "", `abc`}},
		{"delete negative version", gateCall{http.MethodDelete, rule, "", `"-3"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serveGate(t, h, tt.call)

			wantStatus(t, rec, http.StatusBadRequest)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.RequestIfMatchInvalid, Args: nil, RequestBody: nil})
			wantMessage(t, rec.Body.Bytes(), ifMatchInvalidMessage)
		})
	}

	var got ruleDoc

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, rule, "", ""}), &got)

	if got.Version != 1 {
		t.Fatalf("stored version = %d, want 1: a refused write changed the rule", got.Version)
	}
}

// TestApprovalDefaultRefusesBadBodies proves that an invalid default answers
// 400 or 422, and stores nothing.
func TestApprovalDefaultRefusesBadBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want int
	}{
		{"unknown field", `{"count":1,"approvers":{},"unless":"true"}`, http.StatusBadRequest},
		{"trailing data", `{"count":1,"approvers":{}} {}`, http.StatusBadRequest},
		{"not an object", `[1]`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"count zero", `{"count":0,"approvers":{}}`, http.StatusUnprocessableEntity},
		{"count above ten", `{"count":11,"approvers":{}}`, http.StatusUnprocessableEntity},
		{"window too short", `{"count":1,"approvers":{},"freshAuthSeconds":30}`, http.StatusUnprocessableEntity},
		{"window too long", `{"count":1,"approvers":{},"freshAuthSeconds":901}`, http.StatusUnprocessableEntity},
		{"user for internal use", `{"count":1,"approvers":{"users":["!no-approver"]}}`, http.StatusUnprocessableEntity},
		{
			"body over the cap",
			`{"count":1,"approvers":{"groups":["` + strings.Repeat("g", 65*1024) + `"]}}`,
			http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			target := gateBase + "acme" + gateDefault

			wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, target, tt.body, ""}), tt.want)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), http.StatusNotFound)
		})
	}
}

// TestApprovalDefaultFreshAuthWindow proves that the window 0 and a window of
// 60 to 900 seconds are stored, and none reads back as null.
func TestApprovalDefaultFreshAuthWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"none", `{"count":1,"approvers":{}}`, `"freshAuthSeconds":null`},
		{"every approval", `{"count":1,"approvers":{},"freshAuthSeconds":0}`, `"freshAuthSeconds":0`},
		{"one window", `{"count":1,"approvers":{},"freshAuthSeconds":300}`, `"freshAuthSeconds":300`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			target := gateBase + "acme" + gateDefault

			wantStatus(
				t,
				serveGate(t, h, gateCall{http.MethodPut, target, tt.body, ""}),
				http.StatusOK,
			)

			rec := serveGate(t, h, gateCall{http.MethodGet, target, "", ""})
			wantStatus(t, rec, http.StatusOK)

			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body = %s, want it to hold %s", rec.Body.String(), tt.want)
			}
		})
	}
}

// TestGateConfigAuthorization proves each route checks its own action on the
// namespace of the path: a read does not write, a write does not read, and an
// action on another namespace gives nothing. A refused write changes nothing.
func TestGateConfigAuthorization(t *testing.T) {
	t.Parallel()

	read, manage := identity.ActionNamespaceReadGates, identity.ActionNamespaceManageGates

	tests := []struct {
		name   string
		allow  map[identity.Action]namespace.Name
		method string
		path   string
		body   string
		want   int
	}{
		{
			"read allowed",
			map[identity.Action]namespace.Name{read: "acme"},
			http.MethodGet,
			"acme" + gateDefault,
			"",
			http.StatusNotFound,
		},
		{
			"read needs read",
			map[identity.Action]namespace.Name{manage: "acme"},
			http.MethodGet,
			"acme" + gateDefault,
			"",
			http.StatusForbidden,
		},
		{
			"list needs read",
			map[identity.Action]namespace.Name{manage: "acme"},
			http.MethodGet,
			"acme" + gateRulesURL,
			"",
			http.StatusForbidden,
		},
		{
			"put needs manage",
			map[identity.Action]namespace.Name{read: "acme"},
			http.MethodPut,
			"acme" + gateDefault,
			platformDefault,
			http.StatusForbidden,
		},
		{
			"delete needs manage",
			map[identity.Action]namespace.Name{read: "acme"},
			http.MethodDelete,
			"acme" + gateDefault,
			"",
			http.StatusForbidden,
		},
		{
			"rule put needs manage",
			map[identity.Action]namespace.Name{read: "acme"},
			http.MethodPut,
			"acme" + gateRulesURL + "/ops",
			`{"count":1,"approvers":{"groups":["ops"]}}`,
			http.StatusForbidden,
		},
		{
			"rule delete needs manage",
			map[identity.Action]namespace.Name{read: "acme"},
			http.MethodDelete,
			"acme" + gateRulesURL + "/ops",
			"",
			http.StatusForbidden,
		},
		{
			"other namespace put",
			map[identity.Action]namespace.Name{manage: "acme", read: "acme"},
			http.MethodPut,
			"beta" + gateDefault,
			platformDefault,
			http.StatusForbidden,
		},
		{
			"other namespace get",
			map[identity.Action]namespace.Name{manage: "acme", read: "acme"},
			http.MethodGet,
			"beta" + gateDefault,
			"",
			http.StatusForbidden,
		},
		{
			"no action at all",
			map[identity.Action]namespace.Name{},
			http.MethodPut,
			"acme" + gateDefault,
			platformDefault,
			http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, gateAuthorizer{allow: tt.allow})
			wantStatus(
				t,
				serveGate(t, h, gateCall{tt.method, gateBase + tt.path, tt.body, ""}),
				tt.want,
			)
		})
	}

	// A refused write leaves the namespace without a default.
	_, h := newGateHandler(
		t,
		gateAuthorizer{allow: map[identity.Action]namespace.Name{read: "beta", manage: "acme"}},
	)
	wantStatus(
		t,
		serveGate(
			t,
			h,
			gateCall{http.MethodPut, gateBase + "beta" + gateDefault, platformDefault, ""},
		),
		http.StatusForbidden,
	)
	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodGet, gateBase + "beta" + gateDefault, "", ""}),
		http.StatusNotFound,
	)
}

// TestGateConfigDefaultsStaySeparate proves that a default of one namespace
// is not read at another.
func TestGateConfigDefaultsStaySeparate(t *testing.T) {
	t.Parallel()

	authz := gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceReadGates: "acme", identity.ActionNamespaceManageGates: "acme",
	}}
	_, h := newGateHandler(t, authz)

	wantStatus(
		t,
		serveGate(
			t,
			h,
			gateCall{http.MethodPut, gateBase + "acme" + gateDefault, platformDefault, ""},
		),
		http.StatusOK,
	)
	wantStatus(
		t,
		serveGate(t, h, gateCall{http.MethodGet, gateBase + "beta" + gateDefault, "", ""}),
		http.StatusForbidden,
	)
}

// TestGateConfigNotMountedWithoutStore proves that a server whose store keeps
// no gate configuration serves none of the routes.
func TestGateConfigNotMountedWithoutStore(t *testing.T) {
	t.Parallel()

	h := newGateHandlerWithoutStore(t, adminGates())

	for _, target := range []string{gateDefault, gateRulesURL, gateRulesURL + "/ops"} {
		rec := serveGate(t, h, gateCall{http.MethodGet, gateBase + "acme" + target, "", ""})
		if rec.Code == http.StatusOK || rec.Code == http.StatusForbidden {
			t.Fatalf("GET %s = %d, want no route", target, rec.Code)
		}
	}
}

// TestApprovalRuleLifecycle follows one approval rule through create, list,
// read, replace and delete, with its fields.
func TestApprovalRuleLifecycle(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	list := gateBase + "acme" + gateRulesURL
	item := list + "/platform-review"

	rec := serveGate(t, h, gateCall{http.MethodGet, list, "", ""})
	wantStatus(t, rec, http.StatusOK)

	if strings.TrimSpace(rec.Body.String()) != `{"rules":[]}` {
		t.Fatalf("empty list = %s, want {\"rules\":[]}", rec.Body.String())
	}

	checkRuleCreated(t, h, item)

	body := `{"count":1,"approvers":{"users":["bo"]}}`
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, list + "/another", body, ""}), http.StatusOK)

	var listed struct {
		Rules []ruleDoc `json:"rules"`
	}

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, list, "", ""}), &listed)

	if len(listed.Rules) != 2 || listed.Rules[0].ID != "another" || listed.Rules[1].ID != "platform-review" {
		t.Fatalf("list = %+v, want another then platform-review (ordered by ID)", listed.Rules)
	}

	checkRuleReplaced(t, h, item)

	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", ""}), http.StatusNoContent)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", ""}), http.StatusNotFound)

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, list, "", ""}), &listed)

	if len(listed.Rules) != 1 || listed.Rules[0].ID != "another" {
		t.Fatalf("list after delete = %+v, want only another", listed.Rules)
	}
}

func checkRuleCreated(t *testing.T, h http.Handler, item string) {
	t.Helper()

	body := `{"count":2,"approvers":{"groups":["platform"]},"authorMayApprove":true,"timeoutSeconds":7200}`

	rec := serveGate(t, h, gateCall{http.MethodPut, item, body, ""})
	wantStatus(t, rec, http.StatusOK)

	var created ruleDoc

	decodeGateJSON(t, rec, &created)

	if created.ID != "platform-review" || created.Count != 2 || !created.AuthorMayApprove || created.Version != 1 ||
		created.TimeoutSeconds == nil || *created.TimeoutSeconds != 7200 {
		t.Fatalf("created rule = %+v, want id platform-review, count 2, author may approve, version 1, 7200 s", created)
	}
}

func checkRuleReplaced(t *testing.T, h http.Handler, item string) {
	t.Helper()

	rec := serveGate(t, h, gateCall{http.MethodPut, item, `{"count":1,"approvers":{"groups":["platform"]}}`, ""})
	wantStatus(t, rec, http.StatusOK)

	var replaced ruleDoc

	decodeGateJSON(t, rec, &replaced)

	if replaced.Version != 2 || replaced.Count != 1 || replaced.AuthorMayApprove || replaced.TimeoutSeconds != nil {
		t.Fatalf("replaced rule = %+v, want version 2, count 1, no author approval, default timeout", replaced)
	}
}

// TestApprovalRuleRefusals proves that an invalid rule answers 400 or 422 and
// stores nothing.
func TestApprovalRuleRefusals(t *testing.T) {
	t.Parallel()

	ok := `{"count":1,"approvers":{"groups":["ops"]}}`

	tests := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"uppercase ID", "Ops", ok, http.StatusUnprocessableEntity},
		{"reserved default", "default", ok, http.StatusUnprocessableEntity},
		{"reserved owners", "owners", ok, http.StatusUnprocessableEntity},
		{"ID starts with dash", "-ops", ok, http.StatusUnprocessableEntity},
		{"ID too long", strings.Repeat("a", 64), ok, http.StatusUnprocessableEntity},
		{"ID in body", "ops", `{"id":"other","count":1,"approvers":{"groups":["ops"]}}`, http.StatusBadRequest},
		{"unknown field", "ops", `{"count":1,"approvers":{"groups":["ops"]},"scope":"x"}`, http.StatusBadRequest},
		{"count zero", "ops", `{"count":0,"approvers":{"groups":["ops"]}}`, http.StatusUnprocessableEntity},
		{"count eleven", "ops", `{"count":11,"approvers":{"groups":["ops"]}}`, http.StatusUnprocessableEntity},
		{"no approver", "ops", `{"count":1,"approvers":{}}`, http.StatusUnprocessableEntity},
		{
			"timeout under an hour",
			"ops",
			`{"count":1,"approvers":{"groups":["ops"]},"timeoutSeconds":3599}`,
			http.StatusUnprocessableEntity,
		},
		{
			"timeout over 30 days",
			"ops",
			`{"count":1,"approvers":{"groups":["ops"]},"timeoutSeconds":2592001}`,
			http.StatusUnprocessableEntity,
		},
		{
			"window too short",
			"ops",
			`{"count":1,"approvers":{"groups":["ops"]},"freshAuthSeconds":59}`,
			http.StatusUnprocessableEntity,
		},
		{
			"unit owners at the namespace is allowed",
			"ops",
			`{"count":1,"approvers":{"unitOwners":true}}`,
			http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			item := gateBase + "acme" + gateRulesURL + "/" + tt.id

			wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, item, tt.body, ""}), tt.want)

			if tt.want != http.StatusOK {
				rec := serveGate(t, h, gateCall{http.MethodGet, gateBase + "acme" + gateRulesURL, "", ""})
				if strings.TrimSpace(rec.Body.String()) != `{"rules":[]}` {
					t.Fatalf("list after a refused put = %s, want no rule", rec.Body.String())
				}
			}
		})
	}
}

// TestApprovalRuleLimit proves that a namespace holds at most
// gate.MaxApprovalRulesPerScope rules, and the next one answers 422.
func TestApprovalRuleLimit(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, adminGates())
	list := gateBase + "acme" + gateRulesURL

	for i := range gate.MaxApprovalRulesPerScope {
		id := "rule-" + strings.Repeat("a", i%5) + string(rune('a'+i/5))
		wantStatus(
			t,
			serveGate(
				t,
				h,
				gateCall{http.MethodPut, list + "/" + id, `{"count":1,"approvers":{"groups":["ops"]}}`, ""},
			),
			http.StatusOK,
		)
	}

	wantStatus(
		t,
		serveGate(
			t,
			h,
			gateCall{http.MethodPut, list + "/one-too-many", `{"count":1,"approvers":{"groups":["ops"]}}`, ""},
		),
		http.StatusUnprocessableEntity,
	)
}

// TestGateConfigWritesAuditEntries proves that every write adds one entry of
// the kind change to the audit log of its namespace, and a read adds none.
func TestGateConfigWritesAuditEntries(t *testing.T) {
	t.Parallel()

	st, h := newGateHandler(t, adminGates())
	def := gateBase + "acme" + gateDefault
	rule := gateBase + "acme" + gateRulesURL + "/ops"

	calls := []gateCall{
		{http.MethodPut, def, platformDefault, ""},
		{http.MethodGet, def, "", ""},
		{http.MethodPut, rule, `{"count":1,"approvers":{"groups":["ops"]}}`, ""},
		{http.MethodDelete, rule, "", ""},
		{http.MethodDelete, def, "", ""},
	}

	for _, c := range calls {
		if rec := serveGate(t, h, c); rec.Code >= http.StatusBadRequest {
			t.Fatalf("%s %s = %d: %s", c.method, c.target, rec.Code, rec.Body.String())
		}
	}

	want := []string{
		gate.AuditSetApprovalDefault, gate.AuditSetApprovalRule, gate.AuditDeleteApprovalRule,
		gate.AuditDeleteApprovalDefault,
	}

	var got []string

	for _, e := range listNamespaceEntries(t, st, "acme") {
		if strings.Contains(e.Action, "approval") {
			if e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Subject == "" ||
				len(e.Changes) != 1 {
				t.Fatalf("entry = %+v, want kind change, success, a subject and one change", e)
			}

			got = append(got, e.Action)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("approval entries = %v, want %v", got, want)
	}

	if len(listNamespaceEntries(t, st, "beta")) != 0 {
		t.Fatal("beta holds audit entries of a change in acme")
	}
}

// TestGateConfigDeniedWriteKeepsDefault proves that a write refused by the
// authorizer reaches no store: the namespace keeps its default.
func TestGateConfigDeniedWriteKeepsDefault(t *testing.T) {
	t.Parallel()

	read, manage := identity.ActionNamespaceReadGates, identity.ActionNamespaceManageGates

	authz := gateAuthorizer{allow: map[identity.Action]namespace.Name{read: "acme", manage: "beta"}}
	handler := newGateEnv(t, authz)

	nss, err := namespace.NewService(namespace.Config{Store: handler.env.store, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: handler.env.store, Namespaces: nss, Approvers: testApprovers{}, Engine: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("gate config service: %v", err)
	}

	seed := gate.ApprovalDefault{
		Scope:     gate.NamespaceScope("acme"),
		Version:   0,
		Count:     2,
		FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{
			Groups:     []string{"platform"},
			GroupIDs:   nil,
			Users:      nil,
			UnitOwners: false,
		},
		Unless: "",
		Reads:  nil,
	}

	_, err = svc.PutApprovalDefault(t.Context(), seed, 0, auditEntryFor("acme"))
	if err != nil {
		t.Fatalf("seed default: %v", err)
	}

	target := gateBase + "acme" + gateDefault

	wantStatus(t, serveGate(t, handler, gateCall{http.MethodDelete, target, "", ""}), http.StatusForbidden)
	wantStatus(
		t,
		serveGate(t, handler, gateCall{http.MethodPut, target, `{"count":1,"approvers":{}}`, ""}),
		http.StatusForbidden,
	)

	var got defaultDoc

	decodeGateJSON(t, serveGate(t, handler, gateCall{http.MethodGet, target, "", ""}), &got)

	if got.Count != 2 || got.Version != 1 {
		t.Fatalf("default after refused writes = %+v, want count 2, version 1", got)
	}
}

// auditEntryFor returns the caller part of an audit entry for a service call.
func auditEntryFor(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: ns, Subject: "seed", IDPSubject: "", TokenID: "", Action: "seed", Object: "seed",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
		Origin: nil,
	}
}

// TestGateConfigNeedsFreshSignIn proves that a console session whose sign-in
// is older than the freshness window cannot set or delete an approval default
// or an approval rule, and that its reads still work.
func TestGateConfigNeedsFreshSignIn(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	stale := e.staleSession(t)
	def := gateBase + testNamespace + gateDefault
	rule := gateBase + testNamespace + gateRulesURL + "/ops"

	refused := []*httptest.ResponseRecorder{
		e.sendWithSession(t, http.MethodPut, stale, def, platformDefault),
		e.sendWithSession(t, http.MethodDelete, stale, def, ""),
		e.sendWithSession(t, http.MethodPut, stale, rule, `{"count":1,"approvers":{"groups":["ops"]}}`),
		e.sendWithSession(t, http.MethodDelete, stale, rule, ""),
	}

	for _, rec := range refused {
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "reauthenticate") {
			t.Fatalf("change from a stale session = %d %s, want 403 with reauthenticate", rec.Code, rec.Body.String())
		}
	}

	if rec := e.sendWithSession(t, http.MethodGet, stale, def, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("read from a stale session = %d, want 404 (no default, nothing stored)", rec.Code)
	}

	fresh := e.freshSignIn(t)
	if rec := e.sendWithSession(t, http.MethodPut, fresh, def, platformDefault); rec.Code != http.StatusOK {
		t.Fatalf("change from a fresh session = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestGateConfigRefusesAccessTokenWrites proves that an access token never
// gives fresh authentication: a set or a delete with a token answers 403,
// stores nothing, changes no version and adds a failure entry. A read with
// the token works.
func TestGateConfigRefusesAccessTokenWrites(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, adminGates())
	raw := g.env.handler
	def := gateBase + "acme" + gateDefault
	rule := gateBase + "acme" + gateRulesURL + "/ops"

	// The session writes the seed, so the refused calls have a version to keep.
	wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, def, platformDefault, ""}), http.StatusOK)
	wantStatus(
		t, serveGate(t, g, gateCall{http.MethodPut, rule, `{"count":1,"approvers":{"groups":["ops"]}}`, ""}),
		http.StatusOK,
	)

	refused := []gateCall{
		{http.MethodPut, def, `{"count":3,"approvers":{"groups":["x"]}}`, ""},
		{http.MethodDelete, def, "", ""},
		{http.MethodPut, rule, `{"count":3,"approvers":{"groups":["x"]}}`, ""},
		{http.MethodDelete, rule, "", ""},
		{http.MethodPut, gateBase + "acme" + gateRulesURL + "/new", `{"count":1,"approvers":{"groups":["ops"]}}`, ""},
	}

	for _, c := range refused {
		rec := serveGate(t, raw, c)
		wantStatus(t, rec, http.StatusForbidden)

		if !strings.Contains(rec.Body.String(), "recent sign-in") ||
			!strings.Contains(rec.Body.String(), `"errorCode":"mfa.fresh-step-required"`) {
			t.Fatalf("%s %s body = %s, want a message about a recent sign-in and the code mfa.fresh-step-required",
				c.method, c.target, rec.Body.String())
		}
	}

	var gotDefault defaultDoc

	decodeGateJSON(t, serveGate(t, raw, gateCall{http.MethodGet, def, "", ""}), &gotDefault)

	if gotDefault.Count != 2 || gotDefault.Version != 1 {
		t.Fatalf("default after refused writes = %+v, want count 2, version 1", gotDefault)
	}

	wantStatus(t, serveGate(t, raw, gateCall{http.MethodGet, rule, "", ""}), http.StatusOK)
	wantStatus(
		t, serveGate(t, raw, gateCall{http.MethodGet, gateBase + "acme" + gateRulesURL + "/new", "", ""}),
		http.StatusNotFound,
	)

	failures := 0

	for _, e := range listNamespaceEntries(t, g.env.store, "acme") {
		if e.Outcome == audit.OutcomeFailure && e.Action == string(identity.ActionNamespaceManageGates) {
			failures++
		}
	}

	if failures != len(refused) {
		t.Fatalf("failure entries = %d, want %d", failures, len(refused))
	}
}

// TestApprovalDefaultRefusesApproversThatCannotBeMet proves that a default
// whose approvers cannot give the count it asks for answers 422, and that the
// namespace keeps no default. The cases are a group name that is not valid, a
// group that does not exist, and a count above the people who can approve.
func TestApprovalDefaultRefusesApproversThatCannotBeMet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"group name that is not valid", `{"count":1,"approvers":{"groups":["<b>x</b>"],"users":[]}}`},
		{"group that does not exist", `{"count":1,"approvers":{"groups":["nogroup"],"users":[]}}`},
		{"user name with a control character", `{"count":1,"approvers":{"groups":[],"users":["bo\u0007b"]}}`},
		{"count above the named users", `{"count":10,"approvers":{"groups":[],"users":["alice"],"unitOwners":false}}`},
		{"count above users and group members", `{"count":10,"approvers":{"groups":["platform"],"users":["alice"]}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			target := gateBase + "acme" + gateDefault

			rec := serveGate(t, h, gateCall{http.MethodPut, target, tc.body, ""})
			wantStatus(t, rec, http.StatusUnprocessableEntity)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, target, "", ""}), http.StatusNotFound)
		})
	}
}

// TestApprovalDefaultAcceptsApproversThatCanBeMet proves the cases next to the
// refusals: a count that the approvers can give, the unit owners, and no
// approver at all (any holder of the approve action).
func TestApprovalDefaultAcceptsApproversThatCanBeMet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"count within the named users", `{"count":2,"approvers":{"groups":[],"users":["alice","bob"]}}`},
		{"count within a group", `{"count":3,"approvers":{"groups":["platform"],"users":[]}}`},
		{
			"count above the list with the unit owners",
			`{"count":5,"approvers":{"groups":[],"users":["alice"],"unitOwners":true}}`,
		},
		{"count with no approver named", `{"count":4,"approvers":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())

			wantStatus(
				t,
				serveGate(t, h, gateCall{http.MethodPut, gateBase + "acme" + gateDefault, tc.body, ""}),
				http.StatusOK,
			)
		})
	}
}

// testApprovers is the group directory of the gate tests: the groups ops,
// platform, finance, sre and x exist, each with enabled members that are all
// persons, as many as below. Any other valid name names no group, and a name
// that is not valid is refused.
type testApprovers struct{}

// testApproverGroups returns the number of enabled members of each group.
func testApproverGroups() map[string]int64 {
	return map[string]int64{"ops": 10, "platform": 5, "finance": 5, "sre": 5, "x": 10}
}

// ResolveGroup implements gate.ApproverDirectory. The ID of a group is "id-"
// and its name.
func (testApprovers) ResolveGroup(_ context.Context, name string) (gate.GroupRef, error) {
	if err := principal.ValidateGroupName(name); err != nil {
		return gate.GroupRef{ID: "", Name: ""}, fmt.Errorf("%w: %w", gate.ErrInvalid, err)
	}

	if _, ok := testApproverGroups()[name]; ok {
		return gate.GroupRef{ID: "id-" + name, Name: name}, nil
	}

	return gate.GroupRef{ID: "", Name: ""}, fmt.Errorf("%w: group %q", gate.ErrNotFound, name)
}

// GroupByID implements gate.ApproverDirectory.
func (testApprovers) GroupByID(_ context.Context, id string) (gate.GroupRef, gate.MemberCounts, error) {
	name := strings.TrimPrefix(id, "id-")
	if n, ok := testApproverGroups()[name]; ok && name != id {
		return gate.GroupRef{ID: id, Name: name}, gate.MemberCounts{Enabled: n, Persons: n}, nil
	}

	return gate.GroupRef{ID: "", Name: ""}, gate.MemberCounts{Enabled: 0, Persons: 0},
		fmt.Errorf("%w: group %q", gate.ErrNotFound, id)
}

// testGateEngine is the linked policy engine, with the one action that the
// access limits of the tests name.
//
//nolint:ireturn // The service takes the engine port.
func testGateEngine() gate.Engine {
	return cedargo.New(cedargo.WithAccessActions("unit:write"))
}

// TestGateConfigUnknownNamespace proves that each route of the approval
// configuration answers 404 with the namespace error for a namespace that
// does not exist, also a read and a delete that find nothing to change.
func TestGateConfigUnknownNamespace(t *testing.T) {
	t.Parallel()

	_, h := newGateHandler(t, gateAuthorizer{allow: map[identity.Action]namespace.Name{
		identity.ActionNamespaceReadGates:   "ghost",
		identity.ActionNamespaceManageGates: "ghost",
	}})

	def := gateBase + "ghost" + gateDefault
	rules := gateBase + "ghost" + gateRulesURL
	rule := rules + "/ops"
	ruleBody := `{"count":1,"approvers":{"users":["bo"]}}`

	tests := []struct {
		name string
		call gateCall
	}{
		{"list the rules", gateCall{http.MethodGet, rules, "", ""}},
		{"read a rule", gateCall{http.MethodGet, rule, "", ""}},
		{"read the default", gateCall{http.MethodGet, def, "", ""}},
		{"delete the default", gateCall{http.MethodDelete, def, "", ""}},
		{"delete the default at a version", gateCall{http.MethodDelete, def, "", `"1"`}},
		{"delete a rule", gateCall{http.MethodDelete, rule, "", ""}},
		{"put the default", gateCall{http.MethodPut, def, platformDefault, ""}},
		{"put a rule", gateCall{http.MethodPut, rule, ruleBody, ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serveGate(t, h, tt.call)
			wantStatus(t, rec, http.StatusNotFound)

			if !strings.Contains(rec.Body.String(), "namespace not found") {
				t.Fatalf("%s body = %s, want the namespace error", tt.name, rec.Body.String())
			}
		})
	}
}

// scopeCaseBody is a rule that any scope level accepts.
const scopeCaseBody = `{"count":1,"approvers":{"groups":["ops"]}}`

// wantCode fails unless the body of rec holds the error code code.
func wantCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()

	if !strings.Contains(rec.Body.String(), `"errorCode":"`+code+`"`) {
		t.Fatalf("body = %s, want the code %s", rec.Body.String(), code)
	}
}

// entriesOf returns the audit entries of the chain ns for which keep is true.
func entriesOf(t *testing.T, st *sqlite.Store, ns namespace.Name, keep func(e *audit.Entry) bool) []audit.Entry {
	t.Helper()

	all := listNamespaceEntries(t, st, ns)
	out := make([]audit.Entry, 0, len(all))

	for i := range all {
		if keep(&all[i]) {
			out = append(out, all[i])
		}
	}

	return out
}

// ruleList is the answer of a list of approval rules.
type ruleList struct {
	Rules []ruleDoc `json:"rules"`
}

// TestApprovalRuleScopes proves that the approval rules have list, get, set
// and delete at the namespace, the collection and the unit scope, that a list
// and a read show the objects of their own scope only, and that each route
// needs its action and a valid scope segment.
func TestApprovalRuleScopes(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		t.Run(level, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			list, item := base+gateRulesURL, base+gateRulesURL+"/review"

			rec := serveGate(t, h, gateCall{http.MethodGet, list, "", ""})
			wantStatus(t, rec, http.StatusOK)

			if strings.TrimSpace(rec.Body.String()) != `{"rules":[]}` {
				t.Fatalf("empty list = %s, want {\"rules\":[]}", rec.Body.String())
			}

			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)

			rec = serveGate(t, h, gateCall{http.MethodPut, item, scopeCaseBody, ""})
			wantStatus(t, rec, http.StatusOK)

			var created ruleDoc

			decodeGateJSON(t, rec, &created)

			if created.ID != "review" || created.Version != 1 || rec.Header().Get("ETag") != `"1"` {
				t.Fatalf(
					"created = %+v, ETag %q; want id review, version 1, ETag \"1\"",
					created,
					rec.Header().Get("ETag"),
				)
			}

			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusOK)
			checkRuleVisibility(t, h, level)

			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", `"9"`}), http.StatusConflict)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", `"1"`}), http.StatusNoContent)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, item, "", ""}), http.StatusNotFound)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, item, "", ""}), http.StatusNotFound)
		})
	}

	t.Run("authorization", func(t *testing.T) {
		t.Parallel()

		approvalAuthorizationCases(t, gateRulesURL+"/x", scopeCaseBody)
	})

	t.Run("scope segments", func(t *testing.T) {
		t.Parallel()

		approvalSegmentCases(t, gateRulesURL)
	})
}

// checkRuleVisibility checks, for the rule review that the scope of level
// holds, that the list and the read of each scope show it at its own scope
// only.
func checkRuleVisibility(t *testing.T, h http.Handler, level string) {
	t.Helper()

	for other, otherBase := range gateScopeURLs() {
		var listed ruleList

		decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, otherBase + gateRulesURL, "", ""}), &listed)

		want, status := 0, http.StatusNotFound
		if other == level {
			want, status = 1, http.StatusOK
		}

		if len(listed.Rules) != want {
			t.Errorf("%s list holds %d rules of %s, want %d", other, len(listed.Rules), level, want)
		}

		rec := serveGate(t, h, gateCall{http.MethodGet, otherBase + gateRulesURL + "/review", "", ""})
		wantStatus(t, rec, status)

		if status == http.StatusNotFound {
			wantCode(t, rec, errcode.GateNotFound)
		}
	}
}

// TestApprovalDefaultScopes proves that the approval default has get, set and
// reset at the namespace, the collection and the unit scope, and that the
// reset answers 204 when none is set.
func TestApprovalDefaultScopes(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		t.Run(level, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			def := base + gateDefault

			rec := serveGate(t, h, gateCall{http.MethodGet, def, "", ""})
			wantStatus(t, rec, http.StatusNotFound)
			wantCode(t, rec, errcode.GateNotFound)

			// A reset with no default set is not an error.
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, def, "", ""}), http.StatusNoContent)

			rec = serveGate(t, h, gateCall{http.MethodPut, def, platformDefault, ""})
			wantStatus(t, rec, http.StatusOK)

			var created defaultDoc

			decodeGateJSON(t, rec, &created)

			if created.Count != 2 || created.Version != 1 || rec.Header().Get("ETag") != `"1"` {
				t.Fatalf(
					"created = %+v, ETag %q; want count 2, version 1, ETag \"1\"",
					created,
					rec.Header().Get("ETag"),
				)
			}

			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, def, "", ""}), http.StatusOK)
			checkDefaultVisibility(t, h, level)

			wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, def, platformDefault, `"1"`}), http.StatusOK)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, def, "", `"1"`}), http.StatusConflict)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, def, "", `"2"`}), http.StatusNoContent)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, def, "", ""}), http.StatusNotFound)
			wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, def, "", ""}), http.StatusNoContent)
		})
	}

	t.Run("authorization", func(t *testing.T) {
		t.Parallel()

		approvalAuthorizationCases(t, gateDefault, platformDefault)
	})

	t.Run("scope segments", func(t *testing.T) {
		t.Parallel()

		approvalSegmentCases(t, gateDefault)
	})
}

// checkDefaultVisibility checks that the default that the scope of level holds
// is no default of another scope.
func checkDefaultVisibility(t *testing.T, h http.Handler, level string) {
	t.Helper()

	for other, otherBase := range gateScopeURLs() {
		if other != level {
			wantStatus(
				t,
				serveGate(t, h, gateCall{http.MethodGet, otherBase + gateDefault, "", ""}),
				http.StatusNotFound,
			)
		}
	}
}

// approvalAuthCase is one caller of approvalAuthorizationCases.
type approvalAuthCase struct {
	name   string
	allow  map[identity.Action]namespace.Name
	method string
}

// approvalAuthCases returns the callers that lack the action of their request.
func approvalAuthCases() []approvalAuthCase {
	read, manage := identity.ActionNamespaceReadGates, identity.ActionNamespaceManageGates

	return []approvalAuthCase{
		{"read needs read", map[identity.Action]namespace.Name{manage: "acme"}, http.MethodGet},
		{"put needs manage", map[identity.Action]namespace.Name{read: "acme"}, http.MethodPut},
		{"delete needs manage", map[identity.Action]namespace.Name{read: "acme"}, http.MethodDelete},
		{
			"manage of another namespace",
			map[identity.Action]namespace.Name{read: "beta", manage: "beta"},
			http.MethodPut,
		},
		{"no action", nil, http.MethodGet},
	}
}

// approvalAuthorizationCases proves, at the collection and the unit scope,
// that a read needs namespace:read-gates and a write needs
// namespace:manage-gates on the namespace of the path, with no stored change
// and no audit entry of a change after a refusal.
func approvalAuthorizationCases(t *testing.T, suffix, body string) {
	t.Helper()

	for _, level := range []string{"collection", "unit"} {
		for _, tt := range approvalAuthCases() {
			t.Run(level+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				runApprovalAuthCase(t, gateScopeURLs()[level]+suffix, body, tt)
			})
		}
	}
}

// runApprovalAuthCase sends the request of tt and checks the refusal.
func runApprovalAuthCase(t *testing.T, target, body string, tt approvalAuthCase) {
	t.Helper()

	st, h := newGateHandler(t, gateAuthorizer{allow: tt.allow})

	payload := ""
	if tt.method == http.MethodPut {
		payload = body
	}

	rec := serveGate(t, h, gateCall{tt.method, target, payload, ""})
	wantStatus(t, rec, http.StatusForbidden)
	wantCode(t, rec, errcode.AccessDenied)

	changes := entriesOf(t, st, "acme", func(e *audit.Entry) bool {
		return strings.HasPrefix(e.Action, "namespace:set-") || strings.HasPrefix(e.Action, "namespace:delete-")
	})
	if len(changes) != 0 {
		t.Errorf("a refused request wrote the audit entries %+v", changes)
	}
}

// approvalSegmentCases proves that a collection name or a unit name that is
// not valid answers 400 before any store call, for a read and for a write.
func approvalSegmentCases(t *testing.T, suffix string) {
	t.Helper()

	bad := map[string]string{
		"collection name too short":     gateBase + "acme/collections/ab",
		"collection name with a dash":   gateBase + "acme/collections/-payments-",
		"collection name with a slash":  gateBase + "acme/collections/pay%2Fments",
		"unit name with a control byte": gateBase + "acme/units/by-name/pulumi/web%07",
	}

	body, target := scopeCaseBody, suffix+"/x"
	if suffix == gateDefault {
		body, target = platformDefault, suffix
	}

	for name, base := range bad {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			t.Run(name+"/"+method, func(t *testing.T) {
				t.Parallel()

				_, h := newGateHandler(t, adminGates())

				payload := ""
				if method == http.MethodPut {
					payload = body
				}

				wantStatus(t, serveGate(t, h, gateCall{method, base + target, payload, ""}), http.StatusBadRequest)
			})
		}
	}
}

// TestApprovalScopeGuards proves, for each of the three scope levels, one
// guard at a time: the fresh sign-in, the version, the body, the final rule,
// the limit and the audit entry. Each case sends the hostile request and
// checks that the stored row does not change.
func TestApprovalScopeGuards(t *testing.T) {
	t.Parallel()

	for level, base := range gateScopeURLs() {
		t.Run(level, func(t *testing.T) {
			t.Parallel()

			t.Run("access token", func(t *testing.T) { t.Parallel(); guardAccessToken(t, base) })
			t.Run("stale version", func(t *testing.T) { t.Parallel(); guardStaleVersion(t, base) })
			t.Run("body", func(t *testing.T) { t.Parallel(); guardBody(t, base) })
			t.Run("limit", func(t *testing.T) { t.Parallel(); guardLimit(t, base) })
			t.Run("audit entry", func(t *testing.T) { t.Parallel(); guardAuditEntry(t, level, base) })
		})
	}

	for _, level := range []string{"collection", "unit"} {
		t.Run(level+"/final rule of the namespace", func(t *testing.T) {
			t.Parallel()

			guardFinalRule(t, level)
		})

		t.Run(level+"/stale sign-in", func(t *testing.T) {
			t.Parallel()

			guardStaleSession(t, level)
		})
	}
}

// guardFinalRule saves a rule at the namespace and then a rule with the same
// ID at a narrower scope. The narrower scope cannot take the ID.
func guardFinalRule(t *testing.T, level string) {
	t.Helper()

	_, h := newGateHandler(t, adminGates())
	base := gateScopeURLs()[level]

	wantStatus(
		t, serveGate(t, h, gateCall{http.MethodPut, gateBase + "acme" + gateRulesURL + "/shared", scopeCaseBody, ""}),
		http.StatusOK,
	)

	rec := serveGate(t, h, gateCall{http.MethodPut, base + gateRulesURL + "/shared", scopeCaseBody, ""})
	wantStatus(t, rec, http.StatusConflict)
	wantCode(t, rec, errcode.GateFinal)

	var listed ruleList

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, base + gateRulesURL, "", ""}), &listed)

	if len(listed.Rules) != 0 {
		t.Errorf("rules at the %s after the refused save = %+v, want none", level, listed.Rules)
	}
}

// guardAccessToken sends each write with an access token, which never gives a
// fresh authentication, and checks the code and that the rows stay.
func guardAccessToken(t *testing.T, base string) {
	t.Helper()

	g := newGateEnv(t, adminGates())
	def, rule := base+gateDefault, base+gateRulesURL+"/review"

	wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, def, platformDefault, ""}), http.StatusOK)
	wantStatus(t, serveGate(t, g, gateCall{http.MethodPut, rule, scopeCaseBody, ""}), http.StatusOK)

	for _, c := range []gateCall{
		{http.MethodPut, def, `{"count":3,"approvers":{"groups":["x"]}}`, ""},
		{http.MethodDelete, def, "", ""},
		{http.MethodPut, rule, `{"count":3,"approvers":{"groups":["ops"]}}`, ""},
		{http.MethodDelete, rule, "", ""},
		{http.MethodPut, base + gateRulesURL + "/new", scopeCaseBody, ""},
	} {
		rec := serveGate(t, g.env.handler, c)
		wantStatus(t, rec, http.StatusForbidden)
		wantCode(t, rec, errcode.MFAFreshStepRequired)
	}

	var gotDefault defaultDoc

	decodeGateJSON(t, serveGate(t, g.env.handler, gateCall{http.MethodGet, def, "", ""}), &gotDefault)

	var gotRule ruleDoc

	decodeGateJSON(t, serveGate(t, g.env.handler, gateCall{http.MethodGet, rule, "", ""}), &gotRule)

	if gotDefault.Count != 2 || gotDefault.Version != 1 || gotRule.Count != 1 || gotRule.Version != 1 {
		t.Fatalf("rows after refused writes = %+v and %+v, want the seeded versions", gotDefault, gotRule)
	}

	wantStatus(
		t, serveGate(t, g.env.handler, gateCall{http.MethodGet, base + gateRulesURL + "/new", "", ""}),
		http.StatusNotFound,
	)
}

// guardStaleSession sends each write from a session whose sign-in is older
// than the freshness window, at one scope level, and checks that no row
// changes. A fresh session then writes.
func guardStaleSession(t *testing.T, level string) {
	t.Helper()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil,
		namespaces: []namespace.Name{"acme"}, hostile: nil, passwords: nil, failPolicyRead: nil,
		idempotency: false, failTempAdminRead: false,
	})
	createGateUnits(t, e.store)

	stale := e.staleSession(t)
	base := gateScopeURLs()[level]
	def, rule := base+gateDefault, base+gateRulesURL+"/review"

	for _, c := range []gateCall{
		{http.MethodPut, def, platformDefault, ""},
		{http.MethodDelete, def, "", ""},
		{http.MethodPut, rule, scopeCaseBody, ""},
		{http.MethodDelete, rule, "", ""},
	} {
		rec := e.sendWithSession(t, c.method, stale, c.target, c.body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "reauthenticate") {
			t.Fatalf("%s %s from a stale session = %d %s, want 403 with reauthenticate",
				c.method, c.target, rec.Code, rec.Body.String())
		}
	}

	for _, target := range []string{def, rule} {
		if rec := e.sendWithSession(t, http.MethodGet, stale, target, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s after the refused writes = %d, want 404", target, rec.Code)
		}
	}

	fresh := e.freshSignIn(t)
	if rec := e.sendWithSession(t, http.MethodPut, fresh, rule, scopeCaseBody); rec.Code != http.StatusOK {
		t.Fatalf("PUT from a fresh session = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// guardStaleVersion sends a write with an If-Match that is not the stored
// version and checks the code and that the row stays.
func guardStaleVersion(t *testing.T, base string) {
	t.Helper()

	_, h := newGateHandler(t, adminGates())
	def, rule := base+gateDefault, base+gateRulesURL+"/review"

	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, def, platformDefault, ""}), http.StatusOK)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, rule, scopeCaseBody, ""}), http.StatusOK)

	for _, c := range []gateCall{
		{http.MethodPut, def, platformDefault, `"7"`},
		{http.MethodDelete, def, "", `"7"`},
		{http.MethodPut, rule, scopeCaseBody, `"7"`},
		{http.MethodDelete, rule, "", `"7"`},
	} {
		rec := serveGate(t, h, c)
		wantStatus(t, rec, http.StatusConflict)
		wantCode(t, rec, errcode.GateVersionConflict)
	}

	var gotRule ruleDoc

	decodeGateJSON(t, serveGate(t, h, gateCall{http.MethodGet, rule, "", ""}), &gotRule)

	if gotRule.Version != 1 {
		t.Fatalf("rule after refused writes = %+v, want version 1", gotRule)
	}
}

// guardBody sends a body with an unknown field, a body above the cap and a
// body with an ID, and checks that nothing is stored.
func guardBody(t *testing.T, base string) {
	t.Helper()

	_, h := newGateHandler(t, adminGates())
	rule, def := base+gateRulesURL+"/review", base+gateDefault
	over := `{"count":1,"approvers":{"groups":["` + strings.Repeat("g", 64*1024) + `"]}}`

	for _, c := range []gateCall{
		{http.MethodPut, rule, `{"count":1,"approvers":{"groups":["ops"]},"scope":"x"}`, ""},
		{http.MethodPut, rule, `{"id":"x","count":1,"approvers":{"groups":["ops"]}}`, ""},
		{http.MethodPut, rule, over, ""},
		{http.MethodPut, def, `{"count":1,"approvers":{"groups":["ops"]},"scope":"x"}`, ""},
		{http.MethodPut, def, over, ""},
	} {
		wantStatus(t, serveGate(t, h, c), http.StatusBadRequest)
	}

	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, rule, "", ""}), http.StatusNotFound)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, def, "", ""}), http.StatusNotFound)
}

// guardLimit saves gate.MaxApprovalRulesPerScope rules and checks that the
// next one answers 422 with the code gate.limit and is not stored.
func guardLimit(t *testing.T, base string) {
	t.Helper()

	_, h := newGateHandler(t, adminGates())
	list := base + gateRulesURL

	for i := range gate.MaxApprovalRulesPerScope {
		rec := serveGate(t, h, gateCall{http.MethodPut, fmt.Sprintf("%s/r%02d", list, i), scopeCaseBody, ""})
		wantStatus(t, rec, http.StatusOK)
	}

	rec := serveGate(t, h, gateCall{http.MethodPut, list + "/over", scopeCaseBody, ""})
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantCode(t, rec, errcode.GateLimit)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodGet, list + "/over", "", ""}), http.StatusNotFound)

	// A replace at the limit is not a new rule.
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, list + "/r00", scopeCaseBody, ""}), http.StatusOK)
}

// guardAuditEntry writes a rule and a default and checks that each audit entry
// names the caller, the action and the scope key of the row.
func guardAuditEntry(t *testing.T, level, base string) {
	t.Helper()

	st, h := newGateHandler(t, adminGates())
	rule := base + gateRulesURL + "/review"

	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, rule, scopeCaseBody, ""}), http.StatusOK)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodPut, base + gateDefault, platformDefault, ""}), http.StatusOK)
	wantStatus(t, serveGate(t, h, gateCall{http.MethodDelete, rule, "", ""}), http.StatusNoContent)

	prefix := map[string]string{
		"namespace": "namespace/acme", "collection": "collection/acme/payments", "unit": "unit/acme/pulumi/web%2Fprod",
	}[level]

	want := map[string]string{
		gate.AuditSetApprovalRule:    prefix + "/approval-rules/review",
		gate.AuditSetApprovalDefault: prefix + "/approval-default",
		gate.AuditDeleteApprovalRule: prefix + "/approval-rules/review",
	}

	got := entriesOf(t, st, "acme", func(e *audit.Entry) bool { _, ok := want[e.Action]; return ok })
	if len(got) != len(want) {
		t.Fatalf("found %d of the %d entries of the writes", len(got), len(want))
	}

	for i := range got {
		e := &got[i]
		if e.Object != want[e.Action] || e.Subject == "" || e.Kind != audit.KindChange ||
			e.Outcome != audit.OutcomeSuccess {
			t.Errorf("entry %q = %+v, want the object %q, a subject, kind change, success", e.Action, e, want[e.Action])
		}
	}
}
