package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/session"
)

// OIDCProvider configures one OpenID Connect provider of the console
// sign-in. ID names the provider in the sign-in routes and in the subject
// of each principal that signs in through it, "<ID>:<sub>", so it must
// never change after the first sign-in. Issuer is the issuer URL of the
// provider, with HTTPS. ClientID and ClientSecret are the client of the
// server at the provider; the provider must know the redirect URI
// <public URL>/api/server/sign-in/<ID>/callback. Scopes are asked in
// addition to "openid". AutoCreate makes a principal at the first sign-in
// of a person, with no grant and no group; with false, an administrator
// makes each principal first. HTTPClient makes the requests to the
// provider: nil uses a client with a timeout of 10 seconds.
type OIDCProvider struct {
	HTTPClient   *http.Client
	ID           string
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	AutoCreate   bool
}

// WithOIDCProvider adds an OpenID Connect provider to the console sign-in.
// The sign-in needs the public URL (WithPublicURL) with HTTPS, and a store
// that keeps principals and sessions: both SQL adapters do.
func WithOIDCProvider(p OIDCProvider) Option {
	return func(o *settings) { o.oidcProviders = append(o.oidcProviders, p) }
}

// providerIDPattern is the form of a provider ID: a lower-case letter,
// then up to 31 lower-case letters, digits and hyphens.
var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// errSignInSetup reports a sign-in configuration that the server cannot use.
var errSignInSetup = errors.New("console sign-in")

// newSignIn returns the console sign-in of cfg, or nil when cfg names no
// provider.
func newSignIn(cfg *settings, policies *session.Policies, tokens *accesstoken.Service) (*serverapi.SignIn, error) {
	if len(cfg.oidcProviders) == 0 {
		return nil, nil //nolint:nilnil // No provider is a valid setup: the sign-in routes stay unmounted.
	}

	resolver, err := baseurl.Parse(cfg.publicURL)
	if err != nil {
		return nil, fmt.Errorf("%w: public URL: %w", errSignInSetup, err)
	}

	base := resolver.String()

	store, ok := cfg.namespaceStore.(session.Store)
	if !ok || policies == nil {
		return nil, fmt.Errorf("%w: the store keeps no sessions", errSignInSetup)
	}

	origin, err := signInOrigin(base)
	if err != nil {
		return nil, err
	}

	var inactivity session.InactivityRule
	if tokens != nil {
		inactivity = tokenInactivity{tokens: tokens}
	}

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: store, Policies: policies, Inactivity: inactivity, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("session service: %w", err)
	}

	providers := make([]serverapi.SignInProvider, 0, len(cfg.oidcProviders))

	for _, p := range cfg.oidcProviders {
		provider, err := signInProvider(p, base, cfg.now, providers)
		if err != nil {
			return nil, err
		}

		providers = append(providers, provider)
	}

	in := &serverapi.SignIn{
		Sessions: sessions, Limiter: nil, Providers: providers,
		Origins: append([]string{origin}, cfg.allowedOrigins...),
	}

	if cfg.gate != nil {
		in.Limiter = cfg.gate
	}

	return in, nil
}

// newConsoleSignIn builds the session policy service and the console
// sign-in of cfg. A session applies the account inactivity rule of tokens,
// when tokens is not nil. Each result is nil when cfg has none.
func newConsoleSignIn(cfg *settings, tokens *accesstoken.Service) (*session.Policies, *serverapi.SignIn, error) {
	policies, err := newSessionPolicyService(cfg)
	if err != nil {
		return nil, nil, err
	}

	signIn, err := newSignIn(cfg, policies, tokens)
	if err != nil {
		return nil, nil, err
	}

	return policies, signIn, nil
}

// tokenInactivity applies the account inactivity rule of the access token
// service to the owner of a console session, so that a session does not
// keep an account alive that the token lookup and the sweep would end.
type tokenInactivity struct {
	tokens *accesstoken.Service
}

// AccountInactive implements session.InactivityRule.
func (r tokenInactivity) AccountInactive(ctx context.Context, o session.Owner) (bool, error) {
	inactive, err := r.tokens.AccountInactive(ctx, accesstoken.Owner{
		ActiveAt: o.ActiveAt, DisabledAt: o.DisabledAt, ID: o.ID, Subject: o.Subject, DisplayName: o.DisplayName,
		Kind: o.Kind, BreakGlass: o.BreakGlass,
	})
	if err != nil {
		return false, fmt.Errorf("account inactivity of %s: %w", o.ID, err)
	}

	return inactive, nil
}

// signInRequestPruneEvery is the period of the deletion of the expired
// sign-in requests: one lifetime of a request.
const signInRequestPruneEvery = session.RequestLifetime

// pruneSignInRequests deletes the expired sign-in requests once each
// signInPruneEvery, until ctx is done. A failed run logs no error
// text, which may hold driver detail, and the next run tries again.
func (s *Server) pruneSignInRequests(ctx context.Context) {
	if s.signInRequests == nil {
		return
	}

	ticker := time.NewTicker(s.signInPruneEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneSignInRequestsOnce(ctx)
		}
	}
}

// pruneSignInRequestsOnce deletes the expired sign-in requests once.
func (s *Server) pruneSignInRequestsOnce(ctx context.Context) {
	n, err := s.signInRequests.PruneRequests(ctx)

	switch {
	case err != nil && !errors.Is(ctx.Err(), context.Canceled):
		s.logger.WarnContext(ctx, "prune of the expired sign-in requests failed")
	case n > 0:
		s.logger.InfoContext(ctx, "pruned the expired sign-in requests", slog.Int64("requests", n))
	default:
	}
}

// signInOrigin returns the origin of the public URL base, which the
// console sign-in needs with HTTPS (HTTP only on a loopback host, for
// development and tests).
func signInOrigin(base string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("%w: set --public-url, the base of the redirect URI", errSignInSetup)
	}

	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("%w: public URL: %w", errSignInSetup, err)
	}

	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())

	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return "", fmt.Errorf("%w: the public URL %q must use https", errSignInSetup, base)
	}

	return u.Scheme + "://" + u.Host, nil
}

// signInProvider returns the provider of p for the public URL base. It
// refuses an ID of the wrong form or one that an earlier provider has.
func signInProvider(
	p OIDCProvider, base string, now func() time.Time, earlier []serverapi.SignInProvider,
) (serverapi.SignInProvider, error) {
	var none serverapi.SignInProvider

	if !providerIDPattern.MatchString(p.ID) {
		return none, fmt.Errorf("%w: provider ID %q is not 1 to 32 lower-case letters, digits and hyphens "+
			"that start with a letter", errSignInSetup, p.ID)
	}

	if slices.ContainsFunc(earlier, func(e serverapi.SignInProvider) bool { return e.ID == p.ID }) {
		return none, fmt.Errorf("%w: provider ID %q is used two times", errSignInSetup, p.ID)
	}

	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: p.HTTPClient, Now: now, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: p.ClientSecret,
		RedirectURL: base + "/api/server/sign-in/" + p.ID + "/callback", Scopes: p.Scopes,
	})
	if err != nil {
		return none, fmt.Errorf("%w: provider %q: %w", errSignInSetup, p.ID, err)
	}

	displayName := p.DisplayName
	if displayName == "" {
		displayName = p.ID
	}

	return serverapi.SignInProvider{Client: client, ID: p.ID, DisplayName: displayName, AutoCreate: p.AutoCreate}, nil
}
