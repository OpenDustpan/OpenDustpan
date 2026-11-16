package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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
		got := decodeErrorBody(t, rec)
		if rec.Code != http.StatusUnauthorized || got.ErrorCode != errcode.AuthUnauthenticated {
			t.Errorf("%s %s with no token = %d %s, want 401 with errorCode %s",
				c.method, c.path, rec.Code, rec.Body.String(), errcode.AuthUnauthenticated)
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
		{"/api/dustpan/v1/me/tokens?status=active", "request.invalid-query"},
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

// TestCallerIdempotency proves that a create, a rotation and a revoke of an
// own token with an Idempotency-Key make one change: a replay of a create or
// a rotation answers 409 with the ID of the token and no secret, and a
// replay of a revoke answers the stored 204 again.
func TestCallerIdempotency(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: true,
	})
	sess := e.freshSignIn(t)

	// One body serves both sends, so the replay cannot differ from the first
	// request in the bytes it sends.
	body := ownTokenBody()

	first := e.createKeyed(t, sess, replayID, body)
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

	again := e.createKeyed(t, sess, replayID, body)
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

	// The rotation answers the secret of the new token, which the server
	// keeps no copy of: a replay answers 409 with the ID of the new token.
	rotateKey := "own-token-rotate-1"
	rotate := callerCall{
		http.MethodPost, "/api/dustpan/v1/me/tokens/" + made.ID + "/rotate", "", `{}`,
		map[string]string{"Idempotency-Key": rotateKey},
	}

	rotated := e.sendWithHeaders(t, sess, rotate)
	if rotated.Code != http.StatusCreated {
		t.Fatalf("first rotate = %d %s, want 201", rotated.Code, rotated.Body.String())
	}

	var rotation struct {
		Token struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"token"`
	}

	decodeInto(t, rotated.Body.String(), &rotation)

	if rotation.Token.ID == "" || rotation.Token.Secret == "" {
		t.Fatalf("first rotate = %s, want the ID and the secret of the new token", rotated.Body.String())
	}

	rotatedAgain := e.sendWithHeaders(t, sess, rotate)
	if rotatedAgain.Code != http.StatusConflict ||
		!strings.Contains(rotatedAgain.Body.String(), rotation.Token.ID) ||
		strings.Contains(rotatedAgain.Body.String(), rotation.Token.Secret) {
		t.Fatalf("rotate replay = %d %s, want 409 with the ID %s and no secret",
			rotatedAgain.Code, rotatedAgain.Body.String(), rotation.Token.ID)
	}

	// The revoke answers 204 with no body: the store keeps that answer in
	// the transaction of the revoke, and a replay sends it again.
	deleteKey := "own-token-delete-1"
	remove := callerCall{
		http.MethodDelete, "/api/dustpan/v1/me/tokens/" + made.ID, "", "",
		map[string]string{"Idempotency-Key": deleteKey},
	}

	deleted := e.sendWithHeaders(t, sess, remove)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("first delete = %d %s, want 204", deleted.Code, deleted.Body.String())
	}

	deletedAgain := e.sendWithHeaders(t, sess, remove)
	if deletedAgain.Code != http.StatusNoContent || deletedAgain.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("delete replay = %d %s, want the stored 204 marked as a replay",
			deletedAgain.Code, deletedAgain.Body.String())
	}

	// With no session, the replay never reaches the store of the keys.
	rec := serveCaller(t, e.handler, callerCall{
		http.MethodPost, "/api/dustpan/v1/me/tokens", "", body, map[string]string{"Idempotency-Key": replayID},
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay with no credential = %d %s, want 401", rec.Code, rec.Body.String())
	}
}

// TestCallerHostile sends hostile input to each layer of the own token
// routes: the handler, the service and the store adapter. Each table sends
// its input to its layer as if every other layer had failed.
func TestCallerHostile(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.freshSignIn(t)

	t.Run("handler", func(t *testing.T) {
		t.Parallel()

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
	})

	t.Run("service", func(t *testing.T) {
		t.Parallel()

		made := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

		var doc tokenDoc

		decodeInto(t, made.Body.String(), &doc)

		svc, err := accesstoken.NewService(accesstoken.Config{
			Store: e.store, Principals: e.store, Now: e.clock.Now,
		})
		if err != nil {
			t.Fatalf("token service: %v", err)
		}

		// alice is a person of the store, but the token belongs to the
		// signed-in person: a foreign caller reads and changes nothing.
		foreign := identity.Principal{Subject: "alice"} //nolint:exhaustruct // A person with no credential.

		ctx := t.Context()

		own, err := svc.ListOwn(ctx, foreign, accesstoken.PageRequest{
			PrincipalID: "", Cursor: "", Filter: accesstoken.FilterAll, Limit: 10,
		})
		if err != nil || len(own.Items) != 0 {
			t.Errorf("own tokens of a foreign person = %d items, %v, want none", len(own.Items), err)
		}

		entry := audit.Entry{Subject: "alice", Action: "token:manage-own", Channel: audit.ChannelAPI}

		if err := svc.Revoke(ctx, foreign, entry, accesstoken.ScopeOwn, doc.ID); !errors.Is(err, accesstoken.ErrNotFound) {
			t.Errorf("revoke of a foreign token = %v, want accesstoken.ErrNotFound", err)
		}

		_, _, err = svc.Rotate(ctx, foreign, entry, accesstoken.RotateInput{
			OldEndsAt: nil, ExpiresAt: time.Time{}, ID: doc.ID, Scope: accesstoken.ScopeOwn,
		})
		if !errors.Is(err, accesstoken.ErrNotFound) {
			t.Errorf("rotate of a foreign token = %v, want accesstoken.ErrNotFound", err)
		}

		after, err := e.store.GetToken(ctx, doc.ID)
		if err != nil || !after.RevokedAt.IsZero() || after.ReplacedBy != "" {
			t.Errorf("token after the hostile service calls = %+v, %v, want an unchanged token", after, err)
		}
	})

	t.Run("store", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()

		for _, id := range []string{"x' OR '1'='1", "../../tokens", "%00"} {
			if _, err := e.store.GetToken(ctx, id); !errors.Is(err, accesstoken.ErrNotFound) {
				t.Errorf("read token %q = %v, want accesstoken.ErrNotFound", id, err)
			}
		}

		rows, total, err := e.store.ListTokens(ctx, accesstoken.Query{
			At: e.clock.Now(), AfterCreatedAt: e.clock.Now(), PrincipalID: "x' OR '1'='1",
			AfterID: "x' OR '1'='1", Filter: accesstoken.FilterAll, Limit: 10, HasAfter: true,
		})
		if err != nil || len(rows) != 0 || total != 0 {
			t.Errorf("list tokens with hostile values = %d rows, total %d, %v, want no row", len(rows), total, err)
		}
	})

	t.Run("ended credential fails closed", func(t *testing.T) {
		t.Parallel()

		made := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

		var doc tokenDoc

		decodeInto(t, made.Body.String(), &doc)

		rec := serveCaller(t, e.handler, callerCall{http.MethodGet, "/api/dustpan/v1/me/access?object=routes", doc.Secret, "", nil})
		if rec.Code != http.StatusOK {
			t.Fatalf("the token works before its end = %d %s, want 200", rec.Code, rec.Body.String())
		}

		if rec := e.sendWithSession(t, http.MethodDelete, sess, "/api/dustpan/v1/me/tokens/"+doc.ID, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("delete = %d %s, want 204", rec.Code, rec.Body.String())
		}

		// A replay with the ended token answers 401 before any idempotency
		// lookup, and the server keeps no answer for the key: the same key
		// with a valid session then makes the token.
		key := "own-token-ended-1"
		keyed := callerCall{
			http.MethodPost, "/api/dustpan/v1/me/tokens", "", ownTokenBody(), map[string]string{"Idempotency-Key": key},
		}

		if rec := serveCaller(t, e.handler, callerCall{
			keyed.method, keyed.target, doc.Secret, keyed.body, keyed.headers,
		}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("keyed create with the ended token = %d %s, want 401", rec.Code, rec.Body.String())
		}

		if rec := e.sendWithHeaders(t, sess, keyed); rec.Code != http.StatusCreated {
			t.Fatalf("the same key with a valid session = %d %s, want 201: the 401 stored no answer",
				rec.Code, rec.Body.String())
		}
	})
}

// TestCallerRotation proves the two outcomes of a rotation of an own token:
// after a rotation that ends the old token at once, the new token works and
// the old token answers 401, and after a rotation that fails before its
// commit, the old token still works and no new token exists.
func TestCallerRotation(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.freshSignIn(t)

	works := func(t *testing.T, secret string, want int) {
		t.Helper()

		rec := serveCaller(t, e.handler, callerCall{
			http.MethodGet, "/api/dustpan/v1/me/access?object=routes", secret, "", nil,
		})
		if rec.Code != want {
			t.Errorf("a request with the token = %d %s, want %d", rec.Code, rec.Body.String(), want)
		}
	}

	made := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

	var first struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}

	decodeInto(t, made.Body.String(), &first)

	works(t, first.Secret, http.StatusOK)

	// The rotation ends the old token at once.
	now := e.clock.Now().UTC().Truncate(time.Millisecond)
	rotate := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens/"+first.ID+"/rotate",
		`{"oldTokenEndsAt":"`+now.Format(time.RFC3339Nano)+`"}`)
	if rotate.Code != http.StatusCreated {
		t.Fatalf("rotate = %d %s, want 201", rotate.Code, rotate.Body.String())
	}

	var rotation struct {
		Token struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"token"`
	}

	decodeInto(t, rotate.Body.String(), &rotation)

	e.clock.Add(time.Second)

	works(t, first.Secret, http.StatusUnauthorized)
	works(t, rotation.Token.Secret, http.StatusOK)

	// A rotation that the service refuses before its commit changes nothing.
	spare := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

	var second struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}

	decodeInto(t, spare.Body.String(), &second)

	past := e.clock.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	failed := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens/"+second.ID+"/rotate",
		`{"oldTokenEndsAt":"`+past.Format(time.RFC3339Nano)+`"}`)
	if failed.Code != http.StatusBadRequest {
		t.Fatalf("failed rotate = %d %s, want 400", failed.Code, failed.Body.String())
	}

	works(t, second.Secret, http.StatusOK)

	list := e.sendWithSession(t, sess, "/api/dustpan/v1/me/tokens", "")

	var got struct {
		Tokens []struct {
			ID         string `json:"id"`
			ReplacedBy string `json:"replacedBy"`
		} `json:"tokens"`
	}

	decodeInto(t, list.Body.String(), &got)

	if len(got.Tokens) != 3 {
		t.Fatalf("tokens after the rotations = %s, want the two made tokens and the rotated one", list.Body.String())
	}

	for _, tok := range got.Tokens {
		if tok.ID == second.ID && tok.ReplacedBy != "" {
			t.Fatalf("token %s = %+v after a failed rotate, want no replacement", second.ID, tok)
		}
	}
}

