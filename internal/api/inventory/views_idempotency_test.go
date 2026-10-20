package inventory_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

const (
	keyHeader      = "Idempotency-Key"
	replayedHeader = "Idempotent-Replayed"
)

// TestViewCreateReplayKeepsTheIDAndNotTheView proves the record of a create
// keeps the ID of the view and not the view. The first request makes the view.
// A replay with the same key and the same body makes no second view, and it
// answers 409 with the ID of the first.
func TestViewCreateReplayKeepsTheIDAndNotTheView(t *testing.T) {
	t.Parallel()

	f := newViewFixture(t)

	first := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")
	if first.status != http.StatusCreated {
		t.Fatalf("first create = %d (%s)", first.status, first.body)
	}

	id := f.createViewIDFromBody(t, first.body)

	replay := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")
	if replay.status != http.StatusConflict || !strings.Contains(replay.body, id) ||
		!strings.Contains(replay.body, "request.idempotency-key-replayed-secret") {
		t.Fatalf("replay = %d (%s), want 409, the code and the ID %s", replay.status, replay.body, id)
	}

	// The answer of the replay holds the ID and no part of the view.
	if strings.Contains(replay.body, "Payments") || strings.Contains(replay.body, "payments") {
		t.Fatalf("the replay answer holds the view: %s", replay.body)
	}

	// One view only, and one create in the audit log.
	list := f.do(t, http.MethodGet, viewsPath, tokenAna, "")
	if strings.Count(list.body, `"id"`) != 1 {
		t.Fatalf("list after the replay = %s, want one view", list.body)
	}

	var creates int

	for _, e := range f.auditEntries(t) {
		if e.Action == "view:create" {
			creates++
		}
	}

	if creates != 1 {
		t.Fatalf("%d create entries in the audit log, want 1", creates)
	}
}

func (f *viewFixture) createViewIDFromBody(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, `"id":"`)
	if !ok {
		t.Fatalf("no ID in %s", body)
	}

	id, _, _ := strings.Cut(after, `"`)

	return id
}

func TestViewIdempotencyRules(t *testing.T) {
	t.Parallel()

	t.Run("the same key with another body answers 422", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)
		f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")

		res := f.do(t, http.MethodPost, viewsPath, tokenAna, `{"name":"Other"}`, keyHeader, "key-1")
		if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, "request.idempotency-key-reused") {
			t.Fatalf("reused key = %d (%s), want 422", res.status, res.body)
		}
	})

	t.Run("a key is not shared between people", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)
		f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")

		if res := f.do(t, http.MethodPost, viewsPath, tokenBob, payments, keyHeader, "key-1"); res.status != http.StatusCreated {
			t.Fatalf("the same key from another person = %d (%s), want a new view", res.status, res.body)
		}
	})

	t.Run("a new principal with the same subject does not replay the key of the old one", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)
		f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")
		f.people.replace("ana", "prn-anaanaanaanaanaana9")

		res := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")
		if res.status != http.StatusCreated {
			t.Fatalf("create by the new principal = %d (%s), want 201 and no replay of the old ID", res.status, res.body)
		}
	})

	t.Run("a refused create is not stored", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)

		if res := f.do(t, http.MethodPost, viewsPath, tokenAna, `{"name":""}`, keyHeader, "key-1"); res.status != http.StatusBadRequest {
			t.Fatalf("invalid create = %d", res.status)
		}

		// The same key works after the fix.
		if res := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1"); res.status != http.StatusCreated {
			t.Fatalf("create with the same key after a 400 = %d (%s), want 201", res.status, res.body)
		}
	})

	t.Run("a delete replays the 204", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)
		id := f.createView(t, tokenAna, payments)

		first := f.do(t, http.MethodDelete, viewsPath+"/"+id, tokenAna, "", keyHeader, "del-1")
		replay := f.do(t, http.MethodDelete, viewsPath+"/"+id, tokenAna, "", keyHeader, "del-1")

		if first.status != http.StatusNoContent || replay.status != http.StatusNoContent ||
			replay.header.Get(replayedHeader) != "true" {
			t.Fatalf("delete = %d, replay = %d (replayed %q), want 204 twice", first.status, replay.status,
				replay.header.Get(replayedHeader))
		}
	})

	t.Run("an invalid key answers 400", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)

		for _, key := range []string{"", strings.Repeat("k", 256), "key\twith tab"} {
			res := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, key)
			if res.status != http.StatusBadRequest {
				t.Errorf("key %q = %d (%s), want 400", key, res.status, res.body)
			}
		}
	})

	t.Run("a replay checks the action again", func(t *testing.T) {
		t.Parallel()

		f := newViewFixture(t)
		f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1")

		f.authz.denyAction[identity.ActionViewCreate] = true

		if res := f.do(t, http.MethodPost, viewsPath, tokenAna, payments, keyHeader, "key-1"); res.status != http.StatusForbidden {
			t.Fatalf("replay without the action = %d, want 403", res.status)
		}
	})

	t.Run("a server with no replay store refuses the header", func(t *testing.T) {
		t.Parallel()

		f := newViewFixtureWith(t, viewFixtureOptions{views: nil, noIdempotency: true})

		for _, call := range []struct{ method, target, body string }{
			{http.MethodPost, viewsPath, payments},
		} {
			res := f.do(t, call.method, call.target, tokenAna, call.body, keyHeader, "key-1")
			if res.status != http.StatusBadRequest {
				t.Fatalf("%s with a key and no store = %d (%s), want 400", call.method, res.status, res.body)
			}
		}

		// Without the header the route works.
		f.createView(t, tokenAna, payments)
	})
}
