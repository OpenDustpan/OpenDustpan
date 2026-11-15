package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/preferences"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// prefsPath is the route of the preferences of the caller.
const prefsPath = "/api/dustpan/v1/me/preferences"

// prefsPatchType is the media type of a PATCH of the preferences.
const prefsPatchType = "application/merge-patch+json"

// prefsCallers signs in the callers of the preferences tests by token.
type prefsCallers map[string]identity.Principal

func (c prefsCallers) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	p, ok := c[token]
	if !ok {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return p, nil
}

// caller returns a principal for the subject with the kind and the credential.
func caller(subject string, kind identity.Kind, credential identity.Credential, admin bool) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: subject, Admin: admin, TokenID: "tok-" + subject,
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: kind, Credential: credential, JobID: "",
	}
}

// The tokens of the callers of the preferences tests. alice and tester are the
// persons that the handler builder seeds, and ci-deploy is its service
// principal.
const (
	tokenTester  = "t-tester"
	tokenAlice   = "t-alice"
	tokenService = "t-service"
	tokenJob     = "t-job"
	tokenAdmin   = "t-admin"
	tokenGhost   = "t-ghost"
	tokenActing  = "t-acting"
)

func prefsCallerSet() prefsCallers {
	return prefsCallers{
		tokenTester:  caller("tester", identity.KindPerson, identity.CredentialAccessToken, false),
		tokenAlice:   caller("alice", identity.KindPerson, identity.CredentialSession, false),
		tokenService: caller("ci-deploy", identity.KindService, identity.CredentialAccessToken, false),
		tokenJob:     caller("tester", identity.KindWorkload, identity.CredentialJobToken, false),
		// The bootstrap administrator has no principal record and no kind.
		tokenAdmin: caller("bootstrap-admin", "", "", true),
		tokenGhost: caller("nobody-at-all", identity.KindPerson, identity.CredentialAccessToken, false),
		// An agent run acts as its person: the credential is the one of alice.
		tokenActing: caller("alice", identity.KindPerson, identity.CredentialAccessToken, false),
	}
}

// prefsEnv is a handler over a real store, with the preferences callers.
type prefsEnv struct {
	h  testHandler
	st *sqlite.Store
}

func newPrefsEnv(t *testing.T) prefsEnv {
	t.Helper()

	h, st := newServiceBackedHandlerOf(t, prefsCallerSet(), allowAll{}, failedSeed())

	return prefsEnv{h: h, st: st}
}

// response is the answer of a call.
type response struct {
	status int
	header http.Header
	body   string
}

// call sends one request. A contentType of "" sends none.
func (e prefsEnv) call(t *testing.T, token, method, contentType, body string) response {
	t.Helper()

	return e.callIf(t, prefsCall{token: token, method: method, contentType: contentType, body: body, ifMatch: ""})
}

// prefsCall is one request of a test.
type prefsCall struct {
	token, method, contentType, body, ifMatch string
}

// callIf sends one request with the If-Match header ifMatch. An ifMatch of ""
// sends none.
func (e prefsEnv) callIf(t *testing.T, c prefsCall) response {
	t.Helper()

	req := httptest.NewRequestWithContext(testContext(t), c.method, prefsPath, strings.NewReader(c.body))
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	if c.contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}

	if c.ifMatch != "" {
		req.Header.Set("If-Match", c.ifMatch)
	}

	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	return response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

func (e prefsEnv) get(t *testing.T, token string) response {
	t.Helper()

	return e.call(t, token, http.MethodGet, "", "")
}

func (e prefsEnv) patch(t *testing.T, token, body string) response {
	t.Helper()

	return e.call(t, token, http.MethodPatch, prefsPatchType, body)
}

// patchIf sends a PATCH with the If-Match header ifMatch.
func (e prefsEnv) patchIf(t *testing.T, body, ifMatch string) response {
	t.Helper()

	return e.callIf(t, prefsCall{
		token: tokenTester, method: http.MethodPatch, contentType: prefsPatchType, body: body, ifMatch: ifMatch,
	})
}

