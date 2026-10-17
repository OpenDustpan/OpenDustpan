package tfe_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/baseurl"
)

// outputNameDoc returns a json-state-outputs document with one output
// called name.
func outputNameDoc(name string) string {
	return `{"` + name + `":{"sensitive":false,"value":"hello","type":"string"}}`
}

// outputNameDetail returns the detail of the first JSON:API error of rec.
func outputNameDetail(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) == 0 {
		t.Fatal("error document holds no errors")
	}

	return doc.Errors[0].Detail
}

// TestCreateStateVersionRefusesOutputNameOverLimit proves a create with an
// output name of 257 bytes answers 422 naming the limit, so a longer name
// is refused instead of stored.
func TestCreateStateVersionRefusesOutputNameOverLimit(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, outputNameDoc(strings.Repeat("o", 257))),
		),
	)

	checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)

	if got := outputNameDetail(t, rec); !strings.Contains(got, "output name over 256 bytes") {
		t.Fatalf("error detail = %q, want it to name the 256-byte output name limit", got)
	}
}

// TestCreateStateVersionRefusesWrappedOutputNameOverLimit proves a create
// with the wrapped outputs shape and an output name of 257 bytes answers
// 422 too.
func TestCreateStateVersionRefusesWrappedOutputNameOverLimit(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, `{"outputs":`+outputNameDoc(strings.Repeat("o", 257))+`}`),
		),
	)

	checkJSONAPIError(t, rec, http.StatusUnprocessableEntity)

	if got := outputNameDetail(t, rec); !strings.Contains(got, "output name over 256 bytes") {
		t.Fatalf("error detail = %q, want it to name the 256-byte output name limit", got)
	}
}

// TestCreateStateVersionAcceptsOutputNameAtLimit proves a create with an
// output name of exactly 256 bytes answers 201.
func TestCreateStateVersionAcceptsOutputNameAtLimit(t *testing.T) {
	t.Parallel()

	handler := newWriteHandler(t, stubWorkspaces{}, baseurl.Resolver{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		createStateRequest(
			t,
			"/api/v2/workspaces/ws-test123/state-versions",
			stateCreateBody(4, outputNameDoc(strings.Repeat("o", 256))),
		),
	)

	decodeCreateDoc(t, rec)
}
