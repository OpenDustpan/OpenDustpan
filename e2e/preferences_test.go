//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// prefsPatchType is the media type of a PATCH of the preferences.
const prefsPatchType = "application/merge-patch+json"

// prefsRoute is the route of the preferences of the caller.
const prefsRoute = "/api/dustpan/v1/me/preferences"

// prefsAnswer is the part of the preferences document that the steps read.
type prefsAnswer struct {
	Theme            string  `json:"theme"`
	Language         *string `json:"language"`
	FormattingLocale *string `json:"formattingLocale"`
	Digits           *string `json:"digits"`
	TimeZone         *string `json:"timeZone"`
	DateStyle        string  `json:"dateStyle"`
	UpdatedAt        *string `json:"updatedAt"`
	Message          string  `json:"message"`
	ErrorCode        string  `json:"errorCode"`
}

// TestPreferences reads and changes the preferences of two people through the
// API on a server of each store. No CLI reads them, so the steps call the
// routes with the personal tokens of the people, as the console does. They
// prove the document, the merge patch, the checks, the refusal of a caller
// that is not a person, and the audit entry of a real change.
func TestPreferences(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			preferencesScenario(t, env)
		})
	}
}

// prefsReq is one request to the preferences route and the status that the
// step expects.
type prefsReq struct {
	token       string
	method      string
	contentType string
	body        string
	ifMatch     string
	want        int
}

// prefsGet reads the preferences with token.
func prefsGet(token string, want int) prefsReq {
	return prefsReq{token: token, method: http.MethodGet, contentType: "", body: "", ifMatch: "", want: want}
}

// prefsPatch sends a merge patch with token.
func prefsPatch(token, body string, want int) prefsReq {
	return prefsReq{
		token: token, method: http.MethodPatch, contentType: prefsPatchType, body: body, ifMatch: "", want: want,
	}
}

// prefsCall sends one request and decodes the answer.
func prefsCall(ctx context.Context, t *testing.T, env *e2e.Env, in prefsReq) (e2e.APIResponse, prefsAnswer) {
	t.Helper()

	resp, err := env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: in.token, Method: in.method, Path: prefsRoute, ContentType: in.contentType, Body: in.body,
		IfMatch: in.ifMatch,
	})
	if err != nil {
		t.Fatalf("%s %s: %v", in.method, prefsRoute, err)
	}

	var doc prefsAnswer
	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil && resp.Status != http.StatusUnauthorized {
		t.Fatalf("%s %s answered %d with %q, not a JSON object: %v", in.method, prefsRoute, resp.Status, resp.Body, err)
	}

	return resp, doc
}

// prefsStep sends a request and fails unless it answers the status that it names.
func prefsStep(ctx context.Context, t *testing.T, env *e2e.Env, in prefsReq) prefsAnswer {
	t.Helper()

	resp, doc := prefsCall(ctx, t, env, in)
	if resp.Status != in.want {
		t.Fatalf("%s %s %s answers %d, want %d (%s)", in.method, prefsRoute, in.body, resp.Status, in.want, resp.Body)
	}

	return doc
}

// prefsJSON renders a document for a comparison.
func prefsJSON(t *testing.T, doc prefsAnswer) string {
	t.Helper()

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	return string(data)
}

// prefsText returns the value of a nullable field, or "<null>".
func prefsText(p *string) string {
	if p == nil {
		return "<null>"
	}

	return *p
}

func preferencesScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	alice := preferencesPerson(ctx, t, env, "alice")
	bob := preferencesPerson(ctx, t, env, "bob")

	aliceToken := env.PersonalToken(ctx, t, alice, "laptop").Secret
	bobToken := env.PersonalToken(ctx, t, bob, "laptop").Secret

	preferencesReadAndChange(ctx, t, env, aliceToken, bobToken)
	preferencesRefusals(ctx, t, env, aliceToken)
	preferencesCallers(ctx, t, env)
	preferencesAudit(ctx, t, env)
}

// preferencesPerson makes a person and returns its ID.
func preferencesPerson(ctx context.Context, t *testing.T, env *e2e.Env, name string) string {
	t.Helper()

	var person e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject(name+"@example.com") + `","kind":"person"}`, http.StatusCreated,
	}, &person)

	return person.ID
}

