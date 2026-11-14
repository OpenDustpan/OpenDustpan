package tfe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
)

// memAuditStore records appended audit entries in memory, chained exactly
// as the port requires. A set err fails every call.
type memAuditStore struct {
	entries []audit.Entry
	err     error
	mu      sync.Mutex
}

var _ audit.Store = (*memAuditStore)(nil)

func (m *memAuditStore) Append(
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

func (m *memAuditStore) List(
	_ context.Context, _ namespace.Name, _ audit.Page,
) (audit.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return audit.Listing{}, m.err
	}

	out := append([]audit.Entry(nil), m.entries...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return audit.Listing{Entries: out, Total: int64(len(out))}, nil
}

func (m *memAuditStore) Check(_ context.Context, _ namespace.Name) (audit.CheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return audit.CheckResult{}, m.err
	}

	return audit.CheckResult{Checked: int64(len(m.entries)), BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// errAuditBoom is the injected store failure of the failure tests.
var errAuditBoom = errors.New("audit store boom")

// auditFixture is a Handler over the state with a sensitive output, wired to
// the audit store and the failure callback, with its logger.
type auditFixture struct {
	handler  *tfe.Handler
	entries  *memAuditStore
	failures *int
	logs     *bytes.Buffer
}

// newAuditFixture returns a Handler over the state with a sensitive output:
// the greeting output and the secret output. err fails every audit append.
// The request carries the source address, user agent, channel and request ID
// of a CLI run.
func newAuditFixture(t *testing.T, err error) auditFixture {
	t.Helper()

	return newAuditFixtureFor(t, err, tokenAuthenticator{})
}

// newAuditFixtureFor is newAuditFixture with the authenticator authn.
func newAuditFixtureFor(t *testing.T, err error, authn identity.Authenticator) auditFixture {
	t.Helper()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var failures int

	entries := &memAuditStore{entries: nil, err: err, mu: sync.Mutex{}}

	handler, handlerErr := tfe.NewHandler(tfe.Config{
		Registry:      nil,
		Authenticator: authn,
		Authorizer:    allowAll(),
		AddressGate:   nil,
		Logger:        logger,
		Namespaces:    newTestNamespaces(t),
		Workspaces:    sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		OutputChecker: newTestOutputChecker(
			t,
			allowAll(),
			sensitiveOutputsWorkspaces{stubWorkspaces: stubWorkspaces{}},
		),
		BaseURL:       baseurl.Resolver{},
		Audit:         entries,
		Gate:          nil,
		MaxStateBytes: 0,
		OnAuditFailure: func() {
			failures++
		},
		DenialLimiter: nil,
		Sessions:      nil,
		SessionPolicy: nil,
		StateGuard:    stategate.Off("the test keeps no gates"),
		Placer:        testPlacer(),
	})
	if handlerErr != nil {
		t.Fatalf("handler: %v", handlerErr)
	}

	return auditFixture{handler: handler, entries: entries, failures: &failures, logs: &logs}
}

// auditRequest sends one authenticated state request with the source
// address, user agent, channel and request ID of a CLI run.
func auditRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), "req-7"),
		http.MethodGet,
		target,
		http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("User-Agent", "opentofu/1.11.0")
	req.Header.Set("Dustpan-Channel", "api")
	req.RemoteAddr = "192.0.2.10:4321"

	return req
}

// decodeAuditOutputDoc decodes a single output document and fails the test
// when the status is wrong.
func decodeAuditOutputDoc(t *testing.T, rec *httptest.ResponseRecorder) stateOutputTestResource {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var doc stateOutputTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode output body: %v", err)
	}

	return doc.Data
}

// checkToolOperationEntry fails the test unless e is the tool-operation entry
// of a state read: the action unit:read-secrets with the kind tool-operation,
// carrying the unit, the state version, the value path, the subject, the
// source IP, the user agent, the channel and the request ID.
func checkToolOperationEntry(t *testing.T, e audit.Entry, version, valuePath string) {
	t.Helper()

	checkToolOperationWhat(t, e, version, valuePath)
	checkToolOperationMeta(t, e)
}

// checkToolOperationWhat fails the test unless e records the action,
// the kind, the unit, the state version and the value path of a state read.
func checkToolOperationWhat(t *testing.T, e audit.Entry, version, valuePath string) {
	t.Helper()

	checkReadWhat(t, e, version, valuePath, audit.KindToolOperation)
}

