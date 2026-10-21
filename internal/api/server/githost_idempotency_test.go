package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

func keyed(key string) map[string]string { return map[string]string{"Idempotency-Key": key} }

func TestGitHostCreateReplaysTheFirstAnswerAndMakesOneConnection(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	call := hostCall{headers: keyed("create-1"), method: http.MethodPost, target: hostConnectionsPath, body: createBody("101")}

	first := e.do(t, call)
	wantHost(t, first, http.StatusCreated)

	replay := e.do(t, call)
	wantHost(t, replay, http.StatusCreated)

	if replay.body != first.body || replay.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %s (%v), want the first answer and the replay header", replay.body, replay.header)
	}

	list := decodeHost[connectionsDoc](t, e.send(t, http.MethodGet, hostConnectionsPath, ""))
	if len(list.Connections) != 1 {
		t.Fatalf("%d connections after a replay, want 1", len(list.Connections))
	}

	var creates int

	for _, entry := range e.auditEntries(t, "acme") {
		if entry.Action == "git-host-connection:create" {
			creates++
		}
	}

	if creates != 1 {
		t.Fatalf("%d create entries after a replay, want 1", creates)
	}
}

func TestGitHostKeyWithAnotherBodyIsRefused(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	first := hostCall{headers: keyed("create-2"), method: http.MethodPost, target: hostConnectionsPath, body: createBody("101")}
	wantHost(t, e.do(t, first), http.StatusCreated)

	other := first
	other.body = `{"kind":"github","auth":"app","installationID":"101","push":false}`

	r := e.do(t, other)
	wantHost(t, r, http.StatusUnprocessableEntity)

	if !strings.Contains(r.body, "request.idempotency-key-reused") {
		t.Fatalf("answer = %s, want the code of a reused key", r.body)
	}
}

func TestGitHostRegistrationWriteReplays(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	call := hostCall{headers: keyed("app-1"), method: http.MethodPut, target: hostAppPath, body: hostAppBody()}

	first := e.do(t, call)
	wantHost(t, first, http.StatusCreated)

	replay := e.do(t, call)
	wantHost(t, replay, http.StatusCreated)

	if replay.body != first.body {
		t.Fatalf("replay = %s, want %s", replay.body, first.body)
	}

	if got := decodeHost[appDoc](t, e.send(t, http.MethodGet, hostAppPath, "")); got.Version != 1 {
		t.Fatalf("version after a replay = %d, want 1", got.Version)
	}

	// The record of a replay holds no secret: the answer has none, and the
	// replay answer is the stored one.
	for _, canary := range []string{hostKeyCanary, hostSecretCanary} {
		if strings.Contains(replay.body, canary) {
			t.Fatalf("the replay answer holds a secret: %s", replay.body)
		}
	}
}

func TestGitHostDeleteReplays(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")
	call := hostCall{headers: keyed("delete-1"), method: http.MethodDelete, target: hostConnectionsPath + "/" + id, body: ""}

	wantHost(t, e.do(t, call), http.StatusNoContent)

	replay := e.do(t, call)
	wantHost(t, replay, http.StatusNoContent)

	if replay.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("headers = %v, want the replay header", replay.header)
	}

	// Without the key the second delete finds nothing.
	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+id, ""), http.StatusNotFound)

	var deletes int

	for _, entry := range e.auditEntries(t, "acme") {
		if entry.Action == "git-host-connection:delete" && entry.Outcome == audit.OutcomeSuccess {
			deletes++
		}
	}

	if deletes != 1 {
		t.Fatalf("%d delete entries, want 1", deletes)
	}
}

func TestGitHostKeyThatIsNotValidIsRefused(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	for _, key := range []string{"", "has space\x00", strings.Repeat("k", 300)} {
		call := hostCall{headers: map[string]string{"Idempotency-Key": key}, method: http.MethodPost,
			target: hostConnectionsPath, body: createBody("101")}

		r := e.do(t, call)
		if r.status != http.StatusBadRequest {
			t.Errorf("key %q answers %d, want 400: %s", key, r.status, r.body)
		}
	}

	list := decodeHost[connectionsDoc](t, e.send(t, http.MethodGet, hostConnectionsPath, ""))
	if len(list.Connections) != 0 {
		t.Fatalf("%d connections after refused keys, want 0", len(list.Connections))
	}
}

func TestGitHostKeyWithNoMiddlewareIsRefused(t *testing.T) {
	t.Parallel()

	e := newHostEnvWith(t, hostAllowAll(), false)
	call := hostCall{headers: keyed("k"), method: http.MethodPut, target: hostAppPath, body: hostAppBody()}

	r := e.do(t, call)
	wantHost(t, r, http.StatusBadRequest)

	if !strings.Contains(r.body, "request.idempotency-key-unavailable") {
		t.Fatalf("answer = %s, want the code of a missing middleware", r.body)
	}

	wantHost(t, e.send(t, http.MethodGet, hostAppPath, ""), http.StatusNotFound)
}
