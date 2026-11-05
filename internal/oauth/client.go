// Package oauth is the OpenID Connect client of the console sign-in. It
// reads the discovery document of a provider, builds the authorization
// request (state, nonce and PKCE with S256), exchanges the code at the
// token endpoint, and checks the ID token.
//
// github.com/coreos/go-oidc/v3 checks the signature of the ID token, its
// issuer, its audience, its expiry and its nbf claim. This package adds the
// checks that go-oidc leaves to the caller: the nonce, the authorized party
// (azp), the auth_time claim, and the max_age of the request. It also
// refuses a provider whose endpoints do not use HTTPS, an answer larger
// than 1 MiB, and a redirect from HTTPS to HTTP.
//
// The package holds no state between requests. The caller stores the
// Pending value of each authorization request. At the callback, the caller
// checks the state with CheckState, deletes the stored Pending value so
// that it works once, and then gives it to Finish.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Errors of the client. Each one wraps the reason. No error holds a code,
// a token, a state or a nonce.
var (
	// ErrConfig reports a configuration that the client cannot use.
	ErrConfig = errors.New("oauth configuration is not valid")
	// ErrProvider reports a provider that does not answer correctly: no
	// discovery document, no supported signature algorithm, or a refused
	// code exchange.
	ErrProvider = errors.New("identity provider refused the request")
	// ErrIDToken reports an ID token that the client refuses.
	ErrIDToken = errors.New("ID token is not valid")
	// ErrState reports a callback whose state is empty or does not match
	// the pending request.
	ErrState = errors.New("state does not match the sign-in request")
)

// Errors of the HTTP client of the provider.
var (
	errResponseTooLarge = errors.New("answer of the identity provider is larger than 1 MiB")
	errDowngrade        = errors.New("identity provider redirected from https to http")
	errTooManyRedirects = errors.New("identity provider redirected too many times")
)

// signingAlgorithms returns the signature algorithms that the client
// accepts for an ID token. The client never accepts "none" or an HMAC
// algorithm.
func signingAlgorithms() []string {
	return []string{oidc.RS256, oidc.ES256, oidc.EdDSA}
}

// clockSkew is the tolerance for the iat and auth_time claims against the
// clock of the server.
const clockSkew = time.Minute

// defaultTimeout is the timeout of each request to the provider when the
// configuration gives no HTTP client, or one with no timeout.
const defaultTimeout = 10 * time.Second

// maxResponseBytes is the largest answer that the client reads from the
// provider: a discovery document, a JWKS or a token answer.
const maxResponseBytes = 1 << 20

// maxRedirects is the largest number of redirects that the client follows
// for one request, as net/http does by default.
const maxRedirects = 10

// randomBytes is the size of a state and of a nonce: 256 bits.
const randomBytes = 32

// schemeHTTPS is the URL scheme that every endpoint of a provider uses.
const schemeHTTPS = "https"

// scopeOpenID is the scope that makes the authorization request an
// OpenID Connect request.
const scopeOpenID = "openid"

// Config configures a client. Issuer is the issuer URL of the provider.
// It must use HTTPS. An HTTP issuer on a loopback host is accepted for
// development and tests only. RedirectURL is the callback URL of the
// server, which the provider knows. Scopes are added to "openid".
// HTTPClient makes the requests to the provider; nil, or a client with no
// timeout, uses a timeout of 10 seconds. The client always limits an
// answer to 1 MiB and refuses a redirect from HTTPS to HTTP. Now is the
// clock; nil uses time.Now.
type Config struct {
	HTTPClient   *http.Client
	Now          func() time.Time
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// Client is the client of one provider. It is safe for concurrent use.
type Client struct {
	oauth2   oauth2.Config
	verifier *oidc.IDTokenVerifier
	http     *http.Client
	now      func() time.Time
}

// New reads the discovery document of the provider and returns a client.
// It answers ErrConfig for a configuration with a missing or wrong field,
// and ErrProvider when the discovery document cannot be read, names an
// endpoint that does not use HTTPS, or names no signature algorithm that
// the client accepts.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	client := safeClient(cfg.HTTPClient)

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", ErrProvider, err)
	}

	if endpointErr := checkEndpoints(provider); endpointErr != nil {
		return nil, endpointErr
	}

	algs, err := providerAlgorithms(provider)
	if err != nil {
		return nil, err
	}

	scopes := []string{scopeOpenID}
	for _, s := range cfg.Scopes {
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}

	// The key set fetches and caches the JWKS. Its requests use the client
	// of the context, and the cancellation of the context does not apply.
	keyCtx := oidc.ClientContext(context.WithoutCancel(ctx), client)

	return &Client{
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
		},
		verifier: provider.VerifierContext(keyCtx, &oidc.Config{
			ClientID:                   cfg.ClientID,
			SupportedSigningAlgs:       algs,
			Now:                        now,
			SkipClientIDCheck:          false,
			SkipExpiryCheck:            false,
			SkipIssuerCheck:            false,
			InsecureSkipSignatureCheck: false,
		}),
		http: client,
		now:  now,
	}, nil
}

