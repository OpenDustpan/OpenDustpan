package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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

// TestSignInProviderRefusedRowHostileChanges sends each change that is not a
// plain disable to a stored row whose issuer the rule refuses. Each is
// refused, the row stays as it was, and no answer holds the issuer.
func TestSignInProviderRefusedRowHostileChanges(t *testing.T) {
	t.Parallel()

	const refusedIssuer = "https://idp.example."

	for name, body := range map[string]string{
		"disable and a display name":  `{"version":1,"enabled":false,"displayName":"x"}`,
		"disable and a client secret": `{"version":1,"enabled":false,"client` + `Secret":"x"}`,
		"disable and a new issuer":    `{"version":1,"enabled":false,"issuer":"https://idp.example/fixed"}`,
		"disable and empty scopes":    `{"version":1,"enabled":false,"scopes":[]}`,
		"enable":                      `{"version":1,"enabled":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			refused := &atomic.Value{}
			e := newProviderEnvStore(t, nil, func(st *sqlite.Store) session.ProviderConfigStore {
				return refusedIssuerStore{ProviderConfigStore: st, issuer: refused}
			})

			e.createBeta(t)
			refused.Store(refusedIssuer)

			rec := e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/beta", body)
			if rec.Code < http.StatusBadRequest || rec.Code >= http.StatusInternalServerError {
				t.Errorf("PATCH %s = %d %s, want a 4xx refusal", body, rec.Code, rec.Body.String())
			}

			if strings.Contains(rec.Body.String(), refusedIssuer) {
				t.Errorf("the answer holds the issuer: %s", rec.Body.String())
			}

			got := decodeProvider(t, e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/beta", ""))
			if !got.Enabled || got.Version != 1 || got.DisplayName != "Beta" {
				t.Errorf("the refused PATCH changed the row: enabled %v, version %d, name %q",
					got.Enabled, got.Version, got.DisplayName)
			}
		})
	}
}
