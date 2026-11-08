package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	settingsURL       = "/api/dustpan/v1/settings"
	settingsNSURL     = "/api/dustpan/v1/namespaces/acme/settings"
	mergePatchType    = "application/merge-patch+json"
	settingsFreshCode = `"errorCode":"mfa.fresh-step-required"`
)

// settingsEnv is a handler over a real store with the settings routes.
type settingsEnv struct {
	h  testHandler
	st *sqlite.Store
}

// newSettingsEnv builds a handler with the settings test registry, the static
// layer static, and no console sign-in. deny lists the actions that the
// authorizer refuses.
func newSettingsEnv(t *testing.T, static setting.Static, deny ...identity.Action) settingsEnv {
	t.Helper()

	return newSettingsEnvWith(t, settingtest.Registry(), static, deny...)
}

// newSettingsEnvWith builds a handler with the registry reg, the static layer
// static, and no console sign-in. deny lists the actions that the authorizer
// refuses.
func newSettingsEnvWith(
	t *testing.T, reg *setting.Registry, static setting.Static, deny ...identity.Action,
) settingsEnv {
	t.Helper()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), "acme"); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	handler, err := newCheckedHandler(t, settingsConfig(
		st, namespaces, selectiveAuthorizer{deny: denied},
		setting.NewService(st.SettingStore(), reg, static, st, nil),
	))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return settingsEnv{h: handler, st: st}
}

// settingsConfig returns the handler config of the settings tests: no
// console sign-in, the audit log of st and the service svc.
func settingsConfig(
	st *sqlite.Store, namespaces *namespace.Service, authz identity.Authorizer, svc *setting.Service,
) *apiserver.Config {
	return &apiserver.Config{
		Reach:       noReach(),
		LocalUsers:  nil,
		Preferences: nil, Settings: svc, GitHost: nil, Idempotency: nil, Workers: nil, InstallationKind: "",
		StateWriteGates: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:      nil, Now: nil, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Logger: slog.New(slog.DiscardHandler), Namespaces: namespaces, Backups: nil, Restores: nil,
		Maintenance: newFakeMaintenance(maintenance.StateNormal), Metrics: stubMetrics{}, Version: "",
		Schema: stubSchema{migrations: nil}, Journal: nil, Principals: nil, Tokens: nil, Access: nil, Audit: st,
		IPAllowlists: nil, Gates: nil, SessionPolicy: nil, CheckpointEpochs: nil, OnAuditFailure: nil,
		AuditChain: nil, DeletedUnits: nil, AuditSinks: nil, SignIn: nil, DenialLimiter: nil, Instances: nil,
		StoreChecks: nil, GitHostLinks: nil,
	}
}

// settingsCall is one request of a test.
type settingsCall struct {
	method, path, body, contentType, ifMatch string
}

// do sends the request with the test token.
func (e settingsEnv) do(t *testing.T, c settingsCall) response {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	req.Header.Set("Authorization", "Bearer test-token")

	if c.contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}

	if c.ifMatch != "" {
		req.Header.Set("If-Match", c.ifMatch)
	}

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	return response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// getCall returns a GET of path.
func getCall(path string) settingsCall {
	return settingsCall{method: http.MethodGet, path: path, body: "", contentType: "", ifMatch: ""}
}

// patch sends a merge patch with the If-Match header ifMatch.
func (e settingsEnv) patch(t *testing.T, path, ifMatch, body string) response {
	t.Helper()

	return e.do(t, settingsCall{
		method: http.MethodPatch, path: path, body: body, contentType: mergePatchType, ifMatch: ifMatch,
	})
}

// decodeSettings decodes the body of an answer.
func decodeSettings(t *testing.T, r response) map[string]map[string]any {
	t.Helper()

	var doc struct {
		Revision int64            `json:"revision"`
		Settings []map[string]any `json:"settings"`
	}

	if err := json.Unmarshal([]byte(r.body), &doc); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}

	out := map[string]map[string]any{}
	for _, s := range doc.Settings {
		key, ok := s["key"].(string)
		if !ok {
			t.Fatalf("a setting has no key: %v", s)
		}

		out[key] = s
	}

	return out
}

