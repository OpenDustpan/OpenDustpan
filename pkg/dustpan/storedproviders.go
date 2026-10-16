package dustpan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

	static := make([]string, 0, len(cfg.oidcProviders))
	for _, p := range cfg.oidcProviders {
		static = append(static, p.ID)
	}

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
// applies at once, and keeps the client of each provider until its version
// changes, so the client keeps its discovery document.
type storedProviders struct {
	admin  *session.ProviderAdmin
	client *http.Client
	now    func() time.Time
	cache  map[string]cachedProvider
	base   string
	mu     sync.Mutex
}

// cachedProvider is the client of one version of a stored provider.
type cachedProvider struct {
	provider serverapi.SignInProvider
	version  int64
}

func newStoredProviders(
	admin *session.ProviderAdmin, base string, client *http.Client, now func() time.Time,
) *storedProviders {
	return &storedProviders{
		admin: admin, client: client, now: now, cache: map[string]cachedProvider{}, base: base, mu: sync.Mutex{},
	}
}

// Provider implements serverapi.StoredProviders.
func (s *storedProviders) Provider(ctx context.Context, id string) (serverapi.SignInProvider, bool, error) {
	var none serverapi.SignInProvider

	if !session.ValidProviderID(id) || s.admin.Static(id) {
		return none, false, nil
	}

	p, err := s.admin.Get(ctx, id)
	if errors.Is(err, session.ErrProviderNotFound) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("read sign-in provider: %w", err)
	}

	if !p.Enabled {
		return none, false, nil
	}

	s.mu.Lock()
	cached, ok := s.cache[id]
	s.mu.Unlock()

	if ok && cached.version == p.Version {
		return cached.provider, true, nil
	}

	provider, err := s.build(ctx, &p)
	if err != nil {
		return none, false, err
	}

	s.mu.Lock()
	s.cache[id] = cachedProvider{provider: provider, version: p.Version}
	s.mu.Unlock()

	return provider, true, nil
}

// build makes the client of the stored provider p.
func (s *storedProviders) build(ctx context.Context, p *session.ProviderConfig) (serverapi.SignInProvider, error) {
	var none serverapi.SignInProvider

	clientSecret, err := s.admin.ClientSecret(ctx, *p)
	if err != nil {
		return none, fmt.Errorf("sign-in provider %s: %w", p.ID, err)
	}

	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: s.client, Now: s.now, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: s.base + "/api/server/sign-in/" + p.ID + "/callback", Scopes: p.Scopes,
	})
	if err != nil {
		return none, fmt.Errorf("sign-in provider %s: %w", p.ID, err)
	}

	return serverapi.SignInProvider{Client: client, ID: p.ID, DisplayName: p.DisplayName, AutoCreate: p.AutoCreate}, nil
}
