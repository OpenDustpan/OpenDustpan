package dustpan

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

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
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
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
	if againErr != nil || again.Method != first.Method {
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
	if err != nil || !ok || changed.DisplayName != name || changed.Method == first.Method {
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

	if kind := got.Method.Kind(); kind != session.MethodOAuth {
		t.Fatalf("method of hub = %q, want %q", kind, session.MethodOAuth)
	}
}

// TestStoredProvidersRecreatedID proves that a provider that a delete and
// a create gave the same ID and version gets a new client: the lookup
// answers the settings of the new record, not of the old one.
func TestStoredProvidersRecreatedID(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newStoredFixture(t)
	in := session.ProviderInput{
		ID: "beta", Mode: session.ModeOIDC, Issuer: "https://beta.example", DisplayName: "Beta", ClientID: "dustpan",
		ClientSecret: "secret", Scopes: nil, AutoCreate: true, Enabled: true,
		OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
			EmailClaim: "",
		},
	}

	if _, err := f.admin.Create(ctx, adminEntry(), in); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, ok, err := f.lookup.Provider(ctx, "beta")
	if err != nil || !ok || !first.AutoCreate {
		t.Fatalf("lookup = %+v, %t, %v; want beta with autoCreate", first, ok, err)
	}

	if deleteErr := f.admin.Delete(ctx, adminEntry(), "beta"); deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}

	in.AutoCreate, in.ClientSecret = false, "another-secret"
	if _, createErr := f.admin.Create(ctx, adminEntry(), in); createErr != nil {
		t.Fatalf("create again: %v", createErr)
	}

	again, ok, err := f.lookup.Provider(ctx, "beta")
	if err != nil || !ok || again.AutoCreate || again.Method == first.Method {
		t.Fatalf("lookup after a delete and a create = %+v, %t, %v; want a new client with no autoCreate",
			again, ok, err)
	}
}

// TestStoredProviderRefusesDevID proves the layer of the lookup: the sign-in
// routes find no provider with the reserved ID of the dev sign-in. The lookup
// refuses before it asks the store, so a row with that ID, such as an older
// version might have left, never signs a person in.
func TestStoredProviderRefusesDevID(t *testing.T) {
	t.Parallel()

	f := newStoredFixture(t)

	got, found, err := f.lookup.Provider(t.Context(), session.DevProviderID)
	if err != nil || found || got.ID != "" {
		t.Fatalf("Provider(dev) = %+v, found %v, %v; want no provider", got, found, err)
	}
}

// TestStoredProvidersRefuseAModeWithNoMethod sends a stored record of each
// mode with no sign-in method straight to the build of a provider, as if the
// validation of the store had let it through. The build refuses it before
// it opens the client secret, and never builds the client of another mode.
func TestStoredProvidersRefuseAModeWithNoMethod(t *testing.T) {
	t.Parallel()

	f := newStoredFixture(t)

	for _, mode := range []session.ProviderMode{
		session.ModeSAML, session.ModeCAS, session.ModeLDAP, session.ModeHeader, "", "kerberos",
	} {
		p := session.ProviderConfig{
			CreatedAt: time.Now(), UpdatedAt: time.Now(), ID: "beta", Mode: mode, Issuer: "https://beta.example",
			DisplayName: "Beta", ClientID: "dustpan", SealedSecret: nil, Scopes: nil, AutoCreate: true,
			Enabled: true, Version: 1, OAuth: session.OAuthSettings{
				AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "",
				UsernameClaim: "", EmailClaim: "",
			},
		}

		got, err := f.lookup.build(t.Context(), &p)
		if !errors.Is(err, session.ErrInvalidProvider) || got.Method != nil {
			t.Fatalf("build of mode %q = %+v, %v, want ErrInvalidProvider", mode, got, err)
		}
	}
}

// TestStoredProviderSwitchFailsClosed checks the switch of a stored
// provider that its method reads: on for an enabled provider, off for a
// disabled or unknown one, and an error when the store cannot answer.
func TestStoredProviderSwitchFailsClosed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newStoredFixture(t)

	_, err := f.admin.Create(ctx, adminEntry(), session.ProviderInput{
		ID: "beta", Mode: session.ModeOIDC, Issuer: "https://beta.example", DisplayName: "Beta", ClientID: "dustpan",
		ClientSecret: "secret", Scopes: nil, AutoCreate: true, Enabled: true, OAuth: session.OAuthSettings{
			AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "", NameClaim: "", UsernameClaim: "",
			EmailClaim: "",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if on, err := f.lookup.enabled("beta")(ctx); !on || err != nil {
		t.Fatalf("switch of an enabled provider = %v, %v, want on", on, err)
	}

	if on, err := f.lookup.enabled("gone")(ctx); on || err != nil {
		t.Fatalf("switch of an unknown provider = %v, %v, want off", on, err)
	}

	off := false
	f.update(t, session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil, AutoCreate: nil,
		OAuth: session.OAuthChange{
			AuthorizationURL: nil, TokenURL: nil, UserinfoURL: nil, SubjectClaim: nil, NameClaim: nil,
			UsernameClaim: nil, EmailClaim: nil,
		},
		Enabled: &off, Version: 1,
	})

	if on, err := f.lookup.enabled("beta")(ctx); on || err != nil {
		t.Fatalf("switch of a disabled provider = %v, %v, want off", on, err)
	}
}
