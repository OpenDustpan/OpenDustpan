package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
)

const (
	confirmPath = hostAppPath + "/confirm-rotation"
	// hostNewSecret is the event secret of a rotation.
	hostNewSecret = "NEWEVENTCANARY-0123456789abcdef0123456789"
)

// rotationBody is the registration body with the new event secret.
func rotationBody() string {
	return strings.Replace(hostAppBody(), hostSecretCanary, hostNewSecret, 1)
}

// openRotation registers the app, and puts a new event secret, which opens the
// window of the old one. It returns the answer of the second put.
func openRotation(t *testing.T, e *hostEnv) hostResponse {
	t.Helper()

	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusCreated)

	rotated := e.send(t, http.MethodPut, hostAppPath, rotationBody())
	wantHost(t, rotated, http.StatusOK)

	return rotated
}

// endsAtOf reads the field oldSecretEndsAt of an answer. The second result is
// false when the answer has no such field.
func endsAtOf(t *testing.T, r hostResponse) (int64, bool) {
	t.Helper()

	doc := decodeHost[map[string]any](t, r)

	raw, found := doc["oldSecretEndsAt"]
	if !found {
		return 0, false
	}

	n, isNumber := raw.(float64)
	if !isNumber {
		t.Fatalf("oldSecretEndsAt = %#v, want a number of Unix milliseconds", raw)
	}

	return int64(n), true
}

// wantNoSecret fails the test when an answer holds any secret of the tests.
func wantNoSecret(t *testing.T, r hostResponse) {
	t.Helper()

	for _, secret := range []string{hostSecretCanary, hostNewSecret, hostKeyCanary} {
		if strings.Contains(r.body, secret) {
			t.Fatalf("answer holds a secret: %s", r.body)
		}
	}
}

func TestConfirmRotationRoute(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	rotated := openRotation(t, e)

	if _, found := endsAtOf(t, rotated); !found {
		t.Fatalf("the put of a new secret shows no oldSecretEndsAt: %s", rotated.body)
	}

	// A stale version is refused and changes nothing.
	stale := hostCall{
		headers: map[string]string{"If-Match": `"1"`},
		method:  http.MethodPost,
		target:  confirmPath,
		body:    "",
	}
	wantHost(t, e.do(t, stale), http.StatusPreconditionFailed)

	still := e.send(t, http.MethodGet, hostAppPath, "")
	if _, found := endsAtOf(t, still); !found {
		t.Fatalf("a refused confirm closed the window: %s", still.body)
	}

	// The confirm with the version that the caller read.
	current := hostCall{
		headers: map[string]string{
			"If-Match": rotated.header.Get("ETag"),
		},
		method: http.MethodPost,
		target: confirmPath,
		body:   "",
	}

	confirmed := e.do(t, current)
	wantHost(t, confirmed, http.StatusOK)
	wantNoSecret(t, confirmed)

	if _, found := endsAtOf(t, confirmed); found {
		t.Fatalf("the confirm answer shows oldSecretEndsAt: %s", confirmed.body)
	}

	if got := confirmed.header.Get("ETag"); got != `"3"` {
		t.Fatalf("ETag = %q, want the next version", got)
	}

	if _, found := endsAtOf(t, e.send(t, http.MethodGet, hostAppPath, "")); found {
		t.Fatal("a read after the confirm shows oldSecretEndsAt")
	}

	// A second confirm has no window to end: 409, and the answer says why.
	again := e.send(t, http.MethodPost, confirmPath, "")
	wantHost(t, again, http.StatusConflict)

	if !strings.Contains(again.body, "no open event secret rotation") {
		t.Fatalf("answer = %s, want the sentence about the missing window", again.body)
	}

	wantNoSecret(t, again)
}

func TestConfirmRotationWithNoWindowOrNoRegistration(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	wantHost(t, e.send(t, http.MethodPost, confirmPath, ""), http.StatusNotFound)
	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusCreated)
	wantHost(t, e.send(t, http.MethodPost, confirmPath, ""), http.StatusConflict)

	// The same secret again opens no window.
	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusOK)
	wantHost(t, e.send(t, http.MethodPost, confirmPath, ""), http.StatusConflict)

	other := e.send(t, http.MethodPost, hostAppPath+"/../confirm-rotation", "")
	if other.status == http.StatusOK {
		t.Fatalf("a path with a dot segment confirmed: %d", other.status)
	}

	bad := e.send(t, http.MethodPost, "/api/dustpan/v1/git-host-apps/gitlab/confirm-rotation", "")
	wantHost(t, bad, http.StatusBadRequest)
}

