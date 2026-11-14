package pulumi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

const (
	// revealSecretValue is the plain value the reveal fixtures encrypt. It
	// is a test value only.
	revealSecretValue = "s3cr3t-value"
	// revealTarget is the reveal route of the fixture stack.
	revealTarget = "/api/stacks/acme/web/prod/state/1/reveal"
)

// revealPatterns lists the reveal route as "METHOD path". The generic route
// tests skip it: it needs a checkpoint with a service secret, so the
// dedicated tests below prove it instead.
func revealPatterns() []string {
	return []string{
		"POST /api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal",
	}
}

// revealFixture is a Handler with real updates, a real secret service under
// the test key, and the audit wiring under test.
type revealFixture struct {
	handler *pulumi.Handler
	updates *stack.Updates
	secrets *secret.Service
	stacks  *stack.Service
	store   *recordingStackStore
	ref     stack.Ref
}

// revealDoc is the wire shape of a reveal answer in tests.
type revealDoc struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// newRevealFixture builds the reveal fixture with authz and the audit wiring
// of cfg.
func newRevealFixture(t *testing.T, authz identity.Authorizer, cfg fixtureAudit) revealFixture {
	t.Helper()

	return newRevealFixtureWithSessions(t, authz, cfg, aliceChecker(t))
}

// newRevealFixtureWithSessions builds the reveal fixture with authz, the
// audit wiring of cfg and the session cookie check sessions.
func newRevealFixtureWithSessions(
	t *testing.T, authz identity.Authorizer, cfg fixtureAudit, sessions *sessionauth.Checker,
) revealFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		placed: nil, placeErr: nil,
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            mem.resolving(stacks),
		Tokens:             nil,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testSecretKey))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          &memSecretKeys{keys: map[secret.Scope]secret.WrappedKey{}, mu: sync.Mutex{}},
		Wrapper:       wrapper,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 secrets,
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Sessions:                sessions,
		AddressGate:             nil,
		Logger:                  cfg.logger,
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   orTestAudit(cfg.store),
		OnAuditFailure:          cfg.onFail,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return revealFixture{
		handler: handler, updates: updates, secrets: secrets, stacks: stacks, store: stores, ref: ref,
	}
}

// seedRevealVersion encrypts the test secret under the stack's data key,
// saves a deployment with provider and the secret output, and returns the
// version and the masked path of the secret.
func seedRevealVersion(t *testing.T, f revealFixture, provider string) (int64, string) {
	t.Helper()

	st, err := f.stacks.Get(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	scope, scopeErr := secret.NewScope(f.ref.Namespace, st.ID.String())
	if scopeErr != nil {
		t.Fatalf("scope: %v", scopeErr)
	}

	encoded, encodeErr := json.Marshal(revealSecretValue)
	if encodeErr != nil {
		t.Fatalf("encode value: %v", encodeErr)
	}

	cipher, cryptErr := f.secrets.Encrypt(t.Context(), scope, [][]byte{encoded})
	if cryptErr != nil {
		t.Fatalf("encrypt: %v", cryptErr)
	}

	deployment := `{"secrets_providers":{"type":"` + provider + `"},"resources":[{` +
		`"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev","type":"pulumi:pulumi:Stack",` +
		`"inputs":{},"outputs":{"password":{"4dabf18193072939515e22adb298388d":` +
		`"1b47061264138c4ac30d75fd1eb44270","ciphertext":"` +
		base64.StdEncoding.EncodeToString(cipher[0]) + `"},"greeting":"hi"}}]}`

	u, err := f.updates.Create(t.Context(), f.ref, stack.Plan{
		Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: "",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	started, err := f.updates.Start(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	checkpoint := stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(deployment),
		SchemaVersion: 3,
		Sequence:      0,
		Invalid:       false,
	}

	if saveErr := f.updates.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, checkpoint); saveErr != nil {
		t.Fatalf("save checkpoint: %v", saveErr)
	}

	done, err := f.updates.Complete(t.Context(), f.ref, u.ID, started.Token, stack.StatusSucceeded)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev")
	if err != nil {
		t.Fatalf("masked read: %v", err)
	}

	if len(detail.Masked) != 1 {
		t.Fatalf("masked = %q, want one secret path", detail.Masked)
	}

	return done.Version, detail.Masked[0]
}

// reveal sends one reveal request with body from a console session that signed
// in just now, and returns the status, headers and body.
func reveal(t *testing.T, f revealFixture, body string) (int, http.Header, string) {
	t.Helper()

	return revealTo(t, f, revealTarget, body)
}

// revealTo sends one reveal request to target with body and returns the
// status, headers and body.
func revealTo(t *testing.T, f revealFixture, target, body string) (int, http.Header, string) {
	t.Helper()

	sessionSecret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	// A reveal needs a console session with a recent sign-in.
	req := sessionPost(t, target, sessionSecret, body)
	req = req.WithContext(requestid.WithContext(req.Context(), "req-reveal"))

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec.Code, rec.Header(), rec.Body.String()
}

// TestRevealUnknownVersionAnswersNoStore proves a reveal against a version
// with no checkpoint answers 404 with Cache-Control: no-store and no value.
func TestRevealUnknownVersionAnswersNoStore(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	_, path := seedRevealVersion(t, f, "service")

	status, header, body := revealTo(
		t,
		f,
		"/api/stacks/acme/web/prod/state/999/reveal",
		revealBody(t, path),
	)

	if status != http.StatusNotFound {
		t.Fatalf("unknown version answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("404 body holds the secret value: %s", body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// revealBody encodes one reveal request body for path.
func revealBody(t *testing.T, path string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("encode reveal body: %v", err)
	}

	return string(raw)
}

// decodeReveal decodes one reveal answer body.
func decodeReveal(t *testing.T, body string) revealDoc {
	t.Helper()

	var doc revealDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode reveal body: %v (%s)", err, body)
	}

	return doc
}

// checkRevealAudit fails unless entries holds exactly one entry: the reveal
// entry of the kind reveal with the action unit:read-secrets, the unit, the
// version, the path and the success outcome.
func checkRevealAudit(t *testing.T, entries *memAuditStore, version int64, path string) {
	t.Helper()

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries.entries))
	}

	got := entries.entries[0]

	if got.Action != string(identity.ActionUnitReadSecrets) || got.Kind != audit.KindReveal {
		t.Fatalf("entry = %+v, want action unit:read-secrets with kind reveal", got)
	}

	if got.Object != "pulumi:acme/web/prod" || got.ValuePath != path {
		t.Fatalf("entry = %+v, want the unit with the path", got)
	}

	if got.Version != strconv.FormatInt(version, 10) {
		t.Fatalf("entry version = %q, want %d", got.Version, version)
	}

	if got.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry outcome = %q, want success", got.Outcome)
	}
}

