package postgres_test

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/trustrule/trustruletest"
)

// trustRuleHarness returns a harness over a fresh store in its own schema.
// The namespaces it makes have failover version 1.
func trustRuleHarness(base string) trustruletest.Factory {
	return func(t *testing.T) trustruletest.Harness {
		t.Helper()

		return newTrustRuleHarness(t, openURL(t, isolatedURL(t, base)))
	}
}

func newTrustRuleHarness(t *testing.T, s *postgres.Store) trustruletest.Harness {
	t.Helper()

	var (
		mu    sync.Mutex
		homes = map[string]namespace.Name{}
	)

	return trustruletest.Harness{
		Store:            s.TrustRules(),
		Exchanges:        s.TrustRules(),
		Token:            trustRuleToken(s),
		DisablePrincipal: trustRuleDisable(s),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Audit: func(t *testing.T, ns string) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, namespace.Name(ns))
		},
		Principal: func(t *testing.T, ns namespace.Name, kind principal.Kind) string {
			t.Helper()

			p := trustRulePrincipal(ns, kind)

			var err error
			if ns == "" {
				err = s.CreatePrincipal(t.Context(), p, trustRulePrincipalEntry(audit.ServerScope, p.ID))
			} else {
				err = s.CreateNamespacePrincipal(
					t.Context(), p, ensureNamespace(t, s, ns), trustRulePrincipalEntry(ns, p.ID),
				)
			}

			if err != nil {
				t.Fatalf("create principal: %v", err)
			}

			mu.Lock()
			homes[p.ID] = ns
			mu.Unlock()

			return p.ID
		},
		DeletePrincipal: func(t *testing.T, id string) {
			t.Helper()

			mu.Lock()
			ns := homes[id]
			mu.Unlock()

			var err error
			if ns == "" {
				err = s.DeletePrincipal(t.Context(), id, trustRulePrincipalEntry(audit.ServerScope, id))
			} else {
				err = s.DeleteNamespacePrincipal(
					t.Context(), id, ensureNamespace(t, s, ns), trustRulePrincipalEntry(ns, id),
				)
			}

			if err != nil {
				t.Fatalf("delete principal %s: %v", id, err)
			}
		},
	}
}

// trustRulePrincipal returns a new principal of the kind kind, of the
// namespace ns or of the installation when ns is empty.
func trustRulePrincipal(ns namespace.Name, kind principal.Kind) principal.Principal {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	id := ident.NewID(principal.IDPrefix)

	return principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: "subject-" + id,
		DisplayName: id, Kind: kind, Source: principal.SourceLocal, Namespace: ns, GroupCount: 0,
		BreakGlass: false,
	}
}

// trustRulePrincipalEntry returns the audit entry of a principal change in
// the scope ns.
func trustRulePrincipalEntry(ns namespace.Name, id string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: ns, Subject: "admin", IDPSubject: "",
		TokenID: "", Action: "server:manage-principals", Object: principal.Ref(id), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

func TestTrustRuleConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	trustruletest.Run(t, trustRuleHarness(base))
}

// trustRuleToken reads an access token of s by its ID, and reports false
// when no token has the ID.
func trustRuleToken(s *postgres.Store) func(t *testing.T, id string) (accesstoken.Token, bool) {
	return func(t *testing.T, id string) (accesstoken.Token, bool) {
		t.Helper()

		tok, err := s.GetToken(t.Context(), id)
		if errors.Is(err, accesstoken.ErrNotFound) {
			return tok, false
		}

		if err != nil {
			t.Fatalf("get token %s: %v", id, err)
		}

		return tok, true
	}
}

// trustRuleDisable disables a principal of s.
func trustRuleDisable(s *postgres.Store) func(t *testing.T, id string) {
	return func(t *testing.T, id string) {
		t.Helper()

		if err := s.DisablePrincipal(t.Context(), id, trustRulePrincipalEntry(audit.ServerScope, id)); err != nil {
			t.Fatalf("disable principal %s: %v", id, err)
		}
	}
}
