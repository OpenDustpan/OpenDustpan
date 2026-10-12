package tfe_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// resourceTestAttributes decodes one masked resource of the list in tests.
type resourceTestAttributes struct {
	Address  string `json:"address"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Module   string `json:"module"`
	Changed  bool   `json:"changed"`
}

// resourceTestItem decodes one entry of the masked resource list in tests.
type resourceTestItem struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Attributes resourceTestAttributes `json:"attributes"`
}

// resourceTestMeta decodes the meta of the masked resource list in tests.
type resourceTestMeta struct {
	NextCursor string `json:"next-cursor"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Incomplete bool   `json:"incomplete"`
}

// resourceTestList decodes the masked resource list document in tests.
type resourceTestList struct {
	Data []resourceTestItem `json:"data"`
	Meta resourceTestMeta   `json:"meta"`
}

// detailTestInstance decodes one masked instance in tests.
type detailTestInstance struct {
	Attributes map[string]any `json:"attributes"`
}

// detailTestAttributes decodes one masked resource in tests.
type detailTestAttributes struct {
	Address   string               `json:"address"`
	Type      string               `json:"type"`
	Provider  string               `json:"provider"`
	Module    string               `json:"module"`
	Instances []detailTestInstance `json:"instances"`
	Masked    []string             `json:"masked"`
}

// detailTestItem decodes the resource of the detail document in tests.
type detailTestItem struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Attributes detailTestAttributes `json:"attributes"`
}

// detailTestDoc decodes the one masked resource document in tests.
type detailTestDoc struct {
	Data detailTestItem `json:"data"`
}

// resourceResponse serves a masked resource route and fails for any status
// but want. It returns the recorder for error document checks.
func resourceResponse(t *testing.T, target string, want int) *httptest.ResponseRecorder {
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

	return rec
}

// resourceBody serves a masked resource route and returns its body.
func resourceBody(t *testing.T, target string, want int) string {
	t.Helper()

	return resourceResponse(t, target, want).Body.String()
}

// checkNoSecret fails when body holds a secret value of the stub state in
// plain text.
func checkNoSecret(t *testing.T, body string) {
	t.Helper()

	for _, leaked := range []string{"s3cr3t", "pw-s3cr3t"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("masked body holds %q in plain text: %s", leaked, body)
		}
	}
}

// decodeResourceList decodes a masked resource list body, failing the test
// otherwise.
func decodeResourceList(t *testing.T, body string) resourceTestList {
	t.Helper()

	var doc resourceTestList

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource list: %v", err)
	}

	return doc
}

// checkFirstListResource proves the first listed resource is the data
// resource as stored.
func checkFirstListResource(t *testing.T, first resourceTestItem) {
	t.Helper()

	if first.ID != "terraform_data.smoke" || first.Type != "state-version-resources" ||
		first.Attributes.Address != "terraform_data.smoke" || first.Attributes.Type != "terraform_data" ||
		first.Attributes.Provider != `provider["terraform.io/builtin/terraform"]` || first.Attributes.Module != "" {
		t.Errorf("first resource = %+v, want the data resource as stored", first)
	}
}

// TestListStateResources proves the masked resource list route: the two stub
// resources with address, type, provider and module, no attribute value, and
// the meta with the cursor and the completeness.
func TestListStateResources(t *testing.T) {
	t.Parallel()

	body := resourceBody(t, "/api/v2/state-versions/sv-test123/resources", http.StatusOK)
	doc := decodeResourceList(t, body)

	if len(doc.Data) != 2 {
		t.Fatalf("listed resources = %+v, want 2", doc.Data)
	}

	checkFirstListResource(t, doc.Data[0])

	if !doc.Data[0].Attributes.Changed || !doc.Data[1].Attributes.Changed {
		t.Errorf("listed resources = %+v, want every resource new without a previous version", doc.Data)
	}

	if doc.Meta.NextCursor != "" || doc.Meta.Incomplete {
		t.Errorf("meta = %+v, want one complete page", doc.Meta)
	}

	checkNoSecret(t, body)
}

// TestListStateResourcesPaging proves page[size] and page[cursor] of the
// masked resource list: pages of one hold one resource each, and a size
// outside 1 to 500 answers 422.
func TestListStateResourcesPaging(t *testing.T) {
	t.Parallel()

	first := resourceBody(t, "/api/v2/state-versions/sv-test123/resources?page[size]=1", http.StatusOK)

	var firstPage resourceTestList

	if err := json.Unmarshal([]byte(first), &firstPage); err != nil {
		t.Fatalf("decode first page: %v", err)
	}

	if len(firstPage.Data) != 1 || firstPage.Meta.NextCursor == "" {
		t.Fatalf("first page = %+v, want one resource with a next cursor", firstPage)
	}

	second := resourceBody(
		t,
		"/api/v2/state-versions/sv-test123/resources?page[size]=1&page[cursor]="+firstPage.Meta.NextCursor,
		http.StatusOK,
	)

	var secondPage resourceTestList

	if err := json.Unmarshal([]byte(second), &secondPage); err != nil {
		t.Fatalf("decode second page: %v", err)
	}

	if len(secondPage.Data) != 1 || secondPage.Data[0].ID == firstPage.Data[0].ID ||
		secondPage.Meta.NextCursor != "" {
		t.Fatalf("second page = %+v, want the other resource with no next cursor", secondPage)
	}

	for _, target := range []string{
		"/api/v2/state-versions/sv-test123/resources?page[size]=0",
		"/api/v2/state-versions/sv-test123/resources?page[size]=501",
		"/api/v2/state-versions/sv-test123/resources?page[size]=many",
	} {
		rec := resourceResponse(t, target, http.StatusUnprocessableEntity)
		checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)
	}
}