// prefsDoc is the document of an answer.
type prefsDoc struct {
	Theme              string      `json:"theme"`
	ColorMode          string      `json:"colorMode"`
	TerminalSchemes    prefsScheme `json:"terminalSchemes"`
	ShortcutHints      string      `json:"shortcutHints"`
	SingleKeyShortcuts string      `json:"singleKeyShortcuts"`
	ShortcutHintStyle  string      `json:"shortcutHintStyle"`
	Language           *string     `json:"language"`
	FormattingLocale   *string     `json:"formattingLocale"`
	HourCycle          *string     `json:"hourCycle"`
	DateStyle          string      `json:"dateStyle"`
	FirstDayOfWeek     *string     `json:"firstDayOfWeek"`
	TimeZone           *string     `json:"timeZone"`
	Digits             *string     `json:"digits"`
	ResolvedLanguage   *string     `json:"resolvedLanguage"`
	UpdatedAt          *string     `json:"updatedAt"`
}

// testContext returns a context that a cleanup of the test can use. The
// context of a test ends before its cleanup runs, so this one keeps the values
// and never ends.
func testContext(t *testing.T) context.Context {
	t.Helper()

	return context.WithoutCancel(t.Context())
}

// prefsScheme holds the color scheme of each color mode.
type prefsScheme struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// text returns the value of a nullable field, or "<null>".
func text(p *string) string {
	if p == nil {
		return "<null>"
	}

	return *p
}

// doc decodes the document of an answer.
func (r response) doc(t *testing.T) prefsDoc {
	t.Helper()

	var out prefsDoc
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("answer %q is not a document: %v", r.body, err)
	}

	return out
}

// apiError is the error of an answer.
type apiError struct {
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs"`
}

// errorOf decodes the error of an answer.
func (r response) errorOf(t *testing.T) apiError {
	t.Helper()

	var out apiError
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("answer %q is not an error: %v", r.body, err)
	}

	return out
}

// prefsEntries returns the entries of the audit log of the changes that
// succeeded.
func (e prefsEnv) prefsEntries(t *testing.T) []audit.Entry {
	t.Helper()

	return e.entriesWith(t, func(o audit.Outcome) bool { return o == audit.OutcomeSuccess })
}

// prefsRefusals returns the entries of the audit log of the changes that a
// refusal stopped.
func (e prefsEnv) prefsRefusals(t *testing.T) []audit.Entry {
	t.Helper()

	return e.entriesWith(t, func(o audit.Outcome) bool { return o != audit.OutcomeSuccess })
}

// entriesWith returns the preferences entries with an outcome that keep says
// to keep.
func (e prefsEnv) entriesWith(t *testing.T, keep func(audit.Outcome) bool) []audit.Entry {
	t.Helper()

	listing, err := e.st.List(testContext(t), audit.ServerScope, audit.Page{Number: 1, Size: 500})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == preferences.AuditAction && keep(listing.Entries[i].Outcome) {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

const prefsDefaults = `{"theme":"default","colorMode":"system",` +
	`"terminalSchemes":{"light":"opendustpan","dark":"opendustpan"},` +
	`"shortcutHints":"theme","singleKeyShortcuts":"theme","shortcutHintStyle":"brackets","language":null,` +
	`"formattingLocale":null,"hourCycle":null,"dateStyle":"locale","firstDayOfWeek":null,"timeZone":null,` +
	`"digits":null,"resolvedLanguage":null,"updatedAt":null}`

func TestPreferencesGetDefaults(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)
	got := e.get(t, tokenTester)

	if got.status != http.StatusOK || strings.TrimSpace(got.body) != prefsDefaults {
		t.Fatalf("GET = %d %s, want 200 %s", got.status, got.body, prefsDefaults)
	}

	if got.header.Get("Content-Type") != "application/json" || got.header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v, want JSON and no-store", got.header)
	}
}

