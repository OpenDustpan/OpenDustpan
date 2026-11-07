package tfe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// revealPattern is the reveal route as "METHOD path". The generic route
// tests skip it: it needs a state with a masked secret, so the dedicated
// tests below prove it instead.
func revealPattern() string {
	return "POST /api/v2/state-versions/{state_version_id}/actions/reveal"
}

// memRevealAudit records appended audit entries in memory. A set err fails
// every call.
type memRevealAudit struct {
	entries []audit.Entry
	err     error
	mu      sync.Mutex
}

func (m *memRevealAudit) Append(
	_ context.Context, fence namespace.Fence, e audit.Entry,
) (audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return audit.Entry{}, m.err
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	var prevHash string

	if len(m.entries) > 0 {
		prevHash = m.entries[len(m.entries)-1].Hash
	}

	e.Sequence = int64(len(m.entries)) + 1
	e.PrevHash = prevHash
	e.Hash = audit.ComputeHash(e, prevHash)
	m.entries = append(m.entries, e)

	return e, nil
}

func (m *memRevealAudit) List(
	_ context.Context, _ namespace.Name, _ audit.Page,
) (audit.Listing, error) {
	return audit.Listing{Entries: nil, Total: 0}, nil
}

func (m *memRevealAudit) Check(_ context.Context, _ namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// errRevealAuditBoom is the injected store failure of the failure test.
var errRevealAuditBoom = errors.New("audit store boom")

// revealWorkspaces serves the masked resource state of the stub with its
// kept marks, so the reveal tests read real masked paths. It embeds the
// shared stub for every other use case.
type revealWorkspaces struct {
	stubWorkspaces
}

func (r revealWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	sv := stubStateVersion()
	sv.SensitiveMarks = stubResourceMarks()

	switch id.String() {
	case "sv-test123", "sv-encrypted123":
		sv.ID = id

		return sv, nil
	case "sv-entangled-large123", "sv-marks-large123", "sv-disjoint-large123":
		sv.ID = id
		sv.SensitiveMarks = revealOtherMarks()

		return sv, nil
	default:
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}
}

func (r revealWorkspaces) Open(_ context.Context, sv workspace.StateVersion) (io.ReadCloser, error) {
	switch sv.ID.String() {
	case "sv-test123":
		return io.NopCloser(strings.NewReader(stubResourceState())), nil
	case "sv-encrypted123":
		return io.NopCloser(strings.NewReader(stubEncryptedState())), nil
	case "sv-entangled-large123":
		return io.NopCloser(strings.NewReader(revealEntangledLargeState())), nil
	case "sv-marks-large123":
		return io.NopCloser(strings.NewReader(revealMarksLargeState())), nil
	case "sv-disjoint-large123":
		return io.NopCloser(strings.NewReader(revealDisjointLargeState())), nil
	default:
		return nil, workspace.ErrStateVersionNotFound
	}
}

// revealOtherMarks keeps marks non-empty without matching the test address,
// so only sensitive_attributes drives masking.
func revealOtherMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"other.none": {"/id"}}
}

// revealLargeWrapper wraps one attributes object and its sensitive list in a
// state with one managed terraform_data resource named x.
func revealLargeWrapper(attributes, sensitive string) string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"x",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":` + attributes + `,"sensitive_attributes":` + sensitive + `}]}]}`
}

// revealEntangledLargeState builds a state with one instance over the 4 MiB
// fallback limit: one 5 MiB pad string with a small object a holding b, and
// two nested pointers at a and at a then b in the same list.
func revealEntangledLargeState() string {
	pad := strings.Repeat("p", 5<<20)

	attributes := `{"a":{"b":"x"},"pad":` + strconv.Quote(pad) + `}`
	sensitive := `[[{"type":"get_attr","value":"a"}],` +
		`[{"type":"get_attr","value":"a"},{"type":"get_attr","value":"b"}]]`

	return revealLargeWrapper(attributes, sensitive)
}

