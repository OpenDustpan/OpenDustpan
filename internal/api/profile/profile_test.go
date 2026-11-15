package profile_test

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	read   = identity.ActionProfileRead
	write  = identity.ActionProfileWrite
	open   = identity.ActionProfileOpen
	manage = identity.ActionProfileManage
)

// grantsOf returns the actions of a subject. The token of a subject is its
// name.
func grantsOf(subject string) []identity.Action {
	switch subject {
	case "admin":
		return []identity.Action{read, write, open, manage}
	case "writer":
		return []identity.Action{read, write}
	case "reader":
		return []identity.Action{read}
	case "writeonly":
		return []identity.Action{write}
	case "manageonly":
		return []identity.Action{manage}
	default:
		return nil
	}
}

type testAuthn struct{}

func (testAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if grantsOf(token) == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: token, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

// testAuthz allows the actions of the subject, minus the denied ones.
type testAuthz struct {
	deny map[identity.Action]bool
}

func (a testAuthz) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if slices.Contains(grantsOf(p.Subject), action) && !a.deny[action] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

type fixture struct {
	handler *apiprofile.Handler
	// served is the handler that the tests call: it fails a test when a
	// handler answers a success status that the description does not give.
	served http.Handler
	store  *sqlite.Store
	// denied holds the actions that testAuthz refuses. A test can add one
	// while no request runs.
	denied map[identity.Action]bool
	clock  *testClock
}

// testClock is the clock of the Idempotency-Key middleware.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// newFixture returns a handler that takes the Idempotency-Key header.
func newFixture(t *testing.T, deny ...identity.Action) fixture {
	t.Helper()

	return buildFixture(t, keyMiddleware, deny...)
}

// newFixtureWithoutKeys returns a handler with no store of replay records.
func newFixtureWithoutKeys(t *testing.T) fixture {
	t.Helper()

	return buildFixture(t, noMiddleware)
}

// keyMiddleware returns the Idempotency-Key middleware over the store.
func keyMiddleware(t *testing.T, store *sqlite.Store, clock *testClock) *idempotent.Middleware {
	t.Helper()

	idem, err := idempotent.New(idempotent.Config{
		Store:  store.Idempotency(idempotencytest.TestWrapper(t, 1)),
		Logger: slog.New(slog.DiscardHandler), Now: clock.Now, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	return idem
}

// noMiddleware returns no middleware: the handler refuses the header.
func noMiddleware(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware { return nil }

func buildFixture(
	t *testing.T, middleware func(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware,
	deny ...identity.Action,
) fixture {
	t.Helper()

	return buildFixtureWith(t, middleware, nil, deny...)
}

// buildFixtureWith is buildFixture with a function that changes the handler
// config before the handler is built.
func buildFixtureWith(
	t *testing.T, middleware func(*testing.T, *sqlite.Store, *testClock) *idempotent.Middleware,
	tweak func(cfg *apiprofile.Config, store *sqlite.Store, namespaces *namespace.Service),
	deny ...identity.Action,
) fixture {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, err = namespaces.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, a := range deny {
		denied[a] = true
	}

	authz := testAuthz{deny: denied}

	svc, err := profile.NewService(profile.Config{
		Store: store.Profiles(), Namespaces: store, Sealer: secrets, Audit: store, Authorizer: authz, Now: nil,
	})
	if err != nil {
		t.Fatalf("profile service: %v", err)
	}

	clock := &testClock{mu: sync.Mutex{}, now: time.Now()}

	idem := middleware(t, store, clock)

	hcfg := apiprofile.Config{
		Audit:          newTestAudit(),
		Namespaces:     nil,
		OnAuditFailure: nil,
		Profiles:       svc,
		Authenticator:  testAuthn{},
		Authorizer:     authz,
		Sessions:       nil,
		SessionPolicy:  nil,
		Idempotency:    idem,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
		DenialLimiter:  nil,
	}
	if tweak != nil {
		tweak(&hcfg, store, namespaces)
	}

	handler, err := apiprofile.NewHandler(hcfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return fixture{
		handler: handler,
		served:  apidesctest.CheckStatus(t, handler),
		store:   store,
		denied:  denied,
		clock:   clock,
	}
}

const base = "/api/dustpan/v1/namespaces/acme/profiles"

// request is one call of a test.
type request struct {
	// session sends the request with a console session cookie and no
	// Authorization header.
	session bool
	token   string
	method  string
	target  string
	body    string
	headers map[string]string
}

// response is what a call answers.
type response struct {
	code   int
	header http.Header
	body   string
}

func get(token, target string) request {
	return request{session: false, token: token, method: http.MethodGet, target: target, body: "", headers: nil}
}

func send(token, method, target, body string) request {
	return request{session: false, token: token, method: method, target: target, body: body, headers: nil}
}

// inSession returns r as a request of the console session of the fixture,
// with no access token.
func (r request) inSession() request {
	r.session = true
	r.token = ""

	return r
}

// withHeader returns r with one more header.
func (r request) withHeader(key, value string) request {
	headers := map[string]string{key: value}
	maps.Copy(headers, r.headers)

	r.headers = headers

	return r
}

// do sends one request.
func (f fixture) do(t *testing.T, r request) response {
	t.Helper()

	var reader io.Reader = http.NoBody
	if r.body != "" {
		reader = strings.NewReader(r.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.target, reader)
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	if r.session {
		cookieSecret, err := session.NewSecret()
		if err != nil {
			t.Fatalf("secret: %v", err)
		}

		req.AddCookie(&http.Cookie{
			Name: sessionauth.Cookie, Value: cookieSecret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(cookieSecret))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.served.ServeHTTP(rec, req)

	return response{code: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// wantCode fails the test when the answer has another status.
func wantCode(t *testing.T, what string, got response, want int) {
	t.Helper()

	if got.code != want {
		t.Errorf("%s: %d %s, want %d", what, got.code, got.body, want)
	}
}

func jsonString(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(b)
}

// profileWire is the part of a profile answer that the tests read.
type profileWire struct {
	ID   string `json:"id"`
	ETag string `json:"etag"`
	Name string `json:"name"`
}

// entryWire is one entry of an answer.
type entryWire struct {
	Path   string `json:"path"`
	Secret bool   `json:"secret"`
	Value  any    `json:"value"`
}

// pageWire is one page of a list.
type pageWire struct {
	Profiles []profileWire `json:"profiles"`
	Next     string        `json:"nextPageToken"`
}

//nolint:gosec // A document for a test, not a credential.
const secretDoc = "schemaVersion: 1\nvalues:\n  region: eu-west-1\n  db:\n    password: { $secret: hunter2 }\n"

const minimalDoc = "schemaVersion: 1\n"

func (f fixture) create(t *testing.T, name, doc string) profileWire {
	t.Helper()

	got := f.do(
		t,
		send("admin", http.MethodPost, base, jsonString(t, map[string]string{"name": name, "document": doc})),
	)
	if got.code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", name, got.code, got.body)
	}

	var out profileWire
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	return out
}

func TestCreateAndReadBackMasked(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "payments/aws-prod", secretDoc)

	if !strings.HasPrefix(created.ID, "prf-") || created.ETag != `"`+created.ID+`.1"` {
		t.Fatalf("created = %+v", created)
	}

	// A name of two segments is one escaped segment, and a plain slash is not found.
	escaped := base + "/" + url.PathEscape("payments/aws-prod")

	got := f.do(t, get("reader", escaped+"/revisions/1"))
	wantCode(t, "read revision 1", got, http.StatusOK)

	if strings.Contains(got.body, "hunter2") || got.header.Get("ETag") != created.ETag {
		t.Errorf("masked read leaks or has no ETag %q: %s", got.header.Get("ETag"), got.body)
	}

	var entries struct {
		Entries []entryWire `json:"entries"`
	}
	if err := json.Unmarshal([]byte(got.body), &entries); err != nil {
		t.Fatalf("decode entries: %v", err)
	}

	byPath := map[string]entryWire{}
	for _, e := range entries.Entries {
		byPath[e.Path] = e
	}

	if pw := byPath["db.password"]; !pw.Secret || pw.Value != nil {
		t.Errorf("db.password = %+v, want a masked secret", pw)
	}

	if byPath["region"].Value != "eu-west-1" {
		t.Errorf("region = %+v", byPath["region"])
	}

	wantCode(t, "name with a plain slash", f.do(t, get("reader", base+"/payments/aws-prod")), http.StatusNotFound)
}

func TestStoredRevisionHoldsTheSecretSealed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "aws-prod", secretDoc)

	got := f.do(t, get("reader", base+"/"+created.ID+"/revisions/1"))
	if got.code != http.StatusOK || strings.Contains(got.body, "hunter2") || !strings.Contains(got.body, "$sealed") {
		t.Errorf("revision read: %d %s", got.code, got.body)
	}
}

func TestRevealNeedsOpenAndIsAudited(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t, 0)
	created := f.create(t, "aws-prod", secretDoc)
	target := base + "/" + created.ID + "/revisions/1?showSecrets=true"

	wantCode(t, "reveal without profile:open", f.do(t, get("reader", target)), http.StatusForbidden)

	// A reveal needs a console session with a recent sign-in.
	got := f.do(t, get("admin", target).inSession())
	if got.code != http.StatusOK || !strings.Contains(got.body, "hunter2") ||
		got.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reveal: %d %v %s", got.code, got.header, got.body)
	}

	count := f.countReveals(t)
	if count.granted != 1 || count.denied != 1 {
		t.Errorf("reveal audit entries = %d granted, %d denied, want 1 and 1", count.granted, count.denied)
	}
}

// revealCount is the number of granted, denied and failed reveal entries.
type revealCount struct{ granted, denied, failed int }

// countReveals counts the granted, denied and failed reveal entries of the
// audit log, and fails the test for an entry that holds the secret.
func (f fixture) countReveals(t *testing.T) revealCount {
	t.Helper()

	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: 50})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}

	var count revealCount

	for i := range listing.Entries {
		e := &listing.Entries[i]
		count.add(e)

		if strings.Contains(fmt.Sprintf("%+v", *e), "hunter2") {
			t.Errorf("audit entry holds the secret: %+v", *e)
		}
	}

	return count
}

// add counts e when it is a reveal entry of the profile routes.
func (c *revealCount) add(e *audit.Entry) {
	if e.Kind != audit.KindReveal {
		return
	}

	switch {
	case e.Action == "profile:open" && e.Outcome == audit.OutcomeSuccess:
		c.granted++
	case e.Outcome == audit.OutcomeDenied && e.Subject == "reader":
		c.denied++
	case e.Action == "profile:open" && e.Outcome == audit.OutcomeFailure:
		c.failed++
	default:
	}
}

func TestSaveNeedsIfMatch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "aws-prod", secretDoc)
	target := base + "/" + created.ID + "/revisions"
	body := jsonString(t, map[string]string{"document": "schemaVersion: 1\nvalues:\n  region: eu-central-1\n"})

	save := func(tag string) response {
		r := send("writer", http.MethodPost, target, body)
		if tag != "" {
			r = r.withHeader("If-Match", tag)
		}

		return f.do(t, r)
	}

	wantCode(t, "no If-Match", save(""), http.StatusPreconditionRequired)
	wantCode(t, "tag of another profile", save(`"prf-other.1"`), http.StatusPreconditionFailed)
	wantCode(t, "stale tag", save(`"`+created.ID+`.7"`), http.StatusPreconditionFailed)
	wantCode(t, "weak tag", save("W/"+created.ETag), http.StatusPreconditionFailed)

	got := save(created.ETag)
	if got.code != http.StatusCreated || got.header.Get("ETag") != `"`+created.ID+`.2"` {
		t.Fatalf("save: %d %v %s", got.code, got.header, got.body)
	}

	wantCode(t, "save on the old tag", save(created.ETag), http.StatusPreconditionFailed)

	got = f.do(t, get("reader", target))
	if got.code != http.StatusOK || !strings.Contains(got.body, `"revision":2`) ||
		strings.Contains(got.body, "document") {
		t.Errorf("revision list: %d %s", got.code, got.body)
	}
}

