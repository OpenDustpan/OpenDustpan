//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// SignInSubject returns the subject of the principal that a sign-in of the
// person idpSubject through the provider SignInProvider names. A test makes
// a person with this subject before it grants access, and PersonalToken
// signs that person in.
func SignInSubject(idpSubject string) string {
	return SignInProvider + ":" + idpSubject
}

// PersonalToken is a personal access token that its own person made.
type PersonalToken struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// PersonalToken signs in the person principalID through the test provider
// of an env from NewEnvWithSignIn, and makes a personal token called name
// with the session of that person, as the console does. The subject of the
// person must be SignInSubject of its provider subject. It fails the test
// on any error.
func (e *Env) PersonalToken(ctx context.Context, tb testing.TB, principalID, name string) PersonalToken {
	tb.Helper()

	idpSubject := e.signInSubjectOf(ctx, tb, principalID)
	browser := e.BrowserClient(tb)

	csrf := e.signInAs(ctx, tb, browser, idpSubject)

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		tb.Fatalf("encode token body: %v", err)
	}

	status, answer := e.browserDo(ctx, tb, browser, browserRequest{
		method: http.MethodPost, target: e.URL + "/api/server/caller/tokens", body: string(body),
		header: sessionHeader(csrf),
	})
	if status != http.StatusCreated {
		tb.Fatalf("make the personal token of %s with its session = %d: %s", principalID, status, answer)
	}

	var out PersonalToken
	if err := json.Unmarshal([]byte(answer), &out); err != nil || out.Secret == "" {
		tb.Fatalf("decode the personal token of %s: %v: %s", principalID, err, answer)
	}

	return out
}

// signInSubjectOf returns the provider subject of the person principalID.
func (e *Env) signInSubjectOf(ctx context.Context, tb testing.TB, principalID string) string {
	tb.Helper()

	status, body, err := e.ServerAPIStatus(ctx, http.MethodGet,
		"/api/server/principals/"+url.PathEscape(principalID), "")
	if err != nil || status != http.StatusOK {
		tb.Fatalf("read principal %s = %d, %v: %s", principalID, status, err, body)
	}

	var p struct {
		Subject string `json:"subject"`
	}

	if err := json.Unmarshal([]byte(body), &p); err != nil {
		tb.Fatalf("decode principal %s: %v", principalID, err)
	}

	idpSubject, ok := strings.CutPrefix(p.Subject, SignInProvider+":")
	if !ok || idpSubject == "" {
		tb.Fatalf("principal %s has the subject %q: make it with e2e.SignInSubject to sign it in",
			principalID, p.Subject)
	}

	return idpSubject
}

// signInAs runs a whole browser sign-in of the provider subject idpSubject
// in browser, and returns the CSRF value of the session. The test provider
// signs in one person at a time, so the sign-ins of one env take turns.
func (e *Env) signInAs(ctx context.Context, tb testing.TB, browser *http.Client, idpSubject string) string {
	tb.Helper()

	idp := e.IdentityProvider(tb)
	idpClient := *idp.Client()
	idpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	e.signIn.mu.Lock()
	defer e.signIn.mu.Unlock()

	idp.SetUser(oauthtest.User{
		Subject: idpSubject, Name: idpSubject, PreferredUsername: idpSubject, Email: idpSubject + "@example.com",
	})

	start := e.browserLocation(ctx, tb, browser, e.URL+"/api/server/sign-in/"+SignInProvider+"?return=/console&fresh=1",
		http.StatusFound)
	callback := e.browserLocation(ctx, tb, &idpClient, start, http.StatusFound)
	e.browserLocation(ctx, tb, browser, callback, http.StatusSeeOther)

	status, body := e.browserDo(ctx, tb, browser, browserRequest{
		method: http.MethodGet, target: e.URL + "/api/server/session", body: "", header: nil,
	})
	if status != http.StatusOK {
		tb.Fatalf("session of %s = %d: %s", idpSubject, status, body)
	}

	var sess struct {
		Subject   string `json:"subject"`
		CSRFToken string `json:"csrfToken"`
	}

	if err := json.Unmarshal([]byte(body), &sess); err != nil || sess.CSRFToken == "" ||
		sess.Subject != SignInSubject(idpSubject) {
		tb.Fatalf("session of %s = %v: %s", idpSubject, err, body)
	}

	return sess.CSRFToken
}

// browserRequest is one request of a browser sign-in.
type browserRequest struct {
	method, target, body string
	header               http.Header
}

