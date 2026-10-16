//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
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
		header: http.Header{"X-Dustpan-Csrf": {csrf}, "Content-Type": {"application/json"}},
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

	start := e.browserLocation(ctx, tb, browser, e.URL+"/api/server/sign-in/"+SignInProvider+"?return=/console",
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
