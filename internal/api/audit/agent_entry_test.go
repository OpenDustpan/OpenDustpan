package audit_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errorCodeOf serves target and returns the status and the error code of the
// answer.
func errorCodeOf(t *testing.T, handler http.Handler, target string) (int, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, target))

	var body struct {
		Code string `json:"code"`
	}

	// The error body of this API carries the HTTP status in "code" and the
	// catalog code in "errorCode"; read both shapes by their own names.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, rec.Body.String())
	}

	for _, key := range []string{"errorCode", "code"} {
		if s, ok := raw[key].(string); ok {
			body.Code = s

			break
		}
	}

	return rec.Code, body.Code
}

// TestAuditEntryNotFoundCode proves that GET /api/audit/entries/{sequence}
// answers 404 with audit.entry-not-found for a sequence that the scope does
// not hold.
func TestAuditEntryNotFoundCode(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	status, code := errorCodeOf(t, handler, "/api/audit/entries/99?organization=acme")
	if status != http.StatusNotFound || code != errcode.AuditEntryNotFound {
		t.Fatalf("answer = %d %q, want %d %q", status, code, http.StatusNotFound, errcode.AuditEntryNotFound)
	}
}

// TestAuditEntryReadUnavailableCode proves that the chain check answers
// audit.entry-read-unavailable when the store cannot read an entry, and that
// the answer holds a fixed text and not the text of the row.
func TestAuditEntryReadUnavailableCode(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	store.err = fmt.Errorf("read entry 1: %w: tool holds a hidden character", audit.ErrInvalidOrigin)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/check?organization=acme"))

	status, code := errorCodeOf(t, handler, "/api/audit/check?organization=acme")
	if status != http.StatusInternalServerError || code != errcode.AuditEntryReadUnavailable {
		t.Fatalf("answer = %d %q, want %d %q", status, code, http.StatusInternalServerError,
			errcode.AuditEntryReadUnavailable)
	}

	if body := rec.Body.String(); body == "" || strings.Contains(body, "hidden character") {
		t.Fatalf("answer = %s, want a fixed text with no row text", body)
	}
}

// TestAuditTamperedBlockFailsCheck proves that GET /api/audit/check refuses
// the chain after one byte of the agent block of a stored entry changes, and
// accepts it before.
func TestAuditTamperedBlockFailsCheck(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	withBlock := seedEntry(t, store, "acme", "ada")
	withBlock.Sequence, withBlock.PrevHash, withBlock.Hash = 0, "", ""
	withBlock.Channel = audit.ChannelAgent
	withBlock.Origin = mustAgentOrigin(t, "run-1", "run-1")

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	if _, err := store.Append(t.Context(), fence, withBlock); err != nil {
		t.Fatalf("append entry with a block: %v", err)
	}

	var res struct {
		Valid       bool  `json:"valid"`
		BadSequence int64 `json:"badSequence"`
	}

	const target = "/api/audit/check?organization=acme"

	if status := getJSON(t, handler, target, &res); status != http.StatusOK || !res.Valid {
		t.Fatalf("check before the change = %d %+v, want a valid chain", status, res)
	}

	// One byte of the run changes behind the back of the store.
	store.entries["acme"][2].Origin = mustAgentOrigin(t, "run-2", "run-1")

	if status := getJSON(t, handler, target, &res); status != http.StatusOK || res.Valid || res.BadSequence != 3 {
		t.Fatalf("check after the change = %d %+v, want an invalid chain at sequence 3", status, res)
	}
}

// TestAuditEntryLegacyAgentKeyRead proves that GET /api/audit/entries/{sequence}
// answers the agent of an old start entry from its legacy change key, and
// that the block wins when an entry holds both.
func TestAuditEntryLegacyAgentKeyRead(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	legacy := seedEntry(t, store, "acme", "alice")
	legacy.Changes = []audit.Change{
		{Key: audit.LegacyAgentChangeKey, Old: nil, New: "old-run", Layer: "", TakesEffect: ""},
	}
	legacy.Sequence, legacy.PrevHash = 0, ""

	if _, err := store.Append(t.Context(), fence, legacy); err != nil {
		t.Fatalf("append legacy entry: %v", err)
	}

	both := legacy
	both.Origin = mustAgentOrigin(t, "new-run", "new-run")

	if _, err := store.Append(t.Context(), fence, both); err != nil {
		t.Fatalf("append entry with both shapes: %v", err)
	}

	for seq, want := range map[int]string{2: "old-run", 3: "new-run"} {
		var got map[string]any

		target := fmt.Sprintf("/api/audit/entries/%d?organization=acme", seq)
		if status := getJSON(t, handler, target, &got); status != http.StatusOK {
			t.Fatalf("GET %s answers %d", target, status)
		}

		block, ok := got["agent"].(map[string]any)
		if !ok || block["agentRun"] != want || block["agentSession"] != want {
			t.Fatalf("entry %d agent = %v, want run and session %q", seq, got["agent"], want)
		}
	}
}