func TestRoutingChangeNeedsOpen(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	doc := "schemaVersion: 1\nproviders:\n  v: { use: login/vault, with: { address: \"${ values.host }\" } }\n" +
		"values:\n  host: https://a\n"

	got := f.do(
		t,
		send("writer", http.MethodPost, base, jsonString(t, map[string]string{"name": "routed", "document": doc})),
	)
	if got.code != http.StatusForbidden || !strings.Contains(got.body, "profile.routing-change-needs-open") ||
		!strings.Contains(got.body, "providers.v") {
		t.Fatalf("create with a provider block, without open: %d %s", got.code, got.body)
	}

	created := f.create(t, "routed", doc)
	moved := jsonString(
		t,
		map[string]string{"document": strings.Replace(doc, "host: https://a", "host: https://evil", 1)},
	)
	edit := send("writer", http.MethodPost, base+"/"+created.ID+"/revisions", moved).
		withHeader("If-Match", created.ETag)

	got = f.do(t, edit)
	if got.code != http.StatusForbidden || !strings.Contains(got.body, "values.host") {
		t.Errorf("change of a value that a block reads: %d %s", got.code, got.body)
	}
}

// TestRoutingChangeOfALauncherExportNeedsOpen sends, for each export that
// the launcher gets, a write that moves the value that the export reads to
// another secret, from a principal with profile:write and no profile:open.
// The routing review must refuse each one and name the value.
func TestRoutingChangeOfALauncherExportNeedsOpen(t *testing.T) {
	t.Parallel()

	exports := map[string]string{
		"files":     "  files:\n    DB_PASSWORD_FILE: \"${ values.dbPassword }\"\n",
		"variables": "  variables:\n    DB_PASSWORD: \"${ values.dbPassword }\"\n",
		"pulumi":    "  pulumi:\n    \"app:dbPassword\": \"${ values.dbPassword }\"\n",
		"opentofu":  "  opentofu:\n    db_password: \"${ values.dbPassword }\"\n",
	}

	for name, export := range exports {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			doc := "schemaVersion: 1\nvalues:\n  dbPassword: \"${ values.source }\"\n" +
				"  source: { $secret: abc }\n  other: { $secret: def }\nexports:\n" + export

			created := f.create(t, "exports-"+name, doc)
			moved := jsonString(t, map[string]string{
				"document": strings.Replace(
					doc,
					`dbPassword: "${ values.source }"`,
					`dbPassword: "${ values.other }"`,
					1,
				),
			})
			edit := send("writer", http.MethodPost, base+"/"+created.ID+"/revisions", moved).
				withHeader("If-Match", created.ETag)

			got := f.do(t, edit)
			if got.code != http.StatusForbidden || !strings.Contains(got.body, "profile.routing-change-needs-open") ||
				!strings.Contains(got.body, "values.dbPassword") {
				t.Fatalf("change of a value that exports.%s reads, without open: %d %s", name, got.code, got.body)
			}
		})
	}
}

func TestWriteAndDeleteNeedNoRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "aws-prod", minimalDoc)
	target := base + "/" + created.ID
	body := jsonString(t, map[string]string{"document": secretDoc})

	got := f.do(t, send("writeonly", http.MethodPost, target+"/revisions", body).withHeader("If-Match", created.ETag))
	wantCode(t, "save with profile:write only", got, http.StatusCreated)

	wantCode(t, "read with profile:write only", f.do(t, get("writeonly", target)), http.StatusForbidden)
	wantCode(t, "delete with profile:write only", f.do(t, send("writeonly", http.MethodDelete, target, "")),
		http.StatusForbidden)
	wantCode(t, "delete with profile:manage only", f.do(t, send("manageonly", http.MethodDelete, target, "")),
		http.StatusNoContent)
}

// TestEscapedSlashNames proves that the name of a profile with a folder is
// one escaped path segment on every route that names a profile, and that a
// plain slash is a path that does not exist.
func TestEscapedSlashNames(t *testing.T) {
	t.Parallel()

	body := jsonString(t, map[string]string{"document": secretDoc})

	tests := []struct {
		name    string
		method  string
		path    string
		token   string
		body    string
		ifMatch bool
		want    int
	}{
		{"get profile", http.MethodGet, "", "reader", "", false, http.StatusOK},
		{"list revisions", http.MethodGet, "/revisions", "reader", "", false, http.StatusOK},
		{"get revision 1", http.MethodGet, "/revisions/1", "reader", "", false, http.StatusOK},
		{"save revision", http.MethodPost, "/revisions", "writer", body, true, http.StatusCreated},
		{"delete", http.MethodDelete, "", "admin", "", false, http.StatusNoContent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			created := f.create(t, "payments/aws-prod", minimalDoc)

			build := func(target string) request {
				r := send(tc.token, tc.method, target+tc.path, tc.body)
				if tc.ifMatch {
					r = r.withHeader("If-Match", created.ETag)
				}

				return r
			}

			wantCode(t, "plain slash", f.do(t, build(base+"/payments/aws-prod")), http.StatusNotFound)
			wantCode(t, "escaped slash", f.do(t, build(base+"/"+url.PathEscape("payments/aws-prod"))), tc.want)
		})
	}
}

