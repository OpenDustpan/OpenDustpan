package pulumi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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

// errAuditBoom is the injected store failure of the failure test.
var errAuditBoom = errors.New("audit store boom")

// auditCall describes one log-decryption request of a CLI that shows
// secrets.
type auditCall struct {
	target    string
	userAgent string
	channel   string
	requestID string
}

// auditRequest sends one log-decryption request with the request ID, source
// address, user agent and channel of a CLI that shows secrets.
func auditRequest(t *testing.T, f secretFixture, call auditCall) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), call.requestID),
		http.MethodPost,
		call.target,
		strings.NewReader(`{"secretName":"apiKey"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	req.Header.Set("User-Agent", call.userAgent)
	req.Header.Set("Dustpan-Channel", call.channel)
	req.RemoteAddr = "192.0.2.10:4321"

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestLogDecryptionWritesARevealEntry proves each decrypt audit route writes
// one entry with the action unit:decrypt and the kind reveal, carrying the
// subject, source IP, user agent, channel and request ID, and still answers
// 204.
func TestLogDecryptionWritesARevealEntry(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	for _, target := range []string{
		"/api/stacks/acme/web/prod/decrypt/log-decryption",
		"/api/stacks/acme/web/prod/decrypt/log-batch-decryption",
	} {
		call := auditCall{target: target, userAgent: "pulumi-cli/3.200.0", channel: "console", requestID: "req-7"}

		status, body := auditRequest(t, f, call)
		if status != http.StatusNoContent {
			t.Fatalf("%s answers %d, want %d (%s)", target, status, http.StatusNoContent, body)
		}
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries.entries))
	}

	for i := range entries.entries {
		checkRevealEntry(t, entries.entries[i])
	}
}

// checkRevealEntry fails the test unless e is the reveal entry of a CLI that
// shows secrets: the action unit:decrypt with the kind reveal, carrying the
// subject, source IP, user agent, channel and request ID.
func checkRevealEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != string(identity.ActionUnitDecrypt) {
		t.Fatalf("action = %q, want unit:decrypt", e.Action)
	}

	if e.Kind != audit.KindReveal {
		t.Fatalf("kind = %q, want reveal", e.Kind)
	}

	if e.Subject != "tester" {
		t.Fatalf("subject = %q, want tester", e.Subject)
	}

	if e.Object != "pulumi:acme/web/prod" {
		t.Fatalf("object = %q, want the unit reference", e.Object)
	}

	if e.SourceIP != "192.0.2.10" {
		t.Fatalf("source IP = %q, want 192.0.2.10", e.SourceIP)
	}

	if e.UserAgent != "pulumi-cli/3.200.0" {
		t.Fatalf("user agent = %q, want the CLI agent", e.UserAgent)
	}

	if e.Channel != audit.ChannelConsole {
		t.Fatalf("channel = %q, want console", e.Channel)
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

// TestLogDecryptionRejectsLargeBody proves a body over the audit limit
// answers 413 without an entry: the route bounds the body, so a large body
// cannot grow memory without bound, and the next small report still answers
// 204 as the CLI expects.
func TestLogDecryptionRejectsLargeBody(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	target := "/api/stacks/acme/web/prod/decrypt/log-decryption"
	big := `{"secretName":"` + strings.Repeat("x", 32<<10) + `"}`

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), "req-big"),
		http.MethodPost,
		target,
		strings.NewReader(big),
	)
	req.Header.Set("Authorization", "token test-token")
	req.RemoteAddr = "192.0.2.10:4321"

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body answers %d, want %d (%s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}

	entries.mu.Lock()
	n := len(entries.entries)
	entries.mu.Unlock()

	if n != 0 {
		t.Fatalf("entries = %d, want 0 after a rejected body", n)
	}

	status, body := auditRequest(t, f, auditCall{
		target:    target,
		userAgent: "pulumi-cli/3.200.0",
		channel:   "api",
		requestID: "req-after",
	})
	if status != http.StatusNoContent {
		t.Fatalf("small body after a large one answers %d, want %d (%s)", status, http.StatusNoContent, body)
	}
}

// TestDecryptWritesToolOperationEntry proves a decrypt call writes one entry
// with the action unit:decrypt and the kind tool-operation, carrying the unit
// and the count of values, and still answers the plaintext.
func TestDecryptWritesToolOperationEntry(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	ciphertext := f.encryptOne(t, "s3cr3t")

	status, plain := f.decryptOne(t, "/api/stacks/acme/web/prod/decrypt", ciphertext)
	if status != http.StatusOK || plain != "s3cr3t" {
		t.Fatalf("decrypt answers %d %q, want 200 with the plaintext", status, plain)
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries.entries))
	}

	checkToolOperationEntry(t, entries.entries[0], 1)
}

// TestBatchDecryptWritesToolOperationEntry proves a batch decrypt call writes
// one entry with the count of values, and still answers every plaintext.
func TestBatchDecryptWritesToolOperationEntry(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
	)

	first := f.encryptOne(t, "first")
	second := f.encryptOne(t, "second")

	raw, encodeErr := json.Marshal(map[string][]string{"ciphertexts": {first, second}})
	if encodeErr != nil {
		t.Fatalf("encode batch decrypt body: %v", encodeErr)
	}

	status, body := f.plain(t, http.MethodPost, "/api/stacks/acme/web/prod/batch-decrypt", string(raw))
	if status != http.StatusOK {
		t.Fatalf("batch decrypt answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var decrypted struct {
		Plaintexts map[string]string `json:"plaintexts"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &decrypted); decodeErr != nil {
		t.Fatalf("decode batch decrypt: %v (%s)", decodeErr, body)
	}

	for _, want := range []string{"first", "second"} {
		found := false

		for _, got := range decrypted.Plaintexts {
			plain, plainErr := base64.StdEncoding.DecodeString(got)
			if plainErr == nil && string(plain) == want {
				found = true

				break
			}
		}

		if !found {
			t.Fatalf("batch decrypt lacks the plaintext of %q (%s)", want, body)
		}
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries.entries))
	}

	checkToolOperationEntry(t, entries.entries[0], 2)
}