// validate returns ErrConfig when cfg cannot make a client.
func (cfg Config) validate() error {
	switch {
	case cfg.ClientID == "":
		return fmt.Errorf("%w: client ID is empty", ErrConfig)
	case cfg.ClientSecret == "":
		return fmt.Errorf("%w: client secret is empty", ErrConfig)
	case cfg.RedirectURL == "":
		return fmt.Errorf("%w: redirect URL is empty", ErrConfig)
	}

	issuer, err := url.Parse(cfg.Issuer)
	if err != nil || issuer.Host == "" {
		return fmt.Errorf("%w: issuer %q is not an absolute URL", ErrConfig, cfg.Issuer)
	}

	if !secureURL(issuer) {
		return fmt.Errorf("%w: issuer %q must use https", ErrConfig, cfg.Issuer)
	}

	return nil
}

// secureURL reports whether u uses HTTPS, or HTTP on a loopback host.
func secureURL(u *url.URL) bool {
	return u.Scheme == schemeHTTPS || (u.Scheme == "http" && loopback(u.Hostname()))
}

// checkEndpoints returns ErrProvider when the discovery document of p
// names an authorization endpoint, a token endpoint or a JWKS URI that
// is not an absolute URL with HTTPS. go-oidc does not check the scheme.
func checkEndpoints(p *oidc.Provider) error {
	var meta struct {
		JWKSURI string `json:"jwks_uri"` //nolint:tagliatelle // The OpenID Connect wire name; it must not change.
	}

	if err := p.Claims(&meta); err != nil {
		return fmt.Errorf("%w: discovery: %w", ErrProvider, err)
	}

	endpoint := p.Endpoint()

	for name, raw := range map[string]string{
		"authorization_endpoint": endpoint.AuthURL,
		"token_endpoint":         endpoint.TokenURL,
		"jwks_uri":               meta.JWKSURI,
	} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || !secureURL(u) {
			return fmt.Errorf("%w: discovery: %s %q must be an https URL", ErrProvider, name, raw)
		}
	}

	return nil
}

// safeClient returns a copy of base, or a new client when base is nil,
// that limits each answer to maxResponseBytes, refuses a redirect from
// HTTPS to HTTP, and has a timeout.
func safeClient(base *http.Client) *http.Client {
	var c http.Client
	if base != nil {
		c = *base
	}

	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}

	next := c.Transport
	if next == nil {
		next = http.DefaultTransport
	}

	c.Transport = limitTransport{next: next}
	c.CheckRedirect = refuseDowngrade

	return &c
}

// refuseDowngrade is the redirect rule of the client: at most maxRedirects
// redirects, and none from HTTPS to HTTP.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errTooManyRedirects
	}

	if len(via) > 0 && via[len(via)-1].URL.Scheme == schemeHTTPS && req.URL.Scheme != schemeHTTPS {
		return errDowngrade
	}

	return nil
}

// limitTransport limits the body of each answer to maxResponseBytes.
type limitTransport struct {
	next http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // A RoundTripper returns the error of the transport as it is.
	}

	resp.Body = &limitedBody{body: resp.Body, read: 0}

	return resp, nil
}

// limitedBody is a body that fails after maxResponseBytes.
type limitedBody struct {
	body io.ReadCloser
	read int64
}

// Read implements io.Reader. It returns errResponseTooLarge when the body
// is longer than maxResponseBytes.
func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.read += int64(n)

	if b.read > maxResponseBytes {
		return n, errResponseTooLarge
	}

	return n, err //nolint:wrapcheck // A body returns io.EOF as it is; a wrapped EOF breaks its readers.
}