func TestPreferencesPatchAndRead(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	got := e.patch(t, tokenTester, `{"theme":"terminal","language":"ar","formattingLocale":"ar-EG","digits":"latn",`+
		`"timeZone":"Europe/Berlin","hourCycle":"h23","dateStyle":"iso8601","firstDayOfWeek":"sat",`+
		`"resolvedLanguage":"ar","terminalSchemes":{"light":"solarized-light"}}`)
	if got.status != http.StatusOK {
		t.Fatalf("PATCH = %d %s, want 200", got.status, got.body)
	}

	doc := got.doc(t)
	have := strings.Join([]string{
		doc.Theme, doc.ColorMode, text(doc.Language), text(doc.FormattingLocale), text(doc.Digits),
		text(doc.TimeZone), text(doc.HourCycle), doc.DateStyle, text(doc.FirstDayOfWeek),
		text(doc.ResolvedLanguage), doc.TerminalSchemes.Light, doc.TerminalSchemes.Dark,
	}, " ")

	const want = "terminal system ar ar-EG latn Europe/Berlin h23 iso8601 sat ar solarized-light opendustpan"
	if have != want {
		t.Errorf("document = %q, want %q", have, want)
	}

	if doc.UpdatedAt == nil {
		t.Fatal("updatedAt = null, want a time")
	}

	if at, err := time.Parse(time.RFC3339, *doc.UpdatedAt); err != nil || at.Location() != time.UTC {
		t.Errorf("updatedAt = %q (%v), want an RFC 3339 time in UTC", *doc.UpdatedAt, err)
	}

	if read := e.get(t, tokenTester); read.status != http.StatusOK || read.body != got.body {
		t.Errorf("GET after PATCH = %d %s, want the answer of the PATCH %s", read.status, read.body, got.body)
	}
}

func TestPreferencesMergeSemantics(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)
	e.patch(t, tokenTester, `{"language":"he","hourCycle":"h12","terminalSchemes":{"light":"a","dark":"b"}}`)

	// A field that the patch leaves out keeps its value, and null resets.
	got := e.patch(t, tokenTester, `{"hourCycle":null,"terminalSchemes":{"dark":null}}`).doc(t)

	have := strings.Join(
		[]string{text(got.Language), text(got.HourCycle), got.TerminalSchemes.Light, got.TerminalSchemes.Dark}, " ",
	)
	if have != "he <null> a opendustpan" {
		t.Errorf("document = %q, want language he kept, hourCycle null, light a kept, dark reset", have)
	}

	// null on terminalSchemes resets both.
	got = e.patch(t, tokenTester, `{"terminalSchemes":null}`).doc(t)
	if got.TerminalSchemes.Light != "opendustpan" || got.TerminalSchemes.Dark != "opendustpan" {
		t.Errorf("terminalSchemes = %+v, want both reset", got.TerminalSchemes)
	}

	// An empty object changes nothing.
	if got := e.patch(t, tokenTester, `{}`); got.status != http.StatusOK || text(got.doc(t).Language) != "he" {
		t.Errorf("PATCH {} = %d %s, want 200 with language he", got.status, got.body)
	}
}

func TestPreferencesNoOpWritesNothing(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	first := e.patch(t, tokenTester, `{"language":"ru"}`).doc(t)
	if n := len(e.prefsEntries(t)); n != 1 {
		t.Fatalf("entries after the first change = %d, want 1", n)
	}

	for _, body := range []string{
		`{}`, `{"language":"ru"}`, `{"theme":null}`, `{"theme":"default"}`, `{"terminalSchemes":null}`,
	} {
		got := e.patch(t, tokenTester, body)
		if got.status != http.StatusOK {
			t.Fatalf("PATCH %s = %d %s, want 200", body, got.status, got.body)
		}

		if after := got.doc(t).UpdatedAt; after == nil || *after != *first.UpdatedAt {
			t.Errorf("PATCH %s changed updatedAt from %v to %v", body, *first.UpdatedAt, after)
		}
	}

	if n := len(e.prefsEntries(t)); n != 1 {
		t.Errorf("entries after no-op patches = %d, want 1", n)
	}

	// A person that never changed anything keeps updatedAt null.
	if got := e.patch(t, tokenAlice, `{}`); got.doc(t).UpdatedAt != nil {
		t.Errorf("updatedAt of an empty patch = %v, want null", got.doc(t).UpdatedAt)
	}
}

