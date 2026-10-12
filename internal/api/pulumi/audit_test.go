package pulumi_test

import (
	"bytes"
	"context"
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

	return audit.CheckResult{Checked: int64(len(m.entries)), BadSequence: 0, Valid: true}, nil
}

// errAuditBoom is the injected store failure of the failure test.
var errAuditBoom = errors.New("audit store boom")

// auditRequest sends one log-decryption request with the request ID, source
// address, user agent and channel of a CLI that shows secrets.
func auditRequest(
	t *testing.T,
	f secretFixture,
	target, userAgent, channel, requestID string,
) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), requestID),
		http.MethodPost,
		target,
		strings.NewReader(`{"secretName":"apiKey"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Dustpan-Channel", channel)
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

	entries := &memAuditStore{}

	f := buildSecretFixture(t, allowAll(), 0, entries, nil, slog.New(slog.DiscardHandler))

	for _, target := range []string{
		"/api/stacks/acme/web/prod/decrypt/log-decryption",
		"/api/stacks/acme/web/prod/decrypt/log-batch-decryption",
	} {
		status, body := auditRequest(t, f, target, "pulumi-cli/3.200.0", "console", "req-7")
		if status != http.StatusNoContent {
			t.Fatalf("%s answers %d, want %d (%s)", target, status, http.StatusNoContent, body)
		}
	}

	entries.mu.Lock()
	defer entries.mu.Unlock()

	if len(entries.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries.entries))
	}

	for _, e := range entries.entries {
		switch {
		case e.Action != string(identity.ActionUnitDecrypt):
			t.Fatalf("action = %q, want unit:decrypt", e.Action)
		case e.Kind != audit.KindReveal:
			t.Fatalf("kind = %q, want reveal", e.Kind)
		case e.Subject != "tester":
			t.Fatalf("subject = %q, want tester", e.Subject)
		case e.Object != "pulumi:acme/web/prod":
			t.Fatalf("object = %q, want the unit reference", e.Object)
		case e.SourceIP != "192.0.2.10":
			t.Fatalf("source IP = %q, want 192.0.2.10", e.SourceIP)
		case e.UserAgent != "pulumi-cli/3.200.0":
			t.Fatalf("user agent = %q, want the CLI agent", e.UserAgent)
		case e.Channel != audit.ChannelConsole:
			t.Fatalf("channel = %q, want console", e.Channel)
		case e.RequestID != "req-7":
			t.Fatalf("request ID = %q, want req-7", e.RequestID)
		case e.Outcome != audit.OutcomeSuccess:
			t.Fatalf("outcome = %q, want success", e.Outcome)
		case e.Hash == "":
			t.Fatal("hash is empty")
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

	entries := &memAuditStore{err: errAuditBoom}

	f := buildSecretFixture(t, allowAll(), 0, entries, func() { failures++ }, logger)

	status, body := auditRequest(t, f, "/api/stacks/acme/web/prod/decrypt/log-decryption", "agent", "api", "req-9")
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
