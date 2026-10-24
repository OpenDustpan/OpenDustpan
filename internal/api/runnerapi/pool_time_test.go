package runnerapi_test

import (
	"net/http"
	"testing"
)

// poolTimes is the ID and the times of a runner pool in an answer.
type poolTimes struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// TestPoolCreateTimesEqualTheReadTimes proves that the createdAt and the
// updatedAt of the answer of a pool create are the times that a later read of
// the pool gives, so a client that compares the two answers sees one time.
func TestPoolCreateTimesEqualTheReadTimes(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	status, body := e.admin(http.MethodPost, "/api/runners/acme/pools",
		map[string]any{"name": "prod", "purpose": "jobs"})
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var created poolTimes

	decode(t, body, &created)

	status, body = e.admin(http.MethodGet, "/api/runners/acme/pools/"+created.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("read pool = %d %s, want 200", status, body)
	}

	var read poolTimes

	decode(t, body, &read)

	if read != created {
		t.Fatalf("read answers %+v, want the times of the create answer %+v", read, created)
	}
}

// TestPoolPatchAndJoinTokenTimesEqualTheReadTimes proves that the updatedAt of
// the answer of a pool patch, and the createdAt and the expiresAt of the answer
// of a join token create, are the times that a later read gives.
func TestPoolPatchAndJoinTokenTimesEqualTheReadTimes(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	status, body := e.admin(http.MethodPost, "/api/runners/acme/pools",
		map[string]any{"name": "prod", "purpose": "jobs"})
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var created poolTimes

	decode(t, body, &created)

	status, body = e.admin(http.MethodPatch, "/api/runners/acme/pools/"+created.ID, map[string]any{"concurrency": 2})
	if status != http.StatusOK {
		t.Fatalf("patch pool = %d %s, want 200", status, body)
	}

	var patched poolTimes

	decode(t, body, &patched)

	status, body = e.admin(http.MethodGet, "/api/runners/acme/pools/"+created.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("read pool = %d %s, want 200", status, body)
	}

	var read poolTimes

	decode(t, body, &read)

	if read != patched {
		t.Fatalf("read answers %+v, want the times of the patch answer %+v", read, patched)
	}

	status, body = e.admin(http.MethodPost, "/api/runners/acme/pools/"+created.ID+"/join-tokens", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("create join token = %d %s, want 201", status, body)
	}

	var token joinTokenTimes

	decode(t, body, &token)

	status, body = e.admin(http.MethodGet, "/api/runners/acme/pools/"+created.ID+"/join-tokens", nil)
	if status != http.StatusOK {
		t.Fatalf("list join tokens = %d %s, want 200", status, body)
	}

	var listing struct {
		JoinTokens []joinTokenTimes `json:"joinTokens"`
	}

	decode(t, body, &listing)

	if len(listing.JoinTokens) != 1 || listing.JoinTokens[0] != token {
		t.Fatalf("list answers %+v, want the times of the create answer %+v", listing.JoinTokens, token)
	}
}

// joinTokenTimes is the ID and the times of a join token in an answer.
type joinTokenTimes struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}
