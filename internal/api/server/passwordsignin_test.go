package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// Values of the LDAP sign-in tests of the handler.
const (
	ldapRouteID       = "corp-ldap"
	ldapRoutePath     = "/api/dustpan/v1/sign-in/providers/" + ldapRouteID + "/password"
	ldapRoutePassword = "right-password-of-alice"
	ldapRouteUUID     = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// ldapRouteEnv is a sign-in env with the LDAP provider corp-ldap over a test
// directory, over TLS.
type ldapRouteEnv struct {
	providerEnv

	dir  *ldaptest.Server
	logs *bytes.Buffer
}

// newLDAPRouteEnv starts a test directory with the person alice and builds a
// sign-in env with the LDAP provider of that directory.
func newLDAPRouteEnv(t *testing.T) ldapRouteEnv {
	t.Helper()

	dir := ldaptest.Start(t)
	dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"entryUUID": {ldapRouteUUID}}, UserName: "alice", Password: ldapRoutePassword,
	})

	logs := &bytes.Buffer{}

	m, err := ldapauth.NewMethod(ldapauth.Config{
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		AllowPlain: func(context.Context) (bool, error) { return false, nil },
		Now:        nil, Logger: slog.New(slog.NewTextHandler(logs, nil)), RootCAs: dir.CertPool(),
		ProviderID: ldapRouteID, URL: dir.LDAPSURL(), BindDN: ldaptest.ServiceDN, BaseDN: ldaptest.BaseDN,
		UserFilter: "(&(objectClass=person)(uid={user}))", UserNameAttribute: "uid", EmailAttribute: "",
		NameAttribute: "", BindPassword: session.NewPassword(dir.ServicePassword()),
		Subject:  ldapauth.Subject{Kind: ldapauth.SubjectEntryUUID, Attribute: ""},
		Disabled: ldapauth.Disabled{Attribute: "", Values: nil, Mask: 0},
		Groups: ldapauth.Groups{
			Source: ldapauth.GroupsOff, BaseDN: "", Filter: "", MemberAttribute: "", MemberOfAttribute: "",
			NameAttribute: "",
		},
		Egress:         egress.Rules{Allow: nil, LoopbackForTests: true},
		ConnectTimeout: 0, OperationTimeout: 0, GroupTimeout: 0, StartTLS: false,
	})
	if err != nil {
		t.Fatalf("NewMethod() = %v", err)
	}

	e := newProviderEnvWith(t, []apiserver.PasswordProvider{{
		Method: m, ID: ldapRouteID, DisplayName: "Corp directory", AutoCreate: true,
		LDAP: apiserver.LDAPSettings{
			AllowedRanges: nil,
			URL:           dir.LDAPSURL(), BindDN: ldaptest.ServiceDN, BaseDN: ldaptest.BaseDN,
			UserFilter: "(&(objectClass=person)(uid={user}))", UserNameAttribute: "uid", EmailAttribute: "",
			NameAttribute: "", Subject: "entryUUID", DisabledAttribute: "", Groups: "off", GroupBaseDN: "",
			GroupFilter: "", GroupMemberAttribute: "", GroupMemberOfAttribute: "", GroupNameAttribute: "",
			StartTLS: false,
		},
	}})

	return ldapRouteEnv{providerEnv: e, dir: dir, logs: logs}
}

// post sends a password sign-in with body to path, with no credential.
func (e ldapRouteEnv) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return e.postWith(t, path, body, map[string]string{"Content-Type": "application/json"})
}

// postWith sends a password sign-in with body and headers to path, with no
// credential.
func (e ldapRouteEnv) postWith(t *testing.T, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

// signInBody returns the body of a password sign-in.
func signInBody(userName, password string) string {
	b, err := json.Marshal(map[string]string{"userName": userName, "password": password})
	if err != nil {
		panic(err)
	}

	return string(b)
}

// TestLDAPRouteSignsAPersonIn proves the success path: the right password
// over TLS gives 204, a console session cookie and a session of the subject
// of the entryUUID of the person.
func TestLDAPRouteSignsAPersonIn(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)

	rec := e.post(t, ldapRoutePath, signInBody("alice", ldapRoutePassword))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sign-in = %d %s, want 204", rec.Code, rec.Body.String())
	}

	cookie := cookieOf(rec, apiserver.SessionCookie)
	checkBrowserCookie(t, apiserver.SessionCookie, cookie, http.SameSiteStrictMode)

	status, body := e.readSession(t, cookie)
	if status != http.StatusOK || body.Subject != ldapRouteID+":"+ldapRouteUUID || body.Provider != ldapRouteID {
		t.Fatalf("session = %d %+v, want the subject %s:%s", status, body, ldapRouteID, ldapRouteUUID)
	}

	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", rec.Header().Get("Referrer-Policy"))
	}
}