// TestRevealRouteDeclaresBothActions proves the reveal route declares
// unit:read and unit:read-secrets on an authorized route with a resolver.
func TestRevealRouteDeclaresBothActions(t *testing.T) {
	t.Parallel()

	f := newRevealFixture(t, allowAll(), fixtureAudit{store: nil, onFail: nil, logger: slog.New(slog.DiscardHandler)})

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if pattern != revealPatterns()[0] {
			continue
		}

		if route.Scope != pulumi.ScopeAuthorized {
			t.Fatalf("route %q scope = %v, want authorized", pattern, route.Scope)
		}

		if len(route.Actions) != 2 || route.Actions[0] != identity.ActionUnitRead ||
			route.Actions[1] != identity.ActionUnitReadSecrets {
			t.Fatalf("route %q actions = %v, want [unit:read unit:read-secrets]", pattern, route.Actions)
		}

		if route.Resolve == nil {
			t.Fatalf("route %q declares actions but no resolver", pattern)
		}

		return
	}

	t.Fatalf("route %q is not declared", revealPatterns()[0])
}

// TestRevealEnforcesAuthentication proves the reveal route answers 401
// without a token, and 200 with one.
func TestRevealEnforcesAuthentication(t *testing.T) {
	t.Parallel()

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: nil, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	_, path := seedRevealVersion(t, f, "service")

	guest := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		revealTarget,
		strings.NewReader(revealBody(t, path)),
	)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, guest)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous reveal answers %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	status, _, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusOK {
		t.Fatalf("allowed reveal answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// TestRevealAnswersValue proves reveal of a masked path answers the plain
// value with no-store, and writes one audit entry of the kind reveal with
// the action unit:read-secrets, the unit, the version and the path.
func TestRevealAnswersValue(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	version, path := seedRevealVersion(t, f, "service")

	status, header, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusOK {
		t.Fatalf("reveal answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	doc := decodeReveal(t, body)

	if doc.Path != path || doc.Value != revealSecretValue {
		t.Fatalf("reveal = %+v, want the path with the test secret", doc)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	checkRevealAudit(t, entries, version, path)
}

// TestRevealUnknownPathAnswers404 proves a path outside the masked set
// answers 404 with no value.
func TestRevealUnknownPathAnswers404(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	seedRevealVersion(t, f, "service")

	status, _, body := reveal(t, f, `{"path":"/resources/0/outputs/greeting"}`)

	if status != http.StatusNotFound {
		t.Fatalf("plain path answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("404 body holds the secret value: %s", body)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 0 {
		t.Fatalf("entries = %d, want none for a path outside the masked set", len(entries.entries))
	}
}

// TestRevealDeniesWithoutReadSecrets proves a caller without
// unit:read-secrets gets 403.
func TestRevealDeniesWithoutReadSecrets(t *testing.T) {
	t.Parallel()

	f := newRevealFixture(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitReadSecrets: true}},
		fixtureAudit{store: nil, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	_, path := seedRevealVersion(t, f, "service")

	status, _, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusForbidden {
		t.Fatalf("denied reveal answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}
}

// TestRevealAuditFailureAnswers500 proves a failing audit store answers 500
// and no value, writes a log line with the path but never the value, and
// counts the failure.
func TestRevealAuditFailureAnswers500(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var failures int

	entries := &memAuditStore{entries: nil, err: errAuditBoom, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: func() { failures++ }, logger: logger},
	)

	_, path := seedRevealVersion(t, f, "service")

	status, _, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusInternalServerError {
		t.Fatalf("reveal with a failed audit answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("500 body holds the secret value: %s", body)
	}

	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	line := logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	if strings.Contains(line, path) {
		t.Fatalf("log holds the path, which is request data: %q", line)
	}

	if !strings.Contains(line, "op=reveal.audit") && !strings.Contains(line, `"op":"reveal.audit"`) {
		t.Fatalf("log misses the op: %q", line)
	}

	if strings.Contains(line, revealSecretValue) {
		t.Fatalf("log holds the value: %q", line)
	}
}

// TestRevealOtherProviderAnswers409 proves a stack with another secrets
// provider answers 409 with an error that names the provider, and writes an
// entry with that result.
func TestRevealOtherProviderAnswers409(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	_, path := seedRevealVersion(t, f, "passphrase")

	status, _, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusConflict {
		t.Fatalf("passphrase reveal answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	if !strings.Contains(body, "passphrase") {
		t.Fatalf("409 body misses the provider: %s", body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("409 body holds the secret value: %s", body)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries.entries))
	}

	if entries.entries[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("entry outcome = %q, want failure", entries.entries[0].Outcome)
	}
}

// TestRevealOtherProviderAuditFailureAnswers500 proves a stack with another
// secrets provider answers 500 and no value when the audit append fails,
// writes a log line with the path but never the value, and counts the
// failure.
func TestRevealOtherProviderAuditFailureAnswers500(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var failures int

	entries := &memAuditStore{entries: nil, err: errAuditBoom, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: func() { failures++ }, logger: logger},
	)

	_, path := seedRevealVersion(t, f, "passphrase")

	status, header, body := reveal(t, f, revealBody(t, path))

	if status != http.StatusInternalServerError {
		t.Fatalf(
			"passphrase reveal with a failed audit answers %d, want %d (%s)",
			status,
			http.StatusInternalServerError,
			body,
		)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("500 body holds the secret value: %s", body)
	}

	if strings.Contains(body, "passphrase") {
		t.Fatalf("500 body holds provider detail: %s", body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	line := logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	if strings.Contains(line, path) {
		t.Fatalf("log holds the path, which is request data: %q", line)
	}

	if !strings.Contains(line, "op=reveal.audit") && !strings.Contains(line, `"op":"reveal.audit"`) {
		t.Fatalf("log misses the op: %q", line)
	}

	if strings.Contains(line, revealSecretValue) {
		t.Fatalf("log holds the value: %q", line)
	}
}

// TestRevealBodyOverLimitAnswers413 proves a body over 8 KiB answers 413
// with no secret value, Cache-Control: no-store, and no audit entry.
func TestRevealBodyOverLimitAnswers413(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	seedRevealVersion(t, f, "service")

	large := `{"path":"` + strings.Repeat("x", 9<<10) + `"}`

	status, header, body := reveal(t, f, large)

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("large reveal answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("413 body holds the secret value: %s", body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 0 {
		t.Fatalf("entries = %d, want none for an oversized body", len(entries.entries))
	}
}

// TestRevealPathOverLimitAnswers400 proves a value path one byte over 1024
// answers 400 with the documented message, no value and no audit entry,
// while a path of exactly 1024 bytes passes the limit and answers 404 as an
// unknown path.
func TestRevealPathOverLimitAnswers400(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := newRevealFixture(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	seedRevealVersion(t, f, "service")

	status, header, body := reveal(t, f, revealBody(t, strings.Repeat("x", 1025)))

	if status != http.StatusBadRequest {
		t.Fatalf("over-long path answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	if !strings.Contains(body, "reveal path over 1024 bytes") {
		t.Fatalf("400 body misses the limit message: %s", body)
	}

	if strings.Contains(body, revealSecretValue) {
		t.Fatalf("400 body holds the secret value: %s", body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	entries.mu.Lock()
	overEntries := len(entries.entries)
	entries.mu.Unlock()

	if overEntries != 0 {
		t.Fatalf("entries = %d, want none for a refused path", overEntries)
	}

	status, _, body = reveal(t, f, revealBody(t, strings.Repeat("x", 1024)))

	if status != http.StatusNotFound {
		t.Fatalf("path at the limit answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}
}

// revealFresh is one case of TestRevealNeedsAFreshAuthentication.
type revealFresh struct {
	name       string
	token      bool
	age        time.Duration
	wantStatus int
	wantCode   string
	wantPath   string
}

// TestRevealNeedsAFreshAuthentication proves the reveal route refuses an
// access token with 403 and mfa.fresh-step-required, a console session with
// an old sign-in or a sign-in after now with 401 and the path of a new
// sign-in, and answers a session with a recent sign-in. Each refusal answers
// no value and writes one failure entry of the kind reveal.
func TestRevealNeedsAFreshAuthentication(t *testing.T) {
	t.Parallel()

	const newSignIn = "/api/server/sign-in/corp?fresh=1"

	for _, tc := range []revealFresh{
		{name: "access token", token: true, age: 0, wantStatus: http.StatusForbidden, wantCode: freshCode, wantPath: ""},
		{
			name: "old sign-in", token: false, age: time.Hour, wantStatus: http.StatusUnauthorized, wantCode: freshCode,
			wantPath: newSignIn,
		},
		{
			name: "sign-in after now", token: false, age: -time.Hour, wantStatus: http.StatusUnauthorized,
			wantCode: freshCode, wantPath: newSignIn,
		},
		{name: "recent sign-in", token: false, age: time.Minute, wantStatus: http.StatusOK, wantCode: "", wantPath: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRevealFresh(t, tc)
		})
	}
}

// freshCode is the errorCode of a change that has no fresh authentication.
const freshCode = "mfa.fresh-step-required"

func checkRevealFresh(t *testing.T, tc revealFresh) {
	t.Helper()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	f := newRevealFixtureWithSessions(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
		aliceCheckerSignedIn(t, tc.age),
	)

	_, path := seedRevealVersion(t, f, "service")

	var req *http.Request

	if tc.token {
		req = httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, revealTarget, strings.NewReader(revealBody(t, path)),
		)
		req.Header.Set("Authorization", "token test-token")
	} else {
		sessionSecret, err := session.NewSecret()
		if err != nil {
			t.Fatalf("secret: %v", err)
		}

		req = sessionPost(t, revealTarget, sessionSecret, revealBody(t, path))
	}

	rec := serve(f.handler, req)
	if rec.Code != tc.wantStatus {
		t.Fatalf("reveal = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantStatus)
	}

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if tc.wantStatus == http.StatusOK {
		if len(entries.entries) != 1 || entries.entries[0].Outcome != audit.OutcomeSuccess {
			t.Fatalf("entries = %+v, want one success", entries.entries)
		}

		return
	}

	checkRevealRefusal(t, tc, rec)

	if len(entries.entries) != 1 || entries.entries[0].Outcome != audit.OutcomeFailure ||
		entries.entries[0].Kind != audit.KindReveal {
		t.Fatalf("entries = %+v, want one failure entry of the kind reveal", entries.entries)
	}
}

// checkRevealRefusal proves the body of a refused reveal: the error code, the
// path of a new sign-in, and no secret value.
func checkRevealRefusal(t *testing.T, tc revealFresh, rec *httptest.ResponseRecorder) {
	t.Helper()

	var body struct {
		ErrorCode      string `json:"errorCode"`
		Reauthenticate string `json:"reauthenticate"`
		Code           int    `json:"code"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}

	if body.ErrorCode != tc.wantCode || body.Reauthenticate != tc.wantPath || body.Code != tc.wantStatus {
		t.Errorf("body = %+v, want errorCode %q, reauthenticate %q, code %d", body, tc.wantCode, tc.wantPath,
			tc.wantStatus)
	}

	if strings.Contains(rec.Body.String(), revealSecretValue) {
		t.Fatalf("refusal holds the secret value: %s", rec.Body.String())
	}
}