// browserLocation sends a GET to target with client, checks the status
// want, and returns the Location header of the answer.
func (e *Env) browserLocation(ctx context.Context, tb testing.TB, client *http.Client, target string, want int) string {
	tb.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		tb.Fatalf("request GET %s: %v", target, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		tb.Fatalf("GET %s: %v", target, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		tb.Errorf("close body: %v", closeErr)
	}

	if readErr != nil || resp.StatusCode != want {
		tb.Fatalf("GET %s = %d, %v, want %d: %s", target, resp.StatusCode, readErr, want, body)
	}

	return resp.Header.Get("Location")
}

// browserDo sends r with client and returns the status and the body of the
// answer.
func (e *Env) browserDo(ctx context.Context, tb testing.TB, client *http.Client, r browserRequest) (int, string) {
	tb.Helper()

	req, err := http.NewRequestWithContext(ctx, r.method, r.target, strings.NewReader(r.body))
	if err != nil {
		tb.Fatalf("request %s %s: %v", r.method, r.target, err)
	}

	maps.Copy(req.Header, r.header)

	resp, err := client.Do(req)
	if err != nil {
		tb.Fatalf("%s %s: %v", r.method, r.target, err)
	}

	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		tb.Errorf("close body: %v", closeErr)
	}

	if err != nil {
		tb.Fatalf("read %s %s: %v", r.method, r.target, err)
	}

	return resp.StatusCode, string(body)
}

// SessionAPIStatus signs in the person principalID with a fresh sign-in
// through the test provider of an env from NewEnvWithSignIn, sends one
// installation API request with that console session and its CSRF value, and
// returns the status and the body of the answer. The subject of the person
// must be SignInSubject of its provider subject. It fails the test when the
// sign-in or the request cannot run.
func (e *Env) SessionAPIStatus(
	ctx context.Context, tb testing.TB, principalID string, req SessionRequest,
) (int, string) {
	tb.Helper()

	return e.FreshSession(ctx, tb, principalID)(req)
}

// FreshSession signs in the person principalID with a fresh sign-in, as
// SessionAPIStatus does, and returns a function that sends installation API
// requests with that console session. A step that needs the time of the
// request after the sign-in calls the function when it knows the time.
func (e *Env) FreshSession(ctx context.Context, tb testing.TB, principalID string) func(SessionRequest) (int, string) {
	tb.Helper()

	idpSubject := e.signInSubjectOf(ctx, tb, principalID)
	browser := e.BrowserClient(tb)
	csrf := e.signInAs(ctx, tb, browser, idpSubject)

	return func(req SessionRequest) (int, string) {
		tb.Helper()

		return e.browserDo(ctx, tb, browser, browserRequest{
			method: req.Method, target: e.URL + req.Path, body: req.Body,
			header: sessionHeader(csrf),
		})
	}
}

// SessionRequest is one installation API request of SessionAPIStatus.
type SessionRequest struct {
	Method, Path, Body string
}

// freshAdminSubject is the provider subject of the site administrator that
// FreshAdminAPIStatus signs in.
const freshAdminSubject = "fresh-admin@example.com"

// HasSignIn reports whether the env comes from NewEnvWithSignIn or one of its
// variants, so a step can make a change with a fresh console session.
func (e *Env) HasSignIn() bool {
	return e.signIn != nil
}

// FreshAdminAPIStatus sends one installation API request as a site
// administrator with a console session that has a fresh sign-in, and returns
// the status and the body of the answer. An access token never gives the
// fresh authentication that a token change, a sign-in setting or a gate
// write needs, so a step that makes such a change calls this method. The env
// must come from NewEnvWithSignIn. The first call makes the administrator
// in the store. It fails the test when the sign-in or the
// request cannot run.
func (e *Env) FreshAdminAPIStatus(ctx context.Context, tb testing.TB, req SessionRequest) (int, string) {
	tb.Helper()

	if e.signIn == nil {
		tb.Fatal("FreshAdminAPIStatus needs an env from NewEnvWithSignIn")
	}

	return e.FreshSession(ctx, tb, e.freshAdmin(ctx, tb))(req)
}

// FreshAdminMergePatch sends one merge patch of body to path, with the
// If-Match revision ifMatch, as a site administrator with a console session
// that has a fresh sign-in. It returns the status and the body of the answer.
// A step uses it for a change of a setting that reduces security, which needs
// a fresh authentication. The env must come from NewEnvWithSignIn.
func (e *Env) FreshAdminMergePatch(ctx context.Context, tb testing.TB, path, ifMatch, body string) (int, string) {
	tb.Helper()

	if e.signIn == nil {
		tb.Fatal("FreshAdminMergePatch needs an env from NewEnvWithSignIn")
	}

	browser := e.BrowserClient(tb)
	csrf := e.signInAs(ctx, tb, browser, e.signInSubjectOf(ctx, tb, e.freshAdmin(ctx, tb)))

	header := sessionHeader(csrf)
	header.Set("Content-Type", "application/merge-patch+json")
	header.Set("If-Match", ifMatch)

	return e.browserDo(ctx, tb, browser, browserRequest{
		method: http.MethodPatch, target: e.URL + path, body: body, header: header,
	})
}

