package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// ipTestRemote is the client address of every IP allowlist test request.
const ipTestRemote = "192.0.2.10:1234"

// ipAllowlistDoc is one IP allowlist answer on the wire.
type ipAllowlistDoc struct {
	Enabled bool     `json:"enabled"`
	OptedIn bool     `json:"optedIn"`
	CIDRs   []string `json:"cidrs"`
}

// The installation API error shape and its reader live in backups_test.go:
// errorDoc and decodeError.

// ipHandlerConfig carries the options of newIPAllowHandler: the allowlist
// store (the SQLite store when nil), the authenticator, the authorizer and
// the feature switch.
type ipHandlerConfig struct {
	rules   ipallow.Store
	authn   identity.Authenticator
	authz   identity.Authorizer
	enabled bool
}

// allowAllConfig returns the config of a handler that allows every action,
// with the feature switch on.
func allowAllConfig() ipHandlerConfig {
	return ipHandlerConfig{
		rules:   nil,
		authn:   tokenAuthenticator{},
		authz:   selectiveAuthorizer{deny: map[identity.Action]bool{}},
		enabled: true,
	}
}

// openIPAllowFixture opens a fresh SQLite store with the namespaces acme and
// beta and returns it with its namespace service.
func openIPAllowFixture(t *testing.T) (*sqlite.Store, *namespace.Service) {
	t.Helper()

	st := openRouteTestStore(t)

	nss, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	for _, name := range []string{"acme", "beta"} {
		if _, createErr := nss.Create(t.Context(), namespace.Name(name)); createErr != nil {
			t.Fatalf("seed namespace %s: %v", name, createErr)
		}
	}

	return st, nss
}

// newIPAllowHandler builds a Handler whose IP allowlist service runs with
// cfg.
func newIPAllowHandler(
	t *testing.T, st *sqlite.Store, nss *namespace.Service, cfg ipHandlerConfig,
) testHandler {
	t.Helper()

	rules := cfg.rules
	if rules == nil {
		rules = st
	}

	svc, err := ipallow.NewService(ipallow.Config{Store: rules, Namespaces: nss, Now: nil, Enabled: cfg.enabled})
	if err != nil {
		t.Fatalf("ip allowlist service: %v", err)
	}

	signIn, sessions := signInOf(t, st)

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
		Authenticator:    cfg.authn,
		Authorizer:       cfg.authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       nss,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          nil,
		Principals:       sessions.principals,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		IPAllowlists:     svc,
		Gates:            nil,
		SessionPolicy:    nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		SignIn:           signIn,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rememberSessions(handler, sessions)

	return handler
}

// allowAllIPAllowHandler builds a Handler over a fresh fixture that allows
// every action, with the feature switch on.
func allowAllIPAllowHandler(t *testing.T) (*sqlite.Store, testHandler) {
	t.Helper()

	st, nss := openIPAllowFixture(t)

	return st, newIPAllowHandler(t, st, nss, allowAllConfig())
}

// ipCall is one IP allowlist request: its method, namespace, token and body.
// Every call reads from the fixed test address.
type ipCall struct {
	method string
	ns     string
	token  string
	body   string
	// signInAge is the age of the sign-in of a console session. Fresh sets it.
	signInAge time.Duration
	// session sends the call with a console session in place of the token.
	session bool
}

// fresh returns call as a console session that signed in just now. A change
// of an allowlist to off or to a new list needs a fresh authentication, and
// an access token never gives one.
func (call ipCall) fresh() ipCall {
	call.token, call.session, call.signInAge = "", true, 0

	return call
}

// ipGet returns the GET call of ns with the test token.
func ipGet(ns string) ipCall {
	return ipCall{method: http.MethodGet, ns: ns, token: "test-token", body: "", signInAge: 0, session: false}
}

