package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// deniedEntryWire is the part of an audit entry that the denial test reads.
type deniedEntryWire struct {
	Subject string `json:"subject"`
	Action  string `json:"action"`
	Object  string `json:"object"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// deniedEntries returns the denied entries of the scope, read through the
// audit API with the administrator token.
func (w *contextWorld) deniedEntries(t *testing.T, scope string) []deniedEntryWire {
	t.Helper()

	code, body := w.call(t, http.MethodGet, "/api/audit/entries?organization="+scope+"&page[size]=100",
		ctxAdminToken, "")
	if code != http.StatusOK {
		t.Fatalf("list %s entries: status = %d: %s", scope, code, body)
	}

	var list struct {
		Entries []deniedEntryWire `json:"entries"`
	}

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode entries %s: %v", body, err)
	}

	var denied []deniedEntryWire

	for _, e := range list.Entries {
		if e.Outcome == "denied" {
			denied = append(denied, e)
		}
	}

	return denied
}

// deniedCase is one refused request to an adapter and what its entry must say.
type deniedCase struct {
	name       string
	call       deniedCall
	scope      string
	wantAction string
}

// deniedCall is one request of the denial test. The installation API reads the
// scheme Bearer; the other APIs read the scheme token.
type deniedCall struct {
	method, target, body, scheme string
}

// TestDeniedRequestsAreAudited sends refused requests to the adapters of the
// inventory, context, audit and installation APIs with a principal that lacks
// the actions. Each refusal answers 403 and writes a denied entry in the scope
// of its object. A flood of the same refusal writes one entry.
func TestDeniedRequestsAreAudited(t *testing.T) {
	t.Parallel()

	cases := []deniedCase{
		{
			name: "audit API",
			call: deniedCall{
				method: http.MethodGet,
				target: "/api/audit/entries?organization=acme",
				body:   "",
				scheme: "token",
			},
			scope:      "acme",
			wantAction: "namespace:read-audit",
		},
		{
			name: "inventory API",
			call: deniedCall{
				method: http.MethodPost, target: "/api/dustpan/v1/namespaces/acme/collections",
				body: `{"name":"c1"}`, scheme: "token",
			},
			scope: "acme", wantAction: "",
		},
		{
			name: "OpenTofu HTTP backend",
			call: deniedCall{
				method: http.MethodPost, target: "/tofu/state/acme/net", body: `{"version":4}`, scheme: "Bearer",
			},
			scope: "acme", wantAction: "",
		},
		{
			name:  "installation API",
			call:  deniedCall{method: http.MethodGet, target: "/api/server/principals", body: "", scheme: "Bearer"},
			scope: "@server", wantAction: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newContextWorld(t)

			for range 25 {
				code, body := w.callAs(t, tc.call, ctxServiceToken)
				if code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403: %s", code, body)
				}
			}

			checkOneDeniedEntry(t, w.deniedEntries(t, tc.scope), tc.wantAction)
		})
	}
}

// checkOneDeniedEntry fails the test unless entries holds exactly one entry of
// svc-reader, with the policy reason and, when wantAction is set, that action.
func checkOneDeniedEntry(t *testing.T, entries []deniedEntryWire, wantAction string) {
	t.Helper()

	var mine []deniedEntryWire

	for _, e := range entries {
		if e.Subject == "svc-reader" {
			mine = append(mine, e)
		}
	}

	if len(mine) != 1 {
		t.Fatalf("denied entries of svc-reader = %+v, want 1 for 25 refusals", mine)
	}

	if wantAction != "" && mine[0].Action != wantAction {
		t.Fatalf("action = %q, want %q", mine[0].Action, wantAction)
	}

	if !strings.HasPrefix(mine[0].Reason, "denied by policy on ") {
		t.Fatalf("reason = %q, want it to name the policy and the route", mine[0].Reason)
	}
}

// callAs serves one request with the scheme of the call.
func (w *contextWorld) callAs(t *testing.T, c deniedCall, token string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	req.Header.Set("Authorization", c.scheme+" "+token)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	startedHandler(t, w.srv).ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// chainStore is the part of a SQL store that the limiter entry test uses.
type chainStore interface {
	audit.Store
	audit.ServerStore
	namespace.Store
}

// denyEverything refuses every action.
type denyEverything struct{}

func (denyEverything) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return identity.ErrDenied
}

func (denyEverything) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// TestLimiterEntriesPassTheStores writes the entries of the flood limit (the
// count of a quiet key, the aggregate of too many keys, the count of the
// shutdown flush, and the entry of a namespace that does not exist) through the
// SQL stores. Each store must accept them with a valid hash chain in the
// namespace and in the installation scope.
func TestLimiterEntriesPassTheStores(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()

		st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := st.Close(); closeErr != nil {
				t.Errorf("close: %v", closeErr)
			}
		})

		checkLimiterEntries(t, st)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()

		base := os.Getenv(postgresURLEnv)
		if base == "" {
			t.Skipf("set %s to run the PostgreSQL limiter entry test", postgresURLEnv)
		}

		checkLimiterEntries(t, openPostgresChainStore(t, base))
	})
}

// checkLimiterEntries runs the flood of the test through st and checks the
// chains.
func checkLimiterEntries(t *testing.T, st chainStore) {
	t.Helper()

	ctx := t.Context()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: installationName})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	now := time.Now()
	lim := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 3, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: func() time.Time { return now },
	})
	failures := 0
	recorder, err := refusal.New(denyEverything{}, st, svc, slog.New(slog.DiscardHandler), func() { failures++ })
	if err != nil {
		t.Fatalf("refusal.New: %v", err)
	}

	rec := recorder.WithLimiter(lim)

	deny := func(subject, ns string) {
		p := identity.Principal{
			Subject: subject, TokenID: "", DisplayName: "", Admin: false, TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		}

		err := rec.Authorize(ctx, nil, p, identity.ActionUnitWrite, identity.NamespaceObject(namespace.Name(ns)))
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Authorize answers %v, want %v", err, identity.ErrDenied)
		}
	}

	// A quiet key: 5 refusals, then silence past the window.
	for range 5 {
		deny("bo", "acme")
	}

	// A namespace that does not exist.
	deny("bo", "gone")

	// Too many principals: the aggregate entry.
	for i := range 20 {
		deny(fmt.Sprintf("p%d", i), "acme")
	}

	now = now.Add(2 * time.Minute)

	// The next refusal flushes the counts of the window that ended.
	deny("late", "acme")

	// The shutdown flush writes what is left.
	rec.Flush(ctx)

	if failures != 0 {
		t.Fatalf("counted %d failed appends, want 0", failures)
	}

	for _, scope := range []namespace.Name{"acme", audit.ServerScope} {
		res, checkErr := st.Check(ctx, scope)
		if checkErr != nil || !res.Valid || res.Checked == 0 {
			t.Fatalf("check %s = %+v, %v, want a valid chain with entries", scope, res, checkErr)
		}
	}

	checkLimiterScopes(t, st)
}

// checkLimiterScopes checks that the scopes hold the entries of the flood
// limit.
func checkLimiterScopes(t *testing.T, st chainStore) {
	t.Helper()

	var aggregate, flushed, missing bool

	for _, scope := range []namespace.Name{"acme", audit.ServerScope} {
		list, err := st.List(t.Context(), scope, audit.Page{Number: 1, Size: 100})
		if err != nil {
			t.Fatalf("list %s: %v", scope, err)
		}

		for i := range list.Entries {
			e := list.Entries[i]
			if e.Outcome != audit.OutcomeDenied {
				t.Fatalf("entry %+v has the outcome %q, want denied", e, e.Outcome)
			}

			aggregate = aggregate || e.Action == "denials:suppressed"
			flushed = flushed || strings.Contains(e.Reason, "further refusals in the last window")
			missing = missing || strings.Contains(e.Reason, "does not exist")
		}
	}

	if !aggregate || !flushed || !missing {
		t.Fatalf("aggregate = %v, flush = %v, missing namespace = %v, want all true", aggregate, flushed, missing)
	}
}

// TestRunFlushesDenialCountsAtShutdown proves Run writes the counts that the
// limiter holds back before it returns, so the store is still open for the
// write: after Run returns, the namespace holds the entry of the first refusal
// and the entry that counts the others.
func TestRunFlushesDenialCountsAtShutdown(t *testing.T) {
	t.Parallel()

	ln := openTestListener(t)
	w := newContextWorld(t, dustpan.WithListener(ln))

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, w.srv)

	waitForTCP(t, ln.Addr().String())

	call := deniedCall{
		method: http.MethodPost, target: "/api/dustpan/v1/namespaces/acme/collections",
		body: `{"name":"c1"}`, scheme: "token",
	}

	for range 5 {
		if code, body := w.callAs(t, call, ctxServiceToken); code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", code, body)
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	list, err := w.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list acme entries: %v", err)
	}

	var first, counted bool

	for i := range list.Entries {
		e := list.Entries[i]
		if e.Subject != "svc-reader" || e.Outcome != audit.OutcomeDenied {
			continue
		}

		first = first || strings.HasPrefix(e.Reason, "denied by policy on ")
		counted = counted || strings.Contains(e.Reason, "4 further refusals in the last window")
	}

	if !first || !counted {
		t.Fatalf("first entry = %v, count entry = %v, want both after Run returned", first, counted)
	}
}