// isTrue reports whether v is the JSON value true.
func isTrue(v any) bool {
	b, ok := v.(bool)

	return ok && b
}

// isFalse reports whether v is the JSON value false.
func isFalse(v any) bool {
	b, ok := v.(bool)

	return ok && !b
}

func wantSettingsStatus(t *testing.T, r response, status int) {
	t.Helper()

	if r.status != status {
		t.Fatalf("status = %d %s, want %d", r.status, r.body, status)
	}
}

func TestSettingsReadShowsEachSettingWithItsSource(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, settingtest.StaticMap{
		settingtest.KeyLimit:  settingtest.File("3"),
		settingtest.KeySecret: {Source: setting.SourceEnv, Value: "", By: "the environment variable S"},
	})

	r := e.do(t, getCall(settingsURL))
	wantSettingsStatus(t, r, http.StatusOK)

	if r.header.Get("ETag") != `"0"` || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", r.header)
	}

	got := decodeSettings(t, r)
	if len(got) != 5 {
		t.Fatalf("settings = %d, want the 5 installation settings", len(got))
	}

	checkSettingFields(t, got)

	if strings.Contains(r.body, "hunter2") {
		t.Fatal("the body holds a secret")
	}
}

// checkSettingFields checks the fields of the limit, the secret and the switch
// of the read test.
func checkSettingFields(t *testing.T, got map[string]map[string]any) {
	t.Helper()

	limit := got[settingtest.KeyLimit]
	if limit["value"] != float64(3) || limit["source"] != "file" || !isTrue(limit["locked"]) ||
		limit["default"] != float64(10) {
		t.Fatalf("limit = %v", limit)
	}

	secret := got[settingtest.KeySecret]
	if !isTrue(secret["secret"]) || !isTrue(secret["set"]) || secret["value"] != nil || secret["default"] != nil {
		t.Fatalf("secret = %v", secret)
	}

	enabled := got[settingtest.KeyEnabled]
	if !isFalse(enabled["value"]) || !isTrue(enabled["mayReduceSecurity"]) || enabled["kind"] != "bool" {
		t.Fatalf("enabled = %v", enabled)
	}
}

func TestSettingsPatchChangesTheValueAndWritesOneEntry(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	r := e.patch(t, settingsURL, `"0"`, `{"test.label":"blue","test.limit":42}`)
	wantSettingsStatus(t, r, http.StatusOK)

	if r.header.Get("ETag") != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", r.header.Get("ETag"))
	}

	got := decodeSettings(t, r)
	if got[settingtest.KeyLabel]["value"] != "blue" || got[settingtest.KeyLabel]["source"] != "database" ||
		got[settingtest.KeyLimit]["databaseValue"] != float64(42) {
		t.Fatalf("settings = %v", got)
	}

	entries := serverEntries(t, e.st)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}

	checkChangeEntry(t, entries[0])

	// A read returns the new revision, and a second patch needs it.
	again := e.do(t, getCall(settingsURL))
	if again.header.Get("ETag") != `"1"` {
		t.Fatalf("ETag after the change = %q", again.header.Get("ETag"))
	}

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.label":"red"}`), http.StatusPreconditionFailed)
	wantSettingsStatus(t, e.patch(t, settingsURL, `"1"`, `{"test.label":null}`), http.StatusOK)
}