// revealMarksLargeState builds a state whose sensitive_attributes run over
// the 1 MiB marks limit: 40000 one-step entries.
func revealMarksLargeState() string {
	const entries = 40000

	var sensitive strings.Builder

	sensitive.WriteString("[")

	for i := range entries {
		if i > 0 {
			sensitive.WriteString(",")
		}

		sensitive.WriteString(`[[{"type":"get_attr","value":"k` + strconv.Itoa(i) + `"}]]`)
	}

	sensitive.WriteString("]")

	return revealLargeWrapper(`{"id":"a"}`, sensitive.String())
}

// revealDisjointLargeState builds the same 5 MiB instance with one pointer
// at the small token attribute, so the streaming answer has no 4 MiB limit.
func revealDisjointLargeState() string {
	pad := strings.Repeat("p", 5<<20)

	attributes := `{"token":"s3cr3t","pad":` + strconv.Quote(pad) + `}`
	sensitive := `[[{"type":"get_attr","value":"token"}]]`

	return revealLargeWrapper(attributes, sensitive)
}

// revealConfig returns the handler config with the reveal state and the audit
// wiring under test.
func revealConfig(
	t *testing.T, authz identity.Authorizer, store audit.Store, onFail func(), logger *slog.Logger,
) tfe.Config {
	t.Helper()

	return tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		Sessions:       nil,
		SessionPolicy:  nil,
		AddressGate:    nil,
		Logger:         logger,
		Namespaces:     newTestNamespaces(t),
		Workspaces:     revealWorkspaces{stubWorkspaces: stubWorkspaces{}},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          orTestAudit(store),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: onFail,
		DenialLimiter:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}
}

// newRevealHandler builds a Handler with the reveal state and the audit
// wiring under test. The console sessions of the handler signed in just now,
// so a reveal from a session passes the fresh authentication check.
func newRevealHandler(
	t *testing.T, authz identity.Authorizer, store audit.Store, onFail func(), logger *slog.Logger,
) *tfe.Handler {
	t.Helper()

	cfg := revealConfig(t, authz, store, onFail, logger)
	cfg.Sessions = aliceChecker(t, 0)

	return newRevealHandlerFrom(t, cfg)
}

// newRevealHandlerFrom builds a Handler from cfg.
func newRevealHandlerFrom(t *testing.T, cfg tfe.Config) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// revealResultPath is the masked path of the provider secret of the stub
// state: the result the provider schema marks sensitive.
func revealResultPath(t *testing.T) string {
	t.Helper()

	var masked []string

	for _, address := range stubResourceAddresses() {
		detail, err := workspace.ReadStateResource([]byte(stubResourceState()), stubResourceMarks(), address)
		if err != nil {
			t.Fatalf("masked read of %s: %v", address, err)
		}

		masked = append(masked, detail.Masked...)
	}

	for _, path := range masked {
		if strings.HasSuffix(path, "/attributes/result") {
			return path
		}
	}

	t.Fatalf("masked %q holds no result path", masked)

	return ""
}

// revealBody encodes one reveal request body for path.
func revealBody(t *testing.T, path string) string {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"type":       "state-version-reveals",
			"attributes": map[string]any{"path": path},
		},
	})
	if err != nil {
		t.Fatalf("encode reveal body: %v", err)
	}

	return string(raw)
}

// sendReveal sends one reveal request from a console session that signed in
// just now, and returns the status, headers and body. A reveal needs a
// console session with a recent sign-in.
func sendReveal(t *testing.T, handler *tfe.Handler, target, body string) (int, http.Header, string) {
	t.Helper()

	req := sessionPost(t, target, body)
	req = req.WithContext(requestid.WithContext(req.Context(), "req-reveal"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Header(), rec.Body.String()
}

// TestRevealRouteDeclaresBothActions proves the reveal route declares
// unit:read and unit:read-secrets on an authorized route with a resolver.
func TestRevealRouteDeclaresBothActions(t *testing.T) {
	t.Parallel()

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		nil,
		nil,
		slog.New(slog.DiscardHandler),
	)

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path != revealPattern() {
			continue
		}

		if route.Scope != tfe.ScopeConsole {
			t.Fatalf("route scope = %v, want console", route.Scope)
		}

		if len(route.Actions) != 2 || route.Actions[0] != identity.ActionUnitRead ||
			route.Actions[1] != identity.ActionUnitReadSecrets {
			t.Fatalf("route actions = %v, want [unit:read unit:read-secrets]", route.Actions)
		}

		if route.Resolve == nil {
			t.Fatal("route declares actions but no resolver")
		}

		return
	}

	t.Fatalf("route %q is not declared", revealPattern())
}

