package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// keyedSettingsEnv is a handler over a real store with the settings routes, a
// console sign-in and the idempotency middleware.
type keyedSettingsEnv struct {
	h        testHandler
	st       *sqlite.Store
	sessions storeSessions
}

// newKeyedSettingsEnv builds a keyedSettingsEnv. The test token is the person
// tester. A console session of tester gives the fresh step when its sign-in is
// young.
func newKeyedSettingsEnv(t *testing.T) keyedSettingsEnv {
	t.Helper()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	// The principals of the route tests hold grants in testNamespace.
	for _, name := range []namespace.Name{"acme", testNamespace} {
		if _, seedErr := namespaces.Create(t.Context(), name); seedErr != nil {
			t.Fatalf("seed namespace %s: %v", name, seedErr)
		}
	}

	cfg := settingsConfig(
		t, st, namespaces, allowAll{},
		setting.NewService(st.SettingStore(), settingtest.Registry(), nil, st, nil),
	)

	signIn, sessions := signInOf(t, st)
	cfg.SignIn = signIn
	cfg.Principals = seedRoutePrincipals(t, st)

	mw := settingsIdempotency(t, st)

	cfg.Idempotency = mw

	h, err := newCheckedHandler(t, cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return keyedSettingsEnv{h: h, st: st, sessions: sessions}
}

// keyedPatch is one PATCH of a keyedSettingsEnv. With session set, the request
// comes from a console session whose sign-in is age old. Otherwise it carries
// the test token.
type keyedPatch struct {
	path, ifMatch, key, body string
	session                  bool
	age                      time.Duration
}

// tokenPatch returns a PATCH with the test token. A key of "" sends no
// Idempotency-Key, which the route refuses.
func tokenPatch(path, ifMatch, key, body string) keyedPatch {
	return keyedPatch{path: path, ifMatch: ifMatch, key: key, body: body, session: false, age: 0}
}

var sessionKeys atomic.Int64

// sessionKey returns a new Idempotency-Key for a PATCH of a console session.
func sessionKey() string { return "key-session-" + strconv.FormatInt(sessionKeys.Add(1), 10) }

// sessionPatch returns a PATCH from a console session whose sign-in is age old.
func sessionPatch(path, ifMatch, body string, age time.Duration) keyedPatch {
	return keyedPatch{path: path, ifMatch: ifMatch, key: sessionKey(), body: body, session: true, age: age}
}

// patch sends p.
func (e keyedSettingsEnv) patch(t *testing.T, p keyedPatch) response {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, p.path, strings.NewReader(p.body))
	req.Header.Set("Content-Type", mergePatchType)
	req.Header.Set("If-Match", p.ifMatch)

	if p.key != "" {
		req.Header.Set(idempotent.Header, p.key)
	}

	if p.session {
		e.sessions.start(t, req, p.age)
	} else {
		req.Header.Set("Authorization", "Bearer test-token")
	}

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	return response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// read sends a GET with the test token.
func (e keyedSettingsEnv) read(t *testing.T, path string) response {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	return response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// changeEntries counts the audit entries of the changes of the installation
// settings.
func (e keyedSettingsEnv) changeEntries(t *testing.T) int {
	t.Helper()

	n := 0

	entries := serverEntries(t, e.st)

	for i := range entries {
		if entries[i].Action == string(identity.ActionServerManageSettings) {
			n++
		}
	}

	return n
}

// TestSettingsPatchFreshStep checks the fresh step of a change that reduces
// security: an access token and a console session with an old sign-in answer
// 403 and change nothing, and a console session with a young sign-in changes
// the value.
func TestSettingsPatchFreshStep(t *testing.T) {
	t.Parallel()

	e := newKeyedSettingsEnv(t)

	// A change that makes the installation more secure needs no step.
	wantSettingsStatus(t, e.patch(t, tokenPatch(settingsURL, `"0"`, "key-more-secure", `{"test.enabled":true}`)), http.StatusOK)

	weaker := `{"test.enabled":false}`
	stale := session.DefaultTokenFreshness + time.Minute

	refused := map[string]keyedPatch{
		"an access token":               tokenPatch(settingsURL, `"1"`, "key-token-weaker", weaker),
		"a session with an old sign-in": sessionPatch(settingsURL, `"1"`, weaker, stale),
	}

	for name, p := range refused {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusForbidden)

		if !strings.Contains(r.body, settingsFreshCode) {
			t.Fatalf("%s: body = %s, want the code mfa.fresh-step-required", name, r.body)
		}
	}

	if got := e.read(t, settingsURL); got.header.Get("ETag") != `"1"` ||
		!isTrue(decodeSettings(t, got)[settingtest.KeyEnabled]["value"]) {
		t.Fatalf("a refused change moved the settings: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	fresh := e.patch(t, sessionPatch(settingsURL, `"1"`, weaker, 0))
	wantSettingsStatus(t, fresh, http.StatusOK)

	if fresh.header.Get("ETag") != `"2"` {
		t.Fatalf("ETag = %q, want \"2\"", fresh.header.Get("ETag"))
	}
}

// TestSettingsPatchIdempotency checks that a replay with the same key, the same
// body and the same If-Match answers the stored result and writes nothing.
func TestSettingsPatchIdempotency(t *testing.T) {
	t.Parallel()

	e := newKeyedSettingsEnv(t)
	p := tokenPatch(settingsURL, `"0"`, "key-one", `{"test.label":"blue"}`)

	first := e.patch(t, p)
	wantSettingsStatus(t, first, http.StatusOK)

	if first.header.Get(idempotent.ReplayedHeader) != "" || first.header.Get("ETag") != `"1"` {
		t.Fatalf("first answer headers = %v", first.header)
	}

	replay := e.patch(t, p)
	wantSettingsStatus(t, replay, http.StatusOK)

	if replay.header.Get(idempotent.ReplayedHeader) != "true" || replay.header.Get("ETag") != `"1"` ||
		replay.body != first.body {
		t.Fatalf("replay = %v %s, want the stored answer of the first request", replay.header, replay.body)
	}

	if n := e.changeEntries(t); n != 1 {
		t.Fatalf("change entries = %d, want 1: the replay wrote an entry", n)
	}

	// The namespace route keeps its own record.
	ns := tokenPatch(settingsNSURL, `"0"`, "key-one", `{"test.nsLimit":9}`)
	wantSettingsStatus(t, e.patch(t, ns), http.StatusOK)
	wantSettingsStatus(t, e.patch(t, ns), http.StatusOK)
}

// TestSettingsPatchRequiresKey checks that a PATCH with no Idempotency-Key
// answers 400 with the catalog code and changes nothing, for an access token
// and for a console session.
func TestSettingsPatchRequiresKey(t *testing.T) {
	t.Parallel()

	e := newKeyedSettingsEnv(t)

	noKeySession := sessionPatch(settingsURL, `"0"`, `{"test.enabled":true}`, 0)
	noKeySession.key = ""

	for name, p := range map[string]keyedPatch{
		"an access token":    tokenPatch(settingsURL, `"0"`, "", `{"test.enabled":true}`),
		"a console session":  noKeySession,
		"a namespace change": tokenPatch(settingsNSURL, `"0"`, "", `{"test.nsLimit":9}`),
	} {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusBadRequest)

		if !strings.Contains(r.body, idempotent.CodeRequired) {
			t.Fatalf("%s: body = %s, want the code %s", name, r.body, idempotent.CodeRequired)
		}
	}

	if got := e.read(t, settingsURL); got.header.Get("ETag") != `"0"` || e.changeEntries(t) != 0 {
		t.Fatalf("a refused change moved the settings: ETag %q, entries %d", got.header.Get("ETag"), e.changeEntries(t))
	}
}

// settingsIdempotency returns the idempotency middleware over st, which the
// settings routes need for each PATCH.
func settingsIdempotency(t *testing.T, st *sqlite.Store) *idempotent.Middleware {
	t.Helper()

	mw, err := idempotent.New(idempotent.Config{
		Store: st.Idempotency(idempotencytest.TestWrapper(t, 1)), Logger: slog.New(slog.DiscardHandler), Now: nil,
		MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	return mw
}