// preferencesReadAndChange proves the defaults, a change, the merge, the reset
// and the no-op of one person, and that a second person is not changed.
func preferencesReadAndChange(ctx context.Context, t *testing.T, env *e2e.Env, aliceToken, bobToken string) {
	t.Helper()

	doc := prefsStep(ctx, t, env, prefsGet(aliceToken, http.StatusOK))
	if doc.Theme != "default" || doc.DateStyle != "locale" || doc.Language != nil || doc.UpdatedAt != nil {
		t.Fatalf("defaults = %+v, want theme default, dateStyle locale, no language and no updatedAt", doc)
	}

	changed := prefsStep(ctx, t, env, prefsPatch(aliceToken,
		`{"theme":"terminal","language":"ar","formattingLocale":"ar-EG","digits":"latn","timeZone":"Asia/Riyadh"}`,
		http.StatusOK))

	have := strings.Join([]string{
		changed.Theme, prefsText(changed.Language), prefsText(changed.FormattingLocale), prefsText(changed.Digits),
		prefsText(changed.TimeZone),
	}, " ")
	if have != "terminal ar ar-EG latn Asia/Riyadh" || changed.UpdatedAt == nil {
		t.Fatalf("PATCH answer = %+v, want the changed fields and an updatedAt", changed)
	}

	read := prefsStep(ctx, t, env, prefsGet(aliceToken, http.StatusOK))
	if prefsJSON(t, read) != prefsJSON(t, changed) {
		t.Fatalf("GET after PATCH = %s, want %s", prefsJSON(t, read), prefsJSON(t, changed))
	}

	// A person never reads the preferences of another person.
	if other := prefsStep(ctx, t, env, prefsGet(bobToken, http.StatusOK)); other.Theme != "default" ||
		other.Language != nil {
		t.Fatalf("preferences of bob = %+v, want the defaults", other)
	}

	preferencesNoOpAndReset(ctx, t, env, aliceToken, *changed.UpdatedAt)
	preferencesVersions(ctx, t, env, aliceToken)
}

// preferencesNoOpAndReset proves that a patch that changes nothing keeps
// updatedAt, and that null resets a field and a field that the patch leaves out
// keeps its value.
func preferencesNoOpAndReset(ctx context.Context, t *testing.T, env *e2e.Env, token, updatedAt string) {
	t.Helper()

	again := prefsStep(ctx, t, env, prefsPatch(token, `{"language":"ar"}`, http.StatusOK))
	if again.UpdatedAt == nil || *again.UpdatedAt != updatedAt {
		t.Fatalf("updatedAt after a patch that changes nothing = %v, want %s", again.UpdatedAt, updatedAt)
	}

	reset := prefsStep(ctx, t, env, prefsPatch(token, `{"digits":null,"language":null}`, http.StatusOK))
	if reset.Digits != nil || reset.Language != nil || reset.Theme != "terminal" || reset.TimeZone == nil {
		t.Fatalf("PATCH null answer = %+v, want digits and language reset and the rest kept", reset)
	}
}

// preferencesVersions proves that the ETag of a read is the version of the
// document, and that a PATCH with the If-Match header of an older version
// answers 412 and changes nothing.
func preferencesVersions(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	first, _ := prefsCall(ctx, t, env, prefsGet(token, http.StatusOK))

	oldTag := first.Header.Get("ETag")
	if oldTag == "" {
		t.Fatal("GET answers no ETag")
	}

	// A change with the version that the read gave works and gives a new version.
	fresh := prefsPatch(token, `{"hourCycle":"h23"}`, http.StatusOK)
	fresh.ifMatch = oldTag

	resp, doc := prefsCall(ctx, t, env, fresh)
	if resp.Status != http.StatusOK || doc.UpdatedAt == nil || resp.Header.Get("ETag") == "" ||
		resp.Header.Get("ETag") == oldTag {
		t.Fatalf("PATCH with the read version answers %d, ETag %q (%s); want 200 and a new ETag",
			resp.Status, resp.Header.Get("ETag"), resp.Body)
	}

	// The old version is refused and nothing changes.
	stale := prefsPatch(token, `{"hourCycle":"h12"}`, http.StatusPreconditionFailed)
	stale.ifMatch = oldTag

	if refused := prefsStep(ctx, t, env, stale); refused.ErrorCode != "preferences.precondition-failed" {
		t.Fatalf("412 errorCode = %q", refused.ErrorCode)
	}

	after, _ := prefsCall(ctx, t, env, prefsGet(token, http.StatusOK))
	if after.Header.Get("ETag") != resp.Header.Get("ETag") {
		t.Fatalf("ETag after a refused patch = %q, want %q", after.Header.Get("ETag"), resp.Header.Get("ETag"))
	}
}