// TestRevealEnforcesAuthentication proves the reveal route answers 401
// without a token and authorizes both actions on the unit of the workspace.
func TestRevealEnforcesAuthentication(t *testing.T) {
	t.Parallel()

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		nil,
		nil,
		slog.New(slog.DiscardHandler),
	)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/state-versions/sv-test123/actions/reveal",
		strings.NewReader(revealBody(t, revealResultPath(t))),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous reveal answers %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	recorder := &recordingAuthorizer{calls: nil}

	allowed := newRevealHandler(
		t, recorder, nil, nil, slog.New(slog.DiscardHandler),
	)

	status, _, body := sendReveal(
		t, allowed, "/api/v2/state-versions/sv-test123/actions/reveal", revealBody(t, revealResultPath(t)),
	)

	if status != http.StatusOK {
		t.Fatalf("allowed reveal answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets} {
		found := false

		for _, call := range recorder.calls {
			if call.action == action && call.object == "opentofu:acme/web" {
				found = true
			}
		}

		if !found {
			t.Fatalf("route never asks %s on opentofu:acme/web (calls %v)", action, recorder.calls)
		}
	}
}

// revealTestAttributes decodes the attributes of a reveal answer in tests.
type revealTestAttributes struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// revealTestData decodes the data of a reveal answer in tests.
type revealTestData struct {
	ID         string               `json:"id"`
	Type       string               `json:"type"`
	Attributes revealTestAttributes `json:"attributes"`
}

// revealTestDoc decodes a reveal answer in tests.
type revealTestDoc struct {
	Data revealTestData `json:"data"`
}

// TestRevealAnswersValue proves reveal of a masked path answers the plain
// value in a JSON:API document with no-store, and writes one audit entry of
// the kind reveal with the action unit:read-secrets, the unit, the version
// and the path.
func TestRevealAnswersValue(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	path := revealResultPath(t)

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-test123/actions/reveal", revealBody(t, path),
	)

	if status != http.StatusOK {
		t.Fatalf("reveal answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc revealTestDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode reveal body: %v (%s)", err, body)
	}

	if doc.Data.Type != "state-version-reveals" || doc.Data.Attributes.Path != path {
		t.Fatalf("reveal = %+v, want the path in a reveals document", doc)
	}

	if doc.Data.Attributes.Value != "pw-s3cr3t" {
		t.Fatalf("reveal value = %#v, want pw-s3cr3t", doc.Data.Attributes.Value)
	}

	if got := header.Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want JSON:API", got)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	checkRevealAudit(t, entries, "sv-test123", path)
}

// checkRevealAudit fails unless entries holds exactly one entry: the reveal
// entry of the kind reveal with the action unit:read-secrets, the unit, the
// version, the path and the success outcome.
func checkRevealAudit(t *testing.T, entries *memRevealAudit, version, path string) {
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

	if got.Object != "opentofu:acme/web" || got.Version != version || got.ValuePath != path {
		t.Fatalf("entry = %+v, want the unit, the version and the path", got)
	}

	if got.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry outcome = %q, want success", got.Outcome)
	}
}