func TestListPages(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, n := range []string{"a", "b", "team/c"} {
		f.create(t, n, minimalDoc)
	}

	var first pageWire

	got := f.do(t, get("reader", base+"?pageSize=2"))
	wantCode(t, "list", got, http.StatusOK)

	if err := json.Unmarshal([]byte(got.body), &first); err != nil || len(first.Profiles) != 2 || first.Next == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}

	got = f.do(t, get("reader", base+"?pageSize=2&pageToken="+first.Next))
	if got.code != http.StatusOK || !strings.Contains(got.body, "team/c") ||
		strings.Contains(got.body, "nextPageToken") {
		t.Errorf("second page: %d %s", got.code, got.body)
	}

	wantCode(t, "token of another query", f.do(t, get("reader", base+"?folder=team&pageToken="+first.Next)),
		http.StatusBadRequest)
	wantCode(t, "pageSize 0", f.do(t, get("reader", base+"?pageSize=0")), http.StatusBadRequest)
}

func TestCreateErrors(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "a", minimalDoc)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"duplicate name", `{"name":"a","document":"schemaVersion: 1\n"}`, http.StatusConflict},
		{"bad name", `{"name":"Bad Name","document":"schemaVersion: 1\n"}`, http.StatusBadRequest},
		{"unknown body field", `{"name":"x","document":"schemaVersion: 1\n","extra":1}`, http.StatusBadRequest},
		{"bad document", `{"name":"x","document":"values: {}\n"}`, http.StatusBadRequest},
		{"higher schema version", `{"name":"x","document":"schemaVersion: 2\n"}`, http.StatusUnprocessableEntity},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantCode(t, tc.name, f.do(t, send("admin", http.MethodPost, base, tc.body)), tc.want)
		})
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.create(t, "a", minimalDoc)
	target := base + "/" + created.ID

	wantCode(t, "without profile:manage", f.do(t, send("writer", http.MethodDelete, target, "")), http.StatusForbidden)
	wantCode(t, "on a stale tag",
		f.do(t, send("admin", http.MethodDelete, target, "").withHeader("If-Match", `"`+created.ID+`.9"`)),
		http.StatusPreconditionFailed)
	wantCode(t, "delete", f.do(t, send("admin", http.MethodDelete, target, "")), http.StatusNoContent)
	wantCode(t, "read after delete", f.do(t, get("reader", target)), http.StatusNotFound)
	wantCode(t, "unknown name", f.do(t, get("reader", base+"/nothing")), http.StatusNotFound)
}

