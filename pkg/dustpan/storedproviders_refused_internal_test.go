package dustpan

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// swapIssuerStore shows each stored provider of the oidc mode with another
// issuer while issuer holds a non-empty string. It stands for a row of an
// older version or of a restored backup: the real store refuses to write it.
type swapIssuerStore struct {
	providerStores

	issuer *atomic.Value
}

func (s swapIssuerStore) GetProviderConfig(ctx context.Context, id string) (session.ProviderConfig, error) {
	p, err := s.providerStores.GetProviderConfig(ctx, id)
	if v, ok := s.issuer.Load().(string); ok && v != "" && p.Mode == session.ModeOIDC {
		p.Issuer = v
	}

	return p, err
}

// TestStoredProvidersRefuseAnEnabledRowWithARefusedIssuer proves that the
// lookup of the sign-in routes gives an error and no provider for an enabled
// stored row whose issuer the rule refuses, on a first lookup and after a
// lookup that cached a client for a valid issuer. The error holds no part of
// the issuer.
func TestStoredProvidersRefuseAnEnabledRowWithARefusedIssuer(t *testing.T) {
	t.Parallel()

	t.Run("first lookup", func(t *testing.T) {
		t.Parallel()
		requireRefusedLookup(t, 0)
	})

	t.Run("after a cached lookup", func(t *testing.T) {
		t.Parallel()
		requireRefusedLookup(t, 1)
	})
}

// requireRefusedLookup looks the provider up warm times with its valid
// issuer, then changes the issuer under the store and looks it up again.
func requireRefusedLookup(t *testing.T, warm int) {
	t.Helper()

	swap := &atomic.Value{}
	f := newStoredFixtureWith(t, func(st providerStores) providerStores {
		return swapIssuerStore{providerStores: st, issuer: swap}
	})

	_, err := f.admin.Create(t.Context(), adminEntry(), session.ProviderInput{
		ID: "beta", Mode: session.ModeOIDC, Issuer: "https://beta.example", DisplayName: "Beta",
		ClientID: "dustpan", ClientSecret: "secret", Scopes: nil, AutoCreate: true, Enabled: true,
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "",
			UsernameClaim: "", EmailClaim: "",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for range warm {
		if _, ok, lookupErr := f.lookup.Provider(t.Context(), "beta"); lookupErr != nil || !ok {
			t.Fatalf("lookup of the valid issuer = %t, %v, want a provider", ok, lookupErr)
		}
	}

	swap.Store(fakecred.URL("https", "user", "s3cr3t", "idp.example", ""))

	got, ok, err := f.lookup.Provider(t.Context(), "beta")
	if err == nil || ok || got.Method != nil {
		t.Fatalf("lookup = %+v, %t, %v; want an error and no provider", got, ok, err)
	}

	if !errors.Is(err, oauth.ErrConfig) {
		t.Errorf("lookup error = %v, want oauth.ErrConfig", err)
	}

	if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "idp.example") {
		t.Errorf("the error holds the issuer: %v", err)
	}
}