func TestPreferencesChangeWritesOneEntryWithNamesOnly(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	const secret = "Pacific/Auckland"

	if got := e.patch(t, tokenTester, `{"timeZone":"`+secret+`","digits":"arab"}`); got.status != http.StatusOK {
		t.Fatalf("PATCH = %d %s", got.status, got.body)
	}

	entries := e.prefsEntries(t)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}

	got := entries[0]
	if got.Subject != "tester" || got.Object != principal.Ref(testCallerID) || got.Outcome != audit.OutcomeSuccess ||
		got.Kind != audit.KindChange || got.Namespace != audit.ServerScope {
		t.Errorf("entry = %+v, want a success change of the person tester in @server", got)
	}

	checkNamesOnly(t, got, "timeZone", "digits")

	if all := fmt.Sprintf("%+v", got); strings.Contains(all, secret) || strings.Contains(all, "arab") {
		t.Errorf("entry %s holds a value", all)
	}
}

// checkNamesOnly fails the test unless the changes of the entry are exactly
// the names, each with no old value and the word changed.
func checkNamesOnly(t *testing.T, e audit.Entry, names ...string) {
	t.Helper()

	got := map[string]bool{}

	for _, c := range e.Changes {
		got[c.Key] = true

		if c.Old != nil || c.New != "changed" {
			t.Errorf("change %+v holds a value, want the name only", c)
		}
	}

	if len(got) != len(names) {
		t.Errorf("changes = %+v, want %v", e.Changes, names)
	}

	for _, name := range names {
		if !got[name] {
			t.Errorf("changes = %+v, want %s", e.Changes, name)
		}
	}
}

func TestPreferencesNeedTheRightCaller(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	const person = "preferences.person-required"

	tests := []struct {
		name  string
		token string
		want  int
		code  string
	}{
		{name: "no credential", token: "", want: http.StatusUnauthorized, code: ""},
		{name: "an unknown token", token: "nope", want: http.StatusUnauthorized, code: ""},
		{name: "a service principal", token: tokenService, want: http.StatusForbidden, code: person},
		{name: "a job token", token: tokenJob, want: http.StatusForbidden, code: person},
		{name: "the bootstrap administrator", token: tokenAdmin, want: http.StatusForbidden, code: person},
		{name: "a person with no record", token: tokenGhost, want: http.StatusForbidden, code: person},
	}

	// The parallel cases end before a cleanup runs, so the check sees every entry.
	t.Cleanup(func() { checkCallerRefusals(t, e) })

	for i := range tests {
		tc := &tests[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, got := range []response{e.get(t, tc.token), e.patch(t, tc.token, `{"theme":"terminal"}`)} {
				checkRefusal(t, got, tc.want, tc.code)
			}
		})
	}
}

// checkCallerRefusals checks the entries that the requests of the callers
// wrote: none for a change, and one with the outcome denied for the PATCH of
// each of the four callers that are not a person. A GET writes none, and
// neither does a request with no valid credential.
func checkCallerRefusals(t *testing.T, e prefsEnv) {
	t.Helper()

	if n := len(e.prefsEntries(t)); n != 0 {
		t.Errorf("entries = %d, want 0: a refused request changes nothing", n)
	}

	refusals := e.prefsRefusals(t)
	if len(refusals) != 4 {
		t.Fatalf("refusal entries = %d, want 4 (%+v)", len(refusals), refusals)
	}

	for i := range refusals {
		entry := &refusals[i]
		if entry.Outcome != audit.OutcomeDenied || !strings.HasPrefix(entry.Object, "principal:") ||
			len(entry.Changes) != 0 {
			t.Errorf("refusal entry = %+v, want the outcome denied on a principal, with no change", *entry)
		}
	}
}

// checkRefusal fails the test unless got has the status and, when code is not
// empty, the machine code.
func checkRefusal(t *testing.T, got response, status int, code string) {
	t.Helper()

	if got.status != status {
		t.Fatalf("answer = %d %s, want %d", got.status, got.body, status)
	}

	if code != "" && got.errorOf(t).ErrorCode != code {
		t.Errorf("errorCode = %q, want %q (%s)", got.errorOf(t).ErrorCode, code, got.body)
	}
}

