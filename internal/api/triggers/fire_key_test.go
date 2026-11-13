package triggers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
)

// TestFireKeyReachesTheDispatcher proves: a valid key and the request reach the dispatcher.
func TestFireKeyReachesTheDispatcher(t *testing.T) {
	t.Parallel()

	fire := basePath + "/trg-seed/fire"

	r := fullRig(t)
	seed(r)

	rec := serve(t, r.h, request{
		method: http.MethodPost, path: fire, token: testToken, body: "",
		header: map[string]string{"Idempotency-Key": "click-1", "User-Agent": "probe-agent/1"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("fire = %d %s, want 201", rec.Code, rec.Body.String())
	}

	r.disp.mu.Lock()
	defer r.disp.mu.Unlock()

	if len(r.disp.reqs) != 1 {
		t.Fatalf("fires = %d, want 1", len(r.disp.reqs))
	}

	got := r.disp.reqs[0]
	if got.IdempotencyKey != "click-1" {
		t.Errorf("key = %q, want click-1", got.IdempotencyKey)
	}

	if got.Request.Channel != audit.ChannelAPI || got.Request.UserAgent != "probe-agent/1" ||
		got.Request.SourceIP == "" {
		t.Errorf("request = %+v, want the channel, the user agent and the address of the request", got.Request)
	}
}

// TestFireWithoutAKey proves: no key gives an empty key.
func TestFireWithoutAKey(t *testing.T) {
	t.Parallel()

	fire := basePath + "/trg-seed/fire"

	r := fullRig(t)
	seed(r)

	rec := serve(t, r.h, request{method: http.MethodPost, path: fire, token: testToken, body: "", header: nil})
	if rec.Code != http.StatusCreated {
		t.Fatalf("fire = %d %s, want 201", rec.Code, rec.Body.String())
	}

	r.disp.mu.Lock()
	defer r.disp.mu.Unlock()

	if got := r.disp.reqs[0].IdempotencyKey; got != "" {
		t.Errorf("key = %q, want none", got)
	}
}

// TestFireRefusesAKeyThatIsNotValid proves: a key that is not valid fires nothing.
func TestFireRefusesAKeyThatIsNotValid(t *testing.T) {
	t.Parallel()

	fire := basePath + "/trg-seed/fire"

	for name, key := range map[string]string{
		"too long":      strings.Repeat("k", idempotency.MaxKeyLength+1),
		"not printable": "k\x01k",
	} {
		r := fullRig(t)
		seed(r)

		rec := serve(t, r.h, request{
			method: http.MethodPost, path: fire, token: testToken, body: "",
			header: map[string]string{"Idempotency-Key": key},
		})

		if rec.Code != http.StatusBadRequest ||
			!strings.Contains(rec.Body.String(), "request.idempotency-key-invalid") {
			t.Errorf("%s: fire = %d %s, want 400 request.idempotency-key-invalid", name, rec.Code, rec.Body.String())
		}

		if r.disp.fires() != 0 {
			t.Errorf("%s: a key that is not valid fired", name)
		}
	}
}

// TestFireRefusesTwoKeyHeaders proves: two key headers fire nothing.
func TestFireRefusesTwoKeyHeaders(t *testing.T) {
	t.Parallel()

	fire := basePath + "/trg-seed/fire"

	r := fullRig(t)
	seed(r)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fire, strings.NewReader(""))
	req.Header.Set("Authorization", "token "+testToken)
	req.Header.Add("Idempotency-Key", "a")
	req.Header.Add("Idempotency-Key", "b")

	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || r.disp.fires() != 0 {
		t.Errorf("fire = %d %s after %d fires, want 400 and none", rec.Code, rec.Body.String(), r.disp.fires())
	}
}

// TestDeleteRemovesARowThatFailsItsChecks proves a delete reaches a stored row
// that Get refuses, and that every other route still refuses the row.
func TestDeleteRemovesARowThatFailsItsChecks(t *testing.T) {
	t.Parallel()

	bad := func(r *rig) {
		seed(r)

		tr := r.store.triggers["trg-seed"]
		tr.Frequency.Cron = "0 6 * * *" // a preset and a cron expression: not valid.
		r.store.plant(tr)
	}

	r := fullRig(t)
	bad(r)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := basePath + "/trg-seed"
		if method == http.MethodPost {
			path += "/fire"
		}

		rec := serve(t, r.h, request{method: method, path: path, token: testToken, body: "", header: nil})
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated || r.disp.fires() != 0 {
			t.Errorf("%s of a row that fails its checks = %d %s, want a refusal", method, rec.Code, rec.Body.String())
		}
	}

	rec := serve(t, r.h, request{
		method: http.MethodDelete, path: basePath + "/trg-seed", token: testToken, body: "", header: nil,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", rec.Code, rec.Body.String())
	}

	if _, ok := r.store.triggers["trg-seed"]; ok {
		t.Error("the row is still stored after the delete")
	}
}

// TestFireRefusesAReservedKeyPrefix proves: a key with the prefix of the drift
// checks of the server, in any case, fires nothing, and a key with the prefix
// inside or no prefix fires.
func TestFireRefusesAReservedKeyPrefix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"lowercase prefix", "drift-unit-1-29000000", http.StatusBadRequest},
		{"mixed case prefix", "Drift-unit-1", http.StatusBadRequest},
		{"uppercase prefix", "DRIFT-x", http.StatusBadRequest},
		{"prefix inside", "my-drift-1", http.StatusCreated},
		{"plain key", "click-1", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)
			seed(r)

			rec := serve(t, r.h, request{
				method: http.MethodPost, path: basePath + "/trg-seed/fire", token: testToken, body: "",
				header: map[string]string{"Idempotency-Key": tc.key},
			})

			if rec.Code != tc.want {
				t.Fatalf("fire = %d %s, want %d", rec.Code, rec.Body.String(), tc.want)
			}

			if tc.want == http.StatusBadRequest {
				if !strings.Contains(rec.Body.String(), "request.idempotency-key-invalid") || r.disp.fires() != 0 {
					t.Errorf("body = %s, fires = %d, want the key error and no fire", rec.Body.String(), r.disp.fires())
				}
			} else if r.disp.fires() != 1 {
				t.Errorf("fires = %d, want the fire", r.disp.fires())
			}
		})
	}
}
