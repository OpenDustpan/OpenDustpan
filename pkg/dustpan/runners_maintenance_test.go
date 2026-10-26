package dustpan_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// runnerSeedEntry returns a valid audit entry in the namespace acme for the
// rows that the maintenance tests seed through the store.
func runnerSeedEntry() []audit.Entry {
	return []audit.Entry{{
		At: time.Now().UTC(), Namespace: "acme", Subject: "operator", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-runners", Object: "namespace:acme", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}}
}

// seedRunnerRows creates a pool, an unused join token and a runner of the
// pool in acme, and returns the pool ID, the token ID and the runner ID.
func seedRunnerRows(t *testing.T, st *sqlite.Store) (pool, token, rn string) {
	t.Helper()

	ctx := t.Context()
	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	now := time.Now().UTC()

	p, err := st.Runners().CreatePool(ctx, runner.Pool{
		ID: "", Namespace: "acme", Name: "builds", Purpose: runner.PurposeJobs, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      now, UpdatedAt: now,
	}, fence, runnerSeedEntry())
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	newToken := func(secret string) runner.JoinToken {
		sum := sha256.Sum256([]byte(runner.JoinTokenPrefix + secret))

		tok, tokErr := st.Runners().CreateJoinToken(ctx, runner.JoinToken{
			ID: "", Pool: p.ID, SHA256: hex.EncodeToString(sum[:]), CreatedBy: "operator", CreatedAt: now,
			ExpiresAt: now.Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
		}, fence, runnerSeedEntry())
		if tokErr != nil {
			t.Fatalf("create join token: %v", tokErr)
		}

		return tok
	}

	unused := newToken("unused-secret-0123456789")
	used := newToken("used-secret-0123456789")

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate runner key: %v", err)
	}

	joined, err := st.Runners().Join(ctx, runner.Joining{
		TokenSHA256: used.SHA256,
		Request: runner.JoinRequest{
			Token: "", Name: "linux-1", Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes,
			Labels:  map[string]string{},
			Release: runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""},
			Key:     runner.Key{Public: pub, CreatedAt: time.Time{}}, Proof: nil,
		},
		At: now,
	}, fence, func(*runner.Joined) []audit.Entry { return runnerSeedEntry() })
	if err != nil {
		t.Fatalf("join runner: %v", err)
	}

	return p.ID, unused.ID, joined.Runner.ID
}

// TestRunnerManagementInMaintenanceMode proves that only the routes which
// reduce access pass the maintenance gate: the retire of a runner and the
// revoke of a join token succeed, and every other route of the management
// API answers 503.
func TestRunnerManagementInMaintenanceMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   func(pool, token, rn string) string
		body   string
		want   int
	}{
		{
			"retire a runner", http.MethodPost,
			func(_, _, rn string) string { return "/api/runners/acme/runners/" + rn + "/retire" }, "",
			http.StatusNoContent,
		},
		{
			"revoke a join token", http.MethodPost,
			func(pool, token, _ string) string {
				return "/api/runners/acme/pools/" + pool + "/join-tokens/" + token + "/revoke"
			}, "", http.StatusNoContent,
		},
		{
			"drain a runner", http.MethodPost,
			func(_, _, rn string) string { return "/api/runners/acme/runners/" + rn + "/drain" }, "",
			http.StatusServiceUnavailable,
		},
		{
			"create a pool", http.MethodPost, func(_, _, _ string) string { return "/api/runners/acme/pools" },
			`{"name":"other","purpose":"jobs"}`, http.StatusServiceUnavailable,
		},
		{
			"update a pool", http.MethodPatch,
			func(pool, _, _ string) string { return "/api/runners/acme/pools/" + pool }, `{}`,
			http.StatusServiceUnavailable,
		},
		{
			"delete a pool", http.MethodDelete,
			func(pool, _, _ string) string { return "/api/runners/acme/pools/" + pool }, "",
			http.StatusServiceUnavailable,
		},
		{
			"create a join token", http.MethodPost,
			func(pool, _, _ string) string { return "/api/runners/acme/pools/" + pool + "/join-tokens" }, `{}`,
			http.StatusServiceUnavailable,
		},
		{
			"list pools", http.MethodGet, func(_, _, _ string) string { return "/api/runners/acme/pools" }, "",
			http.StatusServiceUnavailable,
		},
		{
			"list runners", http.MethodGet,
			func(pool, _, _ string) string { return "/api/runners/acme/pools/" + pool + "/runners" }, "",
			http.StatusServiceUnavailable,
		},
		{
			"retire with another method", http.MethodGet,
			func(_, _, rn string) string { return "/api/runners/acme/runners/" + rn + "/retire" }, "",
			http.StatusServiceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				st                *sqlite.Store
				pool, token, runn string
			)

			h := runnerServer(t, "https://dustpan.test/", func(s *sqlite.Store) dustpan.Option {
				st = s

				return withRunnerStores(s)
			})

			pool, token, runn = seedRunnerRows(t, st)

			if err := st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
				t.Fatalf("enter maintenance mode: %v", err)
			}

			waitForRunnerAPIStatus(t, h, http.StatusServiceUnavailable)

			req := httptest.NewRequestWithContext(
				t.Context(), tc.method, tc.path(pool, token, runn), strings.NewReader(tc.body),
			)
			req.Header.Set("Authorization", "token "+runnerAdminToken)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("%s %s in maintenance mode = %d %s, want %d", tc.method, req.URL.Path, rec.Code,
					rec.Body.String(), tc.want)
			}
		})
	}
}

// waitForRunnerAPIStatus polls the pool list until the management API
// answers want: the maintenance watcher caches the state for a short time.
func waitForRunnerAPIStatus(t *testing.T, h http.Handler, want int) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/runners/acme/pools", nil)
		req.Header.Set("Authorization", "token "+runnerAdminToken)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code == want {
			return
		}

		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatalf("test ended while waiting for status %d", want)
		}
	}

	t.Fatalf("the management API never answered %d", want)
}
