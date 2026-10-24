package tfe_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// fixtureBody is the body of the 404 answers that the fixture routes write.
const fixtureBody = `{"errors":["fixture"]}`

// The media types of the answers that the fixture routes write.
const (
	registryJSONMedia = "application/json"
	jsonAPIFixture    = "application/vnd.api+json"
)

// The kinds of answer that TestNotFoundAnswers expects.
const (
	wantPassed   = "passed"   // the fixture answer, unchanged
	wantJSONAPI  = "json:api" // the JSON:API 404 of the adapter
	wantRegistry = "registry" // the error document of the registry protocol
)

// registerNotFoundFixtures registers routes that answer 404 with a media type
// of their own. The paths under the registry prefix and the paths of the other
// routes are both covered, so that the path rule of notFoundWriter is tested.
func registerNotFoundFixtures(h *tfe.Handler) {
	answer := func(media string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", media)
			w.WriteHeader(http.StatusNotFound)
			if _, err := fmt.Fprint(w, fixtureBody); err != nil {
				// A short body fails the check of the body in TestNotFoundAnswers.
				return
			}
		}
	}

	h.HandleForTest("GET /v1/providers/acme/payments/json-404", answer(registryJSONMedia))
	h.HandleForTest("GET /api/v2/json-404", answer(registryJSONMedia))
	h.HandleForTest("GET /v1/providers/acme/payments/api-404", answer(jsonAPIFixture))
	h.HandleForTest("GET /api/v2/api-404", answer(jsonAPIFixture))
}

// notFoundCase is one request of TestNotFoundAnswers. media is the media type
// of the answer when want is wantPassed.
type notFoundCase struct {
	name   string
	target string
	want   string
	media  string
}

// TestNotFoundAnswers proves the rule of notFoundWriter through ServeHTTP. A
// 404 with the JSON media type passes only on a path of the registry protocol.
// A JSON:API 404 passes on every path. Any other 404 is replaced: by the
// JSON:API answer on a path of the other routes, and by the registry error
// document with server.route-not-found on a path of the registry protocol.
func TestNotFoundAnswers(t *testing.T) {
	t.Parallel()

	f := buildRegistryFixture(t, registryHost, tokenAuthenticator{}, allowAll(), nil)
	registerNotFoundFixtures(f.handler)

	cases := []notFoundCase{
		{"json 404 on a registry path passes", "/v1/providers/acme/payments/json-404", wantPassed, registryJSONMedia},
		{"json 404 on another path is replaced", "/api/v2/json-404", wantJSONAPI, ""},
		{"json:api 404 on a registry path passes", "/v1/providers/acme/payments/api-404", wantPassed, jsonAPIFixture},
		{"json:api 404 on another path passes", "/api/v2/api-404", wantPassed, jsonAPIFixture},
		{"plain 404 on a registry path is replaced", "/v1/providers/acme/payments/none", wantRegistry, ""},
		{"plain 404 on another path is replaced", "/api/v2/none", wantJSONAPI, ""},
		{"unrouted registry root", "/v1/providers/", wantRegistry, ""},
		{"unrouted registry name", "/v1/providers/x/nonsense", wantRegistry, ""},
		{"unrouted extra segment on versions", "/v1/providers/acme/payments/versions/extra", wantRegistry, ""},
		{
			"unrouted extra segment on download",
			"/v1/providers/acme/payments/1.0.0/download/linux/amd64/extra",
			wantRegistry, "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			rec := f.serve(t, regReq{host: registryHost, target: c.target, token: "", headers: nil})

			switch c.want {
			case wantPassed:
				checkPassedAnswer(t, c.media, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
			case wantJSONAPI:
				checkJSONAPIAnswer(t, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
			case wantRegistry:
				checkRegistryDocument(t, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
			default:
				t.Fatalf("unknown want %q", c.want)
			}
		})
	}
}

// checkPassedAnswer checks that the answer is the fixture answer, with media.
func checkPassedAnswer(t *testing.T, media string, status int, gotMedia string, body []byte) {
	t.Helper()

	if status != http.StatusNotFound || string(body) != fixtureBody {
		t.Fatalf("answer = %d %s, want the fixture 404 %s", status, body, fixtureBody)
	}

	if gotMedia != media {
		t.Fatalf("media type = %q, want %q", gotMedia, media)
	}
}

// checkJSONAPIAnswer checks the JSON:API 404 of the adapter for a path that no
// route serves.
func checkJSONAPIAnswer(t *testing.T, status int, media string, body []byte) {
	t.Helper()

	if media != jsonAPIFixture {
		t.Fatalf("media type = %q, want %q", media, jsonAPIFixture)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, status, body, errcodetest.Want{
		Code: errcode.ServerRouteNotFound, Args: nil, RequestBody: nil,
	})
}

// checkRegistryDocument checks the registry error document with the code of a
// path that no route serves, and no meta member.
func checkRegistryDocument(t *testing.T, status int, media string, body []byte) {
	t.Helper()

	if status != http.StatusNotFound || media != registryJSONMedia {
		t.Fatalf("answer = %d %q, want 404 %q", status, media, registryJSONMedia)
	}

	var doc registryErrorWire
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if len(doc.Errors) != 1 || doc.Errors[0] != notFoundText {
		t.Fatalf("errors = %q, want [%q]", doc.Errors, notFoundText)
	}

	if doc.Code != errcode.ServerRouteNotFound || doc.Meta != nil {
		t.Fatalf("answer = %s, want code %q and no meta", body, errcode.ServerRouteNotFound)
	}
}

// TestRegistryAnswerMetaArgs proves that a registry error answer carries the
// arguments of its code in meta.args. No registry code has arguments today, so
// the test uses backup.corrupt, a code with one argument, as a fixture.
func TestRegistryAnswerMetaArgs(t *testing.T) {
	t.Parallel()

	answer, err := errcode.New(errcode.BackupCorrupt, errcode.Identifier("backup", "b1"))
	if err != nil {
		t.Fatalf("answer: %v", err)
	}

	f := buildRegistryFixture(t, registryHost, tokenAuthenticator{}, allowAll(), nil)
	f.handler.HandleRegistryAnswerForTest("GET /v1/providers/acme/payments/args", answer)

	rec := f.serve(t, regReq{host: registryHost, target: "/v1/providers/acme/payments/args", token: "", headers: nil})

	var doc registryErrorWire
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}

	if doc.Code != errcode.BackupCorrupt || doc.Meta == nil {
		t.Fatalf("answer = %s, want code %q with meta", rec.Body.String(), errcode.BackupCorrupt)
	}

	if got := doc.Meta.Args["backup"]; got != "b1" || len(doc.Meta.Args) != 1 {
		t.Fatalf("meta.args = %v, want only backup = b1", doc.Meta.Args)
	}
}
