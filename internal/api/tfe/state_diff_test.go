package tfe_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// diffTestChanged decodes one changed resource of the diff in tests.
type diffTestChanged struct {
	Address string `json:"address"`
	// ChangedPaths holds the resource-relative value paths that changed.
	ChangedPaths []string `json:"changed-paths"` //nolint:tagliatelle // The TFE wire name; it must not change.
	// ChangedSecrets holds the resource-relative paths of secrets that changed.
	ChangedSecrets []string `json:"changed-secrets"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// diffTestAttributes decodes the attributes of one diff in tests.
type diffTestAttributes struct {
	Added   []string          `json:"added"`
	Removed []string          `json:"removed"`
	Changed []diffTestChanged `json:"changed"`
}

// diffTestData decodes the data of one diff in tests.
type diffTestData struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Attributes diffTestAttributes `json:"attributes"`
}

// diffTestMeta decodes the meta of one diff in tests.
type diffTestMeta struct {
	NextCursor string `json:"next-cursor"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Incomplete bool   `json:"incomplete"`
}

// diffTestDoc decodes one diff document in tests.
type diffTestDoc struct {
	Data diffTestData `json:"data"`
	Meta diffTestMeta `json:"meta"`
}

// diffBody serves the diff route and returns its body.
func diffBody(t *testing.T, target string, want int) string {
	t.Helper()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, target))

	if rec.Code != want {
		t.Fatalf("GET %s answers %d, want %d (%s)", target, rec.Code, want, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	return rec.Body.String()
}

// decodeDiffBody decodes a diff body, failing the test otherwise.
func decodeDiffBody(t *testing.T, body string) diffTestDoc {
	t.Helper()

	var doc diffTestDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode diff body: %v", err)
	}

	return doc
}

// checkDiffNoSecret fails when the diff body holds a secret value of the
// stub states in plain text.
func checkDiffNoSecret(t *testing.T, body string) {
	t.Helper()

	for _, leaked := range []string{"s3cr3t", `"other"`, "pw-s3cr3t"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("diff body holds %q in plain text: %s", leaked, body)
		}
	}
}

// TestStateDiff proves the diff route: the second stub version against the
// first answers one added resource and the changed sensitive resource with
// the input in changed-secrets and no secret value in the body.
func TestStateDiff(t *testing.T) {
	t.Parallel()

	body := diffBody(t, "/api/v2/state-versions/sv-second123/diff?from=sv-test123", http.StatusOK)
	doc := decodeDiffBody(t, body)

	if doc.Data.ID != "sv-second123" || doc.Data.Type != "state-version-diffs" {
		t.Fatalf("diff = %+v, want the second version as state-version-diffs", doc.Data)
	}

	if len(doc.Data.Attributes.Added) != 1 || doc.Data.Attributes.Added[0] != "terraform_data.added" {
		t.Errorf("added = %q, want [terraform_data.added]", doc.Data.Attributes.Added)
	}

	if len(doc.Data.Attributes.Removed) != 0 {
		t.Errorf("removed = %q, want none", doc.Data.Attributes.Removed)
	}

	if len(doc.Data.Attributes.Changed) != 1 {
		t.Fatalf("changed = %+v, want only the smoke resource", doc.Data.Attributes.Changed)
	}

	entry := doc.Data.Attributes.Changed[0]
	if entry.Address != "terraform_data.smoke" {
		t.Fatalf("changed address = %q, want terraform_data.smoke", entry.Address)
	}

	if !slices.Contains(entry.ChangedSecrets, "/attributes/input") {
		t.Errorf("changed secrets = %q, want /attributes/input", entry.ChangedSecrets)
	}

	if len(entry.ChangedPaths) != 0 {
		t.Errorf("changed paths = %q, want none", entry.ChangedPaths)
	}

	if doc.Meta.NextCursor != "" || doc.Meta.Incomplete {
		t.Errorf("meta = %+v, want one complete page", doc.Meta)
	}

	checkDiffNoSecret(t, body)
}