// checkChangeEntry checks the entry of the change of the patch test.
func checkChangeEntry(t *testing.T, entry audit.Entry) {
	t.Helper()

	if entry.Action != string(identity.ActionServerManageSettings) || entry.Kind != audit.KindChange ||
		entry.Outcome != audit.OutcomeSuccess || entry.Subject != "tester" || entry.Object != "@server" ||
		len(entry.Changes) != 2 {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestSettingsPatchThatChangesNothingWritesNothing(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.label":"blue"}`), http.StatusOK)

	r := e.patch(t, settingsURL, `"1"`, `{"test.label":"blue","test.limit":null}`)
	wantSettingsStatus(t, r, http.StatusOK)

	if r.header.Get("ETag") != `"1"` || len(serverEntries(t, e.st)) != 1 {
		t.Fatalf("ETag = %q, entries = %d", r.header.Get("ETag"), len(serverEntries(t, e.st)))
	}
}

func TestSettingsPatchRefusals(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("3")})

	tests := []struct {
		name   string
		call   settingsCall
		status int
		code   string
	}{
		{
			"no If-Match",
			settingsCall{http.MethodPatch, settingsURL, `{}`, mergePatchType, ""},
			http.StatusPreconditionRequired, "request.precondition-required",
		},
		{
			"a stale revision",
			settingsCall{http.MethodPatch, settingsURL, `{"test.label":"a"}`, mergePatchType, `"9"`},
			http.StatusPreconditionFailed, "request.precondition-failed",
		},
		{
			"a weak tag",
			settingsCall{http.MethodPatch, settingsURL, `{}`, mergePatchType, `W/"0"`},
			http.StatusPreconditionFailed, "request.precondition-failed",
		},
		{
			"an If-Match that is not a revision",
			settingsCall{http.MethodPatch, settingsURL, `{}`, mergePatchType, `0`},
			http.StatusBadRequest, "request.if-match-invalid",
		},
		{
			"a wrong content type",
			settingsCall{http.MethodPatch, settingsURL, `{}`, "application/json", `"0"`},
			http.StatusUnsupportedMediaType, "request.content-type-unsupported",
		},
		{
			"a body that is not an object",
			settingsCall{http.MethodPatch, settingsURL, `[]`, mergePatchType, `"0"`},
			http.StatusBadRequest, "settings.patch-invalid",
		},
		{
			"a nested value",
			settingsCall{http.MethodPatch, settingsURL, `{"test.label":{"a":1}}`, mergePatchType, `"0"`},
			http.StatusBadRequest, "settings.value-invalid",
		},
		{
			"an unknown key",
			settingsCall{http.MethodPatch, settingsURL, `{"nope":1}`, mergePatchType, `"0"`},
			http.StatusBadRequest, "settings.key-unknown",
		},
		{
			"a key of the other level",
			settingsCall{http.MethodPatch, settingsURL, `{"test.nsFlag":true}`, mergePatchType, `"0"`},
			http.StatusBadRequest, "settings.key-unknown",
		},
		{
			"a value of the wrong kind",
			settingsCall{http.MethodPatch, settingsURL, `{"test.enabled":"yes"}`, mergePatchType, `"0"`},
			http.StatusBadRequest, "settings.value-invalid",
		},
		{
			"a locked key",
			settingsCall{http.MethodPatch, settingsURL, `{"test.label":"a","test.limit":5}`, mergePatchType, `"0"`},
			http.StatusConflict, "settings.locked",
		},
		{
			"a static-only key",
			settingsCall{http.MethodPatch, settingsURL, `{"test.fixed":"a"}`, mergePatchType, `"0"`},
			http.StatusConflict, "settings.static-only",
		},
		{
			"a secret that only the static layer sets",
			settingsCall{http.MethodPatch, settingsURL, `{"test.secret":"hunter2"}`, mergePatchType, `"0"`},
			http.StatusConflict, "settings.static-only",
		},
		{
			"a body over the limit",
			settingsCall{
				http.MethodPatch,
				settingsURL,
				`{"test.label":"` + strings.Repeat("a", setting.MaxBodyBytes) + `"}`,
				mergePatchType,
				`"0"`,
			},
			http.StatusRequestEntityTooLarge,
			"request.body-too-large",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := e.do(t, tc.call)
			wantSettingsStatus(t, r, tc.status)

			if !strings.Contains(r.body, `"errorCode":"`+tc.code+`"`) {
				t.Fatalf("body = %s, want the code %s", r.body, tc.code)
			}

			if strings.Contains(r.body, "hunter2") {
				t.Fatal("the answer repeats the secret of the request")
			}
		})
	}

	got := e.do(t, getCall(settingsURL))
	if got.header.Get("ETag") != `"0"` {
		t.Fatalf("a refused change moved the revision to %s", got.header.Get("ETag"))
	}
}