func TestCreateRefusesASecretInAList(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	doc := "schemaVersion: 1\nvalues:\n  list: [{$secret: hunter2}]\n"
	body := jsonString(t, map[string]string{"name": "a", "document": doc})

	wantCode(t, "create", f.do(t, send("admin", http.MethodPost, base, body)), http.StatusBadRequest)

	wantCode(t, "the refused create left a profile", f.do(t, get("reader", base+"/a")), http.StatusNotFound)

	list := f.do(t, get("reader", base))
	if list.code != http.StatusOK || strings.Contains(list.body, `"a"`) {
		t.Errorf("list after the refused create: %d %s", list.code, list.body)
	}
}

func TestIdempotencyKeyIsRefusedNotIgnored(t *testing.T) {
	t.Parallel()

	// A server with no store of replay records refuses the key.
	f := newFixtureWithoutKeys(t)
	body := jsonString(t, map[string]string{"name": "a", "document": minimalDoc})

	got := f.do(t, send("admin", http.MethodPost, base, body).withHeader("Idempotency-Key", "k1"))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, "request.idempotency-key-unsupported") {
		t.Errorf("POST with a key: %d %s", got.code, got.body)
	}

	wantCode(t, "GET with a key", f.do(t, get("reader", base).withHeader("Idempotency-Key", "k1")), http.StatusOK)
	wantCode(t, "the refused create left a profile", f.do(t, get("reader", base+"/a")), http.StatusNotFound)
}

func TestAuthentication(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	wantCode(t, "no token", f.do(t, get("", base)), http.StatusUnauthorized)
	wantCode(t, "unknown token", f.do(t, get("stranger", base)), http.StatusUnauthorized)
	wantCode(t, "bad namespace", f.do(t, get("reader", "/api/dustpan/v1/namespaces/bad%20name/profiles")),
		http.StatusBadRequest)
}

