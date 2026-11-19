package session_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestProviderAdminStoredRowWithARefusedIssuer starts from a stored row, in
// use, whose issuer the current rule refuses: a row of an older version or of
// a restored backup. An administrator can always disable it. The refusal of a
// change of the issuer and of a delete stays, and neither text holds the
// issuer.
func TestProviderAdminStoredRowWithARefusedIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range map[string]string{
		"trailing dot":      "https://idp.example.",
		"dot-dot segment":   "https://idp.example/a/../b",
		"encoded dot-dot":   "https://idp.example/%2e%2e/b",
		"upper-case scheme": "HTTPS://idp.example",
		"pipe in the path":  "https://idp.example/a|b",
		"user information":  fakecred.URL("https", "user", "s3cr3t", "idp.example", ""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newAdminFixture(t)
			stored := seedRefusedRow(t, f, issuer)

			requireSignInRefused(t, issuer)
			requireIssuerChangeRefused(t, f, stored)

			err := f.admin.Delete(t.Context(), sessionCallerEntry(), stored.ID)
			if !errors.Is(err, session.ErrProviderInUse) {
				t.Fatalf("delete = %v, want ErrProviderInUse", err)
			}

			requireDisableWorks(t, f, stored)
		})
	}
}

// seedRefusedRow stores a provider that is in use, and whose issuer is the
// refused issuer, next to a valid provider "other".
func seedRefusedRow(t *testing.T, f adminFixture, issuer string) session.ProviderConfig {
	t.Helper()

	stored, err := f.admin.Create(t.Context(), sessionCallerEntry(), corpInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	stored.Issuer = issuer
	f.store.providers[stored.ID] = stored
	f.store.inUse = []string{stored.ID}
	f.binder.issuers[stored.ID] = issuer

	other := stored
	other.ID, other.Issuer = "other", "https://idp.example/other"
	f.store.providers[other.ID] = other

	return stored
}

// requireSignInRefused checks that the sign-in client refuses the issuer
// with no part of it in the text, and that a valid issuer still makes a client.
func requireSignInRefused(t *testing.T, issuer string) {
	t.Helper()

	cfg := oauth.Config{
		HTTPClient: nil, Now: nil, Issuer: issuer, ClientID: "dustpan", ClientSecret: "x",
		RedirectURL: "https://dustpan.example/cb", Scopes: nil,
	}

	_, err := oauth.NewLazy(cfg)
	if !errors.Is(err, oauth.ErrConfig) {
		t.Fatalf("NewLazy on the refused issuer = %v, want ErrConfig", err)
	}

	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("the error holds the user information: %v", err)
	}

	cfg.Issuer = "https://idp.example/other"
	if _, err := oauth.NewLazy(cfg); err != nil {
		t.Fatalf("NewLazy on the other provider: %v", err)
	}
}

// requireIssuerChangeRefused checks that a change of the issuer is refused
// with a text that holds no stored issuer and does not tell to restore it.
func requireIssuerChangeRefused(t *testing.T, f adminFixture, stored session.ProviderConfig) {
	t.Helper()

	fixed := "https://idp.example/fixed"

	_, err := f.admin.Update(t.Context(), sessionCallerEntry(), stored.ID, session.ProviderChange{
		Mode: nil, Issuer: &fixed, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil,
		OAuth: noOAuthChange(), AutoCreate: nil, Enabled: nil, Version: stored.Version,
	})
	if !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("change of the issuer = %v, want ErrIssuerChanged", err)
	}

	if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "--oidc-issuer") {
		t.Errorf("the text holds the stored issuer or tells to restore it: %v", err)
	}
}

// requireDisableWorks checks that the disable works, takes the narrowing
// write and leaves the other provider as it was.
func requireDisableWorks(t *testing.T, f adminFixture, stored session.ProviderConfig) {
	t.Helper()

	off := false

	got, err := f.admin.Update(t.Context(), sessionCallerEntry(), stored.ID, session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil,
		OAuth: noOAuthChange(), AutoCreate: nil, Enabled: &off, Version: stored.Version,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}

	if got.Enabled || f.store.disables != 1 {
		t.Errorf("enabled %v, narrowing writes %d, want false and 1", got.Enabled, f.store.disables)
	}

	if !f.store.providers["other"].Enabled {
		t.Error("the other provider changed")
	}
}
