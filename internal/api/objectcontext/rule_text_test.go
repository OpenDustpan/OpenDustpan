package objectcontext_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPatchNamesTheRuleOfAnInvalidField proves that a PATCH with a field out
// of its bounds answers 400 with the field and the rule only: no call chain of
// the service or the store, so every store adapter gives the same text.
func TestPatchNamesTheRuleOfAnInvalidField(t *testing.T) {
	t.Parallel()

	const nsPath = "/api/dustpan/v1/namespaces/acme/context"

	cases := []struct {
		name, body, want string
	}{
		{
			name: "a summary over 2048 bytes",
			body: `{"summary":"` + strings.Repeat("x", 2049) + `"}`,
			want: "invalid context: summary must be valid UTF-8 of 2048 bytes or fewer, " +
				"with no control character other than a line break or a tab",
		},
		{
			name: "a summary with a form feed",
			body: `{"summary":"page \f"}`,
			want: "invalid context: summary must be valid UTF-8 of 2048 bytes or fewer, " +
				"with no control character other than a line break or a tab",
		},
		{
			name: "an on-call text with a line break",
			body: `{"onCall":"a\nb"}`,
			want: "invalid context: onCall must be valid UTF-8 of 500 bytes or fewer, with no control character",
		},
		{
			name: "a link of an unknown kind",
			body: `{"links":[{"title":"a","url":"https://x.example","kind":"nope"}]}`,
			want: `invalid context: link kind "nope" must be one of docs, runbook, dashboard, repository and other`,
		},
		{
			name: "a link with no title",
			body: `{"links":[{"title":" ","url":"https://x.example","kind":"docs"}]}`,
			want: "invalid context: each link needs a title of 1 to 200 bytes, with no control character",
		},
		{
			name: "a group owner with an invalid name",
			body: `{"owners":["group:"]}`,
			want: `invalid context: owner "group:" must name a group with a valid name after "group:"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			rec := serve(t, newHandler(t, newAuthorizer("namespace:read acme", "namespace:set-context acme"), store),
				request{method: http.MethodPatch, path: nsPath, token: testToken, body: tc.body})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d %s, want 400", rec.Code, rec.Body.String())
			}

			var got struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode answer %s: %v", rec.Body.String(), err)
			}

			if got.Message != tc.want {
				t.Errorf("message = %q, want %q", got.Message, tc.want)
			}
		})
	}
}