// ldapRefusal is one hostile request to the password route and its answer.
type ldapRefusal struct {
	name, path, body, code string
	status                 int
	// spends is the count of units of the password budget that the request
	// spends.
	spends int
}

// ldapRefusals are the hostile requests to the password route. Each refusal
// of a person, a name or a provider gets the same answer.
func ldapRefusals() []ldapRefusal {
	const failed = "sign-in.failed"

	otherPath := "/api/dustpan/v1/sign-in/providers/"

	return []ldapRefusal{
		{
			name: "wrong password", path: ldapRoutePath, body: signInBody("alice", "wrong-password"),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name: "unknown user", path: ldapRoutePath, body: signInBody("nobody", ldapRoutePassword),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name: "empty password", path: ldapRoutePath, body: signInBody("alice", ""),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name: "filter characters in the name", path: ldapRoutePath, body: signInBody("*)(uid=*", ldapRoutePassword),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name: "CR and LF in the name", path: ldapRoutePath, body: signInBody("alice\r\nadmin", ldapRoutePassword),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name: "unknown provider", path: otherPath + "nope/password", body: signInBody("alice", ldapRoutePassword),
			code: failed, status: http.StatusUnauthorized, spends: 1,
		},
		{
			name:   "ID of an OIDC provider",
			path:   otherPath + "corp/password",
			body:   signInBody("alice", ldapRoutePassword),
			code:   failed,
			status: http.StatusUnauthorized,
			spends: 1,
		},
		{
			name: "unknown field", path: ldapRoutePath, body: `{"userName":"alice","password":"x","admin":true}`,
			code: "request.body-invalid", status: http.StatusBadRequest, spends: 1,
		},
		{
			name: "oversized body", path: ldapRoutePath,
			body: signInBody("alice", strings.Repeat("a", 9*1024)), code: "request.body-invalid",
			status: http.StatusBadRequest, spends: 1,
		},
	}
}

// TestLDAPRouteRefusals sends each hostile request to the password route.
// Each one spends the password budget, sets no cookie, and gets an answer
// that holds no password and no text of the directory. The refusals of a
// person, a name and a provider have the same body. No log line of the
// method holds a password.
func TestLDAPRouteRefusals(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)

	var failedBody string

	for _, tc := range ldapRefusals() {
		rec := checkLDAPRefusal(t, e, &tc)

		if rec.Code != http.StatusUnauthorized {
			continue
		}

		if failedBody == "" {
			failedBody = rec.Body.String()
		}

		if rec.Body.String() != failedBody {
			t.Fatalf("%s: answer %q differs from %q", tc.name, rec.Body.String(), failedBody)
		}
	}

	for _, secret := range []string{ldapRoutePassword, "wrong-password", e.dir.ServicePassword()} {
		if strings.Contains(e.logs.String(), secret) {
			t.Fatalf("a log line of the method holds a password: %s", e.logs.String())
		}
	}
}

// checkLDAPRefusal sends the request of tc and fails when its answer, its
// spend of the password budget or its content is not what tc wants.
func checkLDAPRefusal(t *testing.T, e ldapRouteEnv, tc *ldapRefusal) *httptest.ResponseRecorder {
	t.Helper()

	before := e.limiter.count()
	rec := e.post(t, tc.path, tc.body)

	if rec.Code != tc.status || errorCodeOf(t, rec.Body.String()) != tc.code {
		t.Fatalf("%s: answer = %d %s, want %d %s", tc.name, rec.Code, rec.Body.String(), tc.status, tc.code)
	}

	if got := e.limiter.count() - before; got != tc.spends {
		t.Fatalf("%s: the request spent %d units of the budget, want %d", tc.name, got, tc.spends)
	}

	checkLDAPAnswerIsClean(t, e, tc.name, rec)

	return rec
}

// checkLDAPAnswerIsClean fails when rec sets a cookie, or holds a password,
// the text of the directory or a DN.
func checkLDAPAnswerIsClean(t *testing.T, e ldapRouteEnv, name string, rec *httptest.ResponseRecorder) {
	t.Helper()

	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("%s: the answer sets a cookie", name)
	}

	for _, text := range []string{ldapRoutePassword, e.dir.ServicePassword(), ldaptest.DiagnosticPrefix, "dc=example"} {
		if strings.Contains(rec.Body.String(), text) {
			t.Fatalf("%s: the answer %q holds %q", name, rec.Body.String(), text)
		}
	}
}