// A request under act-as acts on its subject: it reads and changes the
// preferences of the person, never of another principal.
func TestPreferencesActOnTheSubject(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	if got := e.patch(t, tokenActing, `{"language":"uk"}`); got.status != http.StatusOK {
		t.Fatalf("PATCH as alice with another credential = %d %s", got.status, got.body)
	}

	if got := e.get(t, tokenAlice).doc(t); text(got.Language) != "uk" {
		t.Errorf("alice reads language %v, want uk: the credential changes nothing", text(got.Language))
	}

	if got := e.get(t, tokenTester).doc(t); got.Language != nil {
		t.Errorf("tester reads language %v, want null: no other person changes", text(got.Language))
	}

	entries := e.prefsEntries(t)
	if len(entries) != 1 || entries[0].Object != principal.Ref(testPrincipalID) {
		t.Errorf("entries = %+v, want one entry on the object of alice", entries)
	}
}

func TestPreferencesMediaType(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	for _, contentType := range []string{
		"", "application/json", "text/plain", "application/x-www-form-urlencoded", "application/merge-patch+json2",
		"application/json-patch+json", "application/merge-patch+json; charset=iso-8859-1",
		"application/merge-patch+json; boundary=x", "not a type",
	} {
		got := e.call(t, tokenTester, http.MethodPatch, contentType, `{"theme":"terminal"}`)
		checkRefusal(t, got, http.StatusUnsupportedMediaType, "preferences.media-type-unsupported")

		if got.header.Get("Accept-Patch") != prefsPatchType {
			t.Errorf("Content-Type %q: Accept-Patch = %q, want %q",
				contentType, got.header.Get("Accept-Patch"), prefsPatchType)
		}
	}

	// A refused type changes nothing.
	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" {
		t.Errorf("theme = %v, want default", got.Theme)
	}

	for _, contentType := range []string{
		prefsPatchType, "Application/Merge-Patch+JSON", prefsPatchType + "; charset=utf-8", prefsPatchType + ";charset=UTF-8",
	} {
		if got := e.call(t, tokenTester, http.MethodPatch, contentType, `{}`); got.status != http.StatusOK {
			t.Errorf("Content-Type %q = %d %s, want 200", contentType, got.status, got.body)
		}
	}
}

func TestPreferencesBodyLimit(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	// A body of exactly 4096 bytes is read. A body of one byte more is not.
	pad := strings.Repeat(" ", preferences.MaxBodyBytes-len(`{"language":"en"}`))

	if got := e.patch(t, tokenTester, `{"language":"en"}`+pad); got.status != http.StatusOK {
		t.Errorf("body of %d bytes = %d %s, want 200", preferences.MaxBodyBytes, got.status, got.body)
	}

	for name, body := range map[string]string{
		"one byte over":  `{"language":"en"}` + pad + " ",
		"a large body":   `{"language":"` + strings.Repeat("a", 1<<20) + `"}`,
		"a huge padding": `{}` + strings.Repeat(" ", 10<<20),
	} {
		got := e.patch(t, tokenTester, body)
		if got.status != http.StatusRequestEntityTooLarge || got.errorOf(t).ErrorCode != "preferences.body-too-large" {
			t.Errorf("%s = %d %s, want 413 preferences.body-too-large", name, got.status, got.body)
		}
	}
}

// refusal is a body that the route refuses with 400, with the machine code
// and the field that the arguments name, or "" for none.
type refusal struct {
	name  string
	body  string
	code  string
	field string
}

// invalid is a refusal of a value of a field.
func invalid(name, body, field string) refusal {
	return refusal{name: name, body: body, code: "preferences.field-invalid", field: field}
}