func TestSettingsLockedAnswerNamesTheKeyAndItsSource(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("3")})

	r := e.patch(t, settingsURL, `"0"`, `{"test.limit":5}`)
	wantSettingsStatus(t, r, http.StatusConflict)

	if !strings.Contains(r.body, `"key":"test.limit"`) || !strings.Contains(r.body, `"source":"file"`) {
		t.Fatalf("body = %s, want the key and the source", r.body)
	}
}

// egressRegistry returns the registry with the declarations of the outbound
// connection settings.
func egressRegistry(t *testing.T) *setting.Registry {
	t.Helper()

	reg, err := setting.NewRegistry(plainconn.SettingDeclarations())
	if err != nil {
		t.Fatalf("egress registry: %v", err)
	}

	return reg
}

// TestSettingsPlainConnectionsLockedByTheStaticLayer proves the hosted
// refusal: with the variable at false, the static layer locks the setting, a
// change to true answers the locked conflict, stores nothing and writes one
// failure entry.
func TestSettingsPlainConnectionsLockedByTheStaticLayer(t *testing.T) {
	t.Parallel()

	static := settingtest.StaticMap{
		plainconn.SettingAllowPlainConnections: {
			Source: setting.SourceEnv, Value: "false",
			By: "the environment variable DUSTPAN_ALLOW_PLAIN_CONNECTIONS",
		},
	}
	e := newSettingsEnvWith(t, egressRegistry(t), static)

	read := e.do(t, getCall(settingsURL))
	wantSettingsStatus(t, read, http.StatusOK)
	checkPlainSetting(t, decodeSettings(t, read)[plainconn.SettingAllowPlainConnections])

	r := e.patch(t, settingsURL, `"0"`, `{"egress.allowPlainConnections":true}`)
	wantSettingsStatus(t, r, http.StatusConflict)

	if !strings.Contains(r.body, `"errorCode":"settings.locked"`) ||
		!strings.Contains(r.body, `"key":"egress.allowPlainConnections"`) {
		t.Fatalf("body = %s, want the locked conflict with the key", r.body)
	}

	after := e.do(t, getCall(settingsURL))
	if after.header.Get("ETag") != `"0"` {
		t.Fatalf("ETag = %q, want \"0\": the refused change stored a value", after.header.Get("ETag"))
	}

	checkPlainSetting(t, decodeSettings(t, after)[plainconn.SettingAllowPlainConnections])
	checkOneFailureEntry(t, serverEntries(t, e.st))
}

// checkPlainSetting fails the test unless item is the plain-connection
// setting with the static value: false from the environment, locked, with a
// live database layer.
func checkPlainSetting(t *testing.T, item map[string]any) {
	t.Helper()

	if !isFalse(item["value"]) || item["source"] != "env" || !isTrue(item["locked"]) {
		t.Fatalf("setting = %v, want false from the environment, locked", item)
	}

	if !isTrue(item["databaseLayer"]) || item["apply"] != "live" {
		t.Fatalf("setting = %v, want a live database layer", item)
	}
}

// checkOneFailureEntry fails the test unless entries holds one failure
// change entry.
func checkOneFailureEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}

	if entries[0].Outcome != audit.OutcomeFailure || entries[0].Kind != audit.KindChange {
		t.Fatalf("entry = %+v, want a failure change", entries[0])
	}
}

func TestSettingsNullOnALockedKeyDeletesTheShadowedValue(t *testing.T) {
	t.Parallel()

	st := openRouteTestStore(t)
	// The value was set before the file locked the key.
	first := newSettingsEnvOn(t, st, nil)
	wantSettingsStatus(t, first.patch(t, settingsURL, `"0"`, `{"test.limit":42}`), http.StatusOK)

	locked := newSettingsEnvOn(t, st, settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("3")})

	r := locked.do(t, getCall(settingsURL))
	limit := decodeSettings(t, r)[settingtest.KeyLimit]

	if !isTrue(limit["shadowed"]) || limit["databaseValue"] != float64(42) || limit["value"] != float64(3) {
		t.Fatalf("limit = %v, want the file value over the shadowed database value", limit)
	}

	r = locked.patch(t, settingsURL, `"1"`, `{"test.limit":null}`)
	wantSettingsStatus(t, r, http.StatusOK)

	limit = decodeSettings(t, r)[settingtest.KeyLimit]
	if !isFalse(limit["shadowed"]) || limit["databaseValue"] != nil || limit["value"] != float64(3) {
		t.Fatalf("limit = %v", limit)
	}
}