// FreshAdminAPIStatusFrom is FreshAdminAPIStatus for a request that leaves
// from the local address source. The sign-in itself leaves from the default
// address. A step uses it for a route that an IP allowlist limits.
func (e *Env) FreshAdminAPIStatusFrom(
	ctx context.Context, tb testing.TB, source string, req SessionRequest,
) (int, string) {
	tb.Helper()

	if e.signIn == nil {
		tb.Fatal("FreshAdminAPIStatusFrom needs an env from NewEnvWithSignIn")
	}

	browser := e.BrowserClient(tb)
	csrf := e.signInAs(ctx, tb, browser, e.signInSubjectOf(ctx, tb, e.freshAdmin(ctx, tb)))

	base, ok := browser.Transport.(*http.Transport)
	if !ok {
		tb.Fatal("the browser client has no HTTP transport")
	}

	transport := base.Clone()
	//nolint:exhaustruct_v5 // The dialer sets only the source address; the rest stays zero.
	transport.DialContext = (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).DialContext
	browser.Transport = transport

	defer transport.CloseIdleConnections()

	return e.browserDo(ctx, tb, browser, browserRequest{
		method: req.Method, target: e.URL + req.Path, body: req.Body,
		header: sessionHeader(csrf),
	})
}

// FreshAdminSession signs in the site administrator of FreshAdminAPIStatus
// and returns a function that sends installation API requests with the
// console session, as FreshSession does.
func (e *Env) FreshAdminSession(ctx context.Context, tb testing.TB) func(SessionRequest) (int, string) {
	tb.Helper()

	if e.signIn == nil {
		tb.Fatal("FreshAdminSession needs an env from NewEnvWithSignIn")
	}

	return e.FreshSession(ctx, tb, e.freshAdmin(ctx, tb))
}

// freshAdminID is the ID of the person that freshAdmin makes.
const freshAdminID = "prn-e2e-fresh-admin"

// freshAdminActor is the actor of the writes that make the fresh administrator.
const freshAdminActor = "e2e-fresh-admin"

// freshAdmin returns the ID of the site administrator person of
// FreshAdminAPIStatus and makes it on the first call. The person and its
// grant go straight into the store: an access token never gives the fresh
// authentication that a grant needs, so no route can make the first
// administrator. The binary server owns its database, so the write runs while
// the binary is stopped. The in-process server writes through its own store.
func (e *Env) freshAdmin(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	e.signIn.adminMu.Lock()
	defer e.signIn.adminMu.Unlock()

	if e.signIn.adminID != "" {
		return e.signIn.adminID
	}

	write := func(st namespace.Store) error { return seedFreshAdmin(ctx, st) }

	if e.binPath == "" {
		if err := write(e.inprocess); err != nil {
			tb.Fatalf("make the fresh administrator: %v", err)
		}
	} else {
		e.StopServer(ctx, tb)
		e.writeStopped(ctx, tb, write)
		e.StartServer(ctx, tb)
	}

	e.signIn.adminID = freshAdminID

	return freshAdminID
}

// errNoAccessRows reports a store that keeps no principals or grants.
var errNoAccessRows = errors.New("the store keeps no principals or grants")

// seedFreshAdmin makes the person of freshAdmin and gives it the site
// administrator role on the installation.
func seedFreshAdmin(ctx context.Context, st namespace.Store) error {
	rows, ok := st.(interface {
		principal.Store
		access.Store
	})
	if !ok {
		return errNoAccessRows
	}

	at := time.Now().UTC().Truncate(time.Millisecond)

	person := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: freshAdminID,
		Subject: SignInSubject(freshAdminSubject), DisplayName: freshAdminSubject, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if err := rows.CreatePrincipal(ctx, person, freshAdminEntry(at, principal.Ref(person.ID))); err != nil {
		return fmt.Errorf("create the person: %w", err)
	}

	obj := identity.ServerObject()
	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-e2e-fresh-admin", Role: access.RoleSiteAdministrator,
		CreatedBy: freshAdminActor, Subject: access.Subject{Kind: access.SubjectPrincipal, ID: person.ID},
	}

	// The grant is on the installation, so the fence is the zero fence.
	err := rows.CreateGrant(
		ctx,
		grant,
		namespace.Fence{Namespace: "", FailoverVersion: 0},
		freshAdminEntry(at, access.GrantRef(grant)),
		nil,
	)
	if err != nil {
		return fmt.Errorf("grant the site administrator role: %w", err)
	}

	return nil
}

// freshAdminEntry returns the audit entry of a write of seedFreshAdmin to the
// record object at the time at.
func freshAdminEntry(at time.Time, object string) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     audit.ServerScope,
		Subject:       freshAdminActor,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-principals",
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     freshAdminActor,
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// sessionHeader returns the headers of an installation API request from a
// console session with the CSRF value csrf.
func sessionHeader(csrf string) http.Header {
	return http.Header{
		"X-Dustpan-Csrf": {csrf}, "Content-Type": {"application/json"}, "Sec-Fetch-Site": {"same-origin"},
	}
}