func patchRefusals() []refusal {
	badBody, unknown := "preferences.body-invalid", "preferences.field-unknown"
	readOnly := "preferences.field-read-only"

	return []refusal{
		{name: "an empty body", body: ``, code: badBody, field: ""},
		{name: "not JSON", body: `{`, code: badBody, field: ""},
		{name: "an array", body: `[]`, code: badBody, field: ""},
		{name: "a string", body: `"x"`, code: badBody, field: ""},
		{name: "null", body: `null`, code: badBody, field: ""},
		{name: "a repeated field", body: `{"theme":"terminal","theme":"default"}`, code: badBody, field: ""},
		{name: "text after the object", body: `{} x`, code: badBody, field: ""},
		{name: "updatedAt", body: `{"updatedAt":1760745600000}`, code: readOnly, field: "updatedAt"},
		{name: "updatedAt null", body: `{"updatedAt":null}`, code: readOnly, field: "updatedAt"},
		{name: "an unknown field", body: `{"nope":1}`, code: unknown, field: ""},
		{name: "a field with another case", body: `{"Language":"en"}`, code: unknown, field: ""},
		{name: "a flat scheme field", body: `{"terminalSchemes.light":"a"}`, code: unknown, field: ""},
		{name: "an unknown scheme member", body: `{"terminalSchemes":{"mid":"a"}}`, code: unknown, field: ""},
		invalid("schemes as a string", `{"terminalSchemes":"a"}`, "terminalSchemes"),
		invalid("a number for a text field", `{"theme":1}`, "theme"),
		invalid("a theme that is not allowed", `{"theme":"neon"}`, "theme"),
		invalid("a color mode that is not allowed", `{"colorMode":"auto"}`, "colorMode"),
		invalid("a scheme ID with capitals", `{"terminalSchemes":{"dark":"Nord"}}`, "terminalSchemes.dark"),
		invalid("a scheme ID over the limit", `{"terminalSchemes":{"light":"`+strings.Repeat("a", 65)+`"}}`,
			"terminalSchemes.light"),
		invalid("a shortcut setting", `{"shortcutHints":"yes"}`, "shortcutHints"),
		invalid("a single key setting", `{"singleKeyShortcuts":"maybe"}`, "singleKeyShortcuts"),
		invalid("a hint style", `{"shortcutHintStyle":"keycaps"}`, "shortcutHintStyle"),
		invalid("a language with an underscore", `{"language":"ar_EG"}`, "language"),
		invalid("an empty language", `{"language":""}`, "language"),
		invalid("a resolved language with a space", `{"resolvedLanguage":"ar EG"}`, "resolvedLanguage"),
		invalid("an extension in the formatting locale", `{"formattingLocale":"ar-u-nu-latn"}`, "formattingLocale"),
		invalid("an hour cycle", `{"hourCycle":"h24"}`, "hourCycle"),
		invalid("a date style", `{"dateStyle":"ymd"}`, "dateStyle"),
		invalid("a first day", `{"firstDayOfWeek":"monday"}`, "firstDayOfWeek"),
		invalid("a zone with a path", `{"timeZone":"../../etc/passwd"}`, "timeZone"),
		invalid("a zone that does not exist", `{"timeZone":"Not/AZone"}`, "timeZone"),
		invalid("the zone of the host", `{"timeZone":"Local"}`, "timeZone"),
		invalid("digits", `{"digits":"deva"}`, "digits"),
		invalid("digits in capitals", `{"digits":"LATN"}`, "digits"),
		invalid("a number for digits", `{"digits":0}`, "digits"),
	}
}

func TestPreferencesPatchRefusals(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	// The parallel cases end before a cleanup runs, so the check sees every entry.
	t.Cleanup(func() { checkBodyRefusals(t, e) })

	cases := patchRefusals()

	for i := range cases {
		tc := cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRefusedBody(t, e, tc)
		})
	}

	// A refused patch changes nothing, also when the other members are good.
	if got := e.patch(t, tokenTester, `{"theme":"terminal","digits":"deva"}`); got.status != http.StatusBadRequest {
		t.Fatalf("mixed patch = %d, want 400", got.status)
	}
}

// checkBodyRefusals checks that the refused patches changed nothing, and that
// each wrote one failure entry. No entry names a value or a field of the
// request.
func checkBodyRefusals(t *testing.T, e prefsEnv) {
	t.Helper()

	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" || got.UpdatedAt != nil {
		t.Errorf("document = %+v, want the defaults: a refused patch changes nothing", got)
	}

	if n := len(e.prefsEntries(t)); n != 0 {
		t.Errorf("entries = %d, want 0", n)
	}

	refusals := e.prefsRefusals(t)
	if want := len(patchRefusals()) + 1; len(refusals) != want {
		t.Fatalf("refusal entries = %d, want %d", len(refusals), want)
	}

	for i := range refusals {
		entry := &refusals[i]
		if entry.Outcome != audit.OutcomeFailure || entry.Object != principal.Ref(testCallerID) ||
			len(entry.Changes) != 0 {
			t.Errorf("refusal entry = %+v, want the outcome failure on the person tester, with no change", *entry)
		}
	}
}