// newSettingsEnvOn is newSettingsEnv over an existing store.
func newSettingsEnvOn(t *testing.T, st *sqlite.Store, static setting.Static) settingsEnv {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, settingsConfig(
		st, namespaces, allowAll{},
		setting.NewService(st.SettingStore(), settingtest.Registry(), static, st, nil),
	))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return settingsEnv{h: handler, st: st}
}

func TestSettingsOfANamespace(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	r := e.do(t, getCall(settingsNSURL))
	wantSettingsStatus(t, r, http.StatusOK)

	if got := decodeSettings(t, r); len(got) != 2 || got[settingtest.KeyNSLimit]["scope"] != "namespace" {
		t.Fatalf("settings = %v", got)
	}

	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"0"`, `{"test.nsLimit":9}`), http.StatusOK)

	entries := listNamespaceEntries(t, e.st, "acme")
	if len(entries) != 1 || entries[0].Action != string(identity.ActionNamespaceManageSettings) ||
		entries[0].Object != "acme" {
		t.Fatalf("entries = %+v", entries)
	}

	if len(serverEntries(t, e.st)) != 0 {
		t.Fatal("a change of a namespace wrote an entry in the server scope")
	}

	// A key of the installation is not a key of the namespace.
	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"1"`, `{"test.limit":1}`), http.StatusBadRequest)

	missing := e.do(t, getCall("/api/dustpan/v1/namespaces/nowhere/settings"))
	wantSettingsStatus(t, missing, http.StatusNotFound)
}

func TestSettingsActionsAreChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		deny   identity.Action
		method string
		path   string
	}{
		{"read installation", identity.ActionServerReadSettings, http.MethodGet, settingsURL},
		{"manage installation", identity.ActionServerManageSettings, http.MethodPatch, settingsURL},
		{"read namespace", identity.ActionNamespaceReadSettings, http.MethodGet, settingsNSURL},
		{"manage namespace", identity.ActionNamespaceManageSettings, http.MethodPatch, settingsNSURL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSettingsEnv(t, nil, tc.deny)

			r := e.do(t, settingsCall{
				method: tc.method, path: tc.path, body: `{}`, contentType: mergePatchType, ifMatch: `"0"`,
			})
			wantSettingsStatus(t, r, http.StatusForbidden)
		})
	}

	// Denying one action leaves the others.
	e := newSettingsEnv(t, nil, identity.ActionServerManageSettings)
	wantSettingsStatus(t, e.do(t, getCall(settingsURL)), http.StatusOK)
}

func TestSettingsRefusalWritesAFailureEntry(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"nope":"hunter2"}`), http.StatusBadRequest)

	entries := serverEntries(t, e.st)
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeFailure || entries[0].Kind != audit.KindChange {
		t.Fatalf("entries = %+v, want one failure entry", entries)
	}

	if strings.Contains(entries[0].Object+entries[0].Action, "hunter2") {
		t.Fatal("the failure entry holds a value of the request")
	}

	// A stale revision changes nothing and is no failure of the caller.
	wantSettingsStatus(t, e.patch(t, settingsURL, `"7"`, `{"test.label":"a"}`), http.StatusPreconditionFailed)

	if len(serverEntries(t, e.st)) != 1 {
		t.Fatal("a stale revision wrote an entry")
	}
}

// TestSettingsWithoutAStoreAnswers501 checks that a server with no settings
// store answers 501 on every route of the resource.
func TestSettingsWithoutAStoreAnswers501(t *testing.T) {
	t.Parallel()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, settingsConfig(st, namespaces, allowAll{}, nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	e := settingsEnv{h: handler, st: st}

	wantSettingsStatus(t, e.do(t, getCall(settingsURL)), http.StatusNotImplemented)
	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"0"`, `{}`), http.StatusNotImplemented)
}