// ipPut returns the PUT call of body on ns from a console session that
// signed in just now: a change of a list needs a fresh authentication.
func ipPut(ns, body string) ipCall {
	return ipCall{method: http.MethodPut, ns: ns, token: "test-token", body: body, signInAge: 0, session: false}.fresh()
}

// ipDelete returns the DELETE call of ns from a console session that signed
// in just now.
func ipDelete(ns string) ipCall {
	return ipCall{
		method:    http.MethodDelete,
		ns:        ns,
		token:     "test-token",
		body:      "",
		signInAge: 0,
		session:   false,
	}.fresh()
}

// withToken returns call with the given access token in place of a session.
func (call ipCall) withToken(token string) ipCall {
	call.token, call.session = token, false

	return call
}

// serveIPAllowlist serves call and answers it.
func serveIPAllowlist(t *testing.T, h testHandler, call ipCall) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader = http.NoBody
	if call.body != "" {
		reader = strings.NewReader(call.body)
	}

	req := httptest.NewRequestWithContext(
		t.Context(), call.method, "/api/server/namespaces/"+call.ns+"/ip-allowlist", reader,
	)
	req.RemoteAddr = ipTestRemote

	if call.token != "" {
		req.Header.Set("Authorization", "Bearer "+call.token)
	}

	if call.session {
		startSession(t, h, req, call.signInAge)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// decodeIPAllowlistDoc reads a 200 answer as an IP allowlist document.
func decodeIPAllowlistDoc(t *testing.T, rec *httptest.ResponseRecorder) ipAllowlistDoc {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc ipAllowlistDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode allowlist body %q: %v", rec.Body.String(), err)
	}

	return doc
}

// seedIPAllowlist writes the list cidrs for ns straight into the store, so a
// test can start from a stored rule without calling the API.
func seedIPAllowlist(t *testing.T, st *sqlite.Store, ns namespace.Name, cidrs ...string) {
	t.Helper()

	list, err := ipallow.ParseList(cidrs)
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}

	entry := audit.Entry{
		At:            time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC),
		Namespace:     ns,
		Subject:       "tester",
		IDPSubject:    "",
		ObjectID:      "",
		TokenID:       "",
		Action:        "namespace:set-ip-allowlist",
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	fence := namespace.Fence{Namespace: ns, FailoverVersion: 1}

	if err := st.SetNamespaceList(t.Context(), ns, list, fence, entry); err != nil {
		t.Fatalf("seed list of %s: %v", ns, err)
	}
}

// TestIPAllowlistGetWithoutRule proves GET of a namespace without a rule
// answers 200 with the switch on, optedIn false and an empty (never null)
// list.
func TestIPAllowlistGetWithoutRule(t *testing.T) {
	t.Parallel()

	_, handler := allowAllIPAllowHandler(t)

	doc := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if !doc.Enabled || doc.OptedIn || len(doc.CIDRs) != 0 {
		t.Fatalf("GET without a rule = %+v, want enabled, not opted in and no CIDRs", doc)
	}
}

