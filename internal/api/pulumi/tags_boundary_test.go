package pulumi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// tagBoundaryBodies are tag maps that the HTTP boundary must refuse.
func tagBoundaryBodies() map[string]string {
	many := make([]string, 0, 65)
	for i := range 65 {
		many = append(many, `"k`+strings.Repeat("0", i%5)+string(rune('a'+i%26))+strings.Repeat("1", i/26)+`":"v"`)
	}

	return map[string]string{
		"CR LF in the name":     `{"evil\r\nInjected: yes":"v"}`,
		"zero-width in name":    `{"a\u200bb":"v"}`,
		"empty name":            `{"":"v"}`,
		"41-byte name":          `{"` + strings.Repeat("k", 41) + `":"v"}`,
		"space in the name":     `{"a b":"v"}`,
		"value over 256 bytes":  `{"k":"` + strings.Repeat("v", 257) + `"}`,
		"value with NUL":        `{"k":"a\u0000b"}`,
		"value with CR":         `{"k":"a\rb"}`,
		"value with ESC":        `{"k":"a\u001bb"}`,
		"value with U+202E":     `{"k":"a\u202eb"}`,
		"more than 64 tags":     "{" + strings.Join(many, ",") + "}",
		"tag name over 100 KiB": `{"` + strings.Repeat("k", 100000) + `":"v"}`,
	}
}

// checkTagRefusal fails the test unless rec is the 400 answer of the handler
// boundary check, in the error shape of the Pulumi Cloud-compatible API.
// The message starts with "tags: ": the stack service words its refusal
// differently, so this proves the handler refused the request itself.
func checkTagRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("hostile tags answer %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var got struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("answer %q is not the Pulumi error shape: %v", rec.Body.String(), err)
	}

	if got.Code != http.StatusBadRequest || !strings.HasPrefix(got.Message, "tags: ") {
		t.Fatalf("answer = %+v, want code 400 and a message that starts with %q", got, "tags: ")
	}

	if strings.ContainsAny(got.Message, "\r\n") || strings.Contains(got.Message, "Injected") {
		t.Fatalf("message %q holds the hostile input", got.Message)
	}
}

// TestTagBoundaryRefusesHostileTags proves the handler refuses hostile tags
// on the tag update route and on the stack create route, with the 400 error
// shape of the Pulumi API, before any store call.
func TestTagBoundaryRefusesHostileTags(t *testing.T) {
	t.Parallel()

	for name, tags := range tagBoundaryBodies() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPatch, "/api/stacks/acme/web/prod/tags", strings.NewReader(tags),
			)
			req.Header.Set("Authorization", "token test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			checkTagRefusal(t, rec)

			create := `{"stackName":"prod","tags":` + tags + `}`
			req = httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/api/stacks/acme/web", strings.NewReader(create),
			)
			req.Header.Set("Authorization", "token test-token")
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			checkTagRefusal(t, rec)

			if len(stores.tags) != 0 || len(stores.created) != 0 {
				t.Fatalf("store calls: %d tag updates, %d creates, want 0 and 0", len(stores.tags), len(stores.created))
			}
		})
	}
}

// TestTagBoundaryAcceptsMultiLineDescription proves the handler passes a tag
// value with a line break and a tab, as the Pulumi CLI sends for a block
// scalar description in Pulumi.yaml.
func TestTagBoundaryAcceptsMultiLineDescription(t *testing.T) {
	t.Parallel()

	handler, stores := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch, "/api/stacks/acme/web/prod/tags",
		strings.NewReader(`{"pulumi:description":"a\nb\tc"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("multi-line description answers %d (%s), want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	if len(stores.tags) != 1 || stores.tags[0].tags["pulumi:description"] != "a\nb\tc" {
		t.Fatalf("stored tags = %v, want the description unchanged", stores.tags)
	}
}

// TestTagBoundaryAgreesWithStackLimits proves the handler and pkg/stack accept
// and refuse the same tag maps at each limit: the tag count, the name length,
// the value length and the hidden characters of a value. A limit that
// changes in one layer only fails here.
func TestTagBoundaryAgreesWithStackLimits(t *testing.T) {
	t.Parallel()

	many := func(n int) map[string]string {
		tags := make(map[string]string, n)
		for i := range n {
			tags["k"+strconv.Itoa(i)] = "v"
		}

		return tags
	}

	cases := []struct {
		name string
		tags map[string]string
		ok   bool
	}{
		{"64 tags", many(64), true},
		{"65 tags", many(65), false},
		{"40-byte name", map[string]string{strings.Repeat("k", 40): "v"}, true},
		{"41-byte name", map[string]string{strings.Repeat("k", 41): "v"}, false},
		{"256-byte value", map[string]string{"k": strings.Repeat("v", 256)}, true},
		{"257-byte value", map[string]string{"k": strings.Repeat("v", 257)}, false},
		{"line break and tab", map[string]string{"k": "a\nb\tc"}, true},
		{"NUL", map[string]string{"k": "a\x00b"}, false},
		{"CR", map[string]string{"k": "a\rb"}, false},
		{"U+202E", map[string]string{"k": "a\u202eb"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := stack.CheckTags(tc.tags) == nil; got != tc.ok {
				t.Fatalf("stack.CheckTags accepts = %v, want %v", got, tc.ok)
			}

			body, err := json.Marshal(tc.tags)
			if err != nil {
				t.Fatalf("marshal tags: %v", err)
			}

			handler, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPatch, "/api/stacks/acme/web/prod/tags", strings.NewReader(string(body)),
			)
			req.Header.Set("Authorization", "token test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if got := rec.Code == http.StatusOK; got != tc.ok {
				t.Fatalf("handler answers %d (%s), accepts = %v, want %v", rec.Code, rec.Body.String(), got, tc.ok)
			}
		})
	}
}
