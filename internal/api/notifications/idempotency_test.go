package notifications_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// countSinks returns how many sinks the list answers.
func (f fixture) countSinks(t *testing.T) int {
	t.Helper()

	got := f.do(t, get("reader", base))
	wantCode(t, "list", got, http.StatusOK)

	return len(decode[listWire](t, got).Sinks)
}

// TestIdempotentCreateNeverRepeatsTheKey proves that a create makes one sink
// per key, and that a replay does not answer the signing key again: the first
// answer held a secret, so the replay names the sink and nothing else.
func TestIdempotentCreateNeverRepeatsTheKey(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := webhookBody(t, "Ops")

	first := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	wantCode(t, "first create", first, http.StatusCreated)

	made := decode[sinkWire](t, first)
	if !hex64.MatchString(made.SigningKey) {
		t.Fatalf("first answer = %s, want the signing key", first.body)
	}

	again := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if again.code != http.StatusConflict || !strings.Contains(again.body, "request.idempotency-key-replayed-secret") ||
		!strings.Contains(again.body, made.ID) || strings.Contains(again.body, made.SigningKey) {
		t.Fatalf("replay: %d %s, want 409 request.idempotency-key-replayed-secret naming %s and no key",
			again.code, again.body, made.ID)
	}

	if n := f.countSinks(t); n != 1 {
		t.Errorf("sinks after a replay = %d, want 1", n)
	}

	if n := len(f.sinkEntries(t)); n != 1 {
		t.Errorf("audit entries after a replay = %d, want 1", n)
	}

	// The same key with another body is another request.
	other := f.do(t, keyed(send("admin", http.MethodPost, base, webhookBody(t, "Other")), "k1"))
	if other.code != http.StatusUnprocessableEntity || !strings.Contains(other.body, "request.idempotency-key-reused") {
		t.Errorf("another body: %d %s, want 422 request.idempotency-key-reused", other.code, other.body)
	}

	if n := f.countSinks(t); n != 1 {
		t.Errorf("sinks after the reuse = %d, want 1", n)
	}
}

func TestIdempotentChatCreateNamesTheSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := chatBody(t, "Chat")

	first := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	wantCode(t, "first create", first, http.StatusCreated)

	again := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if again.code != http.StatusConflict || !strings.Contains(again.body, decode[sinkWire](t, first).ID) ||
		strings.Contains(again.body, canary) {
		t.Fatalf("replay: %d %s, want 409 naming the sink and no URL", again.code, again.body)
	}

	if n := f.countSinks(t); n != 1 {
		t.Errorf("sinks after a replay = %d, want 1", n)
	}
}

func TestIdempotentReplaceDeleteAndTest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t, "Ops")
	path := base + "/" + s.ID

	put := keyed(send("admin", http.MethodPut, path, `{"version":1,"name":"Ops team"}`), "put-1")
	first := f.do(t, put)
	wantCode(t, "replace", first, http.StatusOK)

	again := f.do(t, put)
	if again.code != http.StatusOK || again.body != first.body || again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replace replay: %d %s, want the first answer with the replay header", again.code, again.body)
	}

	// Without the key, the same body has an old version: a new request.
	wantCode(t, "replace without the key", f.do(t, send("admin", http.MethodPut, path,
		`{"version":1,"name":"Ops team"}`)), http.StatusConflict)

	test := keyed(send("admin", http.MethodPost, path+"/test", ""), "test-1")
	one := f.do(t, test)
	wantCode(t, "test", one, http.StatusOK)

	two := f.do(t, test)
	if two.code != http.StatusOK || two.body != one.body || two.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("test replay: %d %s, want the first answer with the replay header", two.code, two.body)
	}

	if n := f.hook.count(); n != 1 {
		t.Errorf("sender got %d messages after a replay, want 1", n)
	}

	del := keyed(send("admin", http.MethodDelete, path, ""), "del-1")
	wantCode(t, "delete", f.do(t, del), http.StatusNoContent)

	replayed := f.do(t, del)
	if replayed.code != http.StatusNoContent || replayed.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("delete replay: %d %s, want 204 with the replay header", replayed.code, replayed.body)
	}

	if n := len(f.sinkEntries(t)); n != 4 {
		t.Errorf("audit entries = %d, want create, update, test and delete", n)
	}
}

func TestIdempotentKeyMustBeValid(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := webhookBody(t, "Ops")

	for name, key := range map[string]string{
		"empty": "", "too long": strings.Repeat("k", idempotency.MaxKeyLength+1), "non-ASCII": "clé",
	} {
		got := f.do(t, keyed(send("admin", http.MethodPost, base, body), key))
		if got.code != http.StatusBadRequest || !strings.Contains(got.body, "request.idempotency-key-invalid") {
			t.Errorf("%s key: %d %s, want 400 request.idempotency-key-invalid", name, got.code, got.body)
		}
	}

	if n := f.countSinks(t); n != 0 {
		t.Errorf("sinks after invalid keys = %d, want 0", n)
	}
}

func TestIdempotentReplayChecksAuthorizationAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t, "Ops")
	test := keyed(send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""), "k1")

	wantCode(t, "first test", f.do(t, test), http.StatusOK)

	f.denied[manage] = true

	got := f.do(t, test)
	if got.code != http.StatusForbidden || strings.Contains(got.body, "delivered") {
		t.Errorf("replay without the action: %d %s, want 403 with no stored answer", got.code, got.body)
	}
}

func TestIdempotentErrorsAreNotStored(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bad := jsonString(t, map[string]string{"kind": "webhook", "name": "x", "target": "https://127.0.0.1/"})

	wantCode(t, "refused target", f.do(t, keyed(send("admin", http.MethodPost, base, bad), "k1")),
		http.StatusBadRequest)
	wantCode(t, "refused target again", f.do(t, keyed(send("admin", http.MethodPost, base, bad), "k1")),
		http.StatusBadRequest)

	// An unknown sink answers 404 and stores nothing: the key still works.
	wantCode(t, "delete of an unknown sink",
		f.do(t, keyed(send("admin", http.MethodDelete, base+"/snk-x", ""), "k2")), http.StatusNotFound)

	s := f.makeWebhook(t, "Ops")
	wantCode(t, "delete with the same key", f.do(t, keyed(send("admin", http.MethodDelete, base+"/"+s.ID, ""), "k3")),
		http.StatusNoContent)
}

func TestIdempotencyKeyIsRefusedNotIgnored(t *testing.T) {
	t.Parallel()

	// A server with no store of replay records refuses the key.
	f := newFixtureWithoutKeys(t)

	got := f.do(t, keyed(send("admin", http.MethodPost, base, webhookBody(t, "a")), "k1"))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, "request.idempotency-key-unsupported") {
		t.Errorf("POST with a key: %d %s", got.code, got.body)
	}

	wantCode(t, "GET with a key", f.do(t, keyed(get("reader", base), "k1")), http.StatusOK)

	if n := f.countSinks(t); n != 0 {
		t.Errorf("sinks after the refused create = %d, want 0", n)
	}
}