// TestIPAllowlistPutCanonicalizes proves PUT stores the list and answers 200
// with the canonical CIDR text: a bare address reads as a host prefix and a
// prefix with host bits is masked.
func TestIPAllowlistPutCanonicalizes(t *testing.T) {
	t.Parallel()

	_, handler := allowAllIPAllowHandler(t)

	rec := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["192.0.2.7","10.1.2.3/8"]}`))
	doc := decodeIPAllowlistDoc(t, rec)

	if !doc.Enabled || !doc.OptedIn {
		t.Fatalf("PUT answer = %+v, want enabled and opted in", doc)
	}

	want := "192.0.2.7/32,10.0.0.0/8"
	if strings.Join(doc.CIDRs, ",") != want {
		t.Fatalf("PUT answer CIDRs = %v, want %s", doc.CIDRs, want)
	}

	got := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if strings.Join(got.CIDRs, ",") != want {
		t.Fatalf("GET after PUT CIDRs = %v, want %s", got.CIDRs, want)
	}
}

// badIPAllowlistBodies holds one bad PUT body per name.
func badIPAllowlistBodies() []struct {
	name string
	body string
} {
	items := make([]string, 0, 65)
	for i := range 65 {
		items = append(items, `"192.0.2.`+strconv.Itoa(i)+`"`)
	}

	return []struct {
		name string
		body string
	}{
		{name: "invalid item", body: `{"cidrs":["nope"]}`},
		{name: "empty list", body: `{"cidrs":[]}`},
		{name: "missing cidrs", body: `{}`},
		{name: "null body", body: `null`},
		{name: "null cidrs", body: `{"cidrs":null}`},
		{name: "unknown field", body: `{"cidrs":["10.0.0.0/8"],"extra":1}`},
		{name: "trailing data", body: `{"cidrs":["10.0.0.0/8"]} {}`},
		{name: "body over the cap", body: strings.Repeat("x", 17*1024)},
		{name: "too many entries", body: `{"cidrs":[` + strings.Join(items, ",") + `]}`},
	}
}

// TestIPAllowlistPutBadBodies proves every bad PUT body answers 400 and
// leaves the stored list unchanged.
func TestIPAllowlistPutBadBodies(t *testing.T) {
	t.Parallel()

	for _, tt := range badIPAllowlistBodies() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, handler := allowAllIPAllowHandler(t)

			seed := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["10.0.0.0/8"]}`))
			if seed.Code != http.StatusOK {
				t.Fatalf("seed PUT answers %d, want 200: %s", seed.Code, seed.Body.String())
			}

			rec := serveIPAllowlist(t, handler, ipPut("acme", tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s answers %d, want 400: %s", tt.name, rec.Code, rec.Body.String())
			}

			if tt.name == "empty list" && !strings.Contains(rec.Body.String(), "DELETE") {
				t.Fatalf("empty list answer %s, want it to name DELETE", rec.Body.String())
			}

			doc := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
			if strings.Join(doc.CIDRs, ",") != "10.0.0.0/8" {
				t.Fatalf("stored list after a bad PUT = %v, want the seed list", doc.CIDRs)
			}
		})
	}
}

// TestIPAllowlistDisabled proves a server with the feature off answers GET
// with enabled false, refuses PUT with 409 that names the setting, and still
// serves DELETE with 204.
func TestIPAllowlistDisabled(t *testing.T) {
	t.Parallel()

	st, nss := openIPAllowFixture(t)
	seedIPAllowlist(t, st, "acme", "10.0.0.0/8")

	cfg := allowAllConfig()
	cfg.enabled = false
	handler := newIPAllowHandler(t, st, nss, cfg)

	doc := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if doc.Enabled || !doc.OptedIn || strings.Join(doc.CIDRs, ",") != "10.0.0.0/8" {
		t.Fatalf("GET while off = %+v, want disabled but with the stored list", doc)
	}

	put := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["192.0.2.0/24"]}`))
	if put.Code != http.StatusConflict {
		t.Fatalf("PUT while off answers %d, want 409: %s", put.Code, put.Body.String())
	}

	want := "IP allowlists are off for this installation: start the server with --ip-allowlists"
	if got := decodeError(t, put.Body.String()); got.Code != http.StatusConflict || got.Message != want {
		t.Fatalf("PUT while off error = %+v, want 409 %q", got, want)
	}

	remove := serveIPAllowlist(t, handler, ipDelete("acme"))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE while off answers %d, want 204: %s", remove.Code, remove.Body.String())
	}

	after := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if after.OptedIn || len(after.CIDRs) != 0 {
		t.Fatalf("GET after DELETE while off = %+v, want no rule", after)
	}
}

// checkAllowlistActions proves entries hold one set and one removal entry.
func checkAllowlistActions(t *testing.T, entries []audit.Entry) {
	t.Helper()

	var sets, removes int

	for i := range entries {
		switch entries[i].Action {
		case "namespace:set-ip-allowlist":
			sets++
		case "namespace:remove-ip-allowlist":
			removes++
		default:
			t.Fatalf("unexpected audit action %q", entries[i].Action)
		}
	}

	if sets != 1 || removes != 1 {
		t.Fatalf("audit sets = %d, removals = %d, want one of each", sets, removes)
	}
}

// TestIPAllowlistDelete proves DELETE removes the rule with 204, and a second
// DELETE answers 204 with no second audit entry.
func TestIPAllowlistDelete(t *testing.T) {
	t.Parallel()

	st, handler := allowAllIPAllowHandler(t)

	seed := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["10.0.0.0/8"]}`))
	if seed.Code != http.StatusOK {
		t.Fatalf("seed PUT answers %d, want 200: %s", seed.Code, seed.Body.String())
	}

	remove := serveIPAllowlist(t, handler, ipDelete("acme"))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE answers %d, want 204: %s", remove.Code, remove.Body.String())
	}

	if body := remove.Body.String(); body != "" {
		t.Fatalf("DELETE body = %q, want none", body)
	}

	after := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if after.OptedIn || len(after.CIDRs) != 0 {
		t.Fatalf("GET after DELETE = %+v, want no rule", after)
	}

	again := serveIPAllowlist(t, handler, ipDelete("acme"))
	if again.Code != http.StatusNoContent {
		t.Fatalf("second DELETE answers %d, want 204: %s", again.Code, again.Body.String())
	}

	checkAllowlistActions(t, listNamespaceEntries(t, st, "acme"))
}