// preferencesRefusals proves the 400, 413 and 415 answers and their codes.
func preferencesRefusals(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	for _, step := range []struct{ name, body, code string }{
		{"digits that are not allowed", `{"digits":"deva"}`, "preferences.field-invalid"},
		{"updatedAt", `{"updatedAt":1}`, "preferences.field-read-only"},
		{"an unknown field", `{"nope":"x"}`, "preferences.field-unknown"},
		{"an array", `[]`, "preferences.body-invalid"},
		{"a zone with a path", `{"timeZone":"../../etc/passwd"}`, "preferences.field-invalid"},
		{"an extension in the formatting locale", `{"formattingLocale":"ar-u-nu-latn"}`, "preferences.field-invalid"},
	} {
		doc := prefsStep(ctx, t, env, prefsPatch(token, step.body, http.StatusBadRequest))
		if doc.ErrorCode != step.code {
			t.Fatalf("%s: errorCode = %q, want %q", step.name, doc.ErrorCode, step.code)
		}
	}

	digits := prefsStep(ctx, t, env, prefsPatch(token, `{"digits":"deva"}`, http.StatusBadRequest))
	for _, want := range []string{"latn", "arab", "null"} {
		if !strings.Contains(digits.Message, want) {
			t.Fatalf("the 400 for digits says %q, want it to name %s", digits.Message, want)
		}
	}

	notPatch := prefsReq{
		token: token, method: http.MethodPatch, contentType: "application/json", body: `{"theme":"default"}`,
		ifMatch: "", want: http.StatusUnsupportedMediaType,
	}
	if doc := prefsStep(ctx, t, env, notPatch); doc.ErrorCode != "preferences.media-type-unsupported" {
		t.Fatalf("415 errorCode = %q", doc.ErrorCode)
	}

	big := `{"language":"` + strings.Repeat("a", 5000) + `"}`
	tooLarge := prefsStep(ctx, t, env, prefsPatch(token, big, http.StatusRequestEntityTooLarge))
	if tooLarge.ErrorCode != "preferences.body-too-large" {
		t.Fatalf("413 errorCode = %q", tooLarge.ErrorCode)
	}

	if read := prefsStep(ctx, t, env, prefsGet(token, http.StatusOK)); read.Theme != "terminal" {
		t.Fatalf("theme = %q after refused patches, want terminal", read.Theme)
	}
}

// preferencesCallers proves the answers for a caller that is not a person.
func preferencesCallers(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var robot e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"ci-prefs","kind":"service"}`, http.StatusCreated,
	}, &robot)

	var token e2eToken

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + robot.ID + `","name":"ci"}`,
		http.StatusCreated,
	}, &token)

	// The bootstrap admin token has no person record, so it is no person.
	for name, secret := range map[string]string{"a service principal": token.Secret, "the bootstrap admin": env.TokenA()} {
		for _, req := range []prefsReq{
			prefsGet(secret, http.StatusForbidden), prefsPatch(secret, `{"theme":"terminal"}`, http.StatusForbidden),
		} {
			if doc := prefsStep(ctx, t, env, req); doc.ErrorCode != "preferences.person-required" {
				t.Fatalf("%s %s: errorCode = %q, want preferences.person-required", name, req.method, doc.ErrorCode)
			}
		}
	}

	resp, _ := prefsCall(ctx, t, env, prefsGet("", http.StatusUnauthorized))
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("GET with no credential answers %d, want 401", resp.Status)
	}
}

// preferencesAudit proves the entries of the steps: one success entry for each
// real change, and one entry for each refused patch. The patches that change
// nothing wrote none, and neither did a refused read.
func preferencesAudit(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	entries, err := fetchAuditEntries(ctx, env, "@server")
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	counts := map[string]int{}

	for i := range entries {
		if entries[i].Action == "preferences-changed" {
			counts[entries[i].Outcome]++
		}
	}

	// Three real changes of alice (the change, the reset and the change with
	// If-Match; the patch with an old version wrote none), nine patches that
	// the checks of the body or of the media type refused, and two patches of a
	// caller that is not a person.
	want := map[string]int{"success": 3, "failure": 9, "denied": 2}
	if len(counts) != len(want) || counts["success"] != want["success"] || counts["failure"] != want["failure"] ||
		counts["denied"] != want["denied"] {
		t.Fatalf("preferences-changed entries by outcome = %v, want %v", counts, want)
	}
}
