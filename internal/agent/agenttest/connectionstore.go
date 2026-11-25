package agenttest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/audit"
)

// ConnectionStoreHarness is one fresh store of connection documents for one
// case.
type ConnectionStoreHarness struct {
	Store agent.ConnectionStore
	// Items is the secret item store of the same database as Store.
	Items agent.SecretItems
}

// ConnectionStoreFactory returns a fresh harness for one case.
type ConnectionStoreFactory func(t *testing.T) ConnectionStoreHarness

// RunConnectionStore runs the connection store suite against the store that f
// makes.
func RunConnectionStore(t *testing.T, f ConnectionStoreFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ConnectionStoreHarness)
	}{
		{name: "current answers ErrDisabled when none exists", run: caseConnectionStoreDisabled},
		{name: "pending and promote", run: caseConnectionStorePendingPromote},
		{name: "delete all in one transaction", run: caseConnectionStoreDeleteAll},
		{name: "missing secret reference", run: caseConnectionStoreMissingSecretRef},
		{name: "secret item in use", run: caseConnectionStoreSecretInUse},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

func caseConnectionStoreDisabled(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	_, _, err := h.Store.Current(t.Context())
	if !errors.Is(err, agent.ErrDisabled) {
		t.Fatalf("Current() = %v, want ErrDisabled: no connection exists yet", err)
	}
}

func caseConnectionStorePendingPromote(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-1")

	if err := h.Store.PutPending(t.Context(), probe, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	got, ver, err := h.Store.Pending(t.Context(), probe)
	if err != nil {
		t.Fatalf("Pending() = %v", err)
	}

	if got.Format() != "chat-completions" {
		t.Fatalf("Pending() format = %q, want chat-completions", got.Format())
	}

	if ver != 1 {
		t.Fatalf("Pending() version = %d, want 1", ver)
	}

	if err = h.Store.Promote(t.Context(), probe, entry); err != nil {
		t.Fatalf("Promote() = %v", err)
	}

	cur, curVer, err := h.Store.Current(t.Context())
	if err != nil {
		t.Fatalf("Current() = %v", err)
	}

	if cur.Format() != "chat-completions" {
		t.Fatalf("Current() format = %q, want chat-completions", cur.Format())
	}

	if curVer != 1 {
		t.Fatalf("Current() version = %d, want 1", curVer)
	}

	_, _, err = h.Store.Pending(t.Context(), probe)
	if !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("Pending() after promote = %v, want ErrNotFound", err)
	}
}

func caseConnectionStoreDeleteAll(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-2")

	if err := h.Store.PutPending(t.Context(), probe, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	if err := h.Store.Promote(t.Context(), probe, entry); err != nil {
		t.Fatalf("Promote() = %v", err)
	}

	if err := h.Store.Delete(t.Context(), entry); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if _, _, curErr := h.Store.Current(t.Context()); !errors.Is(curErr, agent.ErrDisabled) {
		t.Fatalf("Current() after delete = %v, want ErrDisabled", curErr)
	}

	if _, _, pendErr := h.Store.Pending(t.Context(), probe); !errors.Is(pendErr, agent.ErrNotFound) {
		t.Fatalf("Pending() after delete = %v, want ErrNotFound", pendErr)
	}
}

func caseConnectionStoreMissingSecretRef(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-nope" },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-3")

	if err := h.Store.PutPending(t.Context(), probe, doc, entry); err == nil {
		t.Fatal("PutPending() with a missing secret ref = nil, want an error")
	}
}

func caseConnectionStoreSecretInUse(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	item, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "`+string(item.Ref)+`" },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-4")

	if err = h.Store.PutPending(t.Context(), probe, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	if err = h.Items.Delete(t.Context(), item.Ref, entry); !errors.Is(err, agent.ErrSecretInUse) {
		t.Fatalf("Delete() of an item that a pending document uses = %v, want ErrSecretInUse", err)
	}

	if err = h.Store.Promote(t.Context(), probe, entry); err != nil {
		t.Fatalf("Promote() = %v", err)
	}

	if err = h.Items.Delete(t.Context(), item.Ref, entry); !errors.Is(err, agent.ErrSecretInUse) {
		t.Fatalf("Delete() of an item that the current document uses = %v, want ErrSecretInUse", err)
	}

	if err = h.Store.Delete(t.Context(), entry); err != nil {
		t.Fatalf("Delete() of the connection = %v", err)
	}

	if err = h.Items.Delete(t.Context(), item.Ref, entry); err != nil {
		t.Fatalf("Delete() after the connection is gone = %v, want nil", err)
	}
}

func mustParseConnectionSpec(t *testing.T, raw string) agent.ConnectionSpec {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(raw))
	if err != nil {
		t.Fatalf("ParseConnectionSpec() = %v, want no error", err)
	}

	return spec
}