// listNamespaceEntries returns the audit entries of the chain ns.
func listNamespaceEntries(t *testing.T, st *sqlite.Store, ns namespace.Name) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return listing.Entries
}

// refusalStore fails the test on any call, so the test proves a denied
// request never reaches the store.
type refusalStore struct {
	t *testing.T
}

// NamespaceList implements ipallow.Store.
func (s refusalStore) NamespaceList(_ context.Context, ns namespace.Name) (ipallow.List, error) {
	s.t.Errorf("denied request reached the store for %s", ns)

	return ipallow.List{}, namespace.ErrNotFound
}

// SetNamespaceList implements ipallow.Store.
func (s refusalStore) SetNamespaceList(
	_ context.Context, ns namespace.Name, _ ipallow.List, _ namespace.Fence, _ audit.Entry,
) error {
	s.t.Errorf("denied request reached the store for %s", ns)

	return namespace.ErrNotFound
}

// NamespacesWithList implements ipallow.Store.
func (s refusalStore) NamespacesWithList(context.Context) ([]namespace.Name, error) {
	s.t.Errorf("denied request reached the store")

	return nil, namespace.ErrNotFound
}

// TestIPAllowlistDeniedWithoutAction proves a principal without the action
// gets 403 on all three methods and no store call happens.
func TestIPAllowlistDeniedWithoutAction(t *testing.T) {
	t.Parallel()

	st, nss := openIPAllowFixture(t)
	seedIPAllowlist(t, st, "acme", "10.0.0.0/8")

	cfg := allowAllConfig()
	cfg.authz = selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionNamespaceManageIPAllowlist: true}}
	cfg.rules = refusalStore{t: t}
	handler := newIPAllowHandler(t, st, nss, cfg)

	for _, call := range []ipCall{ipGet("acme"), ipPut("acme", `{"cidrs":["192.0.2.0/24"]}`), ipDelete("acme")} {
		rec := serveIPAllowlist(t, handler, call)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s answers %d, want 403: %s", call.method, rec.Code, rec.Body.String())
		}
	}
}

// The bootstrap admin authenticator lives in metrics_test.go:
// adminAuthenticator signs in "admin-token" as an administrator.

