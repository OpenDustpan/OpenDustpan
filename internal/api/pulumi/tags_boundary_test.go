package pulumi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
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
