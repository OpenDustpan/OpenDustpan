// Package identity defines who is calling (authentication) and what they may
// do (authorization) as two separate ports.
//
// OpenDustpan always issues its own access tokens; single sign-on providers
// (OIDC, SAML, CAS, LDAP, local users) only authenticate humans so that a token
// can be minted. Requests from the Pulumi CLI are therefore always token-based.
package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/namespace"
)

// Sentinel errors returned by Authenticator and Authorizer implementations.
var (
	// ErrUnauthenticated reports a missing, malformed or unknown credential.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrDenied reports an authenticated principal without permission.
	ErrDenied = errors.New("permission denied")
	// ErrWeakToken reports a configured token that is too short to be safe.
	ErrWeakToken = errors.New("token too short")
)

// minTokenLength is the shortest static token accepted, in bytes.
const minTokenLength = 32

// Principal is an authenticated caller.
type Principal struct {
	// Subject uniquely identifies the caller, for example a user name.
	Subject string
	// DisplayName is a human-readable name.
	DisplayName string
	// Admin grants every action in every namespace.
	Admin bool
}

// Action is something a principal wants to do.
type Action string

// Actions known to the core. Adapters may define their own.
const (
	ActionNamespaceRead  Action = "namespace:read"
	ActionNamespaceAdmin Action = "namespace:admin"
	ActionStackRead      Action = "stack:read"
	ActionStackWrite     Action = "stack:write"
)

// Authenticator turns an access token into a Principal.
type Authenticator interface {
	// AuthenticateToken returns the principal owning token, or ErrUnauthenticated.
	AuthenticateToken(ctx context.Context, token string) (Principal, error)
}

// Authorizer decides whether a principal may perform an action in a namespace.
type Authorizer interface {
	// Authorize returns nil if allowed and an error wrapping ErrDenied otherwise.
	Authorize(ctx context.Context, p Principal, action Action, ns namespace.Name) error
}

// StaticToken authenticates exactly one token as one administrator. It exists
// to bootstrap a fresh install; replace it once real identity is configured.
type StaticToken struct {
	principal Principal
	token     []byte
}

var _ Authenticator = (*StaticToken)(nil)

// NewStaticToken returns an authenticator accepting token for an admin named subject.
func NewStaticToken(subject, token string) (*StaticToken, error) {
	if len(token) < minTokenLength {
		return nil, fmt.Errorf("%w: need at least %d bytes", ErrWeakToken, minTokenLength)
	}

	return &StaticToken{
		principal: Principal{Subject: subject, DisplayName: subject, Admin: true},
		token:     []byte(token),
	}, nil
}

// AuthenticateToken compares token in constant time.
func (s *StaticToken) AuthenticateToken(_ context.Context, token string) (Principal, error) {
	if subtle.ConstantTimeCompare([]byte(token), s.token) != 1 {
		return Principal{}, ErrUnauthenticated
	}

	return s.principal, nil
}

// AdminOnly allows administrators and denies everyone else. It is the safe
// default until a real authorizer (roles, teams) is configured.
type AdminOnly struct{}

var _ Authorizer = AdminOnly{}

// Authorize allows the action only for administrators.
func (AdminOnly) Authorize(_ context.Context, p Principal, action Action, ns namespace.Name) error {
	if p.Admin {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s in %q", ErrDenied, p.Subject, action, ns)
}