// checkRefusedBody sends the body of tc and checks the 400, the code and the
// arguments of the answer.
func checkRefusedBody(t *testing.T, e prefsEnv, tc refusal) {
	t.Helper()

	got := e.patch(t, tokenTester, tc.body)
	checkRefusal(t, got, http.StatusBadRequest, tc.code)

	args := got.errorOf(t).ErrorArgs

	switch {
	case tc.field == "" && len(args) != 0:
		t.Errorf("errorArgs = %v, want none", args)
	case tc.field != "" && (len(args) != 1 || args["field"] != tc.field):
		t.Errorf("errorArgs = %v, want the field %s", args, tc.field)
	default:
	}
}

func TestPreferencesDigitsErrorNamesTheValidValues(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)
	got := e.patch(t, tokenTester, `{"digits":"deva"}`)

	for _, want := range []string{"latn", "arab", "null"} {
		if !strings.Contains(got.errorOf(t).Message, want) {
			t.Errorf("message %q does not name %q", got.errorOf(t).Message, want)
		}
	}
}

// No error holds a value or a field name from the request.
func TestPreferencesErrorsHoldNoRequestText(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	for _, body := range []string{
		`{"Zz9-secret-field":1}`,
		`{"theme":"Zz9-secret-value"}`,
		`{"language":"Zz9-secret-value"}`,
		`{"digits":"Zz9-secret-value"}`,
		`{"timeZone":"Zz9-secret-value"}`,
		`{"formattingLocale":"Zz9-secret-value-u-x"}`,
		`{"terminalSchemes":{"Zz9-secret-field":"x"}}`,
		`{"terminalSchemes":{"light":"Zz9_secret"}}`,
	} {
		got := e.patch(t, tokenTester, body)
		if got.status != http.StatusBadRequest || strings.Contains(strings.ToLower(got.body), "zz9") {
			t.Errorf("PATCH %s = %d %s, want a 400 with no request text", body, got.status, got.body)
		}
	}
}

func TestPreferencesOtherMethodsAreRefused(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete} {
		got := e.call(t, tokenTester, method, prefsPatchType, `{}`)
		if got.status != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, got.status)
		}
	}
}

func TestPreferencesPatchInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	if err := e.st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	if got := e.patch(t, tokenTester, `{"theme":"terminal"}`); got.status != http.StatusServiceUnavailable {
		t.Errorf("PATCH in maintenance mode = %d %s, want 503", got.status, got.body)
	}

	if got := e.get(t, tokenTester); got.status != http.StatusOK || got.doc(t).Theme != "default" {
		t.Errorf("GET in maintenance mode = %d %s, want 200 with the defaults", got.status, got.body)
	}
}

// A server whose store keeps no preferences answers 501 with its own code.
func TestPreferencesUnavailable(t *testing.T) {
	t.Parallel()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	h, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    prefsCallerSet(),
		Authorizer:       allowAll{},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           nil,
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	e := prefsEnv{h: h, st: nil}

	for _, got := range []response{e.get(t, tokenTester), e.patch(t, tokenTester, `{}`)} {
		checkRefusal(t, got, http.StatusNotImplemented, "preferences.unavailable")
	}
}