// TestIPAllowlistBootstrapAdmin proves the bootstrap admin passes the route
// authorization on every method. A PUT and a DELETE with its access token
// then reach the freshness check, which refuses them: an access token never
// gives a fresh authentication.
func TestIPAllowlistBootstrapAdmin(t *testing.T) {
	t.Parallel()

	st, nss := openIPAllowFixture(t)

	cfg := allowAllConfig()
	cfg.authn = adminAuthenticator{}
	cfg.authz = identity.AdminOnly{}
	admin := newIPAllowHandler(t, st, nss, cfg)

	put := ipPut("acme", `{"cidrs":["10.0.0.0/8"]}`).withToken("admin-token")

	if rec := serveIPAllowlist(t, admin, put); rec.Code != http.StatusForbidden ||
		decodeErrorBody(t, rec).ErrorCode != freshErrorCode {
		t.Fatalf("admin PUT answers %d %s, want 403 with %s", rec.Code, rec.Body.String(), freshErrorCode)
	}

	get := ipGet("acme")
	get.token = "admin-token"

	if rec := serveIPAllowlist(t, admin, get); rec.Code != http.StatusOK {
		t.Fatalf("admin GET answers %d, want 200: %s", rec.Code, rec.Body.String())
	}

	remove := ipDelete("acme").withToken("admin-token")

	if rec := serveIPAllowlist(t, admin, remove); rec.Code != http.StatusForbidden ||
		decodeErrorBody(t, rec).ErrorCode != freshErrorCode {
		t.Fatalf("admin DELETE answers %d %s, want 403 with %s", rec.Code, rec.Body.String(), freshErrorCode)
	}
}

// checkSetEntry proves entry is the change entry of tester from the test
// address for the set of acme.
func checkSetEntry(t *testing.T, entry audit.Entry) {
	t.Helper()

	if entry.Action != "namespace:set-ip-allowlist" {
		t.Errorf("audit action = %q, want the set action", entry.Action)
	}

	if entry.Kind != audit.KindChange {
		t.Errorf("audit kind = %q, want change", entry.Kind)
	}

	if entry.Outcome != audit.OutcomeSuccess {
		t.Errorf("audit outcome = %q, want success", entry.Outcome)
	}

	if entry.Namespace != "acme" {
		t.Errorf("audit namespace = %q, want acme", entry.Namespace)
	}

	if entry.Subject != "tester" {
		t.Errorf("audit subject = %q, want tester", entry.Subject)
	}

	if entry.SourceIP != "192.0.2.10" {
		t.Errorf("audit source = %q, want the client address", entry.SourceIP)
	}

	if entry.Object != "acme" {
		t.Errorf("audit object = %q, want acme", entry.Object)
	}
}

// TestIPAllowlistAudit proves a PUT writes one change entry with the caller,
// the namespace and the client address, and a DELETE writes the removal.
func TestIPAllowlistAudit(t *testing.T) {
	t.Parallel()

	st, handler := allowAllIPAllowHandler(t)

	put := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["10.0.0.0/8"]}`))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT answers %d, want 200: %s", put.Code, put.Body.String())
	}

	entries := listNamespaceEntries(t, st, "acme")
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	checkSetEntry(t, entries[0])

	remove := serveIPAllowlist(t, handler, ipDelete("acme"))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE answers %d, want 204: %s", remove.Code, remove.Body.String())
	}

	checkAllowlistActions(t, listNamespaceEntries(t, st, "acme"))
}

// failWriteStore delegates reads to the inner store and fails every write,
// so the test proves a lost audit entry fails closed.
type failWriteStore struct {
	inner ipallow.Store
}

// NamespaceList implements ipallow.Store.
func (s failWriteStore) NamespaceList(ctx context.Context, ns namespace.Name) (ipallow.List, error) {
	l, err := s.inner.NamespaceList(ctx, ns)
	if err != nil {
		return ipallow.List{}, err
	}

	return l, nil
}

// SetNamespaceList implements ipallow.Store.
func (s failWriteStore) SetNamespaceList(
	_ context.Context, _ namespace.Name, _ ipallow.List, _ namespace.Fence, _ audit.Entry,
) error {
	return fmt.Errorf("append audit entry: %w", audit.ErrInvalidEntry)
}

// NamespacesWithList implements ipallow.Store.
func (s failWriteStore) NamespacesWithList(ctx context.Context) ([]namespace.Name, error) {
	return s.inner.NamespacesWithList(ctx)
}

// TestIPAllowlistAuditFailureClosed proves a store that fails the audit
// append leaves the list unchanged and answers an error, not 200.
func TestIPAllowlistAuditFailureClosed(t *testing.T) {
	t.Parallel()

	st, nss := openIPAllowFixture(t)

	cfg := allowAllConfig()
	cfg.rules = failWriteStore{inner: st}
	handler := newIPAllowHandler(t, st, nss, cfg)

	put := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["10.0.0.0/8"]}`))
	if put.Code == http.StatusOK {
		t.Fatalf("PUT with a failing store answers 200, want an error: %s", put.Body.String())
	}

	doc := decodeIPAllowlistDoc(t, serveIPAllowlist(t, handler, ipGet("acme")))
	if doc.OptedIn || len(doc.CIDRs) != 0 {
		t.Fatalf("stored list after a failed PUT = %+v, want no rule", doc)
	}
}

