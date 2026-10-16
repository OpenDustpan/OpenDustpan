package oauth

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// ErrUserInfo reports a user-info answer that the client refuses: not a
// JSON object, or no usable subject claim.
var ErrUserInfo = errors.New("user-info answer is not valid")

// Claims names the claims of the user-info answer that identify a person.
// Subject is required: its value is the identity of the person at the
// provider. Name, Username and Email give the display name, in that order.
type Claims struct {
	Subject  string
	Name     string
	Username string
	Email    string
}

// Default claim names, as OpenID Connect names them.
const (
	defaultSubjectClaim  = "sub"
	defaultNameClaim     = "name"
	defaultUsernameClaim = "preferred_username"
	defaultEmailClaim    = "email"
)

// WithDefaults returns c with the OpenID Connect name of each claim that
// is empty: sub, name, preferred_username and email.
func (c Claims) WithDefaults() Claims {
	return Claims{
		Subject:  cmp.Or(c.Subject, defaultSubjectClaim),
		Name:     cmp.Or(c.Name, defaultNameClaim),
		Username: cmp.Or(c.Username, defaultUsernameClaim),
		Email:    cmp.Or(c.Email, defaultEmailClaim),
	}
}

// PlainConfig configures a client of a plain OAuth 2.0 provider: a
// provider with no discovery document and no ID token. Each endpoint must
// be an absolute URL with HTTPS, or with HTTP on a loopback host, and with
// no fragment. The client finds the person with a GET of UserinfoURL with
// the access token. Scopes are sent as given. HTTPClient, Now, ClientID,
// ClientSecret and RedirectURL are as in Config.
type PlainConfig struct {
	HTTPClient       *http.Client
	Now              func() time.Time
	AuthorizationURL string
	TokenURL         string
	UserinfoURL      string
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	Scopes           []string
	Claims           Claims
}

// PlainClient is the client of one plain OAuth 2.0 provider. It sends a
// state and a PKCE challenge, and no nonce: with no ID token, nothing
// carries a nonce back. It is safe for concurrent use.
type PlainClient struct {
	oauth2   oauth2.Config
	userinfo string
	claims   Claims
	http     *http.Client
	now      func() time.Time
}

// NewPlain returns a client of cfg. It makes no request. It returns
// ErrConfig for a configuration with a missing or wrong field.
func NewPlain(cfg PlainConfig) (*PlainClient, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &PlainClient{
		oauth2: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL: cfg.AuthorizationURL, DeviceAuthURL: "", TokenURL: cfg.TokenURL,
				AuthStyle: oauth2.AuthStyleAutoDetect,
			},
			RedirectURL: cfg.RedirectURL,
			Scopes:      slices.Clone(cfg.Scopes),
		},
		userinfo: cfg.UserinfoURL,
		claims:   cfg.Claims.WithDefaults(),
		http:     noRedirect(safeClient(cfg.HTTPClient)),
		now:      now,
	}, nil
}

// noRedirect returns c with no redirect: a redirect answers as it is, and
// the client refuses it as an answer that is not 200. The identity of a
// person must come from the configured user-info URL, and the code from
// the configured token URL, never from a host that a redirect names.
func noRedirect(c *http.Client) *http.Client {
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return c
}

// validate returns ErrConfig when cfg cannot make a client.
func (cfg PlainConfig) validate() error {
	switch {
	case cfg.ClientID == "":
		return fmt.Errorf("%w: client ID is empty", ErrConfig)
	case cfg.ClientSecret == "":
		return fmt.Errorf("%w: client secret is empty", ErrConfig)
	case cfg.RedirectURL == "":
		return fmt.Errorf("%w: redirect URL is empty", ErrConfig)
	}

	for name, raw := range map[string]string{
		"authorization URL": cfg.AuthorizationURL, "token URL": cfg.TokenURL, "user-info URL": cfg.UserinfoURL,
	} {
		if err := checkEndpointURL(name, raw); err != nil {
			return err
		}
	}

	return nil
}

// checkEndpointURL returns ErrConfig when raw, the endpoint name, is not an
// absolute URL with HTTPS, or HTTP on a loopback host, with no fragment.
func checkEndpointURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.Fragment != "" {
		return fmt.Errorf("%w: %s %q is not an absolute URL with no fragment", ErrConfig, name, raw)
	}

	if !secureURL(u) {
		return fmt.Errorf("%w: %s %q must use https", ErrConfig, name, raw)
	}

	return nil
}