// decodeResourceDetail decodes one masked resource body, failing the test
// otherwise.
func decodeResourceDetail(t *testing.T, body string) detailTestDoc {
	t.Helper()

	var doc detailTestDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource detail: %v", err)
	}

	if doc.Data.ID != "terraform_data.smoke" || doc.Data.Type != "state-version-resources" ||
		doc.Data.Attributes.Address != "terraform_data.smoke" || len(doc.Data.Attributes.Instances) != 1 {
		t.Fatalf("detail = %+v, want the data resource with one instance", doc.Data)
	}

	return doc
}

// checkDetailMarkerPath proves key of attributes holds a secret marker for
// the value path want.
func checkDetailMarkerPath(t *testing.T, attributes map[string]any, key, want string) {
	t.Helper()

	marker := checkDetailMarker(t, attributes, key)
	if marker["path"] != want {
		t.Errorf("marker path = %v, want %q", marker["path"], want)
	}
}

// TestGetStateResource proves the one masked resource route: the sensitive
// input answers as a marker with its value path, the derived output answers
// masked as a duplicate, the plain id stays, and Masked names each marker
// path. An unknown address answers 404.
func TestGetStateResource(t *testing.T) {
	t.Parallel()

	body := resourceBody(
		t, "/api/v2/state-versions/sv-test123/resources/terraform_data.smoke", http.StatusOK,
	)

	doc := decodeResourceDetail(t, body)
	attributes := doc.Data.Attributes.Instances[0].Attributes

	if attributes["id"] != "smoke-id" {
		t.Errorf("id = %v, want the plain value", attributes["id"])
	}

	checkDetailMarkerPath(t, attributes, "input", "/resources/0/instances/0/attributes/input")
	checkDetailMarkerPath(t, attributes, "output", "/resources/0/instances/0/attributes/output")

	want := []string{
		"/resources/0/instances/0/attributes/input",
		"/resources/0/instances/0/attributes/output",
	}

	if len(doc.Data.Attributes.Masked) != len(want) || doc.Data.Attributes.Masked[0] != want[0] ||
		doc.Data.Attributes.Masked[1] != want[1] {
		t.Errorf("masked = %q, want %q", doc.Data.Attributes.Masked, want)
	}

	checkNoSecret(t, body)

	resourceResponse(t, "/api/v2/state-versions/sv-test123/resources/missing.none", http.StatusNotFound)
	resourceResponse(t, "/api/v2/state-versions/sv-missing/resources/terraform_data.smoke", http.StatusNotFound)
}

// checkDetailMarker proves key of attributes holds a secret marker, failing
// the test otherwise. It returns the marker.
func checkDetailMarker(t *testing.T, attributes map[string]any, key string) map[string]any {
	t.Helper()

	marker, ok := attributes[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want a secret marker", key, attributes[key])
	}

	secret, ok := marker["secret"].(bool)
	if !ok || !secret {
		t.Fatalf("%s = %v, want a secret marker", key, attributes[key])
	}

	return marker
}

// TestGetStateResourceWithoutMarks proves the fallback at the route: a
// version with no kept json-state marks every attribute value, including the
// plain id.
func TestGetStateResourceWithoutMarks(t *testing.T) {
	t.Parallel()

	body := resourceBody(
		t, "/api/v2/state-versions/sv-nomarks123/resources/terraform_data.smoke", http.StatusOK,
	)

	var doc detailTestDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode resource detail: %v", err)
	}

	attributes := doc.Data.Attributes.Instances[0].Attributes

	checkDetailMarker(t, attributes, "id")

	for _, key := range []string{"input", "output"} {
		wrapped, ok := attributes[key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want the wrapped object", key, attributes[key])
		}

		checkDetailMarker(t, wrapped, "value")
	}

	if len(doc.Data.Attributes.Masked) != 6 {
		t.Errorf("masked = %q, want every attribute leaf", doc.Data.Attributes.Masked)
	}

	checkNoSecret(t, body)
}

// TestStateResourceClientEncrypted proves a state the client encrypted
// answers 409 with encrypted-by-client on both resource routes.
func TestStateResourceClientEncrypted(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"/api/v2/state-versions/sv-encrypted123/resources",
		"/api/v2/state-versions/sv-encrypted123/resources/terraform_data.smoke",
	} {
		body := resourceBody(t, target, http.StatusConflict)

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

// TestStateResourceDenied proves both resource routes need unit:read: a
// caller without it answers 403.
func TestStateResourceDenied(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, selectiveAuthorizer{
		deny: map[identity.Action]bool{identity.ActionUnitRead: true},
	})

	for _, target := range []string{
		"/api/v2/state-versions/sv-test123/resources",
		"/api/v2/state-versions/sv-test123/resources/terraform_data.smoke",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, stateRequest(t, target))

		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s answers %d, want %d", target, rec.Code, http.StatusForbidden)
		}
	}
}
