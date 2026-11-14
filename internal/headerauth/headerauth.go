// Package headerauth is the adapter of the trusted header: a
// session.RequestMethod that reads the identity of a person from a request
// that a trusted proxy sends.
//
// Each mode has two layers. The peer of the request must be in the proxy
// list of the provider, which is separate from the proxy list that the
// server uses to read the client address. In the jwt mode, the request must
// also carry a JWT that the keys of the issuer sign. In the plain mode, the
// request must also carry a shared secret header.
package headerauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Mode is the mode of a header provider.
type Mode string

// Modes of a header provider.
const (
	// ModeJWT reads a signed JWT from the user header.
	ModeJWT Mode = "jwt"
	// ModePlain reads the subject from the user header and needs a shared
	// secret header.
	ModePlain Mode = "plain"
)

// Preset fills the header name, the key URL and the issuer of a known
// proxy in the jwt mode.
type Preset string

// Presets of the jwt mode.
const (
	// PresetCustom is a custom issuer with a JWKS URL.
	PresetCustom Preset = ""
	// PresetGoogleIAP is Google Cloud Identity-Aware Proxy.
	PresetGoogleIAP Preset = "google-iap"
	// PresetAWSALB is the AWS Application Load Balancer.
	PresetAWSALB Preset = "aws-alb"
)

// Values of the presets.
const (
	// GoogleIAPHeader is the header that Google IAP sends.
	GoogleIAPHeader = "X-Goog-Iap-Jwt-Assertion"
	// GoogleIAPIssuer is the issuer of the JWT of Google IAP.
	GoogleIAPIssuer = "https://cloud.google.com/iap"
	// GoogleIAPKeyURL is the JWKS URL of Google IAP.
	GoogleIAPKeyURL = "https://www.gstatic.com/iap/verify/public_key-jwk"
	// AWSALBHeader is the header that the AWS ALB sends.
	AWSALBHeader = "X-Amzn-Oidc-Data"
)

// Limits of the method.
const (
	// MaxSubject is the longest subject of the plain mode, in bytes.
	MaxSubject = session.MaxAssertionSubject
	// MaxSecretBytes is the longest shared secret, in bytes.
	MaxSecretBytes = 1024
	// MinSecretBytes is the shortest shared secret, in bytes.
	MinSecretBytes = 32
	// maxGroupsHeader is the longest groups header, in bytes: the most
	// groups, each of the longest name, with a comma.
	maxGroupsHeader = session.MaxAssertionGroups * (session.MaxAssertionGroup + 1)
)

// ErrConfig reports a configuration of the method that is not valid.
var ErrConfig = errors.New("trusted header configuration is not valid")

// subjectPattern is the allowlist of the characters of a subject of the
// plain mode.
var subjectPattern = regexp.MustCompile(`^[A-Za-z0-9._@+:-]{1,255}$`)

// albKeyIDPattern is the allowlist of a key ID of the AWS ALB. The key ID
// goes into the key URL, so it can hold no path character.
var albKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