// checkToolOperationEntry fails the test unless e is the tool-operation entry
// of a server-side decrypt: the action unit:decrypt with the kind
// tool-operation, carrying the unit, the count of values in the value count
// field, an empty version and no value path.
func checkToolOperationEntry(t *testing.T, e audit.Entry, count int64) {
	t.Helper()

	if e.Action != string(identity.ActionUnitDecrypt) {
		t.Fatalf("action = %q, want unit:decrypt", e.Action)
	}

	if e.Kind != audit.KindToolOperation {
		t.Fatalf("kind = %q, want tool-operation", e.Kind)
	}

	if e.Subject != "tester" {
		t.Fatalf("subject = %q, want tester", e.Subject)
	}

	if e.Object != "pulumi:acme/web/prod" {
		t.Fatalf("object = %q, want the unit reference", e.Object)
	}

	if e.Version != "" {
		t.Fatalf("version = %q, want empty", e.Version)
	}

	if e.ValueCount == nil || *e.ValueCount != count {
		t.Fatalf("value count = %+v, want %d", e.ValueCount, count)
	}

	if e.ValuePath != "" {
		t.Fatalf("value path = %q, want empty", e.ValuePath)
	}

	if e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("outcome = %q, want success", e.Outcome)
	}

	if e.Hash == "" {
		t.Fatal("hash is empty")
	}
}

// TestDecryptFailureAnswers500WithoutSecret proves a failed audit append on a
// decrypt route answers 500 with no secret value, while it writes a log line
// with no secret and counts the failure on the metric callback.
func TestDecryptFailureAnswers500WithoutSecret(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		call func(t *testing.T, f secretFixture, ciphertext string) (int, string)
	}{
		{
			name: "decrypt",
			call: func(t *testing.T, f secretFixture, ciphertext string) (int, string) {
				t.Helper()

				return f.decryptOne(t, "/api/stacks/acme/web/prod/decrypt", ciphertext)
			},
		},
		{
			name: "batch decrypt",
			call: func(t *testing.T, f secretFixture, ciphertext string) (int, string) {
				t.Helper()

				return f.decryptBatch(t, "/api/stacks/acme/web/prod/batch-decrypt", ciphertext)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDecryptFailure(t, tc.call)
		})
	}
}

// checkDecryptFailure proves one decrypt route with a failing audit store
// answers 500 with no secret value, while it writes a log line with no
// secret and counts the failure on the metric callback.
func checkDecryptFailure(t *testing.T, call func(t *testing.T, f secretFixture, ciphertext string) (int, string)) {
	t.Helper()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var failures int

	entries := &memAuditStore{entries: nil, err: errAuditBoom, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: func() { failures++ }, logger: logger},
	)

	// Encrypt writes no audit entry, so it works with a failing
	// store and mints the ciphertext the decrypt call needs.
	ciphertext := f.encryptOne(t, "s3cr3t")

	status, body := call(t, f, ciphertext)
	if status != http.StatusInternalServerError {
		t.Fatalf("decrypt answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	if strings.Contains(body, "s3cr3t") {
		t.Fatalf("decrypt answers the secret value: %s", body)
	}

	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	line := logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	for _, secret := range []string{"s3cr3t", "boom"} {
		if strings.Contains(line, secret) {
			t.Fatalf("log holds %q, want no secret and no value: %q", secret, line)
		}
	}
}

// TestLogDecryptionFailureStillAnswers204 proves a failed append still
// answers 204 while it writes a log line with no secret and counts the
// failure on the metric callback.
func TestLogDecryptionFailureStillAnswers204(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var failures int

	entries := &memAuditStore{entries: nil, err: errAuditBoom, mu: sync.Mutex{}}

	f := buildSecretFixture(
		t,
		allowAll(),
		0,
		fixtureAudit{store: entries, onFail: func() { failures++ }, logger: logger},
	)

	status, body := auditRequest(t, f, auditCall{
		target:    "/api/stacks/acme/web/prod/decrypt/log-decryption",
		userAgent: "agent",
		channel:   "api",
		requestID: "req-9",
	})
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (%s)", status, http.StatusNoContent, body)
	}

	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	line := logs.String()
	if !strings.Contains(line, "audit append failed") {
		t.Fatalf("log misses the failure line: %q", line)
	}

	for _, secret := range []string{"apiKey", "s3cr3t", "boom"} {
		if strings.Contains(line, secret) {
			t.Fatalf("log holds %q, want no secret and no value: %q", secret, line)
		}
	}
}