// TestLDAPRouteFullBudgetSendsNoBind proves that a full password budget
// refuses the sign-in with 429 before the handler reads the body, so the
// directory gets no request at all.
func TestLDAPRouteFullBudgetSendsNoBind(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)

	e.limiter.mu.Lock()
	e.limiter.refuse = true
	e.limiter.mu.Unlock()

	rec := e.post(t, ldapRoutePath, signInBody("alice", ldapRoutePassword))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("sign-in = %d %s, want 429", rec.Code, rec.Body.String())
	}

	if n := e.dir.Connections(); n != 0 {
		t.Fatalf("the directory got %d connections, want 0", n)
	}
}

// TestLDAPRouteDirectoryDown proves that a directory that does not answer
// gives 503, spends no unit of the password budget and gives no cookie.
func TestLDAPRouteDirectoryDown(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)
	e.dir.Stop()

	before := e.limiter.count()

	rec := e.post(t, ldapRoutePath, signInBody("alice", ldapRoutePassword))
	if rec.Code != http.StatusServiceUnavailable || errorCodeOf(t, rec.Body.String()) != "sign-in.unavailable" {
		t.Fatalf("sign-in = %d %s, want 503 sign-in.unavailable", rec.Code, rec.Body.String())
	}

	if e.limiter.count() != before {
		t.Fatalf("the budget changed from %d to %d, want no change", before, e.limiter.count())
	}

	checkLDAPAnswerIsClean(t, e, "directory down", rec)
}

// ldapListDoc is the list of the sign-in providers, in the LDAP tests.
type ldapListDoc struct {
	Providers []ldapListProvider `json:"providers"`
}

// ldapListProvider is a provider of ldapListDoc.
type ldapListProvider struct {
	LDAP   *ldapListSettings `json:"ldap"`
	ID     string            `json:"id"`
	Source string            `json:"source"`
	Mode   string            `json:"mode"`
}

// ldapListSettings are the LDAP settings of a provider of ldapListDoc.
type ldapListSettings struct {
	AllowedRanges   []string `json:"allowedRanges"`
	BindDN          string   `json:"bindDn"`
	Subject         string   `json:"subject"`
	Groups          string   `json:"groups"`
	BindPasswordSet bool     `json:"bindPasswordSet"`
}

// checkLDAPListEntry fails when p is not the static LDAP provider with its
// effective settings.
func checkLDAPListEntry(t *testing.T, p *ldapListProvider) {
	t.Helper()

	if p.Source != "static" || p.Mode != "ldap" || p.LDAP == nil || p.LDAP.BindDN != ldaptest.ServiceDN ||
		p.LDAP.Subject != "entryUUID" || p.LDAP.Groups != "off" || !p.LDAP.BindPasswordSet ||
		p.LDAP.AllowedRanges == nil || len(p.LDAP.AllowedRanges) != 0 {
		t.Fatalf("provider = %+v, want static ldap with its effective settings", p)
	}
}

// TestLDAPProviderListShowsNoPassword proves that the list of the sign-in
// providers shows the LDAP provider with its effective settings and the
// source static, and never the bind password.
func TestLDAPProviderListShowsNoPassword(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)

	rec := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	var list ldapListDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	found := false

	for i := range list.Providers {
		p := &list.Providers[i]
		if p.ID != ldapRouteID {
			continue
		}

		found = true

		checkLDAPListEntry(t, p)
	}

	if !found {
		t.Fatalf("list %s has no provider %s", rec.Body.String(), ldapRouteID)
	}

	if strings.Contains(rec.Body.String(), e.dir.ServicePassword()) {
		t.Fatal("the list holds the bind password")
	}
}