// Close implements io.Closer.
func (b *limitedBody) Close() error {
	return b.body.Close() //nolint:wrapcheck // A body returns the error of the connection as it is.
}

// loopback reports whether host names the loopback interface.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// providerAlgorithms returns the signature algorithms that both the
// provider and the client support, in the order of the client.
func providerAlgorithms(p *oidc.Provider) ([]string, error) {
	var meta struct {
		Algorithms []string `json:"id_token_signing_alg_values_supported"` //nolint:tagliatelle // The OpenID Connect wire name; it must not change.
	}

	if err := p.Claims(&meta); err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", ErrProvider, err)
	}

	var algs []string

	for _, alg := range signingAlgorithms() {
		if slices.Contains(meta.Algorithms, alg) {
			algs = append(algs, alg)
		}
	}

	if len(algs) == 0 {
		return nil, fmt.Errorf("%w: discovery names no supported signature algorithm %v, want one of %v",
			ErrProvider, meta.Algorithms, signingAlgorithms())
	}

	return algs, nil
}

// Options are the choices of one authorization request. State is the
// state to send: a caller that keys its stored requests by the state gives
// one. An empty State makes a new one of 256 bits. When HasMaxAge is true,
// the request sends max_age with MaxAge in whole seconds, and Finish
// refuses an authentication that is older. A MaxAge of 0 asks the provider
// to authenticate the person again.
type Options struct {
	State     string
	MaxAge    time.Duration
	HasMaxAge bool
}

// Pending is what the caller stores between Begin and Finish. It holds
// secrets: the caller keeps it on the server, uses it once, and never
// shows it.
type Pending struct {
	State     string
	Nonce     string
	Verifier  string
	MaxAge    time.Duration
	HasMaxAge bool
}

// CheckState returns ErrState when got, the state of the callback, is
// empty or differs from the state of the pending request p. The compare
// takes constant time.
func CheckState(got string, p Pending) error {
	if got == "" || p.State == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.State)) != 1 {
		return ErrState
	}

	return nil
}

// Begin returns the authorization URL to send the browser to, and the
// Pending value of the request. Each call makes a new state, nonce and
// PKCE verifier. The request sends MaxAge in whole seconds, and a
// negative MaxAge sends 0.
func (c *Client) Begin(opts Options) (string, Pending) {
	state := opts.State
	if state == "" {
		state = randomText()
	}

	p := Pending{
		State:     state,
		Nonce:     randomText(),
		Verifier:  oauth2.GenerateVerifier(),
		MaxAge:    max(opts.MaxAge, 0).Truncate(time.Second),
		HasMaxAge: opts.HasMaxAge,
	}

	params := []oauth2.AuthCodeOption{oidc.Nonce(p.Nonce), oauth2.S256ChallengeOption(p.Verifier)}
	if p.HasMaxAge {
		params = append(params, oauth2.SetAuthURLParam("max_age", strconv.FormatInt(int64(p.MaxAge/time.Second), 10)))
	}

	return c.oauth2.AuthCodeURL(p.State, params...), p
}

// Identity is the person that the provider signed in. Subject is the sub
// claim, which is never empty. AuthTime is the auth_time claim, or the
// time of Finish when the ID token has none. AuthTimeKnown is true only
// when AuthTime comes from the auth_time claim.
type Identity struct {
	AuthTime          time.Time
	Subject           string
	Name              string
	PreferredUsername string
	Email             string
	AuthTimeKnown     bool
}

// Finish exchanges the code of the callback for tokens and checks the ID
// token against the pending request p. Before it calls Finish, the caller
// checks the state of the callback with CheckState and deletes the stored
// p, so that a pending request works once. Finish answers
// ErrProvider when the token endpoint refuses the code, and ErrIDToken
// when the ID token is missing or fails a check.
func (c *Client) Finish(ctx context.Context, code string, p Pending) (Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.http)

	token, err := c.oauth2.Exchange(ctx, code, oauth2.VerifierOption(p.Verifier))
	if err != nil {
		return Identity{}, exchangeError(err)
	}

	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, fmt.Errorf("%w: the token answer has no ID token", ErrIDToken)
	}

	idToken, err := c.verifier.Verify(oidc.ClientContext(ctx, c.http), raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrIDToken, err)
	}

	return c.checkClaims(idToken, p)
}

