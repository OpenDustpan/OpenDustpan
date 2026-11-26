package agenttest

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ConnectionStoreHarness is one fresh store of connection documents for one
// case.
type ConnectionStoreHarness struct {
	Store agent.ConnectionStore
	// Items is the secret item store of the same database as Store.
	Items agent.SecretItems
	// Allowlist is the egress allowlist store of the same database as Store.
	// A factory can leave it nil: the suite then skips its allowlist case.
	Allowlist agent.EgressAllowlistStore
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
		{name: "pending only delete", run: caseConnectionStorePendingOnlyDelete},
		{name: "list all pending", run: caseConnectionStoreListPending},
		{name: "delete all in one transaction", run: caseConnectionStoreDeleteAll},
		{name: "missing secret reference", run: caseConnectionStoreMissingSecretRef},
		{name: "missing secret reference names the item", run: caseConnectionStoreMissingSecretRefSentinel},
		{name: "secret item in use", run: caseConnectionStoreSecretInUse},
		{name: "a write with an entry of another scope is refused", run: caseConnectionStoreWrongScopeEntry},
		{name: "an allowlist that the rules refuse is refused in Set", run: caseConnectionStoreAllowlistRules},
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

func caseConnectionStorePendingOnlyDelete(t *testing.T, h ConnectionStoreHarness) {
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

	// No document was promoted yet, so only the pending document exists.
	probe := agent.ProbeRef("probe-pending-only")

	if err := h.Store.PutPending(t.Context(), probe, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	// The disconnect removes the pending document too, also when no
	// current document exists.
	if err := h.Store.Delete(t.Context(), entry); err != nil {
		t.Fatalf("Delete() of a pending document without a current one = %v, want nil", err)
	}

	if _, _, pendErr := h.Store.Pending(t.Context(), probe); !errors.Is(pendErr, agent.ErrNotFound) {
		t.Fatalf("Pending() after delete = %v, want ErrNotFound", pendErr)
	}

	if _, _, curErr := h.Store.Current(t.Context()); !errors.Is(curErr, agent.ErrDisabled) {
		t.Fatalf("Current() after delete = %v, want ErrDisabled", curErr)
	}
}

func caseConnectionStoreListPending(t *testing.T, h ConnectionStoreHarness) {
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

	first := agent.ProbeRef("probe-list-1")

	if err := h.Store.PutPending(t.Context(), first, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	// created_at_ms holds Unix milliseconds: wait one millisecond, so
	// that the two writes keep their order.
	waitOneMillisecond(t)

	second := agent.ProbeRef("probe-list-2")

	if err := h.Store.PutPending(t.Context(), second, doc, entry); err != nil {
		t.Fatalf("PutPending() = %v", err)
	}

	wantPendingList(t, h, first, second)

	// A promote removes its pending document from the list.
	if err := h.Store.Promote(t.Context(), first, entry); err != nil {
		t.Fatalf("Promote() = %v", err)
	}

	list, err := h.Store.PendingAll(t.Context())
	if err != nil {
		t.Fatalf("PendingAll() after promote = %v", err)
	}

	if len(list) != 1 || list[0].Probe != second {
		t.Fatalf("PendingAll() after promote = %d entries, want the second probe only", len(list))
	}
}

// wantPendingList checks that PendingAll returns the two pending documents,
// oldest first, each with its version and its format.
func wantPendingList(t *testing.T, h ConnectionStoreHarness, first, second agent.ProbeRef) {
	t.Helper()

	list, err := h.Store.PendingAll(t.Context())
	if err != nil {
		t.Fatalf("PendingAll() = %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("PendingAll() = %d pending documents, want 2", len(list))
	}

	if list[0].Probe != first || list[1].Probe != second {
		t.Fatalf("PendingAll() = %v, %v, want the probes oldest first", list[0].Probe, list[1].Probe)
	}

	for i := range list {
		if list[i].Version != 1 {
			t.Fatalf("PendingAll() entry %d version = %d, want 1", i, list[i].Version)
		}

		if list[i].Spec.Format() != "chat-completions" {
			t.Fatalf("PendingAll() entry %d format = %q, want chat-completions", i, list[i].Spec.Format())
		}
	}
}

// waitOneMillisecond waits one millisecond, or fails the test when its
// context ends first.
func waitOneMillisecond(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatalf("wait of one millisecond: %v", t.Context().Err())
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

// caseConnectionStoreMissingSecretRefSentinel sends a document that refers to
// a secret item that the store does not know. The write fails with
// agent.ErrSecretNotFound, so a caller can tell a missing item from a fault of
// the store.
func caseConnectionStoreMissingSecretRefSentinel(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "header": "Authorization", "prefix": "Bearer ", "secret": "itm-nothere" },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-3-ref")

	err := h.Store.PutPending(t.Context(), probe, doc, entry)
	if !errors.Is(err, agent.ErrSecretNotFound) {
		t.Fatalf("PutPending() with a missing secret ref = %v, want ErrSecretNotFound", err)
	}
}

// caseConnectionStoreWrongScopeEntry writes a pending document with an audit
// entry of a namespace scope. The store refuses it: a write of the connection
// records its entry in the scope @server, and a new caller cannot store a
// record of another scope.
func caseConnectionStoreWrongScopeEntry(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	entry := TestEntry(namespace.Name("ns-other"), "admin", "server:manage-agent")

	doc := mustParseConnectionSpec(t, `{
  "version": 1,
  "format": "chat-completions",
  "baseURL": "https://example.local/v1",
  "model": "example-model",
  "auth": { "none": true },
  "outputTokens": { "field": "max_tokens", "max": 8192 },
  "tools": { "choice": "auto", "parallel": "omit" }
}`)

	probe := agent.ProbeRef("probe-scope")

	err := h.Store.PutPending(t.Context(), probe, doc, entry)
	if !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("PutPending() with an entry of a namespace scope = %v, want ErrInvalid", err)
	}

	if _, _, pendErr := h.Store.Pending(t.Context(), probe); !errors.Is(pendErr, agent.ErrNotFound) {
		t.Fatalf("Pending() after the refused write = %v, want ErrNotFound: the write stored nothing", pendErr)
	}
}

// caseConnectionStoreAllowlistRules writes an allowlist with a rule without a
// port. The store refuses it, as the service does, so a new caller cannot
// store a list that the rules would refuse. A list that the rules accept is
// stored and read back unchanged.
func caseConnectionStoreAllowlistRules(t *testing.T, h ConnectionStoreHarness) {
	t.Helper()

	if h.Allowlist == nil {
		t.Skip("the harness has no allowlist store")
	}

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-settings")

	noPort := egress.Allowlist{
		Hosts: []string{"models.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 0, HTTP: false}},
	}

	if err := h.Allowlist.Set(t.Context(), noPort, entry); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Set() with a rule without a port = %v, want ErrInvalid", err)
	}

	stored := egress.Allowlist{
		Hosts: []string{"models.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 443, HTTP: true}},
	}

	if err := h.Allowlist.Set(t.Context(), stored, entry); err != nil {
		t.Fatalf("Set() = %v, want nil", err)
	}

	got, err := h.Allowlist.Get(t.Context())
	if err != nil {
		t.Fatalf("Get() = %v, want nil", err)
	}

	if len(got.Hosts) != 1 || got.Hosts[0] != stored.Hosts[0] {
		t.Fatalf("Get() hosts = %v, want %v", got.Hosts, stored.Hosts)
	}

	if len(got.CIDRs) != 1 || got.CIDRs[0] != stored.CIDRs[0] {
		t.Fatalf("Get() rules = %v, want %v", got.CIDRs, stored.CIDRs)
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