// routeTarget fills the path values of a route.
func routeTarget(rt apiprofile.Route) string {
	return strings.NewReplacer(
		"{namespace}", "acme", "{profile}", "prf-aaaaaaaaaaaaaaaaaaaa", "{revision}", "1",
	).Replace(rt.Path)
}

func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	byPattern := map[string]apiprofile.Route{}

	for _, rt := range f.handler.Routes() {
		pattern := rt.Method + " " + rt.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = rt

		if rt.Scope != route.ScopeAuthorized || len(rt.Actions) == 0 || rt.Resolve == nil {
			t.Fatalf("route %q must be authorized with actions and a resolver", pattern)
		}

		if mutating := rt.Method != http.MethodGet; rt.IdempotencyKey != mutating ||
			(rt.Answer != nil) != mutating {
			t.Errorf("route %q: takes the key = %t, has an answer = %t, want both %t",
				pattern, rt.IdempotencyKey, rt.Answer != nil, mutating)
		}
	}

	for _, pattern := range f.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for pattern, rt := range byPattern {
		// A request without a token fails closed with 401, never 404.
		got := f.do(t, send("", rt.Method, routeTarget(rt), ""))
		wantCode(t, "route "+pattern+" without a token", got, http.StatusUnauthorized)
	}
}

func TestRoutesEnforceDeclaredActions(t *testing.T) {
	t.Parallel()

	for _, rt := range newFixture(t).handler.Routes() {
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, rt.Actions...)

			got := f.do(t, send("admin", rt.Method, routeTarget(rt), "{}"))
			wantCode(t, fmt.Sprintf("with %v denied", rt.Actions), got, http.StatusForbidden)
		})
	}
}

// TestNoMuxBypass proves every served route goes through the recorder.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, readErr := packageSources.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read file %s: %v", name, readErr)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// TestSharedLimiterFlushReachesNamespace proves that a refusal of the profile
// API writes the held count of a key of another adapter into the namespace of
// that key.
func TestSharedLimiterFlushReachesNamespace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: func() time.Time { return now },
	})

	var namespaces *namespace.Service

	f := buildFixtureWith(t, keyMiddleware,
		func(cfg *apiprofile.Config, store *sqlite.Store, ns *namespace.Service) {
			cfg.Audit, cfg.Namespaces, cfg.DenialLimiter = store, ns, lim
			namespaces = ns
		})

	// Another adapter holds the counts of a key in the namespace.
	otherRec, err := refusal.New(nil, f.store, namespaces, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatalf("refusal.New: %v", err)
	}

	other := otherRec.WithLimiter(lim)

	bo := identity.Principal{
		Subject: "bo@example.com", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: "", JobID: "",
	}

	for range 3 {
		other.Record(t.Context(), nil, bo, refusal.Denial{
			Action: identity.ActionUnitWrite, Object: identity.NamespaceObject("acme"), Reason: "denied",
			ReasonWithin: nil, Window: 0,
		})
	}

	now = now.Add(2 * time.Minute)

	// A refusal of the profile API sweeps the limiter and writes the flush entry.
	denied := f.do(t, send("reader", http.MethodPost, base, "{}"))
	wantCode(t, "write without profile:write", denied, http.StatusForbidden)

	list, err := f.store.List(t.Context(), namespace.Name("acme"), audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	found := false

	for i := range list.Entries {
		if strings.Contains(list.Entries[i].Reason, "2 further refusals in the last window") {
			found = true
		}
	}

	if !found {
		t.Fatalf("entries = %+v, want the flush entry of the count 2 in namespace acme", list.Entries)
	}
}

func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	_, err := apiprofile.NewHandler(apiprofile.Config{
		Audit:          newTestAudit(),
		Namespaces:     nil,
		OnAuditFailure: nil,
		Profiles:       nil,
		Authenticator:  nil,
		Authorizer:     nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		Idempotency:    nil,
		Logger:         nil,
		Now:            nil,
		DenialLimiter:  nil,
	})
	if err == nil {
		t.Error("NewHandler accepted an empty config")
	}
}

// adminSessions accepts every session as the session of admin. The sign-in of
// the session is age old: a negative age is a sign-in after now.
type adminSessions struct {
	age time.Duration
}