// The ETag of a read is the version of the document. A PATCH with If-Match
// writes only when the version is the one that the caller read.
func TestPreferencesETagAndIfMatch(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	first := e.get(t, tokenTester)
	if got := first.header.Get("ETag"); got != `"0"` {
		t.Errorf("ETag of the defaults = %q, want \"0\"", got)
	}

	changed := e.patchIf(t, `{"language":"ru"}`, first.header.Get("ETag"))
	checkRefusal(t, changed, http.StatusOK, "")

	tag := changed.header.Get("ETag")
	if tag == "" || tag == `"0"` || e.get(t, tokenTester).header.Get("ETag") != tag {
		t.Fatalf("ETag after a change = %q, want the version that a read gives", tag)
	}

	// The old version is refused: 412, no write and no entry of any kind.
	entries := len(e.prefsEntries(t)) + len(e.prefsRefusals(t))

	stale := e.patchIf(t, `{"theme":"terminal"}`, first.header.Get("ETag"))
	checkRefusal(t, stale, http.StatusPreconditionFailed, "preferences.precondition-failed")

	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" {
		t.Errorf("theme = %q after a refused patch, want default", got.Theme)
	}

	if n := len(e.prefsEntries(t)) + len(e.prefsRefusals(t)); n != entries {
		t.Errorf("entries = %d after a 412, want %d", n, entries)
	}

	// A patch that changes nothing keeps the version, and * matches.
	same := e.patchIf(t, `{"language":"ru"}`, tag)
	if same.status != http.StatusOK || same.header.Get("ETag") != tag {
		t.Errorf("no-op patch = %d, ETag %q; want 200 and %q", same.status, same.header.Get("ETag"), tag)
	}

	if got := e.patchIf(t, `{"theme":"terminal"}`, "*"); got.status != http.StatusOK {
		t.Errorf("If-Match * = %d %s, want 200", got.status, got.body)
	}
}

func TestPreferencesIfMatchMustBeOneVersion(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	for _, header := range []string{`0`, `"x"`, `"-1"`, `W/"x"`, `W/0`, `W/""`, `"0", "1"`, `""`, `"`} {
		got := e.patchIf(t, `{"theme":"terminal"}`, header)
		checkRefusal(t, got, http.StatusBadRequest, "preferences.if-match-invalid")
	}

	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" {
		t.Errorf("theme = %q after refused patches, want default", got.Theme)
	}
}

// TestPreferencesEmptyIfMatchIsRefused proves that an If-Match header with an
// empty value, or two If-Match headers, answer 400 and change nothing: a
// client that builds the header from an empty variable does not lose its
// precondition without an error.
func TestPreferencesEmptyIfMatchIsRefused(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	for _, values := range [][]string{{""}, {"  "}, {`"0"`, `"0"`}} {
		req := httptest.NewRequestWithContext(testContext(t), http.MethodPatch, prefsPath,
			strings.NewReader(`{"theme":"terminal"}`))
		req.Header.Set("Authorization", "Bearer "+tokenTester)
		req.Header.Set("Content-Type", prefsPatchType)
		req.Header["If-Match"] = values

		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)

		got := response{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
		checkRefusal(t, got, http.StatusBadRequest, "preferences.if-match-invalid")
	}

	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" {
		t.Errorf("theme = %q after refused patches, want default", got.Theme)
	}
}

// TestPreferencesWeakIfMatchNeverMatches proves that a weak tag in If-Match
// never matches, because If-Match uses strong comparison: the PATCH answers
// 412, changes nothing and writes no entry, also when the weak tag holds the
// current version.
func TestPreferencesWeakIfMatchNeverMatches(t *testing.T) {
	t.Parallel()

	e := newPrefsEnv(t)

	if got := e.get(t, tokenTester).header.Get("ETag"); got != `"0"` {
		t.Fatalf("ETag of the defaults = %q, want \"0\"", got)
	}

	entries := len(e.prefsEntries(t)) + len(e.prefsRefusals(t))

	for _, header := range []string{`W/"0"`, `W/"123"`} {
		got := e.patchIf(t, `{"theme":"terminal"}`, header)
		checkRefusal(t, got, http.StatusPreconditionFailed, "preferences.precondition-failed")

		if msg := got.errorOf(t).Message; !strings.Contains(msg, "weak tag") {
			t.Errorf("message = %q, want a text that names the weak tag", msg)
		}
	}

	if got := e.get(t, tokenTester).doc(t); got.Theme != "default" {
		t.Errorf("theme = %q after a weak If-Match, want default", got.Theme)
	}

	if n := len(e.prefsEntries(t)) + len(e.prefsRefusals(t)); n != entries {
		t.Errorf("entries = %d after a weak If-Match, want %d", n, entries)
	}
}