// TestConfirmRotationRefusesWhatThePutRefuses proves that the confirm needs the
// action of the put, and a console session with a recent sign-in, and that it
// takes no Idempotency-Key.
func TestConfirmRotationRefusesWhatThePutRefuses(t *testing.T) {
	t.Parallel()

	deny := map[identity.Action]bool{}
	e := newHostEnv(t, hostAuthorizer{deny: deny, anyNamespace: false})
	rotated := openRotation(t, e)

	// A key is refused before any write.
	withKey := hostCall{
		headers: map[string]string{
			"Idempotency-Key": "abcdefgh-1",
		},
		method: http.MethodPost,
		target: confirmPath,
		body:   "",
	}
	wantHost(t, e.do(t, withKey), http.StatusBadRequest)

	// An access token never gives the recent sign-in that a write needs.
	token := httptest.NewRequestWithContext(t.Context(), http.MethodPost, confirmPath, http.NoBody)
	token.RemoteAddr = hostRemote
	token.Header.Set("Authorization", "Bearer test-token")

	tokenRec := httptest.NewRecorder()
	e.handler.ServeHTTP(tokenRec, token)

	if tokenRec.Code != http.StatusForbidden {
		t.Fatalf("access token: status = %d, want 403: %s", tokenRec.Code, tokenRec.Body.String())
	}

	// No credential at all is the usual 401.
	anon := httptest.NewRequestWithContext(t.Context(), http.MethodPost, confirmPath, http.NoBody)
	anon.RemoteAddr = hostRemote

	anonRec := httptest.NewRecorder()
	e.handler.ServeHTTP(anonRec, anon)

	if anonRec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: status = %d, want 401", anonRec.Code)
	}

	// A principal that may read the registration and not manage it.
	deny[identity.ActionServerManageGitHost] = true

	denied := e.send(t, http.MethodPost, confirmPath, "")
	wantHost(t, denied, http.StatusForbidden)
	wantNoSecret(t, denied)

	if n := countEntries(e.auditEntries(t, audit.ServerScope), audit.OutcomeDenied, "server:manage-git-host"); n != 1 {
		t.Fatalf("%d denied entries, want 1", n)
	}

	// The refusals changed nothing: the window is open.
	deny[identity.ActionServerManageGitHost] = false

	if _, found := endsAtOf(t, e.send(t, http.MethodGet, hostAppPath, "")); !found {
		t.Fatalf("a refused confirm closed the window; the put answered %s", rotated.body)
	}
}

// TestConfirmRotationAuditsItsWrite proves that the confirm writes one entry
// for the action git-host-app:confirm-rotation, with no secret.
func TestConfirmRotationAuditsItsWrite(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	openRotation(t, e)
	wantHost(t, e.send(t, http.MethodPost, confirmPath, ""), http.StatusOK)

	entries := e.auditEntries(t, audit.ServerScope)
	if n := countEntries(entries, audit.OutcomeSuccess, "git-host-app:confirm-rotation"); n != 1 {
		t.Fatalf("%d confirm entries, want 1", n)
	}

	for i := range entries {
		text := entries[i].Reason + entries[i].Object + entries[i].Action

		for _, secret := range []string{hostSecretCanary, hostNewSecret, hostKeyCanary} {
			if strings.Contains(text, secret) {
				t.Fatalf("entry %d holds a secret", i)
			}
		}
	}
}

func TestRegistrationShowsOldSecretEndsAt(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	first := e.send(t, http.MethodPut, hostAppPath, hostAppBody())
	wantHost(t, first, http.StatusCreated)

	if _, found := endsAtOf(t, first); found {
		t.Fatalf("a first registration shows oldSecretEndsAt: %s", first.body)
	}

	rotated := e.send(t, http.MethodPut, hostAppPath, rotationBody())
	wantHost(t, rotated, http.StatusOK)
	wantNoSecret(t, rotated)

	ends, found := endsAtOf(t, rotated)
	if !found {
		t.Fatalf("the put of a new secret shows no oldSecretEndsAt: %s", rotated.body)
	}

	// The window is 7 days from the clock of the service, in Unix milliseconds.
	// That clock starts at hostNow and moves 1 second at each read.
	wantEnd := hostNow().Add(7 * 24 * time.Hour)
	if got := time.UnixMilli(ends); got.Before(wantEnd) || got.After(wantEnd.Add(time.Hour)) {
		t.Fatalf("oldSecretEndsAt = %v, want about %v", got, wantEnd)
	}

	read := e.send(t, http.MethodGet, hostAppPath, "")
	if got, readFound := endsAtOf(t, read); !readFound || got != ends {
		t.Fatalf("read shows %d (%v), the put showed %d", got, readFound, ends)
	}

	wantNoSecret(t, read)
	checkNoOldSetID(t, read, rotated)
}

// checkNoOldSetID fails the test when an answer holds the set ID of the old
// event secret: the answer names the current set only.
func checkNoOldSetID(t *testing.T, read, rotated hostResponse) {
	t.Helper()

	cur := decodeHost[appDoc](t, read).EventSecret.SetID

	for _, r := range []hostResponse{read, rotated} {
		doc := decodeHost[map[string]any](t, r)
		if _, leaked := doc["oldEventSecret"]; leaked {
			t.Fatalf("answer shows an old secret: %s", r.body)
		}

		if got := decodeHost[appDoc](t, r).EventSecret.SetID; got != cur {
			t.Fatalf("event secret set ID = %q, want the current %q", got, cur)
		}
	}
}

// TestRegistrationHidesAnEndedWindow proves that a window whose end passed does
// not show: the field is the effective end.
func TestRegistrationHidesAnEndedWindow(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	openRotation(t, e)

	reg, err := e.store.GitHost().GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get registration: %v", err)
	}

	reg.OldEventSecretEndsAt = time.UnixMilli(1)

	if _, err = e.store.GitHost().PutRegistration(t.Context(), reg, reg.Version, hostRegistrationEntry()); err != nil {
		t.Fatalf("put registration: %v", err)
	}

	if _, found := endsAtOf(t, e.send(t, http.MethodGet, hostAppPath, "")); found {
		t.Fatal("a window that ended shows oldSecretEndsAt")
	}
}