// TestIPAllowlistUnknownNamespace proves GET, PUT and DELETE of an unknown
// namespace answer 404.
func TestIPAllowlistUnknownNamespace(t *testing.T) {
	t.Parallel()

	_, handler := allowAllIPAllowHandler(t)

	for _, call := range []ipCall{ipGet("ghost"), ipPut("ghost", `{"cidrs":["10.0.0.0/8"]}`), ipDelete("ghost")} {
		rec := serveIPAllowlist(t, handler, call)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s of an unknown namespace answers %d, want 404: %s", call.method, rec.Code, rec.Body.String())
		}
	}
}

// TestIPAllowlistErrorsHideOtherLists proves no answer about one namespace
// ever holds the list of another: not the GET body, not a 400 or 404 error,
// and not a 403.
func TestIPAllowlistErrorsHideOtherLists(t *testing.T) {
	t.Parallel()

	st, nss := openIPAllowFixture(t)
	seedIPAllowlist(t, st, "beta", "198.51.100.0/24")

	handler := newIPAllowHandler(t, st, nss, allowAllConfig())

	bodies := make([]string, 0, 4)

	get := serveIPAllowlist(t, handler, ipGet("acme"))
	if get.Code != http.StatusOK {
		t.Fatalf("GET answers %d, want 200: %s", get.Code, get.Body.String())
	}

	bodies = append(bodies, get.Body.String())

	bad := serveIPAllowlist(t, handler, ipPut("acme", `{"cidrs":["nope"]}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad PUT answers %d, want 400: %s", bad.Code, bad.Body.String())
	}

	bodies = append(bodies, bad.Body.String())

	ghost := serveIPAllowlist(t, handler, ipPut("ghost", `{"cidrs":["10.0.0.0/8"]}`))
	if ghost.Code != http.StatusNotFound {
		t.Fatalf("PUT of an unknown namespace answers %d, want 404: %s", ghost.Code, ghost.Body.String())
	}

	bodies = append(bodies, ghost.Body.String())

	cfg := allowAllConfig()
	cfg.authz = selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionNamespaceManageIPAllowlist: true}}
	refused := newIPAllowHandler(t, st, nss, cfg)

	forbidden := serveIPAllowlist(t, refused, ipGet("acme"))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("denied GET answers %d, want 403: %s", forbidden.Code, forbidden.Body.String())
	}

	bodies = append(bodies, forbidden.Body.String())

	for _, body := range bodies {
		if strings.Contains(body, "198.51.100.0/24") {
			t.Fatalf("answer %q holds the list of another namespace", body)
		}
	}
}