// TestLDAPRouteRefusesAnotherSite proves the check against login CSRF at the
// password route of a provider: a request that another site can send gets the
// answer of a failed sign-in, no cookie, no spend of the budget and no
// connection to the directory, also with the right password. A request of the
// console signs in.
func TestLDAPRouteRefusesAnotherSite(t *testing.T) {
	t.Parallel()

	const evil = "https://evil.example"

	body := signInBody("alice", ldapRoutePassword)

	cases := []struct {
		headers map[string]string
		name    string
	}{
		{name: "a text/plain request of another site", headers: map[string]string{
			"Content-Type": "text/plain", "Origin": evil, "Sec-Fetch-Site": "cross-site",
		}},
		{name: "a URL-encoded form", headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
		{name: "a multipart form", headers: map[string]string{"Content-Type": "multipart/form-data; boundary=x"}},
		{name: "no media type", headers: map[string]string{}},
		{
			name:    "JSON from a foreign origin",
			headers: map[string]string{"Content-Type": "application/json", "Origin": evil},
		},
		{
			name:    "JSON from the null origin",
			headers: map[string]string{"Content-Type": "application/json", "Origin": "null"},
		},
		{name: "JSON marked cross-site", headers: map[string]string{
			"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site",
		}},
	}

	e := newLDAPRouteEnv(t)

	for _, tc := range cases {
		before := e.limiter.count()

		rec := e.postWith(t, ldapRoutePath, body, tc.headers)
		if rec.Code != http.StatusUnauthorized || errorCodeOf(t, rec.Body.String()) != "sign-in.failed" {
			t.Fatalf("%s: answer = %d %s, want 401 sign-in.failed", tc.name, rec.Code, rec.Body.String())
		}

		if e.limiter.count() != before {
			t.Fatalf("%s: the refusal spent the budget", tc.name)
		}

		checkLDAPAnswerIsClean(t, e, tc.name, rec)
	}

	if n := e.dir.Connections(); n != 0 {
		t.Fatalf("the directory got %d connections from requests of another site, want 0", n)
	}

	rec := e.postWith(t, ldapRoutePath, body, map[string]string{
		"Content-Type": "application/json", "Origin": signInBase, "Sec-Fetch-Site": "same-origin",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sign-in from the console = %d %s, want 204", rec.Code, rec.Body.String())
	}
}

// TestLDAPRouteOfAnotherSiteHasABoundedAudit proves the bound of the audit of
// the check against login CSRF on the password route of a provider. 50
// refusals with 50 Origins and 50 source addresses write one denied entry for
// the configured provider, and 50 refusals with 50 other provider names write
// one denied entry for all of them. The caller cannot open a new key of the
// limiter with the path, the Origin or the address.
func TestLDAPRouteOfAnotherSiteHasABoundedAudit(t *testing.T) {
	t.Parallel()

	e := newLDAPRouteEnv(t)
	body := signInBody("alice", ldapRoutePassword)

	for i := range 50 {
		for _, path := range []string{ldapRoutePath, fmt.Sprintf("/api/dustpan/v1/sign-in/providers/p%d/password", i)} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", fmt.Sprintf("https://evil-%d.example", i))
			req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", i+1)

			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("refusal %d at %s = %d %s, want 401", i, path, rec.Code, rec.Body.String())
			}
		}
	}

	subjects := map[string]int{}

	entries := e.serverEntries(t)
	for i := range entries {
		en := &entries[i]
		if en.Outcome == audit.OutcomeDenied && en.Action == session.ActionSignIn &&
			strings.Contains(en.Reason, "another site") {
			subjects[en.Subject]++
		}
	}

	want := map[string]int{"sign-in:" + ldapRouteID: 1, "sign-in:password": 1}
	if !maps.Equal(subjects, want) {
		t.Fatalf("denied sign-in entries by subject = %v, want %v", subjects, want)
	}

	if n := e.dir.Connections(); n != 0 {
		t.Fatalf("the directory got %d connections, want 0", n)
	}
}

// TestPasswordRoutesAnswerTheSameRefusal proves that a wrong password on the
// route of a local user and on the route of a provider gets the same status
// and the same text, so that a person sees one answer for every password
// sign-in. Each route keeps its own error code.
func TestPasswordRoutesAnswerTheSameRefusal(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	createRouteLocalUser(t, f)

	local := publicPost(t, f, "/api/dustpan/v1/sign-in/password", `{"userName":"carol","password":"wrong-password"}`)

	e := newLDAPRouteEnv(t)
	ldap := e.post(t, ldapRoutePath, signInBody("alice", "wrong-password"))

	type answer struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
		Code      int    `json:"code"`
	}

	var a, b answer

	if err := json.Unmarshal(local.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode the local answer %q: %v", local.Body.String(), err)
	}

	if err := json.Unmarshal(ldap.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode the LDAP answer %q: %v", ldap.Body.String(), err)
	}

	const text = "The user name or the password is wrong."

	if local.Code != http.StatusUnauthorized || ldap.Code != http.StatusUnauthorized ||
		a.Message != text || b.Message != text || a.Code != b.Code {
		t.Fatalf("answers = %d %+v and %d %+v, want 401 with %q on both routes", local.Code, a, ldap.Code, b, text)
	}

	if a.ErrorCode != "local-user.sign-in-failed" || b.ErrorCode != "sign-in.failed" {
		t.Fatalf("error codes = %q and %q, want the code of each route", a.ErrorCode, b.ErrorCode)
	}
}
