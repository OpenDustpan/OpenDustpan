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
	UpdatedAt        *int64  `json:"updatedAt"`
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

// prefsCall sends one request to the preferences route and decodes the answer.
func prefsCall(
	ctx context.Context, t *testing.T, env *e2e.Env, token, method, contentType, body string,
) (e2e.APIResponse, prefsAnswer) {
	t.Helper()

	resp, err := env.ServerAPIStatusTyped(ctx, token, method, prefsRoute, contentType, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, prefsRoute, err)
	}

	var doc prefsAnswer
	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil && resp.Status != http.StatusUnauthorized {
		t.Fatalf("%s %s answered %d with %q, not a JSON object: %v", method, prefsRoute, resp.Status, resp.Body, err)
	}

	return resp, doc
}

// prefsStep sends a request and fails unless it answers want.
func prefsStep(
	ctx context.Context, t *testing.T, env *e2e.Env, token, method, contentType, body string, want int,
) prefsAnswer {
	t.Helper()

	resp, doc := prefsCall(ctx, t, env, token, method, contentType, body)
	if resp.Status != want {
		t.Fatalf("%s %s %s answers %d, want %d (%s)", method, prefsRoute, body, resp.Status, want, resp.Body)
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

	doc := prefsStep(ctx, t, env, aliceToken, http.MethodGet, "", "", http.StatusOK)
	if doc.Theme != "default" || doc.DateStyle != "locale" || doc.Language != nil || doc.UpdatedAt != nil {
		t.Fatalf("defaults = %+v, want theme default, dateStyle locale, no language and no updatedAt", doc)
	}

	changed := prefsStep(ctx, t, env, aliceToken, http.MethodPatch, prefsPatchType,
		`{"theme":"terminal","language":"ar","formattingLocale":"ar-EG","digits":"latn","timeZone":"Asia/Riyadh"}`,
		http.StatusOK)

	have := strings.Join([]string{
		changed.Theme, prefsText(changed.Language), prefsText(changed.FormattingLocale), prefsText(changed.Digits),
		prefsText(changed.TimeZone),
	}, " ")
	if have != "terminal ar ar-EG latn Asia/Riyadh" || changed.UpdatedAt == nil {
		t.Fatalf("PATCH answer = %+v, want the changed fields and an updatedAt", changed)
	}

	read := prefsStep(ctx, t, env, aliceToken, http.MethodGet, "", "", http.StatusOK)
	if prefsJSON(t, read) != prefsJSON(t, changed) {
		t.Fatalf("GET after PATCH = %s, want %s", prefsJSON(t, read), prefsJSON(t, changed))
	}

	// A person never reads the preferences of another person.
	if other := prefsStep(ctx, t, env, bobToken, http.MethodGet, "", "", http.StatusOK); other.Theme != "default" ||
		other.Language != nil {
		t.Fatalf("preferences of bob = %+v, want the defaults", other)
	}

	// The same patch again changes nothing, also not updatedAt.
	again := prefsStep(ctx, t, env, aliceToken, http.MethodPatch, prefsPatchType, `{"language":"ar"}`, http.StatusOK)
	if again.UpdatedAt == nil || *again.UpdatedAt != *changed.UpdatedAt {
		t.Fatalf("updatedAt after a patch that changes nothing = %v, want %d", again.UpdatedAt, *changed.UpdatedAt)
	}

	// null resets a field, and a field that the patch leaves out keeps its value.
	reset := prefsStep(ctx, t, env, aliceToken, http.MethodPatch, prefsPatchType,
		`{"digits":null,"language":null}`, http.StatusOK)
	if reset.Digits != nil || reset.Language != nil || reset.Theme != "terminal" || reset.TimeZone == nil {
		t.Fatalf("PATCH null answer = %+v, want digits and language reset and the rest kept", reset)
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
		doc := prefsStep(ctx, t, env, token, http.MethodPatch, prefsPatchType, step.body, http.StatusBadRequest)
		if doc.ErrorCode != step.code {
			t.Fatalf("%s: errorCode = %q, want %q", step.name, doc.ErrorCode, step.code)
		}
	}

	digits := prefsStep(ctx, t, env, token, http.MethodPatch, prefsPatchType, `{"digits":"deva"}`, http.StatusBadRequest)
	for _, want := range []string{"latn", "arab", "null"} {
		if !strings.Contains(digits.Message, want) {
			t.Fatalf("the 400 for digits says %q, want it to name %s", digits.Message, want)
		}
	}

	doc := prefsStep(ctx, t, env, token, http.MethodPatch, "application/json", `{"theme":"default"}`,
		http.StatusUnsupportedMediaType)
	if doc.ErrorCode != "preferences.media-type-unsupported" {
		t.Fatalf("415 errorCode = %q", doc.ErrorCode)
	}

	big := `{"language":"` + strings.Repeat("a", 5000) + `"}`

	doc = prefsStep(ctx, t, env, token, http.MethodPatch, prefsPatchType, big, http.StatusRequestEntityTooLarge)
	if doc.ErrorCode != "preferences.body-too-large" {
		t.Fatalf("413 errorCode = %q", doc.ErrorCode)
	}

	if read := prefsStep(ctx, t, env, token, http.MethodGet, "", "", http.StatusOK); read.Theme != "terminal" {
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
	for name, secret := range map[string]string{"a service principal": token.Secret, "the bootstrap admin": ""} {
		if secret == "" {
			secret = env.TokenA()
		}

		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			doc := prefsStep(ctx, t, env, secret, method, prefsPatchType, `{"theme":"terminal"}`, http.StatusForbidden)
			if doc.ErrorCode != "preferences.person-required" {
				t.Fatalf("%s %s: errorCode = %q, want preferences.person-required", name, method, doc.ErrorCode)
			}
		}
	}

	if resp, _ := prefsCall(ctx, t, env, "", http.MethodGet, "", ""); resp.Status != http.StatusUnauthorized {
		t.Fatalf("GET with no credential answers %d, want 401", resp.Status)
	}
}

// preferencesAudit proves that the changes of the steps wrote one entry each,
// and the patches that change nothing and the refused patches wrote none.
func preferencesAudit(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	entries, err := fetchAuditEntries(ctx, env, "@server")
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	count := 0

	for i := range entries {
		if entries[i].Action != "preferences-changed" {
			continue
		}

		count++

		if entries[i].Outcome != "success" || !strings.HasPrefix(entries[i].Object, "principal:") {
			t.Fatalf("preferences entry = %+v, want a success on a principal", entries[i])
		}
	}

	// Two real changes of alice: the change and the reset.
	if count != 2 {
		t.Fatalf("preferences-changed entries = %d, want 2: one for each real change", count)
	}
}