// exchangeError returns ErrProvider for a failed code exchange. For an
// answer of the token endpoint, it keeps only the HTTP status and the
// OAuth error code. The body of the answer stays out, because a provider
// can echo the request in it.
func exchangeError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		if re.ErrorCode != "" {
			return refusedError{err: fmt.Errorf(
				"%w: code exchange: status %d, error %q",
				ErrProvider,
				re.Response.StatusCode,
				re.ErrorCode,
			)}
		}

		return refusedError{err: fmt.Errorf("%w: code exchange: status %d", ErrProvider, re.Response.StatusCode)}
	}

	return fmt.Errorf("%w: code exchange: %w", ErrProvider, err)
}

// idClaims are the claims that Finish reads after go-oidc checked the
// token.
type idClaims struct {
	AuthTime          *int64 `json:"auth_time"` //nolint:tagliatelle // The OpenID Connect wire name; it must not change.
	AuthorizedParty   string `json:"azp"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"` //nolint:tagliatelle // The OpenID Connect wire name; it must not change.
	Email             string `json:"email"`
}

// checkClaims applies the checks that go-oidc leaves to the caller: the
// nonce, the sub claim, the authorized party, the iat claim, the auth_time
// claim and the max_age of the request.
func (c *Client) checkClaims(t *oidc.IDToken, p Pending) (Identity, error) {
	if p.Nonce == "" || subtle.ConstantTimeCompare([]byte(t.Nonce), []byte(p.Nonce)) != 1 {
		return Identity{}, fmt.Errorf("%w: nonce does not match the request", ErrIDToken)
	}

	if t.Subject == "" {
		return Identity{}, fmt.Errorf("%w: sub claim is empty", ErrIDToken)
	}

	var claims idClaims
	if err := t.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims: %w", ErrIDToken, err)
	}

	// OpenID Connect Core 1.0, section 3.1.3.7: a token for more than one
	// audience, or with an azp claim, must name this client as azp.
	if (len(t.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != c.oauth2.ClientID {
		return Identity{}, fmt.Errorf("%w: azp claim %q does not name this client", ErrIDToken, claims.AuthorizedParty)
	}

	now := c.now()

	if t.IssuedAt.After(now.Add(clockSkew)) {
		return Identity{}, fmt.Errorf("%w: iat %s is in the future", ErrIDToken, t.IssuedAt.UTC().Format(time.RFC3339))
	}

	authTime, err := checkAuthTime(claims.AuthTime, p, now)
	if err != nil {
		return Identity{}, err
	}

	return Identity{
		AuthTime:          authTime,
		Subject:           t.Subject,
		Name:              claims.Name,
		PreferredUsername: claims.PreferredUsername,
		Email:             claims.Email,
		AuthTimeKnown:     claims.AuthTime != nil,
	}, nil
}

// checkAuthTime checks the auth_time claim at now and returns the time of
// the authentication. A request with max_age needs the claim (OpenID
// Connect Core 1.0, section 3.1.2.1), and the authentication must not be
// older than max_age. The claim must not be in the future. Each check
// allows a skew of one minute.
func checkAuthTime(claim *int64, p Pending, now time.Time) (time.Time, error) {
	if claim == nil {
		if p.HasMaxAge {
			return time.Time{}, fmt.Errorf("%w: auth_time claim is missing, and the request sent max_age", ErrIDToken)
		}

		return now, nil
	}

	authTime := time.Unix(*claim, 0)

	if authTime.After(now.Add(clockSkew)) {
		return time.Time{}, fmt.Errorf(
			"%w: auth_time %s is in the future",
			ErrIDToken,
			authTime.UTC().Format(time.RFC3339),
		)
	}

	if p.HasMaxAge && now.Sub(authTime) > p.MaxAge+clockSkew {
		return time.Time{}, fmt.Errorf("%w: authentication at %s is older than max_age %s",
			ErrIDToken, authTime.UTC().Format(time.RFC3339), p.MaxAge)
	}

	return authTime, nil
}

// randomText returns randomBytes from crypto/rand, base64url-encoded.
// crypto/rand.Read never returns an error: it ends the program instead.
func randomText() string {
	b := make([]byte, randomBytes)
	_, _ = rand.Read(b)

	return base64.RawURLEncoding.EncodeToString(b)
}
