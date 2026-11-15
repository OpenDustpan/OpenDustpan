package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
)

// callerCall is one request to a route of the caller: the subject of its
// access token, or no token when subject is empty.
type callerCall struct {
	method, target, subject, body string
	headers                       map[string]string
}

// serveCaller sends one request to the handler of the fixture with the
// headers, and returns the answer.
func serveCaller(t *testing.T, h http.Handler, c callerCall) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	if c.subject != "" {
		req.Header.Set("Authorization", "Bearer "+c.subject)
	}

	for name, value := range c.headers {
		req.Header.Set(name, value)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestCallerRoutesMoved proves that the routes of the caller answer under the
// me prefix with an access token of the caller, that the old paths answer 404
// from the router, and that a request with no token answers 401. A create and
// a rotate need a console session with a recent sign-in, so an access token
// answers 403 there.
func TestCallerRoutesMoved(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/dustpan/v1/me/access?object=acme", http.StatusOK},
		{http.MethodGet, "/api/dustpan/v1/me/access/objects?type=unit", http.StatusOK},
		{http.MethodGet, "/api/dustpan/v1/me/grants", http.StatusOK},
		{http.MethodGet, "/api/dustpan/v1/me/tokens", http.StatusOK},
		{http.MethodPost, "/api/dustpan/v1/me/tokens", http.StatusForbidden},
		{http.MethodPost, "/api/dustpan/v1/me/tokens/tok-1/rotate", http.StatusForbidden},
		{http.MethodDelete, "/api/dustpan/v1/me/tokens/tok-missing", http.StatusNotFound},
	} {
		body := ""
		if c.method == http.MethodPost {
			body = `{"name":"laptop"}`
		}

		rec := serveCaller(t, f.handler, callerCall{c.method, c.path, "bob", body, nil})
		if rec.Code != c.want {
			t.Errorf("%s %s = %d %s, want %d", c.method, c.path, rec.Code, rec.Body.String(), c.want)
		}

		rec = serveCaller(t, f.handler, callerCall{c.method, c.path, "", body, nil})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no token = %d %s, want 401", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/server/caller/access?object=acme"},
		{http.MethodGet, "/api/server/caller/grants"},
		{http.MethodGet, "/api/server/caller/tokens"},
		{http.MethodPost, "/api/server/caller/tokens"},
		{http.MethodDelete, "/api/server/caller/tokens/tok-1"},
		{http.MethodPost, "/api/server/caller/tokens/tok-1/rotate"},
	} {
		rec := serveCaller(t, f.handler, callerCall{c.method, c.path, "bob", "", nil})
		if rec.Code != http.StatusNotFound {
			t.Errorf("old path %s %s = %d %s, want 404", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// TestCallerPagingNames proves that the caller lists take pageSize and
// pageToken and refuse cursor, limit, a bad pageToken and a pageSize above the
// maximum with 400 and a stable code.
func TestCallerPagingNames(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for _, c := range []struct{ path, code string }{
		{"/api/dustpan/v1/me/tokens?cursor=x", "request.invalid-query"},
		{"/api/dustpan/v1/me/tokens?limit=1", "request.invalid-query"},
		{"/api/dustpan/v1/me/tokens?pageToken=%21", "request.page-token-invalid"},
		{"/api/dustpan/v1/me/tokens?pageSize=201", "request.page-size-invalid"},
		{"/api/dustpan/v1/me/tokens?pageSize=0", "request.page-size-invalid"},
		{"/api/dustpan/v1/me/grants?cursor=x", "request.invalid-query"},
		{"/api/dustpan/v1/me/grants?pageToken=%21", "request.page-token-invalid"},
		{"/api/dustpan/v1/me/grants?pageSize=201", "request.page-size-invalid"},
		{"/api/dustpan/v1/me/access/objects?type=unit&limit=1", "request.invalid-query"},
		{"/api/dustpan/v1/me/access/objects?type=unit&pageToken=%21", "request.page-token-invalid"},
		{"/api/dustpan/v1/me/access/objects?type=unit&pageSize=201", "request.page-size-invalid"},
	} {
		rec := serveCaller(t, f.handler, callerCall{http.MethodGet, c.path, "bob", "", nil})
		got := decodeErrorBody(t, rec)

		if rec.Code != http.StatusBadRequest || got.ErrorCode != c.code {
			t.Errorf("GET %s = %d %s, want 400 with errorCode %s", c.path, rec.Code, rec.Body.String(), c.code)
		}
	}

	var page struct {
		Tokens        []tokenDoc `json:"tokens"`
		NextPageToken string     `json:"nextPageToken"`
	}

	f.as(t, "bob", apiCall{http.MethodGet, "/api/dustpan/v1/me/tokens?pageSize=1", "", http.StatusOK}, &page)

	if page.NextPageToken != "" {
		t.Errorf("nextPageToken of a list with no token = %q, want empty", page.NextPageToken)
	}
}

// replayID is the Idempotency-Key of the replay test.
const replayID = "own-token-replay-1"

// TestCallerIdempotency proves that a create of an own token with an
// Idempotency-Key makes one token: the replay answers 409 with the ID of the
// token and no secret, and the list holds one token.
func TestCallerIdempotency(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: true,
	})
	sess := e.freshSignIn(t)

	first := e.createKeyed(t, sess, replayID)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s, want 201", first.Code, first.Body.String())
	}

	var made struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}

	decodeInto(t, first.Body.String(), &made)

	if made.ID == "" || made.Secret == "" {
		t.Fatalf("first create = %s, want the ID and the secret", first.Body.String())
	}

	again := e.createKeyed(t, sess, replayID)
	if again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), made.ID) ||
		strings.Contains(again.Body.String(), made.Secret) {
		t.Fatalf("replay = %d %s, want 409 with the ID %s and no secret", again.Code, again.Body.String(), made.ID)
	}

	list := e.sendWithSession(t, http.MethodGet, sess, "/api/dustpan/v1/me/tokens", "")

	var got struct {
		Tokens []tokenDoc `json:"tokens"`
	}

	decodeInto(t, list.Body.String(), &got)

	if len(got.Tokens) != 1 {
		t.Fatalf("tokens after a replay = %s, want one", list.Body.String())
	}

	// With no session, the replay never reaches the store of the keys.
	keyed := map[string]string{"Idempotency-Key": replayID}
	rec := serveCaller(t, e.handler, callerCall{
		http.MethodPost, "/api/dustpan/v1/me/tokens", "", ownTokenBody(), keyed,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay with no credential = %d %s, want 401", rec.Code, rec.Body.String())
	}
}

