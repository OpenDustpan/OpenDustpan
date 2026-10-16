package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

var errProviderStoreDown = errors.New("provider store down")

// memoryProviders keeps the first issuer of each provider ID.
type memoryProviders struct {
	err     error
	issuers map[string]string
}

func (m *memoryProviders) BindProvider(_ context.Context, id, issuer string, _ time.Time) (string, error) {
	if m.err != nil {
		return "", m.err
	}

	if recorded, ok := m.issuers[id]; ok {
		return recorded, nil
	}

	m.issuers[id] = issuer

	return issuer, nil
}

func TestBindProviders(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}
	corp := session.Provider{ID: "corp", Issuer: "https://idp.example"}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("second start with the same issuer: %v", err)
	}

	moved := session.Provider{ID: "corp", Issuer: "https://other.example"}

	err := session.BindProviders(ctx, store, []session.Provider{moved}, time.Now())
	if err == nil || !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("start with another issuer = %v, want ErrIssuerChanged", err)
	}

	want := `sign-in provider "corp" has the issuer "https://idp.example" in the database and the issuer ` +
		`"https://other.example" in the configuration, so set the issuer (--oidc-issuer) back to ` +
		`"https://idp.example", or set a new provider ID only if the identity provider changed`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	store.err = errProviderStoreDown

	err = session.BindProviders(ctx, store, []session.Provider{corp}, time.Now())
	if !errors.Is(err, errProviderStoreDown) {
		t.Fatalf("start with the store down = %v, want the store error", err)
	}
}

// TestBindProvidersNormalizesTheIssuer proves that a change of the case of
// the scheme or the host, or a trailing slash, is the same issuer, and
// that the store records the normalized form.
func TestBindProvidersNormalizesTheIssuer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}

	first := session.Provider{ID: "corp", Issuer: "HTTPS://IdP.Example/tenant/"}
	if err := session.BindProviders(ctx, store, []session.Provider{first}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	if got := store.issuers["corp"]; got != "https://idp.example/tenant" {
		t.Fatalf("recorded issuer = %q, want the normalized form", got)
	}

	for _, issuer := range []string{"https://idp.example/tenant", "https://IDP.example/tenant/"} {
		same := session.Provider{ID: "corp", Issuer: issuer}
		if err := session.BindProviders(ctx, store, []session.Provider{same}, time.Now()); err != nil {
			t.Errorf("start with %q: %v, want the same issuer", issuer, err)
		}
	}

	other := session.Provider{ID: "corp", Issuer: "https://idp.example/other"}
	if err := session.BindProviders(ctx, store, []session.Provider{other}, time.Now()); !errors.Is(
		err, session.ErrIssuerChanged,
	) {
		t.Fatalf("start with another path = %v, want ErrIssuerChanged", err)
	}
}

func TestNormalizeIssuer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"https://idp.example", "https://idp.example"},
		{"https://idp.example/", "https://idp.example"},
		{"HTTPS://IDP.Example:8443/Realms/Corp/", "https://idp.example:8443/Realms/Corp"},
		{"not a url/", "not a url"},
	} {
		if got := session.NormalizeIssuer(tc.in); got != tc.want {
			t.Errorf("NormalizeIssuer(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