// TestCallerTokenManageOwnRefused proves that a principal that holds
// caller:read but not token:manage-own reads its own access and gets 403 on
// the create, the revoke and the rotate under /me/tokens.
func TestCallerTokenManageOwnRefused(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil,
		authz: selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionTokenManageOwn: true}},
		namespaces: nil, hostile: nil, passwords: nil, failPolicyRead: nil, idempotency: false,
	})
	sess := e.freshSignIn(t)

	if rec := e.sendWithSession(t, sess, http.MethodGet, "/api/dustpan/v1/me/access?object=routes", ""); rec.Code != http.StatusOK {
		t.Fatalf("read own access without token:manage-own = %d %s, want 200", rec.Code, rec.Body.String())
	}

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/dustpan/v1/me/tokens", ownTokenBody()},
		{http.MethodDelete, "/api/dustpan/v1/me/tokens/tok-1", ""},
		{http.MethodPost, "/api/dustpan/v1/me/tokens/tok-1/rotate", `{}`},
	} {
		rec := e.sendWithSession(t, c.method, sess, c.path, c.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without token:manage-own = %d %s, want 403", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// TestCallerObjectsTypeRequired proves the guard on the type of GET
// /me/access/objects: a missing or unknown type, and an unknown action,
// answer 400 with the stable code of an invalid query, and the text names
// the allowed types. Without the guard, the service would refuse the request
// with an answer that carries no code.
func TestCallerObjectsTypeRequired(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	for _, path := range []string{
		"/api/dustpan/v1/me/access/objects",
		"/api/dustpan/v1/me/access/objects?type=group",
		"/api/dustpan/v1/me/access/objects?type=unit&action=nope",
	} {
		rec := serveCaller(t, f.handler, callerCall{http.MethodGet, path, "bob", "", nil})
		got := decodeErrorBody(t, rec)

		if rec.Code != http.StatusBadRequest || got.ErrorCode != errcode.RequestInvalidQuery ||
			!strings.Contains(rec.Body.String(), "namespace|collection|unit|principal") {
			t.Fatalf("GET %s = %d %s, want 400 with errorCode %s and the allowed types",
				path, rec.Code, rec.Body.String(), errcode.RequestInvalidQuery)
		}
	}
}

// createKeyed sends the create of an own token with the session and an
// Idempotency-Key.
func (e signInEnv) createKeyed(t *testing.T, sess *http.Cookie, key, body string) *httptest.ResponseRecorder {
	t.Helper()

	return e.sendWithHeaders(t, sess, callerCall{
		http.MethodPost, "/api/dustpan/v1/me/tokens", "", body, map[string]string{"Idempotency-Key": key},
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

	// A revoke answers 204 with no body, so its replay sends the stored
	// answer again and carries the replay header.
	revoked := func(first, again headertest.Answer) bool {
		return first.Status == http.StatusNoContent && again.Status == http.StatusNoContent &&
			again.Header.Get("Idempotent-Replayed") == "true"
	}

	return map[string]headertest.Scenario{
		"createOwnToken": scenario(http.MethodPost, "", ownTokenBody(), created),
		"deleteOwnToken": scenario(http.MethodDelete, "{id}", "", revoked),
		"rotateOwnToken": scenario(http.MethodPost, "/rotate", `{}`, created),
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
