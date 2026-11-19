package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// refusedIssuerStore shows the stored providers with the issuer that the
// current rule refuses, as a row of an older version or of a restored backup.
// The store itself refuses to write such a row. While issuer is empty, it
// changes nothing.
type refusedIssuerStore struct {
	session.ProviderConfigStore

	issuer *atomic.Value
}

func (s refusedIssuerStore) GetProviderConfig(ctx context.Context, id string) (session.ProviderConfig, error) {
	p, err := s.ProviderConfigStore.GetProviderConfig(ctx, id)

	return s.swap(p), err
}

func (s refusedIssuerStore) ListProviderConfigs(ctx context.Context) ([]session.ProviderConfig, error) {
	rows, err := s.ProviderConfigStore.ListProviderConfigs(ctx)
	for i := range rows {
		rows[i] = s.swap(rows[i])
	}

	return rows, err
}

func (s refusedIssuerStore) swap(p session.ProviderConfig) session.ProviderConfig {
	v, ok := s.issuer.Load().(string)
	if ok && v != "" && p.Mode == session.ModeOIDC {
		p.Issuer = v
	}

	return p
}

// TestSignInProviderDisablesAStoredRowWithARefusedIssuer shows that an
// administrator can disable a stored provider whose issuer the rule refuses,
// and that the answers mark the row.
func TestSignInProviderDisablesAStoredRowWithARefusedIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range map[string]string{
		"trailing dot":          "https://idp.example.",
		"dot-dot segment":       "https://idp.example/a/../b",
		"encoded dot-dot":       "https://idp.example/%2e%2e/b",
		"upper-case scheme":     "HTTPS://idp.example",
		"pipe in the path":      "https://idp.example/a|b",
		"user information":      fakecred.URL("https", "user", "s3cr3t", "idp.example", ""),
		"http on a public host": "http://idp.example",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			refused := &atomic.Value{}
			e := newProviderEnvStore(t, nil, func(st *sqlite.Store) session.ProviderConfigStore {
				return refusedIssuerStore{ProviderConfigStore: st, issuer: refused}
			})

			e.createBeta(t)
			refused.Store(issuer)

			read := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/beta", "")
			if got := decodeProvider(t, read); !got.IssuerRefused {
				t.Fatalf("GET beta: issuerRefused = false for the issuer %q", issuer)
			}

			patch := e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/beta",
				`{"version":1,"enabled":false}`)
			if patch.Code != http.StatusOK {
				t.Fatalf("PATCH enabled false = %d %s, want 200", patch.Code, patch.Body.String())
			}

			if got := decodeProvider(t, patch); got.Enabled || got.Version != 2 {
				t.Errorf("after the disable: enabled %v version %d, want false and 2", got.Enabled, got.Version)
			}

			if n := e.countRefused(t); n != 1 {
				t.Errorf("the list has %d rows with issuerRefused, want 1 (only beta)", n)
			}
		})
	}
}

// countRefused returns the number of rows of the list that have issuerRefused.
func (e providerEnv) countRefused(t *testing.T) int {
	t.Helper()

	list := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers", "")

	var doc struct {
		Providers []providerDoc `json:"providers"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the list: %v", err)
	}

	n := 0

	for i := range doc.Providers {
		if doc.Providers[i].IssuerRefused {
			n++
		}
	}

	return n
}