// TestStateDiffPaging proves page[size] and page[cursor] of the diff: pages
// of one hold one entry each with incomplete while cut, and a size outside 1
// to 500 answers 422.
func TestStateDiffPaging(t *testing.T) {
	t.Parallel()

	first := diffBody(
		t, "/api/v2/state-versions/sv-second123/diff?from=sv-test123&page[size]=1", http.StatusOK,
	)
	firstDoc := decodeDiffBody(t, first)

	count := len(firstDoc.Data.Attributes.Added) + len(firstDoc.Data.Attributes.Removed) +
		len(firstDoc.Data.Attributes.Changed)
	if count != 1 || firstDoc.Meta.NextCursor == "" || !firstDoc.Meta.Incomplete {
		t.Fatalf("first page = %+v, want one entry with a next cursor and incomplete", firstDoc)
	}

	second := diffBody(
		t,
		"/api/v2/state-versions/sv-second123/diff?from=sv-test123&page[size]=1&page[cursor]="+firstDoc.Meta.NextCursor,
		http.StatusOK,
	)
	secondDoc := decodeDiffBody(t, second)

	secondCount := len(secondDoc.Data.Attributes.Added) + len(secondDoc.Data.Attributes.Removed) +
		len(secondDoc.Data.Attributes.Changed)
	if secondCount != 1 || secondDoc.Meta.NextCursor != "" || secondDoc.Meta.Incomplete {
		t.Fatalf("second page = %+v, want the other entry with a closed page", secondDoc)
	}

	checkDiffNoSecret(t, first)
	checkDiffNoSecret(t, second)

	for _, target := range []string{
		"/api/v2/state-versions/sv-second123/diff?from=sv-test123&page[size]=0",
		"/api/v2/state-versions/sv-second123/diff?from=sv-test123&page[size]=501",
		"/api/v2/state-versions/sv-second123/diff?from=sv-test123&page[size]=many",
	} {
		handler := newStateHandler(t, allowAll())
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, stateRequest(t, target))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("GET %s answers %d, want %d", target, rec.Code, http.StatusUnprocessableEntity)
		}

		checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
	}
}

// TestStateDiffErrors proves the diff error cases: a missing from value
// answers 422, an unknown from version and a from version of another
// workspace answer 404, and a state either side encrypted answers 409 with
// encrypted-by-client.
func TestStateDiffErrors(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, stateRequest(t, "/api/v2/state-versions/sv-second123/diff"))

	if missing.Code != http.StatusUnprocessableEntity {
		t.Errorf("diff without from answers %d, want %d", missing.Code, http.StatusUnprocessableEntity)
	}

	checkJSONAPIError(t, missing, http.StatusUnprocessableEntity)

	for _, target := range []string{
		"/api/v2/state-versions/sv-second123/diff?from=sv-missing",
		"/api/v2/state-versions/sv-missing/diff?from=sv-test123",
		"/api/v2/state-versions/sv-second123/diff?from=sv-other123",
	} {
		body := diffBody(t, target, http.StatusNotFound)
		checkJSONAPINotFound(t, body, target)
	}

	for _, target := range []string{
		"/api/v2/state-versions/sv-encrypted123/diff?from=sv-test123",
		"/api/v2/state-versions/sv-second123/diff?from=sv-encrypted123",
	} {
		body := diffBody(t, target, http.StatusConflict)

		if !strings.Contains(body, "encrypted-by-client") {
			t.Errorf("GET %s body lacks encrypted-by-client: %s", target, body)
		}

		for _, leaked := range []string{"ciphertext", "sealed"} {
			if strings.Contains(body, leaked) {
				t.Errorf("GET %s body holds %q: %s", target, leaked, body)
			}
		}
	}
}

// diffErrorItem is one entry of a JSON:API error document in diff tests.
type diffErrorItem struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// diffErrorDoc decodes a JSON:API error document in diff tests.
type diffErrorDoc struct {
	Errors []diffErrorItem `json:"errors"`
}

// checkJSONAPINotFound proves a 404 diff body is a JSON:API error without a
// secret value.
func checkJSONAPINotFound(t *testing.T, body, target string) {
	t.Helper()

	var doc diffErrorDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("GET %s: decode error body: %v", target, err)
	}

	if len(doc.Errors) == 0 {
		t.Fatalf("GET %s error document holds no errors", target)
	}
}

// TestStateDiffDenied proves the diff route needs unit:read: a caller
// without it answers 403.
func TestStateDiffDenied(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, selectiveAuthorizer{
		deny: map[identity.Action]bool{identity.ActionUnitRead: true},
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, stateRequest(t, "/api/v2/state-versions/sv-second123/diff?from=sv-test123"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("GET diff answers %d, want %d", rec.Code, http.StatusForbidden)
	}
}
