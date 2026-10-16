package dustpan

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestPruneSignInRequestsOnce proves that the server deletes the sign-in
// requests that expired, and keeps the others.
func TestPruneSignInRequestsOnce(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	var offset atomic.Int64

	start := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)
	now := func() time.Time { return start.Add(time.Duration(offset.Load())) }

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: now})
	if err != nil {
		t.Fatalf("policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Providers: nil, Policies: policies, Inactivity: nil, Now: now},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	for range 2 {
		state, stateErr := session.NewSecret()
		if stateErr != nil {
			t.Fatalf("state: %v", stateErr)
		}

		begin := session.Begin{
			State: state, Provider: "corp", Nonce: "n-0S6_WzA2Mj-rG7k3QpL",
			Verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", ReturnPath: "/", MaxAge: 0, HasMaxAge: false,
		}
		if beginErr := sessions.BeginRequest(t.Context(), begin); beginErr != nil {
			t.Fatalf("begin: %v", beginErr)
		}
	}

	log := &bytes.Buffer{}
	srv := sweepChainServer(t, log, nil, nil, nil)
	srv.signInRequests = sessions

	srv.pruneSignInRequestsOnce(t.Context())

	if strings.Contains(log.String(), "pruned") {
		t.Fatalf("prune before the lifetime logged %q, want no prune", log.String())
	}

	offset.Store(int64(session.RequestLifetime))
	srv.pruneSignInRequestsOnce(t.Context())

	if !strings.Contains(log.String(), "pruned the expired sign-in requests") ||
		!strings.Contains(log.String(), "requests=2") {
		t.Fatalf("log = %q, want a prune of 2 requests", log.String())
	}
}