// TestCallerHostile sends a foreign token id, an id with SQL text and an
// oversized pageSize to the own token routes.
func TestCallerHostile(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.freshSignIn(t)

	for _, id := range []string{"tok-of-another-principal", "x'%20OR%20'1'='1", "..%2f..%2ftokens", testServiceTokenID} {
		for _, c := range []struct{ method, suffix string }{
			{http.MethodDelete, ""}, {http.MethodPost, "/rotate"},
		} {
			rec := e.sendWithSession(t, c.method, sess, "/api/dustpan/v1/me/tokens/"+id+c.suffix, `{}`)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s own token %q%s = %d %s, want 404", c.method, id, c.suffix, rec.Code, rec.Body.String())
			}
		}
	}

	rec := e.sendWithSession(t, http.MethodGet, sess, "/api/dustpan/v1/me/tokens?pageSize=100000", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("pageSize 100000 = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// createKeyed sends the create of an own token with the session and an
// Idempotency-Key.
func (e signInEnv) createKeyed(t *testing.T, sess *http.Cookie, key string) *httptest.ResponseRecorder {
	t.Helper()

	return e.sendWithHeaders(t, sess, callerCall{
		http.MethodPost, "/api/dustpan/v1/me/tokens", "", ownTokenBody(), map[string]string{"Idempotency-Key": key},
	})
}

// ownTokenScenarios returns the scenarios of the own token operations for the
// check of the header flags. Each probe runs on a fresh environment with a
// fresh session of the person.
func ownTokenScenarios() map[string]headertest.Scenario {
	scenario := func(method, tail, body string, replayed func(first, again headertest.Answer) bool) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			e := newSignInEnvWith(t, signInEnvOptions{
				autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil,
				namespaces: nil, hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: true,
			})
			sess := e.freshSignIn(t)

			target := "/api/dustpan/v1/me/tokens"

			if tail != "" {
				made := e.postWithSession(t, sess, target, ownTokenBody())

				var doc tokenDoc

				decodeInto(t, made.Body.String(), &doc)

				target += "/" + doc.ID
				if tail != "{id}" {
					target += tail
				}
			}

			return headertest.Probe{
				ETag: "",
				Send: func(headers map[string]string) headertest.Answer {
					rec := e.sendWithHeaders(t, sess, callerCall{method, target, "", body, headers})

					return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
				},
				Replayed: replayed,
			}
		}
	}

	created := func(first, again headertest.Answer) bool {
		return first.Status == http.StatusCreated && again.Status == http.StatusConflict
	}

	return map[string]headertest.Scenario{
		"createOwnToken": scenario(http.MethodPost, "", ownTokenBody(), created),
		"deleteOwnToken": scenario(http.MethodDelete, "{id}", "", nil),
		"rotateOwnToken": scenario(http.MethodPost, "/rotate", `{}`, nil),
	}
}

// sendWithHeaders sends a request with the session and the extra headers.
func (e signInEnv) sendWithHeaders(t *testing.T, sess *http.Cookie, c callerCall) *httptest.ResponseRecorder {
	t.Helper()

	_, s := e.readSession(t, sess)

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	req.AddCookie(sess)
	req.Header.Set(apiserver.CSRFHeader, s.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	for name, value := range c.headers {
		req.Header.Set(name, value)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}
