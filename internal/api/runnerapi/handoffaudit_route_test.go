package runnerapi_test

import (
	"net/http"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// refusalEntries returns the handoff refusal entries of the store.
func refusalEntries(t *testing.T, store *testAudit) []audit.Entry {
	t.Helper()

	listing, err := store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if changeOf(&listing.Entries[i], "event") == string(runner.EventHandoffRefused) {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// TestSessionRouteWritesOneEntryPerRefusal drives POST /runner/v1/session over
// HTTP. A refused exchange writes one entry with its reason. A successful
// exchange writes none. A repeat of one refusal from one client writes no second
// entry, and the counter rises.
func TestSessionRouteWritesOneEntryPerRefusal(t *testing.T) {
	t.Parallel()

	store, dropped := newTestAudit(), new(atomic.Int32)
	s := newSessionEnvWith(t, backend{name: "sqlite", open: openSQLite}, func(cfg *runnerapi.Config) {
		cfg.Audit, cfg.OnHandoffRefusalSuppressed = store, func() { dropped.Add(1) }
	})

	// A successful issue of a handoff token and a successful exchange write no
	// refusal entry.
	token := s.runner.handoff(s.work.Operation)

	if got := len(refusalEntries(t, store)); got != 0 {
		t.Fatalf("entries after a successful issue = %d, want 0", got)
	}

	if status, body := s.sendExchange(s.newExchange(token)); status != http.StatusOK {
		t.Fatalf("valid exchange = %d %s, want 200", status, body)
	}

	if got := len(refusalEntries(t, store)); got != 0 {
		t.Fatalf("entries after a successful exchange = %d, want 0", got)
	}

	checkUnknownTokenRefusal(t, s, store, dropped)
	checkWrongRunnerRefusal(t, s, store)
}

// checkUnknownTokenRefusal sends an exchange with an unknown token twice. The
// first writes exactly one entry. The second keeps the answer, writes no entry
// and raises the counter.
func checkUnknownTokenRefusal(t *testing.T, s *sessionEnv, store *testAudit, dropped *atomic.Int32) {
	t.Helper()

	unknown := s.newExchange(runner.HandoffTokenPrefix + "unknown-token-value-for-the-route-test")

	status, body := s.sendExchange(unknown)
	if status != http.StatusNotFound || errorCode(t, body) != protocol.ErrorNotFound {
		t.Fatalf("unknown token = %d %s, want 404 not-found", status, body)
	}

	entries := refusalEntries(t, store)
	if len(entries) != 1 || entries[0].Reason != string(runner.HandoffUnknown) ||
		entries[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("entries after an unknown token = %+v, want one denied unknown entry", entries)
	}

	if holdsToken(&entries[0]) || entries[0].Subject != "unverified-launcher" {
		t.Errorf("entry holds a token or has subject %q", entries[0].Subject)
	}

	// The same refusal again keeps the answer, writes no entry and counts.
	status, body = s.sendExchange(s.newExchange(unknown.token))
	if status != http.StatusNotFound {
		t.Fatalf("second unknown token = %d %s, want 404", status, body)
	}

	if got := len(refusalEntries(t, store)); got != 1 || dropped.Load() != 1 {
		t.Errorf("entries = %d, dropped = %d, want 1 and 1", got, dropped.Load())
	}
}

// checkWrongRunnerRefusal sends the handoff request of a runner that does not
// hold the claim. The answer is claim-lost, and one more entry names the
// verified runner.
func checkWrongRunnerRefusal(t *testing.T, s *sessionEnv, store *testAudit) {
	t.Helper()

	other := s.newRunner("builder-2")
	var (
		status int
		body   []byte
	)

	if status, body = other.join(s.pool, s.joinToken(s.pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	status, body = other.call(opPath(s.work.Operation, "handoff"), nil)
	if status != http.StatusConflict || errorCode(t, body) != protocol.ErrorClaimLost {
		t.Fatalf("handoff by another runner = %d %s, want 409 claim-lost", status, body)
	}

	entries := refusalEntries(t, store)
	if len(entries) != 2 {
		t.Fatalf("entries after the wrong runner = %d, want 2", len(entries))
	}

	if last := entries[1]; last.Reason != string(runner.HandoffWrongRunner) || last.Subject != other.id ||
		changeOf(&last, "runnerVerified") != "true" {
		t.Errorf("entry of the wrong runner = %+v, want wrong-runner by %s, verified", last, other.id)
	}
}
