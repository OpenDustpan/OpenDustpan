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
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
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

// noDevAdmin is the administrator of a server with no dev sign-in.
//
//nolint:gochecknoglobals // A frozen zero value.
var noDevAdmin = identity.Principal{
	TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "", DisplayName: "",
	Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
}

// hasStaticSignIn reports whether cfg names a sign-in that needs no stored
// provider: a provider of the configuration, or the dev sign-in.
func hasStaticSignIn(cfg *settings) bool {
	return len(cfg.oidcProviders) > 0 || len(cfg.ldapProviders) > 0 || cfg.devSignIn != nil
}

// newSignIn returns the console sign-in of cfg, or nil when cfg has none.
// The sign-in runs when cfg names a provider, or when the store keeps the
// providers of the API and cfg has an HTTPS public URL. With a store that
// keeps them, the API manages the providers in addition to those of cfg.
// gates are the failure gates that gatedAuthenticator built for tokens.
func newSignIn(
	cfg *settings, policies *session.Policies, tokens *accesstoken.Service, gates signInGates,
) (*serverapi.SignIn, error) {
	stores, stored := cfg.namespaceStore.(providerStores)
	if !hasStaticSignIn(cfg) && (!stored || !storedSignInReady(cfg, policies)) {
		return nil, nil //nolint:nilnil // No provider is a valid setup: the sign-in routes stay unmounted.
	}

	resolver, err := baseurl.Parse(cfg.publicURL)
	if err != nil {
		return nil, fmt.Errorf("%w: public URL: %w", errSignInSetup, err)
	}

	base := resolver.String()

	origin, err := signInOrigin(base)
	if err != nil {
		return nil, err
	}

	sessions, err := newSignInSessions(cfg, policies, tokens)
	if err != nil {
		return nil, err
	}

	providers, err := staticSignInProviders(cfg, base)
	if err != nil {
		return nil, err
	}

	in := &serverapi.SignIn{
		Service:           nil,
		Sessions:          sessions,
		Limiter:           nil,
		Stored:            nil,
		Admin:             nil,
		Providers:         providers,
		PasswordProviders: nil, Origins: append(
			[]string{origin},
			cfg.allowedOrigins...,
		),
		PasswordLimiter:     nil,
		Passkeys:            nil,
		PasskeySignIn:       nil,
		PasskeyBeginLimiter: nil,
		PasskeyLimiter:      nil,
		DevCode:             nil,
		DevAdmin:            noDevAdmin,
		OnDevRefused:        devRefusalAudit(cfg),
	}

	if cfg.devSignIn != nil {
		in.DevCode, in.DevAdmin = cfg.devSignIn.Code, cfg.devSignIn.Admin
	}

	if err := setSignInLimiters(gates, tokens, in); err != nil {
		return nil, err
	}

	if stored {
		admin, adminErr := newProviderAdmin(cfg, stores)
		if adminErr != nil {
			return nil, adminErr
		}

		in.Admin = admin
		in.Stored = newStoredProviders(admin, base, cfg.signInHTTPClient, cfg.now)
	}

	return in, nil
}

// newSignInSessions returns the session service of the console sign-in
// of cfg. A session applies the account inactivity rule of tokens, when
// tokens is not nil.
func newSignInSessions(
	cfg *settings, policies *session.Policies, tokens *accesstoken.Service,
) (*session.Sessions, error) {
	store, ok := cfg.namespaceStore.(session.Store)
	if !ok || policies == nil {
		return nil, fmt.Errorf("%w: the store keeps no sessions", errSignInSetup)
	}

	var inactivity session.InactivityRule
	if tokens != nil {
		inactivity = tokenInactivity{}
	}

	var providers session.ProviderSessionStore
	if ps, isProviderStore := store.(session.ProviderSessionStore); isProviderStore {
		providers = ps
	}

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: store, Providers: providers, Inactivity: inactivity, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("session service: %w", err)
	}

	return sessions, nil
}

// staticSignInProviders returns the providers of cfg for the public URL
// base.
func staticSignInProviders(cfg *settings, base string) ([]serverapi.SignInProvider, error) {
	providers := make([]serverapi.SignInProvider, 0, len(cfg.oidcProviders))

	for _, p := range cfg.oidcProviders {
		provider, err := signInProvider(p, base, cfg.now, providers)
		if err != nil {
			return nil, err
		}

		providers = append(providers, provider)
	}

	return providers, nil
}

// storedSignInReady reports whether cfg can run the console sign-in with
// only the providers of the API: the store keeps sessions, and cfg has a
// public URL with HTTPS (HTTP only on a loopback host).
func storedSignInReady(cfg *settings, policies *session.Policies) bool {
	if _, ok := cfg.namespaceStore.(session.Store); !ok || policies == nil {
		return false
	}

	resolver, err := baseurl.Parse(cfg.publicURL)
	if err != nil {
		return false
	}

	_, err = signInOrigin(resolver.String())

	return err == nil
}

// newConsoleSignIn builds the session policy service and the console
// sign-in of cfg, and sets the session cookie check of cfg for the APIs
// other than the installation API. A session applies the account
// inactivity rule of tokens, when tokens is not nil. gates are the failure
// gates of the sign-in routes and of the password routes. Each result is nil
// when cfg has none.
func newConsoleSignIn(
	cfg *settings, tokens *accesstoken.Service, gates signInGates,
) (*session.Policies, *serverapi.SignIn, error) {
	policies, err := newSessionPolicyService(cfg)
	if err != nil {
		return nil, nil, err
	}

	signIn, err := newSignIn(cfg, policies, tokens, gates)
	if err != nil {
		return nil, nil, err
	}

	if cfg.sessionCookies, err = sessionCookies(signIn, cfg); err != nil {
		return nil, nil, err
	}

	return policies, signIn, nil
}