// Begin returns the authorization URL and the pending request. It sends
// no max_age: a plain OAuth provider returns no auth_time to check it
// with, so the pending request has no max_age either. The error is
// always nil; it is there so that the client meets the interface of the
// sign-in routes.
//
//nolint:unparam // The sign-in routes call Begin through an interface that Lazy also meets, and Lazy can fail.
func (c *PlainClient) Begin(_ context.Context, opts Options) (string, Pending, error) {
	state := opts.State
	if state == "" {
		state = randomText()
	}

	// The nonce is never sent: with no ID token, nothing carries it back.
	// The pending request still holds a random one, so that a store of
	// pending requests checks one form for each mode.
	p := Pending{State: state, Nonce: randomText(), Verifier: oauth2.GenerateVerifier(), MaxAge: 0, HasMaxAge: false}

	return c.oauth2.AuthCodeURL(p.State, oauth2.S256ChallengeOption(p.Verifier)), p, nil
}

// Finish exchanges the code of the callback for an access token, reads
// the user-info answer with it, and returns the person that the mapped
// claims name. AuthTime is the time of Finish: the provider gives no time
// of the authentication. As for Client.Finish, the caller checks the
// state first and uses p once. Finish answers ErrProvider when the token
// endpoint refuses the code, the token is not a bearer token, or the
// user-info endpoint does not answer 200, and ErrUserInfo when the answer
// is not a JSON object or has no usable subject claim.
func (c *PlainClient) Finish(ctx context.Context, code string, p Pending) (Identity, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.http)

	token, err := c.oauth2.Exchange(ctx, code, oauth2.VerifierOption(p.Verifier))
	if err != nil {
		return Identity{}, exchangeError(err)
	}

	if !strings.EqualFold(token.TokenType, "bearer") || token.AccessToken == "" {
		return Identity{}, fmt.Errorf("%w: the token answer has no bearer access token", ErrProvider)
	}

	info, err := c.readUserinfo(ctx, token.AccessToken)
	if err != nil {
		return Identity{}, err
	}

	return c.identity(info)
}

// readUserinfo sends a GET of the user-info endpoint with the access token
// and returns its JSON object.
func (c *PlainClient) readUserinfo(ctx context.Context, access string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userinfo, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%w: user-info request: %w", ErrProvider, err)
	}

	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: user-info request: %w", ErrProvider, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to its end, or the answer is refused.
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: user-info answer: status %d", ErrProvider, resp.StatusCode)
	}

	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("%w: user-info answer is not application/json", ErrUserInfo)
	}

	return decodeUserinfo(resp.Body)
}

// decodeUserinfo decodes one JSON object from r, with numbers kept as
// text. It refuses any data after the object.
func decodeUserinfo(r io.Reader) (map[string]any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()

	var info map[string]any
	if err := dec.Decode(&info); err != nil || info == nil {
		if errors.Is(err, errResponseTooLarge) {
			return nil, fmt.Errorf("%w: %w", ErrUserInfo, errResponseTooLarge)
		}

		return nil, fmt.Errorf("%w: user-info answer is not a JSON object", ErrUserInfo)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if errors.Is(err, errResponseTooLarge) {
			return nil, fmt.Errorf("%w: %w", ErrUserInfo, errResponseTooLarge)
		}

		return nil, fmt.Errorf("%w: user-info answer has data after the JSON object", ErrUserInfo)
	}

	return info, nil
}

// identity returns the person that the mapped claims of info name.
func (c *PlainClient) identity(info map[string]any) (Identity, error) {
	subject, ok := subjectText(info[c.claims.Subject])
	if !ok {
		return Identity{}, fmt.Errorf("%w: claim %q is not a non-empty string or a whole number",
			ErrUserInfo, c.claims.Subject)
	}

	return Identity{
		AuthTime:          c.now(),
		Subject:           subject,
		Name:              claimText(info[c.claims.Name]),
		PreferredUsername: claimText(info[c.claims.Username]),
		Email:             claimText(info[c.claims.Email]),
	}, nil
}

// subjectText returns the subject that v gives: a non-empty string as it
// is, or a whole number that is not negative in base 10, as a provider
// such as GitHub sends its user IDs.
func subjectText(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, s != ""
	case json.Number:
		n, err := strconv.ParseUint(s.String(), 10, 64)
		if err != nil {
			return "", false
		}

		return strconv.FormatUint(n, 10), true
	default:
		return "", false
	}
}

// claimText returns v when it is a string, and else "": a name claim of
// another type gives no name.
func claimText(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}

	return s
}
