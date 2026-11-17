package dustpan

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// recordingServerAudit keeps the installation audit entries of one test.
type recordingServerAudit struct {
	entries []audit.Entry
}

// AppendServer implements audit.ServerStore.
func (s *recordingServerAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	s.entries = append(s.entries, e)

	return e, nil
}

// TestDenyAnyListDenies is the hostile test of the checker: an address inside
// the namespace list but outside the installation list is refused with the
// installation scope, and the denial entry holds the scope and the address.
// It fails if the checker ignores the installation list.
func TestDenyAnyListDenies(t *testing.T) {
	t.Parallel()

	installation, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		t.Fatalf("parse the installation list: %v", err)
	}

	namespace, err := ipallow.ParseList([]string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("parse the namespace list: %v", err)
	}

	store := &recordingServerAudit{entries: nil}
	auditor := allowlistAudit{
		store: store, now: func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) },
		logger: slog.New(slog.DiscardHandler),
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody)
	record := func(ctx context.Context, d allowDenial) { auditor.record(ctx, req, d) }

	checker := newAllowChecker(true, installation, &namespace, nil, record)
	addr := netip.MustParseAddr("203.0.113.7")

	decision := checker.check(t.Context(), addr)
	if !decision.Denied || decision.Scope != allowScopeInstallation {
		t.Fatalf("decision = %+v, want a denial with scope installation", decision)
	}

	if len(store.entries) != 1 {
		t.Fatalf("denial entries = %d, want 1", len(store.entries))
	}

	entry := store.entries[0]
	if entry.Object != string(allowScopeInstallation) || entry.SourceIP != addr.String() ||
		entry.Outcome != audit.OutcomeDenied || entry.Kind != audit.KindSecurity {
		t.Fatalf("denial entry = %+v, want the installation scope, the address and a security denial", entry)
	}

	if err := entry.Validate(); err != nil {
		t.Fatalf("denial entry is not valid: %v", err)
	}
}