// TestRevealUnknownPathAnswers404 proves a path outside the masked set
// answers 404 with no value and no audit entry.
func TestRevealUnknownPathAnswers404(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	status, _, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-test123/actions/reveal",
		revealBody(t, "/resources/1/instances/0/attributes/id"),
	)

	if status != http.StatusNotFound {
		t.Fatalf("plain path answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	if strings.Contains(body, "pw-s3cr3t") {
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

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionUnitReadSecrets: true}},
		nil,
		nil,
		slog.New(slog.DiscardHandler),
	)

	status, _, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-test123/actions/reveal", revealBody(t, revealResultPath(t)),
	)

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

	entries := &memRevealAudit{entries: nil, err: errRevealAuditBoom, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t, selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries, func() { failures++ }, logger,
	)

	path := revealResultPath(t)

	status, _, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-test123/actions/reveal", revealBody(t, path),
	)

	if status != http.StatusInternalServerError {
		t.Fatalf("reveal with a failed audit answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	if strings.Contains(body, "pw-s3cr3t") {
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

	if strings.Contains(line, "pw-s3cr3t") {
		t.Fatalf("log holds the value: %q", line)
	}
}

// TestRevealClientEncryptedAnswers409 proves a state the client encrypted
// answers 409.
func TestRevealClientEncryptedAnswers409(t *testing.T) {
	t.Parallel()

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		nil,
		nil,
		slog.New(slog.DiscardHandler),
	)

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-encrypted123/actions/reveal",
		revealBody(t, "/resources/0/instances/0/attributes/id"),
	)

	if status != http.StatusConflict {
		t.Fatalf("encrypted reveal answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	if strings.Contains(body, "pw-s3cr3t") {
		t.Fatalf("409 body holds the secret value: %s", body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// TestRevealBodyOverLimitAnswers413 proves a body over 8 KiB answers 413
// with no secret value, Cache-Control: no-store, and no audit entry.
func TestRevealBodyOverLimitAnswers413(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	large := revealBody(t, "/resources/0/instances/0/attributes/"+strings.Repeat("x", 9<<10))

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-test123/actions/reveal", large,
	)

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("large reveal answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	if strings.Contains(body, "pw-s3cr3t") {
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

// TestRevealEntangledLargeInstanceAnswers413 proves a reveal that needs the
// full instance answers 413 when the instance runs over 4 MiB: the entangled
// pointers at a and at a then b force the full decode.
func TestRevealEntangledLargeInstanceAnswers413(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	path := "/resources/0/instances/0/attributes/a"

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-entangled-large123/actions/reveal", revealBody(t, path),
	)

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("entangled large reveal answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	if !strings.Contains(body, "4194304") {
		t.Fatalf("413 body misses the limit 4194304: %s", body)
	}

	if strings.Contains(body, "\"value\"") {
		t.Fatalf("413 body holds a value: %s", body)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 0 {
		t.Fatalf("entries = %d, want none for an entangled large instance", len(entries.entries))
	}
}

// TestRevealLargeSensitiveAttributesAnswers413 proves a lookup whose
// sensitive_attributes run over 1 MiB answers 413.
func TestRevealLargeSensitiveAttributesAnswers413(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-marks-large123/actions/reveal",
		revealBody(t, "/resources/0/instances/0/attributes/id"),
	)

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("large marks reveal answers %d, want %d (%s)", status, http.StatusRequestEntityTooLarge, body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	if !strings.Contains(body, "1048576") {
		t.Fatalf("413 body misses the limit 1048576: %s", body)
	}

	if strings.Contains(body, "\"value\"") {
		t.Fatalf("413 body holds a value: %s", body)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 0 {
		t.Fatalf("entries = %d, want none for large sensitive_attributes", len(entries.entries))
	}
}

// TestRevealDisjointLargeInstanceAnswers proves the same 5 MiB instance with
// one pointer at the small token attribute answers 200: the streaming answer
// has no 4 MiB limit.
func TestRevealDisjointLargeInstanceAnswers(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	path := "/resources/0/instances/0/attributes/token"

	status, header, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-disjoint-large123/actions/reveal", revealBody(t, path),
	)

	if status != http.StatusOK {
		t.Fatalf("disjoint large reveal answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc revealTestDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode reveal body: %v (%s)", err, body)
	}

	if doc.Data.Type != "state-version-reveals" || doc.Data.Attributes.Path != path {
		t.Fatalf("reveal = %+v, want the path in a reveals document", doc)
	}

	if doc.Data.Attributes.Value != "s3cr3t" {
		t.Fatalf("reveal value = %#v, want s3cr3t", doc.Data.Attributes.Value)
	}

	if got := header.Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want JSON:API", got)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	checkRevealAudit(t, entries, "sv-disjoint-large123", path)
}

// TestRevealPathOverLimitAnswers422 proves a value path one byte over 1024
// answers 422 with the documented message, no value and no audit entry,
// while a path of exactly 1024 bytes passes the limit and answers 404 as an
// unknown path.
func TestRevealPathOverLimitAnswers422(t *testing.T) {
	t.Parallel()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}

	handler := newRevealHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{}},
		entries,
		nil,
		slog.New(slog.DiscardHandler),
	)

	target := "/api/v2/state-versions/sv-test123/actions/reveal"

	status, header, body := sendReveal(t, handler, target, revealBody(t, strings.Repeat("x", 1025)))

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("over-long path answers %d, want %d (%s)", status, http.StatusUnprocessableEntity, body)
	}

	if !strings.Contains(body, "reveal path over 1024 bytes") {
		t.Fatalf("422 body misses the limit message: %s", body)
	}

	if strings.Contains(body, "pw-s3cr3t") {
		t.Fatalf("422 body holds the secret value: %s", body)
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

	status, _, body = sendReveal(t, handler, target, revealBody(t, strings.Repeat("x", 1024)))

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

// revealRefusalError is one error of a refused reveal.
type revealRefusalError struct {
	Status string            `json:"status"`
	Code   string            `json:"code"`
	Meta   revealRefusalMeta `json:"meta"`
}

// revealRefusalMeta is the meta member of a refused reveal.
type revealRefusalMeta struct {
	Reauthenticate string `json:"reauthenticate"`
}

// revealRefusalDoc decodes the error of a refused reveal.
type revealRefusalDoc struct {
	Errors []revealRefusalError `json:"errors"`
}

func checkRevealFresh(t *testing.T, tc revealFresh) {
	t.Helper()

	entries := &memRevealAudit{entries: nil, err: nil, mu: sync.Mutex{}}
	cfg := revealConfig(
		t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, entries, nil, slog.New(slog.DiscardHandler),
	)
	cfg.Sessions = aliceChecker(t, tc.age)
	handler := newRevealHandlerFrom(t, cfg)

	target := "/api/v2/state-versions/sv-test123/actions/reveal"
	body := revealBody(t, revealResultPath(t))

	req := sessionPost(t, target, body)
	if tc.token {
		req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

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
		entries.entries[0].Kind != audit.KindReveal || entries.entries[0].ValuePath != "" {
		t.Fatalf("entries = %+v, want one failure entry of the kind reveal with no path", entries.entries)
	}
}

// checkRevealRefusal proves the body of a refused reveal: the error code, the
// path of a new sign-in, and no secret value.
func checkRevealRefusal(t *testing.T, tc revealFresh, rec *httptest.ResponseRecorder) {
	t.Helper()

	var doc revealRefusalDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}

	got := doc.Errors[0]
	if got.Code != "mfa.fresh-step-required" || got.Status != strconv.Itoa(tc.wantStatus) ||
		got.Meta.Reauthenticate != tc.wantPath {
		t.Errorf("error = %+v, want code mfa.fresh-step-required, status %d, reauthenticate %q", got, tc.wantStatus,
			tc.wantPath)
	}

	if strings.Contains(rec.Body.String(), "pw-s3cr3t") {
		t.Fatalf("refusal holds the secret value: %s", rec.Body.String())
	}
}

// stubResourceAddresses names the resource instances of the stub state.
func stubResourceAddresses() []string {
	return []string{"terraform_data.smoke", "random_password.pw"}
}
