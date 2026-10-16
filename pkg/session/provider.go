package session

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrIssuerChanged reports a provider ID of the console sign-in whose
// issuer is not the issuer that the store recorded for it.
var ErrIssuerChanged = errors.New("the issuer of a sign-in provider changed")

// ProviderStore keeps the issuer of each provider ID of the console
// sign-in. The subject of a person holds the provider ID and the sub claim,
// not the issuer, so one provider ID must keep one issuer.
type ProviderStore interface {
	// BindProvider records issuer for the provider ID id at the time at,
	// when id has no record, and returns the issuer of the record: issuer
	// itself after the first call, or the issuer of an earlier call. It
	// writes no audit entry.
	BindProvider(ctx context.Context, id, issuer string, at time.Time) (string, error)
}

// issuerChangedError is the refusal of a provider ID whose issuer changed.
// Its text is one sentence that names the provider ID, both issuers and
// the fix: in most cases the configuration has a mistake, and a new
// provider ID would give each person a new principal.
type issuerChangedError struct {
	id, recorded, configured string
}

func (e issuerChangedError) Error() string {
	return fmt.Sprintf("sign-in provider %q has the issuer %q in the database and the issuer %q in the "+
		"configuration, so set the issuer (--oidc-issuer) back to %q, or set a new provider ID only if the "+
		"identity provider changed", e.id, e.recorded, e.configured, e.recorded)
}

// Unwrap returns ErrIssuerChanged.
func (issuerChangedError) Unwrap() error { return ErrIssuerChanged }

// Provider is one provider of the console sign-in: its ID and what
// identifies its people. For an OpenID provider that is its Issuer. For a
// plain OAuth 2.0 provider, Issuer is empty, and its UserinfoURL and
// SubjectClaim identify its people instead.
type Provider struct {
	ID           string
	Issuer       string
	UserinfoURL  string
	SubjectClaim string
}

// binding returns the form in which a store records p: the issuer in the
// form of NormalizeIssuer, or for a plain OAuth 2.0 provider the user-info
// URL in the form of NormalizeEndpoint, then "#", then the subject claim.
func (p Provider) binding() string {
	if p.Issuer == "" {
		return NormalizeEndpoint(p.UserinfoURL) + "#" + p.SubjectClaim
	}

	return NormalizeIssuer(p.Issuer)
}

// matches reports whether the recorded binding is the binding of p. An
// issuer compares in the form of NormalizeIssuer. The binding of a plain
// OAuth 2.0 provider compares exactly, so a change of the path of its
// user-info URL is a change.
func (p Provider) matches(recorded string) bool {
	if p.Issuer == "" {
		return recorded == p.binding()
	}

	return NormalizeIssuer(recorded) == p.binding()
}

// BindProviders records the issuer of each provider of providers at the
// time at, and checks that no provider ID changed its issuer. It returns
// an error that matches ErrIssuerChanged, whose text is one sentence that
// names the provider ID, both issuers and the fix, for the first provider
// whose recorded issuer is another one.
//
// It records and compares the issuer in the form of NormalizeIssuer, so a
// change of the case of the scheme or the host, or a trailing slash, is
// the same issuer.
func BindProviders(ctx context.Context, store ProviderStore, providers []Provider, at time.Time) error {
	for _, p := range providers {
		recorded, err := store.BindProvider(ctx, p.ID, p.binding(), at)
		if err != nil {
			return fmt.Errorf("record the issuer of sign-in provider %q: %w", p.ID, err)
		}

		if p.matches(recorded) {
			continue
		}

		if p.Issuer == "" {
			return identityChangedError{id: p.ID, recorded: recorded, configured: p.binding()}
		}

		return issuerChangedError{id: p.ID, recorded: recorded, configured: p.Issuer}
	}

	return nil
}

// identityChangedError is the refusal of a plain OAuth 2.0 provider whose
// user-info URL or subject claim is not the one that the store recorded
// for its ID. Its text is one sentence that names the provider ID, both
// bindings and the fix.
type identityChangedError struct {
	id, recorded, configured string
}

func (e identityChangedError) Error() string {
	return fmt.Sprintf("sign-in provider %q identifies its people with %q in the database and with %q in the "+
		"request, so set the user-info URL and the subject claim back, or use a new provider ID only if the "+
		"identity provider changed", e.id, e.recorded, e.configured)
}

// Unwrap returns ErrIssuerChanged.
func (identityChangedError) Unwrap() error { return ErrIssuerChanged }

// NormalizeIssuer returns issuer with a lowercase scheme and host and with
// no trailing slash. An issuer that is not a URL keeps its text, with no
// trailing slash.
func NormalizeIssuer(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(issuer, "/")
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")

	return u.String()
}
