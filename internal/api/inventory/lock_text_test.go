package inventory_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestLockRefusesAReasonThatIsNotUTF8 proves that a lock body with bytes that
// are not valid UTF-8, or with a lone surrogate escape, answers 400 and takes
// no lock. Before the check, the JSON decode replaced each bad byte and each
// lone surrogate with U+FFFD and the server stored the changed reason.
func TestLockRefusesAReasonThatIsNotUTF8(t *testing.T) {
	t.Parallel()

	const want = "invalid lock body: send the reason as valid UTF-8 text, with no invalid byte and no lone surrogate escape such as \\ud800"

	bodies := []struct{ name, body string }{
		{name: "a byte ff", body: "{\"reason\":\"a\xffb\"}"},
		{name: "a cut sequence", body: "{\"reason\":\"freeze \xc3\"}"},
		{name: "a lone high surrogate escape", body: `{"reason":"lone \ud800"}`},
		{name: "a lone low surrogate escape", body: `{"reason":"lone \udc00"}`},
	}

	for _, obj := range lockObjects() {
		for _, tc := range bodies {
			t.Run(obj.name+": "+tc.name, func(t *testing.T) {
				t.Parallel()

				f := newLockFixture(t, selectiveAuthorizer{deny: nil})

				rec := f.do(t, "alice", http.MethodPut, obj.path, tc.body)
				if rec.Code != http.StatusBadRequest || messageOf(t, rec.Body.Bytes()) != want {
					t.Fatalf("lock = %d %s, want 400 with %q", rec.Code, rec.Body.String(), want)
				}

				assertNoLock(t, f, obj.path)
			})
		}
	}
}

// TestLockKeepsASurrogatePair proves that the check refuses only a lone
// surrogate: a pair of escapes is one character, and the lock stores it.
func TestLockKeepsASurrogatePair(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})

	rec := f.do(t, "alice", http.MethodPut, lockNamespacePath, `{"reason":"freeze 🔒"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "freeze \U0001F512") {
		t.Fatalf("lock = %d %s, want 200 with the reason and U+1F512", rec.Code, rec.Body.String())
	}
}

// messageOf returns the message of an answer of the context or lock routes.
func messageOf(t *testing.T, body []byte) string {
	t.Helper()

	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode answer %s: %v", body, err)
	}

	return got.Message
}

// assertNoLock fails the test when a lock is held on path after a refusal.
func assertNoLock(t *testing.T, f lockFixture, path string) {
	t.Helper()

	if rec := f.do(t, "alice", http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
		t.Errorf("read after the refusal = %d %s, want 404", rec.Code, rec.Body.String())
	}
}