// checkReadWhat fails the test unless e records the action, the unit, the
// state version and the value path of a state read, with the kind want.
func checkReadWhat(t *testing.T, e audit.Entry, version, valuePath string, want audit.Kind) {
	t.Helper()

	if e.Action != string(identity.ActionUnitReadSecrets) {
		t.Fatalf("action = %q, want unit:read-secrets", e.Action)
	}

	if e.Kind != want {
		t.Fatalf("kind = %q, want %q", e.Kind, want)
	}

	if e.Object != "opentofu:acme/web" {
		t.Fatalf("object = %q, want the unit reference", e.Object)
	}

	if e.Version != version {
		t.Fatalf("version = %q, want %q", e.Version, version)
	}

	if e.ValuePath != valuePath {
		t.Fatalf("value path = %q, want %q", e.ValuePath, valuePath)
	}
}

// checkToolOperationMeta fails the test unless e carries the subject, the
// source IP, the user agent, the channel, the request ID, the success outcome
// and the hash of a CLI run.
func checkToolOperationMeta(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Subject != "tester" {
		t.Fatalf("subject = %q, want tester", e.Subject)
	}

	if e.SourceIP != "192.0.2.10" {
		t.Fatalf("source IP = %q, want 192.0.2.10", e.SourceIP)
	}

	if e.UserAgent != "opentofu/1.11.0" {
		t.Fatalf("user agent = %q, want the CLI agent", e.UserAgent)
	}

	if e.Channel != audit.ChannelAPI {
		t.Fatalf("channel = %q, want api", e.Channel)
	}

	if e.RequestID != "req-7" {
		t.Fatalf("request ID = %q, want req-7", e.RequestID)
	}

	if e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("outcome = %q, want success", e.Outcome)
	}

	if e.Hash == "" {
		t.Fatal("hash is empty")
	}
}

// principalAuthenticator signs in each test token as its own principal.
type principalAuthenticator map[string]identity.Principal

func (a principalAuthenticator) AuthenticateToken(_ context.Context, raw string) (identity.Principal, error) {
	p, ok := a[raw]
	if !ok {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return p, nil
}

// kindedPrincipal returns a test principal with a kind, a credential and a
// job ID.
func kindedPrincipal(kind identity.Kind, credential identity.Credential, jobID string) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           kind,
		Credential:     credential,
		JobID:          jobID,
	}
}

// TestDownloadKindFollowsThePrincipal proves that a state download writes the
// kind reveal for a person and the kind tool-operation for any other caller,
// whatever channel the client declares, that the entry records the principal
// kind and the job ID, and that the response says no-store.
func TestDownloadKindFollowsThePrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		principal  identity.Principal
		channel    string
		wantKind   audit.Kind
		wantChan   audit.Channel
		wantPKind  string
		wantJobID  string
		wantStatus int
	}{
		{
			"unknown kind counts as a person",
			kindedPrincipal("", "", ""),
			"api",
			audit.KindReveal,
			audit.ChannelAPI,
			"",
			"",
			200,
		},
		{
			"person with a session", kindedPrincipal(identity.KindPerson, identity.CredentialSession, ""),
			"console", audit.KindReveal, audit.ChannelConsole, "person", "", 200,
		},
		{
			"person with a personal token", kindedPrincipal(identity.KindPerson, identity.CredentialAccessToken, ""),
			"api", audit.KindReveal, audit.ChannelAPI, "person", "", 200,
		},
		{
			"person with no credential", kindedPrincipal(identity.KindPerson, "", ""),
			"api", audit.KindReveal, audit.ChannelAPI, "person", "", 200,
		},
		{
			"service token", kindedPrincipal(identity.KindService, identity.CredentialAccessToken, ""),
			"api", audit.KindToolOperation, audit.ChannelAPI, "service", "", 200,
		},
		{
			"service token that declares the console",
			kindedPrincipal(identity.KindService, identity.CredentialAccessToken, ""),
			"console",
			audit.KindToolOperation,
			audit.ChannelConsole,
			"service",
			"",
			200,
		},
		{
			"workload with a trust token", kindedPrincipal(identity.KindWorkload, identity.CredentialTrustToken, ""),
			"api", audit.KindToolOperation, audit.ChannelAPI, "workload", "", 200,
		},
		{
			"person with a job token", kindedPrincipal(identity.KindPerson, identity.CredentialJobToken, "job-7"),
			"api", audit.KindToolOperation, audit.ChannelAPI, "person", "job-7", 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAuditFixtureFor(t, nil, principalAuthenticator{"test-token": tc.principal})

			req := auditRequest(t, "/api/v2/state-versions/sv-test123/download")
			req.Header.Set("Dustpan-Channel", tc.channel)

			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("download answers %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}

			f.entries.mu.Lock()
			defer f.entries.mu.Unlock()

			if len(f.entries.entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(f.entries.entries))
			}

			e := f.entries.entries[0]
			checkReadWhat(t, e, "sv-test123", "", tc.wantKind)

			if e.Channel != tc.wantChan || e.PrincipalKind != tc.wantPKind || e.JobID != tc.wantJobID {
				t.Fatalf("channel, principal kind, job ID = %q, %q, %q; want %q, %q, %q",
					e.Channel, e.PrincipalKind, e.JobID, tc.wantChan, tc.wantPKind, tc.wantJobID)
			}
		})
	}
}