// TestSettingsChangeThatReducesSecurityNeedsAFreshStep checks the rule for an
// access token on an installation that has a console sign-in: the change
// answers 403 with mfa.fresh-step-required and changes nothing, and a change
// that does not reduce security works.
func TestSettingsChangeThatReducesSecurityNeedsAFreshStep(t *testing.T) {
	t.Parallel()

	h, st := newServiceBackedHandlerOf(t, prefsCallerSet(), allowAll{}, failedSeed())
	e := settingsEnv{h: h, st: st}

	send := func(ifMatch, body string) response {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, settingsURL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tokenAdmin)
		req.Header.Set("Content-Type", mergePatchType)
		req.Header.Set("If-Match", ifMatch)

		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)

		return response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
	}

	wantSettingsStatus(t, send(`"0"`, `{"test.enabled":true}`), http.StatusOK)

	r := send(`"1"`, `{"test.enabled":false,"test.label":"x"}`)
	wantSettingsStatus(t, r, http.StatusForbidden)

	if !strings.Contains(r.body, settingsFreshCode) {
		t.Fatalf("body = %s, want the code mfa.fresh-step-required", r.body)
	}

	// The refused change moved no value, not even the key that does not reduce security.
	readReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, settingsURL, nil)
	readReq.Header.Set("Authorization", "Bearer "+tokenAdmin)

	readRec := httptest.NewRecorder()
	e.h.ServeHTTP(readRec, readReq)

	read := response{status: readRec.Code, header: readRec.Header(), body: readRec.Body.String()}
	if got := decodeSettings(t, read)[settingtest.KeyLabel]; got["source"] != "default" {
		t.Fatalf("label = %v, want the default: the refused change moved a key", got)
	}

	wantSettingsStatus(t, send(`"1"`, `{"test.label":"x"}`), http.StatusOK)
}

// TestSettingsChangeThatReducesSecurityNeedsNoStepWithoutSignIn checks the
// rule for an installation with no console sign-in: no authentication step
// exists to ask for, so a change that reduces security works with an access
// token.
func TestSettingsChangeThatReducesSecurityNeedsNoStepWithoutSignIn(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.enabled":true}`), http.StatusOK)
	wantSettingsStatus(t, e.patch(t, settingsURL, `"1"`, `{"test.enabled":false}`), http.StatusOK)
	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"0"`, `{"test.nsFlag":true}`), http.StatusOK)
	wantSettingsStatus(t, e.patch(t, settingsNSURL, `"1"`, `{"test.nsFlag":false}`), http.StatusOK)
}

// settingsScenarios returns the scenarios of the settings operations for the
// check of the header flags.
func settingsScenarios() map[string]headertest.Scenario {
	scenario := func(method, path, body string) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			e := newSettingsEnv(t, nil)

			current := e.do(t, getCall(path))
			wantSettingsStatus(t, current, http.StatusOK)

			return headertest.Probe{
				ETag: current.header.Get("ETag"),
				Send: func(headers map[string]string) headertest.Answer {
					contentType := ""
					if body != "" {
						contentType = mergePatchType
					}

					got := e.do(t, settingsCall{
						method: method, path: path, body: body, contentType: contentType,
						ifMatch: headers["If-Match"],
					})

					return headertest.Answer{Status: got.status, Header: got.header, Body: got.body}
				},
				Replayed: nil,
			}
		}
	}

	return map[string]headertest.Scenario{
		"getSettings":             scenario(http.MethodGet, settingsURL, ""),
		"updateSettings":          scenario(http.MethodPatch, settingsURL, `{"test.label":"blue"}`),
		"getNamespaceSettings":    scenario(http.MethodGet, settingsNSURL, ""),
		"updateNamespaceSettings": scenario(http.MethodPatch, settingsNSURL, `{"test.nsLimit":9}`),
	}
}
