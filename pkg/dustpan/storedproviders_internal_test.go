package dustpan

import (
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// storedFixture is the provider service of a SQLite store with the static
// provider corp, and the lookup of the sign-in routes.
type storedFixture struct {
	admin  *session.ProviderAdmin
	lookup *storedProviders
}

func newStoredFixture(t *testing.T) storedFixture {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	cfg := defaultSettings()
	cfg.namespaceStore, cfg.secretWrapper, cfg.now = st, wrapper, time.Now
	cfg.oidcProviders = []OIDCProvider{{
		HTTPClient: nil, ID: "corp", DisplayName: "", Issuer: "https://corp.example", ClientID: "c", ClientSecret: "s",
		Scopes: nil, AutoCreate: true,
	}}

	admin, err := newProviderAdmin(cfg, st)
	if err != nil {
		t.Fatalf("provider admin: %v", err)
	}

	return storedFixture{admin: admin, lookup: newStoredProviders(admin, "https://dustpan.example", nil, time.Now)}
}

// adminEntry is the entry of an administrator with an access token.
func adminEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "", Action: "server:manage-sign-in",
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
	}
}

// TestStoredProvidersFollowTheStore proves that the lookup of the sign-in
// routes finds an enabled stored provider, keeps its client until its
// version changes, and finds no disabled, unknown or static provider.
func TestStoredProvidersFollowTheStore(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newStoredFixture(t)

	_, err := f.admin.Create(ctx, adminEntry(), session.ProviderInput{
		ID: "beta", Mode: session.ModeOIDC, Issuer: "https://beta.example", DisplayName: "Beta", ClientID: "dustpan",
		ClientSecret: "secret", Scopes: nil, AutoCreate: true, Enabled: true,
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
			EmailClaim: "",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	first, ok, err := f.lookup.Provider(ctx, "beta")
	if err != nil || !ok || first.DisplayName != "Beta" || !first.Stored {
		t.Fatalf("lookup = %+v, %t, %v; want the stored provider beta", first, ok, err)
	}

	again, _, againErr := f.lookup.Provider(ctx, "beta")
	if againErr != nil || again.Client != first.Client {
		t.Fatalf("a second lookup of the same version made a new client (%v)", againErr)
	}

	name := "Beta SSO"
	f.update(t, session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: &name, ClientID: nil, ClientSecret: nil, Scopes: nil, AutoCreate: nil,
		OAuth: session.OAuthChange{
			AuthorizationURL: nil, TokenURL: nil, UserinfoURL: nil, SubjectClaim: nil, NameClaim: nil,
			UsernameClaim: nil, EmailClaim: nil,
		},
		Enabled: nil, Version: 1,
	})

	changed, ok, err := f.lookup.Provider(ctx, "beta")
	if err != nil || !ok || changed.DisplayName != name || changed.Client == first.Client {
		t.Fatalf("lookup after a change = %+v, %t, %v; want the new name and a new client", changed, ok, err)
	}

	disabled := false
	f.update(t, session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil, AutoCreate: nil,
		OAuth: session.OAuthChange{
			AuthorizationURL: nil, TokenURL: nil, UserinfoURL: nil, SubjectClaim: nil, NameClaim: nil,
			UsernameClaim: nil, EmailClaim: nil,
		},
		Enabled: &disabled, Version: 2,
	})

	f.checkNoProvider(t, "beta", "none", "corp", "Not An ID")
}

// update applies c to the stored provider beta.
func (f storedFixture) update(t *testing.T, c session.ProviderChange) {
	t.Helper()

	if _, err := f.admin.Update(t.Context(), adminEntry(), "beta", c); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// checkNoProvider checks that the lookup finds none of ids.
func (f storedFixture) checkNoProvider(t *testing.T, ids ...string) {
	t.Helper()

	for _, id := range ids {
		if _, ok, err := f.lookup.Provider(t.Context(), id); ok || err != nil {
			t.Errorf("lookup of %q = %t, %v; want no provider", id, ok, err)
		}
	}
}

// TestStoredProvidersPlainOAuth proves that the lookup of the sign-in
// routes makes a plain OAuth 2.0 client for a stored provider in the
// oauth mode.
func TestStoredProvidersPlainOAuth(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newStoredFixture(t)

	_, err := f.admin.Create(ctx, adminEntry(), session.ProviderInput{
		ID: "hub", Mode: session.ModeOAuth, Issuer: "", DisplayName: "Hub", ClientID: "dustpan",
		ClientSecret: "secret", Scopes: []string{"read:user"}, AutoCreate: true, Enabled: true,
		OAuth: session.OAuthSettings{ //nolint:gosec // Test fixture: the URLs of a provider, no credential.
			AuthorizationURL: "https://hub.example/login/oauth/authorize",
			TokenURL:         "https://hub.example/login/oauth/access_token",
			UserinfoURL:      "https://api.hub.example/user", SubjectClaim: "id", NameClaim: "",
			UsernameClaim: "login", EmailClaim: "",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok, err := f.lookup.Provider(ctx, "hub")
	if err != nil || !ok || !got.Stored {
		t.Fatalf("lookup = %+v, %t, %v; want the stored provider hub", got, ok, err)
	}

	if _, plain := got.Client.(*oauth.PlainClient); !plain {
		t.Fatalf("client of hub = %T, want *oauth.PlainClient", got.Client)
	}
}