func (a adminSessions) Authenticate(context.Context, session.Secret) (session.Match, error) {
	return session.Match{
		Session: session.Session{
			CreatedAt: time.Time{}, LastSeenAt: time.Time{}, AuthTime: time.Now().Add(-a.age), EndedAt: time.Time{},
			ID: "ses-1", PrincipalID: "prn-1", Provider: "corp", EndReason: "", SourceIP: "", UserAgent: "",
			Generation: 1,
		},
		Owner: session.Owner{
			ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "admin", DisplayName: "Admin",
			Kind: principal.KindPerson, BreakGlass: false,
		},
		Digest:     session.Digest{},
		Policy:     session.DefaultPolicy(),
		Accounts:   session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
		Generation: 1,
	}, nil
}

// newSessionFixture returns a handler that takes the console session cookie.
// The session of admin signed in age ago.
func newSessionFixture(t *testing.T, age time.Duration) fixture {
	t.Helper()

	return buildFixtureWith(t, keyMiddleware,
		func(cfg *apiprofile.Config, _ *sqlite.Store, _ *namespace.Service) {
			checker, err := sessionauth.New(adminSessions{age: age}, []string{"https://dustpan.example"})
			if err != nil {
				t.Fatalf("session check: %v", err)
			}

			cfg.Sessions = checker
		})
}

// revealFresh is one case of TestRevealNeedsAFreshAuthentication.
type revealFresh struct {
	name       string
	token      bool
	age        time.Duration
	wantStatus int
	wantPath   string
}

// TestRevealNeedsAFreshAuthentication proves a reveal refuses an access token
// with 403 and mfa.fresh-step-required, a console session with an old sign-in
// or a sign-in after now with 401 and the path of a new sign-in, and answers a
// session with a recent sign-in. Each refusal answers no value and writes one
// entry of the kind reveal with the outcome failure.
func TestRevealNeedsAFreshAuthentication(t *testing.T) {
	t.Parallel()

	const newSignIn = "/api/server/sign-in/corp?fresh=1"

	for _, tc := range []revealFresh{
		{name: "access token", token: true, age: 0, wantStatus: http.StatusForbidden, wantPath: ""},
		{name: "old sign-in", token: false, age: time.Hour, wantStatus: http.StatusUnauthorized, wantPath: newSignIn},
		{
			name: "sign-in after now", token: false, age: -time.Hour, wantStatus: http.StatusUnauthorized,
			wantPath: newSignIn,
		},
		{name: "recent sign-in", token: false, age: time.Minute, wantStatus: http.StatusOK, wantPath: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRevealFresh(t, tc)
		})
	}
}

func checkRevealFresh(t *testing.T, tc revealFresh) {
	t.Helper()

	f := newSessionFixture(t, tc.age)
	created := f.create(t, "aws-prod", secretDoc)
	target := base + "/" + created.ID + "/revisions/1?showSecrets=true"

	req := get("admin", target)
	if !tc.token {
		req = req.inSession()
	}

	got := f.do(t, req)
	wantCode(t, "reveal", got, tc.wantStatus)

	count := f.countReveals(t)

	if tc.wantStatus == http.StatusOK {
		if count.granted != 1 || count.failed != 0 {
			t.Errorf("reveal entries = %+v, want 1 granted", count)
		}

		return
	}

	checkRevealRefusal(t, tc, got)

	if count.granted != 0 || count.failed != 1 {
		t.Errorf("reveal entries = %+v, want 1 failed and none granted", count)
	}
}

// checkRevealRefusal proves a refused reveal: no-store, the error code, the
// path of a new sign-in, and no secret.
func checkRevealRefusal(t *testing.T, tc revealFresh, got response) {
	t.Helper()

	if got.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got.header.Get("Cache-Control"))
	}

	var body struct {
		ErrorCode      string `json:"errorCode"`
		Reauthenticate string `json:"reauthenticate"`
		Code           int    `json:"code"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode %q: %v", got.body, err)
	}

	if body.ErrorCode != "mfa.fresh-step-required" || body.Reauthenticate != tc.wantPath || body.Code != tc.wantStatus {
		t.Errorf("body = %+v, want errorCode mfa.fresh-step-required, reauthenticate %q, code %d", body, tc.wantPath,
			tc.wantStatus)
	}

	if strings.Contains(got.body, "hunter2") {
		t.Errorf("refusal holds the secret: %s", got.body)
	}
}