// TestDownloadStreamsTheState proves the download route streams the state
// and writes one entry with the state version and an empty value path.
func TestDownloadStreamsTheState(t *testing.T) {
	t.Parallel()

	f := newAuditFixture(t, nil)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, auditRequest(t, "/api/v2/state-versions/sv-test123/download"))

	if rec.Code != http.StatusOK {
		t.Fatalf("download answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if rec.Body.String() != stubStateContent() {
		t.Fatalf("download = %q, want the stub state", rec.Body.String())
	}

	f.entries.mu.Lock()
	defer f.entries.mu.Unlock()

	if len(f.entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(f.entries.entries))
	}

	checkReadWhat(t, f.entries.entries[0], "sv-test123", "", audit.KindReveal)
	checkToolOperationMeta(t, f.entries.entries[0])
}

// TestDownloadFailureAnswers500WithoutState proves a failed audit append on
// the download route answers 500 with no state bytes, while it writes a log
// line with no state and counts the failure on the metric callback.
func TestDownloadFailureAnswers500WithoutState(t *testing.T) {
	t.Parallel()

	f := newAuditFixture(t, errAuditBoom)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, auditRequest(t, "/api/v2/state-versions/sv-test123/download"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("download answers %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	for _, state := range []string{`"greeting"`, "hello", "seed-lineage"} {
		if strings.Contains(rec.Body.String(), state) {
			t.Fatalf("download answers state %q: %s", state, rec.Body.String())
		}
	}

	if *f.failures != 1 {
		t.Fatalf("failures = %d, want 1", *f.failures)
	}

	line := f.logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	for _, state := range []string{`"greeting"`, "hello", "seed-lineage"} {
		if strings.Contains(line, state) {
			t.Fatalf("log holds state %q, want no state: %q", state, line)
		}
	}
}

// TestSingleSensitiveOutputWritesToolOperationEntry proves the single output
// route serves the sensitive value and writes one entry with the value path
// of the output.
func TestSingleSensitiveOutputWritesToolOperationEntry(t *testing.T) {
	t.Parallel()

	f := newAuditFixture(t, nil)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, auditRequest(t, "/api/v2/state-version-outputs/sv-test123.secret"))

	checkSingleSecretShown(t, decodeAuditOutputDoc(t, rec))

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("sensitive output Cache-Control = %q, want no-store", got)
	}

	f.entries.mu.Lock()
	defer f.entries.mu.Unlock()

	if len(f.entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(f.entries.entries))
	}

	checkToolOperationEntry(t, f.entries.entries[0], "sv-test123", "/outputs/secret")
}

// TestSinglePlainOutputWritesNoEntry proves the single output route serves a
// non-sensitive value with no audit entry.
func TestSinglePlainOutputWritesNoEntry(t *testing.T) {
	t.Parallel()

	f := newAuditFixture(t, nil)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, auditRequest(t, "/api/v2/state-version-outputs/sv-test123.greeting"))

	checkGreetingOutput(t, decodeAuditOutputDoc(t, rec))

	if got := rec.Header().Get("Cache-Control"); got == "no-store" {
		t.Fatalf("plain output Cache-Control = %q, want none", got)
	}

	f.entries.mu.Lock()
	defer f.entries.mu.Unlock()

	if len(f.entries.entries) != 0 {
		t.Fatalf("entries = %d, want 0 for a non-sensitive output", len(f.entries.entries))
	}
}

// TestSingleSensitiveOutputFailureAnswers500WithoutValue proves a failed
// audit append on the single output route answers 500 with no value, while
// it writes a log line with no secret and counts the failure on the metric
// callback.
func TestSingleSensitiveOutputFailureAnswers500WithoutValue(t *testing.T) {
	t.Parallel()

	f := newAuditFixture(t, errAuditBoom)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, auditRequest(t, "/api/v2/state-version-outputs/sv-test123.secret"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("output answers %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("output answers the secret value: %s", rec.Body.String())
	}

	if *f.failures != 1 {
		t.Fatalf("failures = %d, want 1", *f.failures)
	}

	line := f.logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	if strings.Contains(line, "s3cr3t") {
		t.Fatalf("log holds the secret value, want no secret: %q", line)
	}
}
