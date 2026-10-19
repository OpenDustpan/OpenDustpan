package profile_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
)

func createBody(t *testing.T, name string) string {
	t.Helper()

	return jsonString(t, map[string]string{"name": name, "document": minimalDoc})
}

func keyed(r request, key string) request { return r.withHeader("Idempotency-Key", key) }

// countProfiles returns how many profiles the list answers.
func (f fixture) countProfiles(t *testing.T) int {
	t.Helper()

	got := f.do(t, get("reader", base))
	wantCode(t, "list", got, http.StatusOK)

	var page pageWire
	if err := json.Unmarshal([]byte(got.body), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	return len(page.Profiles)
}

func TestIdempotentCreateReplays(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	first := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	wantCode(t, "first create", first, http.StatusCreated)

	if first.header.Get("Idempotent-Replayed") != "" {
		t.Error("the first answer has the replay header")
	}

	again := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	wantCode(t, "replay", again, http.StatusCreated)

	if again.body != first.body {
		t.Errorf("replayed body = %q, want the first body %q", again.body, first.body)
	}

	for _, name := range []string{"Etag", "Location", "Content-Type"} {
		if again.header.Get(name) == "" || again.header.Get(name) != first.header.Get(name) {
			t.Errorf("replayed %s = %q, want %q", name, again.header.Get(name), first.header.Get(name))
		}
	}

	if again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("Idempotent-Replayed = %q, want true", again.header.Get("Idempotent-Replayed"))
	}

	if n := f.countProfiles(t); n != 1 {
		t.Errorf("profiles after a replay = %d, want 1", n)
	}

	// Without the key, the same body is a new request: the name is in use.
	wantCode(t, "create without the key", f.do(t, send("admin", http.MethodPost, base, body)), http.StatusConflict)
}

func TestIdempotentKeyWithAnotherBody(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	wantCode(t, "first", f.do(t, keyed(send("admin", http.MethodPost, base, createBody(t, "a")), "k1")),
		http.StatusCreated)

	got := f.do(t, keyed(send("admin", http.MethodPost, base, createBody(t, "b")), "k1"))
	if got.code != http.StatusUnprocessableEntity || !strings.Contains(got.body, "request.idempotency-key-reused") {
		t.Errorf("another body: %d %s, want 422 request.idempotency-key-reused", got.code, got.body)
	}

	if n := f.countProfiles(t); n != 1 {
		t.Errorf("profiles after the reuse = %d, want 1", n)
	}

	// The same key on another path is a new key.
	wantCode(
		t,
		"same key, other route",
		f.do(t, keyed(send("admin", http.MethodPost, base+"/prf-x/revisions", "{}"), "k1")),
		http.StatusNotFound,
	)
}

func TestIdempotentKeyMustBeValid(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	for name, key := range map[string]string{
		"empty": "", "too long": strings.Repeat("k", idempotency.MaxKeyLength+1), "non-ASCII": "clé",
	} {
		got := f.do(t, keyed(send("admin", http.MethodPost, base, body), key))
		if got.code != http.StatusBadRequest || !strings.Contains(got.body, "request.idempotency-key-invalid") {
			t.Errorf("%s key: %d %s, want 400 request.idempotency-key-invalid", name, got.code, got.body)
		}
	}

	if n := f.countProfiles(t); n != 0 {
		t.Errorf("profiles after invalid keys = %d, want 0", n)
	}
}

func TestIdempotentConcurrentRequestsWriteOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	const callers = 12

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		created  int
		replayed int
		other    []response
	)

	for range callers {
		wg.Go(func() {
			got := f.do(t, keyed(send("admin", http.MethodPost, base, body), "same"))

			mu.Lock()
			defer mu.Unlock()

			switch {
			case got.code == http.StatusCreated && got.header.Get("Idempotent-Replayed") == "true":
				replayed++
			case got.code == http.StatusCreated:
				created++
			default:
				other = append(other, got)
			}
		})
	}

	wg.Wait()

	if created != 1 {
		t.Errorf("first answers = %d, want 1 (replays %d)", created, replayed)
	}

	for _, got := range other {
		if got.code != http.StatusConflict || !strings.Contains(got.body, "request.idempotency-key-in-progress") {
			t.Errorf("a concurrent request answered %d %s, want 409 request.idempotency-key-in-progress",
				got.code, got.body)
		}
	}

	if n := f.countProfiles(t); n != 1 {
		t.Errorf("profiles = %d, want 1", n)
	}
}

func TestIdempotentKeysAreScopedByPrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	wantCode(t, "admin create", f.do(t, keyed(send("admin", http.MethodPost, base, body), "shared")),
		http.StatusCreated)

	// Another caller with the same key and body never gets the answer of
	// admin and never learns that the key exists: the request runs on its own
	// and meets the name in use.
	got := f.do(t, keyed(send("writer", http.MethodPost, base, body), "shared"))
	if got.code != http.StatusConflict || got.header.Get("Idempotent-Replayed") != "" ||
		!strings.Contains(got.body, "profile.conflict") {
		t.Errorf("writer with the key of admin: %d %s, want 409 profile.conflict and no replay", got.code, got.body)
	}

	// A caller without the action gets 403, never a stored answer.
	got = f.do(t, keyed(send("reader", http.MethodPost, base, body), "shared"))
	if got.code != http.StatusForbidden || got.header.Get("Idempotent-Replayed") != "" {
		t.Errorf("reader with the key of admin: %d %s, want 403", got.code, got.body)
	}
}

func TestIdempotentReplayChecksAuthorizationAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	wantCode(t, "first", f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1")), http.StatusCreated)

	f.denied[identity.ActionProfileWrite] = true

	got := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if got.code != http.StatusForbidden || strings.Contains(got.body, `"id"`) {
		t.Errorf("replay without the action: %d %s, want 403 with no stored answer", got.code, got.body)
	}
}

func TestIdempotentErrorsAreNotStored(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	existing := f.create(t, "a", minimalDoc)
	body := createBody(t, "a")

	wantCode(t, "name in use", f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1")), http.StatusConflict)

	// The client fixes the cause. The same key and body now run the
	// operation: the 409 was not stored.
	wantCode(t, "delete", f.do(t, send("admin", http.MethodDelete, base+"/"+existing.ID, "")), http.StatusNoContent)

	got := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if got.code != http.StatusCreated || got.header.Get("Idempotent-Replayed") != "" {
		t.Errorf("retry after the fix: %d %s, want a new 201", got.code, got.body)
	}

	// A refused body (400) is not stored either.
	bad := `{"name":"b","document":"values: {}\n"}`
	wantCode(t, "bad document", f.do(t, keyed(send("admin", http.MethodPost, base, bad), "k2")), http.StatusBadRequest)
	wantCode(t, "bad document again", f.do(t, keyed(send("admin", http.MethodPost, base, bad), "k2")),
		http.StatusBadRequest)
}

func TestIdempotentRecordExpires(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := createBody(t, "a")

	// The record starts at the first request: 24 hours later, it is gone.
	wantCode(t, "first", f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1")), http.StatusCreated)

	f.clock.Advance(idempotency.RecordTTL - time.Minute)

	replay := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if replay.code != http.StatusCreated || replay.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay before expiry: %d %s", replay.code, replay.body)
	}

	f.clock.Advance(2 * time.Minute)

	// After the period, the key runs the operation again: the name is in use.
	wantCode(t, "after expiry", f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1")), http.StatusConflict)
}

func TestIdempotentSaveAndDeleteReplay(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "a", minimalDoc)
	target := base + "/" + created.ID

	save := jsonString(t, map[string]string{"document": minimalDoc + "values:\n  region: eu-west-1\n"})
	saveReq := keyed(send("admin", http.MethodPost, target+"/revisions", save), "save-1").
		withHeader("If-Match", created.ETag)

	first := f.do(t, saveReq)
	wantCode(t, "first save", first, http.StatusCreated)

	again := f.do(t, saveReq)
	if again.code != http.StatusCreated || again.body != first.body ||
		again.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("save replay: %d %s, want the first answer", again.code, again.body)
	}

	// The save ran one time: the newest revision is 2, not 3.
	revisions := f.do(t, get("reader", target+"/revisions"))
	if strings.Count(revisions.body, `"revision"`) != 2 {
		t.Errorf("revisions after a replayed save: %s, want 2", revisions.body)
	}

	delReq := keyed(send("admin", http.MethodDelete, target, ""), "delete-1")

	wantCode(t, "first delete", f.do(t, delReq), http.StatusNoContent)

	replay := f.do(t, delReq)
	if replay.code != http.StatusNoContent || replay.header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("delete replay: %d %s, want 204 and the replay header", replay.code, replay.body)
	}

	// Without the key, the profile is gone.
	wantCode(t, "delete again", f.do(t, send("admin", http.MethodDelete, target, "")), http.StatusNotFound)
}

func TestIdempotentAnswerHoldsNoSecret(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	doc := "schemaVersion: 1\nvalues:\n  token: {$secret: hunter2-plain}\n"
	body := jsonString(t, map[string]string{"name": "a", "document": doc})

	first := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	wantCode(t, "create", first, http.StatusCreated)

	again := f.do(t, keyed(send("admin", http.MethodPost, base, body), "k1"))
	if strings.Contains(first.body, "hunter2-plain") || strings.Contains(again.body, "hunter2-plain") {
		t.Errorf("an answer holds the secret: %q, %q", first.body, again.body)
	}
}