// headerNamePattern is the form of a header name of the configuration.
var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// regionPattern is the form of an AWS region, for example eu-west-1.
var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-\d{1,2}$`)

// claimNamePattern is the form of the name of the groups claim.
var claimNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// JWTConfig is the configuration of the jwt mode.
type JWTConfig struct {
	// Preset is the known proxy, or PresetCustom.
	Preset Preset
	// Issuer is the iss claim that the JWT must have. Google IAP fills it.
	// For the AWS ALB, it is the issuer of the identity provider of the
	// load balancer.
	Issuer string
	// Audience is the aud claim that the JWT must hold. The AWS ALB sends
	// no audience: the signer takes its place.
	Audience string
	// KeyURL is the JWKS URL of a custom issuer or of Google IAP, or the
	// base of the key URL of the AWS ALB, which ends with "/". Google IAP
	// fills it, and Region fills it for the AWS ALB.
	KeyURL string
	// Region is the AWS region of the load balancer.
	Region string
	// SignerARN is the ARN of the AWS ALB. The signer field of each JWT
	// must equal it.
	SignerARN string
	// ClientID is the client ID of the authenticate rule of the AWS ALB, or
	// empty. When it is set, the client field of each JWT must equal it. One
	// load balancer can have more than one rule with the same identity
	// provider.
	ClientID string
	// GroupsClaim names the claim of the groups, or is empty for no
	// groups.
	GroupsClaim string
}

// Config is the configuration of a Method.
type Config struct {
	// HTTPClient reads the keys of the issuer. It must apply the egress
	// rules (egresshttp.ClientWith). The jwt mode needs it.
	HTTPClient *http.Client
	// Enabled reports whether the provider is on. Nil means on.
	Enabled func(ctx context.Context) (bool, error)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// ProviderID names the sign-in provider.
	ProviderID string
	// Mode is the mode of the provider.
	Mode Mode
	// TrustedProxies are the addresses and the ranges of the proxies of
	// this provider. An empty list turns the provider off.
	TrustedProxies []netip.Prefix
	// UserHeader names the header of the subject (plain) or of the JWT
	// (jwt). A preset fills it.
	UserHeader string
	// GroupsHeader names the header of the groups in the plain mode, or is
	// empty for no groups.
	GroupsHeader string
	// SecretHeader names the header of the shared secret of the plain
	// mode.
	SecretHeader string
	// Secret is the shared secret of the plain mode.
	Secret session.Password
	// JWT is the configuration of the jwt mode.
	JWT JWTConfig
	// KeyTimeout bounds one read of the keys of the issuer. Zero means
	// egress.Timeout.
	KeyTimeout time.Duration
}

// Method is the trusted header sign-in method.
type Method struct {
	enabled      func(ctx context.Context) (bool, error)
	now          func() time.Time
	keys         *keyCache
	secretSum    [sha256.Size]byte
	providerID   string
	mode         Mode
	preset       Preset
	userHeader   string
	groupsHeader string
	secretHeader string
	issuer       string
	audience     string
	signerARN    string
	albClient    string
	groupsClaim  string
	proxies      []netip.Prefix
}

var _ session.RequestMethod = (*Method)(nil)

// New returns a Method for cfg. It returns ErrConfig for a configuration
// that is not valid.
func New(cfg Config) (*Method, error) {
	m := &Method{
		enabled: cfg.Enabled, now: cfg.Now, providerID: cfg.ProviderID, mode: cfg.Mode,
		preset: cfg.JWT.Preset, userHeader: cfg.UserHeader, groupsHeader: cfg.GroupsHeader,
		secretHeader: cfg.SecretHeader, issuer: cfg.JWT.Issuer, audience: cfg.JWT.Audience,
		signerARN: cfg.JWT.SignerARN, albClient: cfg.JWT.ClientID, groupsClaim: cfg.JWT.GroupsClaim,
		proxies: slices.Clone(cfg.TrustedProxies), keys: nil, secretSum: [sha256.Size]byte{},
	}

	if m.now == nil {
		m.now = time.Now
	}

	if !session.ValidProviderID(cfg.ProviderID) || cfg.ProviderID == session.LocalProviderID ||
		cfg.ProviderID == session.DevProviderID {
		return nil, fmt.Errorf("%w: the provider ID is not valid", ErrConfig)
	}

	if err := checkProxies(cfg.TrustedProxies); err != nil {
		return nil, err
	}

	var err error

	switch cfg.Mode {
	case ModePlain:
		err = configurePlain(m, cfg)
	case ModeJWT:
		err = configureJWT(m, cfg)
	default:
		err = fmt.Errorf("%w: the mode must be %q or %q", ErrConfig, ModeJWT, ModePlain)
	}

	if err != nil {
		return nil, err
	}

	return m, nil
}

// Kind implements session.RequestMethod.
func (*Method) Kind() session.MethodKind {
	return session.MethodHeader
}

// Headers implements session.RequestMethod: the user header, and the groups
// header and the secret header when the provider has them.
func (m *Method) Headers() []string {
	out := []string{m.userHeader}

	for _, h := range []string{m.groupsHeader, m.secretHeader} {
		if h != "" {
			out = append(out, h)
		}
	}

	return out
}

// Strip removes each header of the method from r when peer is not in the
// proxy list of the provider, and reports whether it removed a header. The
// caller passes the TCP peer of the connection (clientip.Peer), never an
// address from a forwarded header, and writes the audit entry of a refused
// header.
func (m *Method) Strip(r *http.Request, peer netip.Addr) bool {
	if r == nil || m.trusted(peer) {
		return false
	}

	return removeHeaders(r, m.Headers())
}

// Consume removes the secret header of the plain mode from r after the
// caller read the request facts and called FromRequest, and reports
// whether it removed a header. The HTTP adapter calls the methods in this
// order: Strip, then FromRequest, then Consume after a successful sign-in
// and after a refused one, then the next handler. So no handler reads the
// shared secret of the proxy. The jwt mode has no secret header, so
// Consume removes nothing.
func (m *Method) Consume(r *http.Request) bool {
	if r == nil || m.secretHeader == "" {
		return false
	}

	return removeHeaders(r, []string{m.secretHeader})
}

// removeHeaders removes each name in names from r in each spelling of the
// name, also with an underscore for a hyphen, and reports whether it
// removed a header. A proxy or a handler that reads another form of the
// name must not get the header.
func removeHeaders(r *http.Request, names []string) bool {
	removed := false

	for _, h := range names {
		for name := range r.Header {
			if strings.EqualFold(strings.ReplaceAll(name, "_", "-"), h) {
				delete(r.Header, name)

				removed = true
			}
		}
	}

	return removed
}

// PeerOf returns the TCP peer of r for Strip. It never reads a header.
func PeerOf(r *http.Request) netip.Addr {
	return clientip.Peer(r)
}

// FromRequest implements session.RequestMethod.
func (m *Method) FromRequest(ctx context.Context, f session.RequestFacts) (session.Assertion, error) {
	a, err := m.assertionFromRequest(ctx, f)
	if err != nil {
		var none session.Assertion

		return none, err
	}

	// Defense in depth: each step above checks its own value. This check
	// stays so that a value that a step missed never reaches the sign-in
	// service.
	if err := session.CheckAssertion(a, m.now()); err != nil {
		var none session.Assertion

		return none, fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	}

	return a, nil
}

// assertionFromRequest runs the checks of FromRequest in their order.
func (m *Method) assertionFromRequest(ctx context.Context, f session.RequestFacts) (session.Assertion, error) {
	var none session.Assertion

	users, ok := f.Headers[m.userHeader]
	if !ok || len(users) == 0 {
		return none, session.ErrNoAssertion
	}

	if f.ProviderID != m.providerID {
		return none, fmt.Errorf("%w: the request is for another provider", session.ErrMethodInput)
	}

	if err := m.checkEnabled(ctx); err != nil {
		return none, err
	}

	// Layer 1: the peer. The address is the TCP peer of the connection,
	// never the client address that the server read from a forwarded
	// header.
	if !m.trusted(f.Peer) {
		return none, fmt.Errorf("%w: the peer is not in the proxy list of the provider", session.ErrUntrustedSource)
	}

	if len(users) != 1 {
		return none, fmt.Errorf("%w: the user header comes more than once", session.ErrMethodInput)
	}

	if m.mode == ModePlain {
		return m.fromPlain(f, users[0])
	}

	return m.fromJWT(ctx, users[0])
}

// trusted reports whether peer is in the proxy list of the provider. An
// empty list trusts no peer.
func (m *Method) trusted(peer netip.Addr) bool {
	if !peer.IsValid() {
		return false
	}

	peer = peer.Unmap().WithZone("")

	for _, p := range m.proxies {
		if p.Contains(peer) {
			return true
		}
	}

	return false
}

// checkEnabled returns ErrProviderDisabled for a provider that is off or
// that has no proxy.
func (m *Method) checkEnabled(ctx context.Context) error {
	if len(m.proxies) == 0 {
		return fmt.Errorf("%w: the proxy list of the provider is empty", session.ErrProviderDisabled)
	}

	if m.enabled == nil {
		return nil
	}

	on, err := m.enabled(ctx)
	if err != nil {
		return fmt.Errorf("%w: read the provider state: %w", session.ErrMethodUnavailable, err)
	}

	if !on {
		return session.ErrProviderDisabled
	}

	return nil
}

// fromJWT returns the Assertion of the jwt mode.
func (m *Method) fromJWT(ctx context.Context, raw string) (session.Assertion, error) {
	var none session.Assertion

	if len(raw) > trustrule.MaxTokenBytes || !printableASCII(raw) {
		return none, fmt.Errorf("%w: the token header is too long or has characters outside printable ASCII",
			session.ErrMethodInput)
	}

	var (
		tok trustrule.Token
		err error
	)

	if m.preset == PresetAWSALB {
		tok, err = m.verifyALB(ctx, raw)
	} else {
		tok, err = m.verifyJWKS(ctx, raw)
	}

	if err != nil {
		return none, err
	}

	return m.assertionOf(&tok)
}

// headerAssertion returns the Assertion of the trusted header for the
// subject sub of the provider id: no factor, no authentication time, no
// groups.
func headerAssertion(id, sub string) session.Assertion {
	return session.Assertion{
		AuthTime: time.Time{}, ProviderID: id, PrincipalID: "", Subject: sub, UserName: "", Email: "",
		Method: session.MethodHeader, Names: nil, Groups: nil, Factors: nil, EmailVerified: false,
		GroupsPresent: false, AuthTimeKnown: false,
	}
}