// sessionCookies returns the session cookie check of the APIs other than
// the installation API, with the sessions and the origins of in. A nil in
// gives a nil check: those APIs then take access tokens only.
func sessionCookies(in *serverapi.SignIn, cfg *settings) (*sessionauth.Checker, error) {
	if in == nil {
		return nil, nil //nolint:nilnil // No sign-in is a valid setup: the APIs take access tokens only.
	}

	var opts []sessionauth.Option
	if cfg.installationKind != installation.KindDev {
		opts = append(opts, sessionauth.RefuseDevSubjects(), sessionauth.OnDevRefused(devRefusalAudit(cfg)))
	}

	c, err := sessionauth.New(in.Sessions, in.Origins, opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSignInSetup, err)
	}

	return c, nil
}

// providerBinding is the store and the providers of the console sign-in,
// whose issuers the start binds. The zero value binds nothing.
type providerBinding struct {
	store     session.ProviderStore
	providers []session.Provider
}

// bindingOf returns the provider binding of cfg when signIn is not nil and
// the store keeps the providers, else the zero value. newSignIn already
// refused a store that keeps no sessions; both SQL adapters keep both.
func bindingOf(cfg *settings, signIn *serverapi.SignIn) providerBinding {
	store, ok := cfg.namespaceStore.(session.ProviderStore)
	if signIn == nil || !ok {
		return providerBinding{store: nil, providers: nil}
	}

	providers := make([]session.Provider, 0, len(cfg.oidcProviders))
	for _, p := range cfg.oidcProviders {
		providers = append(providers, session.Provider{ID: p.ID, Issuer: p.Issuer, UserinfoURL: "", SubjectClaim: ""})
	}

	return providerBinding{store: store, providers: providers}
}

// bindSignInProviders binds each provider of the console sign-in to its
// issuer, and refuses the start when the issuer of a provider ID changed:
// the subject of a person holds the provider ID, not the issuer. Only the api
// component serves the console sign-in, so a process without it binds
// nothing and never stops at a bound issuer that changed.
func (s *Server) bindSignInProviders(ctx context.Context) error {
	if s.signInProviders.store == nil || !s.components.Has(ComponentAPI) {
		return nil
	}

	b := s.signInProviders

	// The refusal of a changed issuer is one sentence, so it goes to the
	// operator as it is.
	return session.BindProviders(
		ctx,
		b.store,
		b.providers,
		s.now().UTC(),
	)
}

// tokenInactivity applies the account inactivity rule of the access token
// policy to the owner of a console session, so that a session does not
// keep an account alive that the token lookup and the sweep would end. The
// policy comes from the session lookup.
type tokenInactivity struct{}

// AccountInactive implements session.InactivityRule.
func (tokenInactivity) AccountInactive(o session.Owner, p session.AccountPolicy, now time.Time) bool {
	policy := accesstoken.DefaultPolicy()
	policy.Inactivity, policy.DisableAccounts = p.Inactivity, p.DisableAccounts

	return policy.AccountInactiveAt(accesstoken.Owner{
		ActiveAt: o.ActiveAt, DisabledAt: o.DisabledAt, ID: o.ID, Subject: o.Subject, DisplayName: o.DisplayName,
		Kind: o.Kind, BreakGlass: o.BreakGlass,
	}, now)
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

	every := s.signInPruneEvery
	if every <= 0 {
		every = signInRequestPruneEvery
	}

	ticker := time.NewTicker(every)
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

	if p.ID == session.DevProviderID {
		return none, fmt.Errorf("%w: provider ID %q is reserved for the dev sign-in", errSignInSetup, p.ID)
	}

	if p.ID == session.LocalProviderID {
		return none, fmt.Errorf("%w: provider ID %q is reserved for the local users", errSignInSetup, p.ID)
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

	method, err := redirectMethod(client, p.ID, session.MethodOIDC, configuredProviderOn, now)
	if err != nil {
		return none, fmt.Errorf("%w: %w", errSignInSetup, err)
	}

	displayName := p.DisplayName
	if displayName == "" {
		displayName = p.ID
	}

	return serverapi.SignInProvider{
		Method: method, ID: p.ID, DisplayName: displayName, AutoCreate: p.AutoCreate, Stored: false,
	}, nil
}

// errSignInUngated reports a console sign-in with access tokens and with no
// failure gate.
var errSignInUngated = errors.New(
	"console sign-in: the store keeps access tokens, but a sign-in failure gate is missing",
)

// setSignInLimiters sets the budgets of the sign-in routes and of the
// password routes of in from gates. A gate that gatedAuthenticator did not
// make stays nil in in: a nil gate in a non-nil interface would pass the nil
// checks of the handler. A nil limit means no failure limit, so with tokens
// it refuses a missing gate, and the server does not start.
func setSignInLimiters(gates signInGates, tokens *accesstoken.Service, in *serverapi.SignIn) error {
	// Defense in depth: gatedAuthenticator makes all three gates when the
	// store keeps tokens, and the assembly passes them here. This check stays
	// so that a new order of the assembly or a new caller cannot start the
	// sign-in routes with no failure limit.
	if tokens != nil && (gates.signInGate == nil || gates.passwordGate == nil || gates.passkeyGate == nil ||
		gates.passkeyBeginGate == nil) {
		return errSignInUngated
	}

	if gates.signInGate != nil {
		in.Limiter = gates.signInGate
	}

	if gates.passwordGate != nil {
		in.PasswordLimiter = gates.passwordGate
	}

	if gates.passkeyGate != nil {
		in.PasskeyLimiter = gates.passkeyGate
	}

	if gates.passkeyBeginGate != nil {
		in.PasskeyBeginLimiter = gates.passkeyBeginGate
	}

	return nil
}
