package inventory_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
)

const (
	keyHeader      = "Idempotency-Key"
	replayedHeader = "Idempotent-Replayed"
)

// keyedCreate returns a create of the view body with the token.
func keyedCreate(token, body string) *viewCall {
	return &viewCall{method: http.MethodPost, target: viewsPath, token: token, body: body}
}

// TestViewCreateReplayKeepsTheIDAndNotTheView proves the record of a create
// keeps the ID of the view and not the view. The first request makes the view.
// A replay with the same key and the same body makes no second view, and it
// answers 409 with the ID of the first.
func TestViewCreateReplayKeepsTheIDAndNotTheView(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	first := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	if first.status != http.StatusCreated {
		t.Fatalf("first create = %d (%s)", first.status, first.body)
	}

	id := idOfBody(t, first.body)

	replay := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	if replay.status != http.StatusConflict || !strings.Contains(replay.body, id) ||
		!strings.Contains(replay.body, "request.idempotency-key-replayed-secret") {
		t.Fatalf("replay = %d (%s), want 409, the code and the ID %s", replay.status, replay.body, id)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, []byte(replay.body),
		errcodetest.Want{
			Code: errcode.RequestIdempotencyKeyReplayedSecret, Args: map[string]any{"objectID": id},
			RequestBody: []byte(payments),
		})

	// The answer of the replay holds the ID and no part of the view.
	if strings.Contains(replay.body, "Payments") || strings.Contains(replay.body, "payments") {
		t.Fatalf("the replay answer holds the view: %s", replay.body)
	}

	// One view only, and one create in the audit log.
	list := f.do(t, http.MethodGet, viewsPath, tokenAna, "")
	if strings.Count(list.body, `"id"`) != 1 {
		t.Fatalf("list after the replay = %s, want one view", list.body)
	}

	if creates := countActions(t, f, "view:create"); creates != 1 {
		t.Fatalf("%d create entries in the audit log, want 1", creates)
	}
}

// countActions returns the number of audit entries of acme with the action.
func countActions(t *testing.T, f *viewFixture, action string) int {
	t.Helper()

	count := 0

	entries := f.auditEntries(t)
	for i := range entries {
		if entries[i].Action == action {
			count++
		}
	}

	return count
}

func idOfBody(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, `"id":"`)
	if !ok {
		t.Fatalf("no ID in %s", body)
	}

	id, _, _ := strings.Cut(after, `"`)

	return id
}

func TestViewIdempotencyReusedKeyAnswers422(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))

	res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, `{"name":"Other"}`))
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "request.idempotency-key-reused") {
		t.Fatalf("reused key = %d (%s), want 422", res.status, res.body)
	}
}

func TestViewIdempotencyKeyIsNotSharedBetweenPeople(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))

	res := f.doKeyed(t, "key-1", keyedCreate(tokenBob, payments))
	if res.status != http.StatusCreated {
		t.Fatalf("the same key from another person = %d (%s), want a new view", res.status, res.body)
	}
}

// TestViewIdempotencyKeyBelongsToThePrincipalID proves that a new principal with
// the same subject does not replay the key of the old principal, so it learns
// no view ID of the old principal.
func TestViewIdempotencyKeyBelongsToThePrincipalID(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	f.people.replace("ana", "prn-anaanaanaanaanaana9")

	res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	if res.status != http.StatusCreated {
		t.Fatalf("create by the new principal = %d (%s), want 201 and no replay of the old ID", res.status, res.body)
	}
}

func TestViewIdempotencyRefusedCreateIsNotStored(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	if res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, `{"name":""}`)); res.status != http.StatusBadRequest {
		t.Fatalf("invalid create = %d", res.status)
	}

	// The same key works after the fix.
	res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	if res.status != http.StatusCreated {
		t.Fatalf("create with the same key after a 400 = %d (%s), want 201", res.status, res.body)
	}
}

func TestViewIdempotencyDeleteReplaysThe204(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	id := f.createView(t, tokenAna, payments)
	del := &viewCall{method: http.MethodDelete, target: viewsPath + "/" + id, token: tokenAna, body: ""}

	first := f.doKeyed(t, "del-1", del)
	replay := f.doKeyed(t, "del-1", del)

	if first.status != http.StatusNoContent || replay.status != http.StatusNoContent ||
		replay.header.Get(replayedHeader) != "true" {
		t.Fatalf("delete = %d, replay = %d (replayed %q), want 204 twice", first.status, replay.status,
			replay.header.Get(replayedHeader))
	}
}

func TestViewIdempotencyInvalidKeyAnswers400(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	for _, key := range []string{emptyKey, strings.Repeat("k", 256), "key\twith tab"} {
		res := f.doKeyed(t, key, keyedCreate(tokenAna, payments))
		if res.status != http.StatusBadRequest {
			t.Errorf("key %q = %d (%s), want 400", key, res.status, res.body)
		}
	}
}

func TestViewIdempotencyReplayChecksTheActionAgain(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)
	f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))

	f.authz.deny(identity.ActionViewCreate)

	if res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments)); res.status != http.StatusForbidden {
		t.Fatalf("replay without the action = %d, want 403", res.status)
	}
}

func TestViewIdempotencyWithNoReplayStoreRefusesTheHeader(t *testing.T) {
	t.Parallel()

	f := newViewFixtureWith(t, viewFixtureOptions{views: nil, units: nil, noIdempotency: true})

	res := f.doKeyed(t, "key-1", keyedCreate(tokenAna, payments))
	if res.status != http.StatusBadRequest {
		t.Fatalf("create with a key and no store = %d (%s), want 400", res.status, res.body)
	}

	// Without the header the route works.
	f.createView(t, tokenAna, payments)
}
