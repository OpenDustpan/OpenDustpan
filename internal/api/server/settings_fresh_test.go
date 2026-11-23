package server_test

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
	"opendustpan.org/opendustpan/pkg/tempadmin"
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

	return newKeyedSettingsEnvWith(t, settingtest.Registry())
}

// newKeyedSettingsEnvWith is newKeyedSettingsEnv over the registry reg.
func newKeyedSettingsEnvWith(t *testing.T, reg *setting.Registry) keyedSettingsEnv {
	t.Helper()

	return newKeyedSettingsEnvAs(t, reg, allowAll{})
}

// newKeyedSettingsEnvAs is newKeyedSettingsEnvWith with the authorizer authz.
func newKeyedSettingsEnvAs(t *testing.T, reg *setting.Registry, authz identity.Authorizer) keyedSettingsEnv {
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

	svc := setting.NewService(st.SettingStore(), reg, nil, st, nil)
	cfg := settingsConfig(t, st, namespaces, authz, svc)

	signIn, sessions := signInOf(t, st)
	cfg.SignIn = signIn
	cfg.Principals = seedRoutePrincipals(t, st)

	mw := settingsIdempotency(t, st)

	cfg.Idempotency = mw

	// The policy reads the setting freshAuthEnabled from the same service, as
	// the server does. A registry with no such setting fails closed: the step
	// is required.
	policy := freshstep.NewPolicy(func(ctx context.Context) (bool, error) {
		item, readErr := svc.Effective(ctx, setting.InstallationScope(), setting.SettingFreshAuthEnabled)
		if readErr != nil {
			return false, readErr
		}

		return item.Value == "true", nil
	})

	h, err := newCheckedHandler(t, cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	h.SetFreshPolicy(policy)

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

// sessionKey returns a new Idempotency-Key for a PATCH of a console session.
func sessionKey() string { return "key-session-" + rand.Text() }

// sessionPatch returns a PATCH from a console session whose sign-in is age old.
func sessionPatch(ifMatch, body string, age time.Duration) keyedPatch {
	return keyedPatch{path: settingsURL, ifMatch: ifMatch, key: sessionKey(), body: body, session: true, age: age}
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
func (e keyedSettingsEnv) read(t *testing.T) response {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, settingsURL, nil)
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
	wantSettingsStatus(
		t,
		e.patch(t, tokenPatch(settingsURL, `"0"`, "key-more-secure", `{"test.enabled":true}`)),
		http.StatusOK,
	)

	weaker := `{"test.enabled":false}`
	stale := session.DefaultTokenFreshness + time.Minute

	refused := map[string]keyedPatch{
		"an access token":               tokenPatch(settingsURL, `"1"`, "key-token-weaker", weaker),
		"a session with an old sign-in": sessionPatch(`"1"`, weaker, stale),
	}

	for name, p := range refused {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusForbidden)

		if !strings.Contains(r.body, settingsFreshCode) {
			t.Fatalf("%s: body = %s, want the code mfa.fresh-step-required", name, r.body)
		}
	}

	if got := e.read(t); got.header.Get("ETag") != `"1"` ||
		!isTrue(decodeSettings(t, got)[settingtest.KeyEnabled]["value"]) {
		t.Fatalf("a refused change moved the settings: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	fresh := e.patch(t, sessionPatch(`"1"`, weaker, 0))
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

	noKeySession := sessionPatch(`"0"`, `{"test.enabled":true}`, 0)
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

	if got := e.read(t); got.header.Get("ETag") != `"0"` || e.changeEntries(t) != 0 {
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

// TestSettingsPatchSessionLimitFreshStep checks that a longer session limit of
// a temporary administrator needs the fresh step: an access token that moves
// the limit to 24 hours gets 403 and changes nothing, a shorter limit passes,
// and a console session with a young sign-in passes.
func TestSettingsPatchSessionLimitFreshStep(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry(append(settingtest.Declarations(), tempadmin.SessionLimitSettingDeclarations()...))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	e := newKeyedSettingsEnvWith(t, reg)

	// The stored limit is the default of 1 hour. A shorter limit is stronger.
	shorter := e.patch(t, tokenPatch(settingsURL, `"0"`, "key-limit-shorter", `{"tempadmin.session-limit":"30m"}`))
	wantSettingsStatus(t, shorter, http.StatusOK)

	longer := `{"tempadmin.session-limit":"24h"}`

	refused := map[string]keyedPatch{
		"an access token":               tokenPatch(settingsURL, `"1"`, "key-limit-token", longer),
		"a session with an old sign-in": sessionPatch(`"1"`, longer, session.DefaultTokenFreshness+time.Minute),
	}

	for name, p := range refused {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusForbidden)

		if !strings.Contains(r.body, settingsFreshCode) {
			t.Fatalf("%s: body = %s, want the code mfa.fresh-step-required", name, r.body)
		}
	}

	if got := e.read(t); got.header.Get("ETag") != `"1"` {
		t.Fatalf("a refused change moved the settings: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	wantSettingsStatus(t, e.patch(t, sessionPatch(`"1"`, longer, 0)), http.StatusOK)
}

// freshAuthChange returns the change entry of the key freshAuthEnabled that
// holds old and next, or fails the test.
func (e keyedSettingsEnv) freshAuthChange(t *testing.T, old, next string) {
	t.Helper()

	entries := serverEntries(t, e.st)
	for i := range entries {
		for _, c := range entries[i].Changes {
			if c.Key == setting.SettingFreshAuthEnabled && c.Old != nil && *c.Old == old && c.New == next {
				return
			}
		}
	}

	t.Fatalf("no audit entry holds the change of %s from %s to %s: %+v",
		setting.SettingFreshAuthEnabled, old, next, entries)
}

// TestFreshAuthEnabledChangeNeedsStep checks the change of the setting
// freshAuthEnabled. The change from on to off reduces security, so an access
// token and a session with an old sign-in get 403 mfa.fresh-step-required and
// change nothing, and a session with a young sign-in changes the value. The
// change from off to on needs no step. Each change writes one audit entry with
// the old and the new value. With the setting off, a weaker change of another
// setting needs no step.
func TestFreshAuthEnabledChangeNeedsStep(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry(append(settingtest.Declarations(), setting.FreshAuthEnabledDeclaration()))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	e := newKeyedSettingsEnvWith(t, reg)

	// The setting is off by default: a weaker change of another setting needs no
	// step and writes no step of its own.
	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"0"`, "key-on-first", `{"test.enabled":true}`)), http.StatusOK,
	)
	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"1"`, "key-off-weaker", `{"test.enabled":false}`)), http.StatusOK,
	)

	// Off to on needs no step.
	turnOn := e.patch(t, tokenPatch(settingsURL, `"2"`, "key-turn-on", `{"freshAuthEnabled":true}`))
	wantSettingsStatus(t, turnOn, http.StatusOK)
	e.freshAuthChange(t, "false", "true")

	// On to off needs a step.
	turnOff := `{"freshAuthEnabled":false}`
	stale := session.DefaultTokenFreshness + time.Minute

	refused := map[string]keyedPatch{
		"an access token":               tokenPatch(settingsURL, `"3"`, "key-turn-off-token", turnOff),
		"a session with an old sign-in": sessionPatch(`"3"`, turnOff, stale),
	}

	for name, p := range refused {
		r := e.patch(t, p)
		wantSettingsStatus(t, r, http.StatusForbidden)

		if !strings.Contains(r.body, settingsFreshCode) {
			t.Fatalf("%s: body = %s, want the code mfa.fresh-step-required", name, r.body)
		}
	}

	got := e.read(t)
	if got.header.Get("ETag") != `"3"` || !isTrue(decodeSettings(t, got)[setting.SettingFreshAuthEnabled]["value"]) {
		t.Fatalf("a refused change moved the setting: ETag %q, body %s", got.header.Get("ETag"), got.body)
	}

	wantSettingsStatus(t, e.patch(t, sessionPatch(`"3"`, turnOff, 0)), http.StatusOK)
	e.freshAuthChange(t, "true", "false")

	// With the setting off again, a weaker change needs no step.
	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"4"`, "key-on-again", `{"test.enabled":true}`)), http.StatusOK,
	)
	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"5"`, "key-weaker-again", `{"test.enabled":false}`)), http.StatusOK,
	)
}

// TestFreshAuthEnabledOnKeepsTheWeakerChangeStep checks that with the setting
// on, a weaker change of another setting from an access token still needs a
// step: the guard of the settings handler asks the policy and does not skip.
func TestFreshAuthEnabledOnKeepsTheWeakerChangeStep(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry(append(settingtest.Declarations(), setting.FreshAuthEnabledDeclaration()))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	e := newKeyedSettingsEnvWith(t, reg)

	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"0"`, "key-on", `{"freshAuthEnabled":true,"test.enabled":true}`)),
		http.StatusOK,
	)

	r := e.patch(t, tokenPatch(settingsURL, `"1"`, "key-weaker", `{"test.enabled":false}`))
	wantSettingsStatus(t, r, http.StatusForbidden)

	if !strings.Contains(r.body, settingsFreshCode) {
		t.Fatalf("body = %s, want the code mfa.fresh-step-required", r.body)
	}
}
