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
	"strings"
	"sync"
	"testing"

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
	return audit.CheckResult{Checked: 0, BadSequence: 0, Valid: true}, nil
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
	default:
		return nil, workspace.ErrStateVersionNotFound
	}
}

// newRevealHandler builds a Handler with the reveal state and the audit
// wiring under test.
func newRevealHandler(
	t *testing.T, authz identity.Authorizer, store audit.Store, onFail func(), logger *slog.Logger,
) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		Logger:         logger,
		Namespaces:     newTestNamespaces(t),
		Workspaces:     revealWorkspaces{stubWorkspaces: stubWorkspaces{}},
		BaseURL:        baseurl.Resolver{},
		Audit:          store,
		OnAuditFailure: onFail,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// revealResultPath is the masked path of the provider secret of the stub
// state: the result the provider schema marks sensitive.
func revealResultPath(t *testing.T) string {
	t.Helper()

	masked, err := workspace.MaskedPaths([]byte(stubResourceState()), stubResourceMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
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

// sendReveal sends one reveal request and returns the status, headers and
// body.
func sendReveal(t *testing.T, handler *tfe.Handler, target, body string) (int, http.Header, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), "req-reveal"),
		http.MethodPost,
		target,
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer test-token")

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

		if route.Scope != tfe.ScopeAuthorized {
			t.Fatalf("route scope = %v, want authorized", route.Scope)
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

	checkRevealAudit(t, entries, path)
}

// checkRevealAudit fails unless entries holds exactly one entry: the reveal
// entry of the kind reveal with the action unit:read-secrets, the unit, the
// version, the path and the success outcome.
func checkRevealAudit(t *testing.T, entries *memRevealAudit, path string) {
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

	if got.Object != "opentofu:acme/web" || got.Version != "sv-test123" || got.ValuePath != path {
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

	if !strings.Contains(line, path) {
		t.Fatalf("log misses the path: %q", line)
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

	status, _, body := sendReveal(
		t, handler, "/api/v2/state-versions/sv-encrypted123/actions/reveal",
		revealBody(t, "/resources/0/instances/0/attributes/id"),
	)

	if status != http.StatusConflict {
		t.Fatalf("encrypted reveal answers %d, want %d (%s)", status, http.StatusConflict, body)
	}
}
