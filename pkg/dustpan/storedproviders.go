package dustpan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
)

// WithSignInHTTPClient sets the HTTP client of the requests to the sign-in
// providers that the API keeps. Nil, the default, uses a client with a
// timeout of 10 seconds. A provider of WithOIDCProvider has its own
// client.
func WithSignInHTTPClient(c *http.Client) Option {
	return func(o *settings) { o.signInHTTPClient = c }
}

// providerStores is a store that keeps the sign-in providers of the API:
// their records, their issuer bindings and the data keys of their client
// secrets. Both SQL adapters are one.
type providerStores interface {
	session.ProviderConfigStore
	session.ProviderStore
	secret.InstallationKeyStore
}

// newProviderAdmin returns the service of the sign-in providers of the API
// for the store s. The providers of cfg are static: the API never changes
// them.
func newProviderAdmin(cfg *settings, s providerStores) (*session.ProviderAdmin, error) {
	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys: s, Wrapper: cfg.secretWrapper, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("installation secrets: %w", err)
	}

	static := make([]string, 0, len(cfg.oidcProviders)+len(cfg.ldapProviders))
	for _, p := range cfg.oidcProviders {
		static = append(static, p.ID)
	}

	static = append(static, ldapIDs(cfg)...)

	admin, err := session.NewProviderAdmin(session.ProviderAdminConfig{
		Store: s, Binder: s, Secrets: secrets, Now: cfg.now, Static: static,
	})
	if err != nil {
		return nil, fmt.Errorf("sign-in provider service: %w", err)
	}

	return admin, nil
}

// storedProviders finds the enabled sign-in providers of the API for the
// sign-in routes. It reads the provider at each sign-in, so a change
// applies at once, and keeps the client of each provider while its stored
// record stays the same, so the client keeps its discovery document. A
// provider that is gone or disabled loses its client, and a provider that
// a delete and a create gave the same ID and version gets a new one.
type storedProviders struct {
	admin *session.ProviderAdmin
	http  *http.Client
	now   func() time.Time
	cache map[string]cachedProvider
	base  string
	mu    sync.Mutex
}

// cachedProvider is the client of one stored record of a provider.
type cachedProvider struct {
	provider serverapi.SignInProvider
	record   session.ProviderConfig
}

func newStoredProviders(
	admin *session.ProviderAdmin, base string, client *http.Client, now func() time.Time,
) *storedProviders {
	return &storedProviders{
		admin: admin, http: client, now: now, cache: map[string]cachedProvider{}, base: base, mu: sync.Mutex{},
	}
}

// Provider implements serverapi.StoredProviders.
func (s *storedProviders) Provider(ctx context.Context, id string) (serverapi.SignInProvider, bool, error) {
	var none serverapi.SignInProvider

	// Defense in depth: the API refuses to store the IDs "dev" and "local",
	// and the configuration refuses them too. This check stays for a
	// database that holds such a row from before, so that the row never
	// signs a person in.
	if !session.ValidProviderID(id) || id == session.DevProviderID || id == session.LocalProviderID ||
		s.admin.Static(id) {
		return none, false, nil
	}

	p, err := s.admin.Get(ctx, id)
	if errors.Is(err, session.ErrProviderNotFound) {
		s.forget(id)

		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("read sign-in provider: %w", err)
	}

	if !p.Enabled {
		s.forget(id)

		return none, false, nil
	}

	s.mu.Lock()
	cached, ok := s.cache[id]
	s.mu.Unlock()

	if ok && sameRecord(&cached.record, &p) {
		return cached.provider, true, nil
	}

	provider, err := s.build(ctx, &p)
	if err != nil {
		return none, false, err
	}

	s.mu.Lock()
	s.cache[id] = cachedProvider{provider: provider, record: p}
	s.mu.Unlock()

	return provider, true, nil
}

