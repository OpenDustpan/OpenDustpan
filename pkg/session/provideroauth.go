package session

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"
)

// ProviderMode names how a stored sign-in provider identifies a person.
type ProviderMode string

// Modes of a stored provider.
const (
	// ModeOIDC is an OpenID provider: the server reads its discovery
	// document, and the ID token identifies the person.
	ModeOIDC ProviderMode = "oidc"
	// ModeOAuth is a plain OAuth 2.0 provider: the server calls its
	// user-info endpoint with the access token, and the subject claim of
	// the answer identifies the person.
	ModeOAuth ProviderMode = "oauth"
	// ModeSAML is a SAML 2.0 identity provider. Validate refuses it until
	// the SAML method and its settings exist.
	ModeSAML ProviderMode = "saml"
	// ModeCAS is a CAS 3.0 server. Validate refuses it until the CAS method
	// and its settings exist.
	ModeCAS ProviderMode = "cas"
	// ModeLDAP is an LDAP directory. Validate refuses it until the LDAP
	// method and its settings exist.
	ModeLDAP ProviderMode = "ldap"
	// ModeHeader is the trusted header of a proxy. Validate refuses it until
	// the trusted-header method and its settings exist.
	ModeHeader ProviderMode = "header"
)

// MethodKind returns the kind of the sign-in method of the mode m, and
// false for a value that is not a mode.
func (m ProviderMode) MethodKind() (MethodKind, bool) {
	switch m {
	case ModeOIDC:
		return MethodOIDC, true
	case ModeOAuth:
		return MethodOAuth, true
	case ModeSAML:
		return MethodSAML, true
	case ModeCAS:
		return MethodCAS, true
	case ModeLDAP:
		return MethodLDAP, true
	case ModeHeader:
		return MethodHeader, true
	default:
		return "", false
	}
}

// Limits of the settings of a provider in ModeOAuth.
const (
	maxEndpoint = 512
	maxClaim    = 64
)

// Default claim names of a provider in ModeOAuth, as OpenID Connect names
// them.
const (
	defaultSubjectClaim  = "sub"
	defaultNameClaim     = "name"
	defaultUsernameClaim = "preferred_username"
	defaultEmailClaim    = "email"
)

// OAuthSettings are the endpoints and the claim mapping of a provider in
// ModeOAuth. The value of SubjectClaim in the user-info answer identifies
// the person; NameClaim, UsernameClaim and EmailClaim give the display
// name, in that order. The server never links a person by email.
type OAuthSettings struct {
	AuthorizationURL string
	TokenURL         string
	UserinfoURL      string
	SubjectClaim     string
	NameClaim        string
	UsernameClaim    string
	EmailClaim       string
}

// WithDefaultClaims returns s with the OpenID Connect name of each claim
// that is empty: sub, name, preferred_username and email.
func (s OAuthSettings) WithDefaultClaims() OAuthSettings {
	s.SubjectClaim = cmp.Or(s.SubjectClaim, defaultSubjectClaim)
	s.NameClaim = cmp.Or(s.NameClaim, defaultNameClaim)
	s.UsernameClaim = cmp.Or(s.UsernameClaim, defaultUsernameClaim)
	s.EmailClaim = cmp.Or(s.EmailClaim, defaultEmailClaim)

	return s
}

// isZero reports whether s has no value at all, as for a provider in
// ModeOIDC.
func (s OAuthSettings) isZero() bool {
	return s.AuthorizationURL == "" && s.TokenURL == "" && s.UserinfoURL == "" && s.SubjectClaim == "" &&
		s.NameClaim == "" && s.UsernameClaim == "" && s.EmailClaim == ""
}

// validate returns ErrInvalidProvider when an endpoint or a claim name of s
// is not valid.
func (s OAuthSettings) validate() error {
	for name, raw := range map[string]string{
		"authorization URL": s.AuthorizationURL, "token URL": s.TokenURL, "user-info URL": s.UserinfoURL,
	} {
		if !validEndpoint(raw) {
			return fmt.Errorf("%w: the %s must be an absolute https URL, or http on a loopback host, "+
				"with no user and no fragment, of at most %d bytes", ErrInvalidProvider, name, maxEndpoint)
		}
	}

	for name, claim := range map[string]string{
		"subject": s.SubjectClaim, "name": s.NameClaim, "username": s.UsernameClaim, "email": s.EmailClaim,
	} {
		if claim == "" || len(claim) > maxClaim || strings.ContainsAny(claim, " ") || hasControl(claim) {
			return fmt.Errorf(
				"%w: the %s claim must have 1 to %d bytes and no space",
				ErrInvalidProvider,
				name,
				maxClaim,
			)
		}
	}

	return nil
}

// validEndpoint reports whether raw is an absolute https URL with a host,
// or an http URL on a loopback host, with no user and no fragment. A query
// is allowed.
func validEndpoint(raw string) bool {
	if raw == "" || len(raw) > maxEndpoint {
		return false
	}

	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}

	return u.Scheme == schemeHTTPS || (u.Scheme == schemeHTTP && loopbackHost(u.Hostname()))
}

// NormalizeEndpoint returns the endpoint raw with a lowercase scheme and
// host and with no default port (443 for https, 80 for http). It keeps the
// path and the query exactly as given. A raw that is not an absolute URL
// comes back as it is.
func NormalizeEndpoint(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || scheme == "" {
		return raw
	}

	scheme = strings.ToLower(scheme)

	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}

	authority, tail := rest[:end], rest[end:]

	u, err := url.Parse(scheme + "://" + authority)
	if err != nil || u == nil || u.Host == "" {
		return raw
	}

	return scheme + "://" + hostPort(scheme, strings.ToLower(u.Hostname()), u.Port()) + tail
}

// hostPort returns host with port, in brackets for an IPv6 address, and
// with no port when port is the default port of scheme.
func hostPort(scheme, host, port string) string {
	if (scheme == schemeHTTPS && port == "443") || (scheme == schemeHTTP && port == "80") {
		port = ""
	}

	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	if port == "" {
		return host
	}

	return host + ":" + port
}

// URL schemes of the endpoints of a provider.
const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
)