// plainClient makes the plain OAuth 2.0 client of the stored provider p,
// which is in the oauth mode. The time of the authentication of its
// sessions is the time of the sign-in: the provider gives none (see
// oauth.PlainClient.Finish). The token freshness check reads only that
// time, so this is the one place where the freshness of such a session
// comes from.
func (s *storedProviders) plainClient(p *session.ProviderConfig, clientSecret string) (*oauth.PlainClient, error) {
	client, err := oauth.NewPlain(oauth.PlainConfig{
		HTTPClient: s.http, Now: s.now, AuthorizationURL: p.OAuth.AuthorizationURL, TokenURL: p.OAuth.TokenURL,
		UserinfoURL: p.OAuth.UserinfoURL, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: s.redirect(p.ID), Scopes: p.Scopes, Claims: oauth.Claims{
			Subject: p.OAuth.SubjectClaim, Name: p.OAuth.NameClaim, Username: p.OAuth.UsernameClaim,
			Email: p.OAuth.EmailClaim,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("plain OAuth client: %w", err)
	}

	return client, nil
}

// openIDClient makes the client of the stored provider p, which is in the
// oidc mode. It reads the discovery document at its first use.
func (s *storedProviders) openIDClient(p *session.ProviderConfig, clientSecret string) (*oauth.Lazy, error) {
	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: s.http, Now: s.now, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: s.redirect(p.ID), Scopes: p.Scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("OpenID client: %w", err)
	}

	return client, nil
}

// redirect returns the callback URL of the provider id.
func (s *storedProviders) redirect(id string) string {
	return s.base + "/api/server/sign-in/" + id + "/callback"
}

// forget drops the client of the provider id.
func (s *storedProviders) forget(id string) {
	s.mu.Lock()
	delete(s.cache, id)
	s.mu.Unlock()
}

// sameRecord reports whether a and b are the same stored record: each
// field, with the creation time, the sealed secret and the scopes.
func sameRecord(a, b *session.ProviderConfig) bool {
	return sameTimes(a, b) && sameSettings(a, b)
}

// sameTimes reports whether a and b have the same ID, version and times.
func sameTimes(a, b *session.ProviderConfig) bool {
	return a.ID == b.ID && a.Version == b.Version && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// sameSettings reports whether a and b have the same settings.
func sameSettings(a, b *session.ProviderConfig) bool {
	return a.Mode == b.Mode && a.Issuer == b.Issuer && a.OAuth == b.OAuth && a.DisplayName == b.DisplayName &&
		a.ClientID == b.ClientID && bytes.Equal(a.SealedSecret, b.SealedSecret) && slices.Equal(a.Scopes, b.Scopes) &&
		a.AutoCreate == b.AutoCreate && a.Enabled == b.Enabled
}

// build makes the client of the stored provider p.
func (s *storedProviders) build(ctx context.Context, p *session.ProviderConfig) (serverapi.SignInProvider, error) {
	var none serverapi.SignInProvider

	// Defense in depth: the validation of a stored provider refuses each
	// mode with no method. This check stays so that a row of another version
	// never signs a person in through the client of another mode. It comes
	// before the read of the client secret, so such a row opens no secret.
	if p.Mode != session.ModeOIDC && p.Mode != session.ModeOAuth {
		return none, fmt.Errorf("sign-in provider %s: %w: the mode %q has no sign-in method", p.ID,
			session.ErrInvalidProvider, p.Mode)
	}

	clientSecret, err := s.admin.ClientSecret(ctx, *p)
	if err != nil {
		return none, fmt.Errorf("sign-in provider %s: %w", p.ID, err)
	}

	var client oauth.MethodClient

	kind := session.MethodOIDC
	if p.Mode == session.ModeOAuth {
		client, err = s.plainClient(p, clientSecret)
		kind = session.MethodOAuth
	} else {
		client, err = s.openIDClient(p, clientSecret)
	}

	if err != nil {
		return none, fmt.Errorf("sign-in provider %s: %w", p.ID, err)
	}

	method, err := redirectMethod(client, p.ID, kind, s.enabled(p.ID), s.now)
	if err != nil {
		return none, err
	}

	return serverapi.SignInProvider{
		Method: method, ID: p.ID, DisplayName: p.DisplayName, AutoCreate: p.AutoCreate, Stored: true,
	}, nil
}

// enabled returns the switch of the stored provider id: it reads the
// provider at each call, so a disable applies to a sign-in that started
// before it. A provider that is gone is off.
func (s *storedProviders) enabled(id string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		p, err := s.admin.Get(ctx, id)
		if errors.Is(err, session.ErrProviderNotFound) {
			return false, nil
		}

		if err != nil {
			return false, fmt.Errorf("read sign-in provider: %w", err)
		}

		return p.Enabled, nil
	}
}
