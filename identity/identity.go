// Package identity defines who is calling (authentication) and what they may
// do (authorization) as two separate ports.
//
// OpenDustpan always issues its own access tokens; single sign-on providers
// (OIDC, SAML, CAS, LDAP, local users) only authenticate humans so that a token
// can be minted. Requests from tools are therefore always token-based.
package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
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
	// ErrInvalidObject reports a malformed namespace or unit reference.
	ErrInvalidObject = errors.New("invalid object reference")
)

// minTokenLength is the shortest static token accepted, in bytes.
const minTokenLength = 32

// Principal is an authenticated caller.
type Principal struct {
	// Subject uniquely identifies the caller, for example a user name.
	Subject string
	// DisplayName is a human-readable name.
	DisplayName string
	// Admin grants every action on every object.
	Admin bool
}

// Action is something a principal wants to do.
type Action string

// Actions known to the core. Adapters may define their own.
const (
	ActionNamespaceRead  Action = "namespace:read"
	ActionNamespaceAdmin Action = "namespace:admin"
	ActionUnitRead       Action = "unit:read"
	ActionUnitWrite      Action = "unit:write"
	ActionUnitManage     Action = "unit:manage"
	ActionUnitEncrypt    Action = "unit:encrypt"
	ActionUnitDecrypt    Action = "unit:decrypt"
	// ActionCallerRead allows reading the caller's own record, for example
	// the current-user endpoint of a tool API. Its object is CallerObject.
	ActionCallerRead Action = "caller:read"
)

// Object limits for the tool and unit parts of a unit reference.
const (
	maxToolLength = 100
	maxUnitLength = 300
)

// Object is the thing an action applies to: a namespace, a unit reference,
// or the authenticated caller itself. A namespace reference is the
// namespace name. A unit reference is the tool, the namespace and the tool's
// own name for the unit, written as <tool>:<namespace>/<name>. The core keeps
// the tool-specific name opaque and never parses it. The caller reference
// names the principal in the request and has no namespace or unit. Its wire
// form starts with '@', which namespace names never contain, so ParseObject
// rejects it and it never equals a namespace. Use IsCaller to test for it;
// String alone also differs, but IsCaller states the intent.
type Object struct {
	raw      string
	ns       namespace.Name
	tool     string
	unit     string
	isUnit   bool
	isCaller bool
}

// NamespaceObject returns the reference to the namespace ns.
func NamespaceObject(ns namespace.Name) Object {
	return Object{raw: ns.String(), ns: ns, tool: "", unit: "", isUnit: false, isCaller: false}
}

// CallerObject returns the reference to the authenticated caller itself, for
// endpoints that read the caller's own record and act on no namespace or
// unit. Its wire form is "@caller", which ParseObject rejects: '@' is not a
// valid namespace character, so a namespace named "caller" never equals it.
// Every tool API needs it for its current-user endpoint, so it lives in the
// core.
func CallerObject() Object {
	return Object{raw: "@caller", ns: namespace.Name(""), tool: "", unit: "", isUnit: false, isCaller: true}
}

// UnitObject returns the reference to the unit called name in the namespace ns
// for the tool tool. The name is opaque to the core. Tool names match exactly:
// "pulumi" and "Pulumi" name different tools.
func UnitObject(tool string, ns namespace.Name, name string) (Object, error) {
	if !ident.Valid(tool, maxToolLength) {
		return Object{}, fmt.Errorf("%w: tool %q", ErrInvalidObject, tool)
	}

	if !validUnitName(name) {
		return Object{}, fmt.Errorf("%w: unit %q", ErrInvalidObject, name)
	}

	raw := tool + ":" + ns.String() + "/" + name

	return Object{raw: raw, ns: ns, tool: tool, unit: name, isUnit: true, isCaller: false}, nil
}

// ParseObject parses s as a namespace or unit reference. It rejects the
// caller reference form: use CallerObject for that.
func ParseObject(s string) (Object, error) {
	if !strings.Contains(s, ":") {
		ns, err := namespace.ParseName(s)
		if err != nil {
			return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
		}

		return Object{raw: s, ns: ns, tool: "", unit: "", isUnit: false, isCaller: false}, nil
	}

	return parseUnitObject(s)
}

// parseUnitObject parses s as a unit reference of the form
// <tool>:<namespace>/<name>.
func parseUnitObject(s string) (Object, error) {
	tool, rest, _ := strings.Cut(s, ":")

	if strings.Contains(rest, ":") {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	if !ident.Valid(tool, maxToolLength) {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	nsPart, unitPart, ok := strings.Cut(rest, "/")
	if !ok {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	ns, err := namespace.ParseName(nsPart)
	if err != nil {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	if !validUnitName(unitPart) {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	return Object{raw: s, ns: ns, tool: tool, unit: unitPart, isUnit: true, isCaller: false}, nil
}

// validUnitName reports whether name is a usable opaque unit name.
func validUnitName(name string) bool {
	if name == "" || len(name) > maxUnitLength {
		return false
	}

	if strings.Contains(name, ":") {
		return false
	}

	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}

	return !strings.Contains(name, "//")
}

// String returns the reference in its wire form.
func (o Object) String() string {
	return o.raw
}

// IsUnit reports whether the reference names a unit rather than a namespace.
func (o Object) IsUnit() bool {
	return o.isUnit
}

// IsCaller reports whether the reference names the authenticated caller
// itself rather than a namespace or a unit.
func (o Object) IsCaller() bool {
	return o.isCaller
}

// Namespace returns the namespace that owns the object.
func (o Object) Namespace() namespace.Name {
	return o.ns
}

// Tool returns the tool that owns the unit, or "" for a namespace.
func (o Object) Tool() string {
	return o.tool
}

// UnitName returns the tool's own opaque name for the unit, or "" for a namespace.
func (o Object) UnitName() string {
	return o.unit
}

// Authenticator turns an access token into a Principal.
type Authenticator interface {
	// AuthenticateToken returns the principal owning token, or ErrUnauthenticated.
	AuthenticateToken(ctx context.Context, token string) (Principal, error)
}

// Authorizer decides whether a principal may perform an action on an object.
type Authorizer interface {
	// Authorize returns nil if allowed and an error wrapping ErrDenied otherwise.
	Authorize(ctx context.Context, p Principal, action Action, obj Object) error
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
func (AdminOnly) Authorize(_ context.Context, p Principal, action Action, obj Object) error {
	if p.Admin {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", ErrDenied, p.Subject, action, obj.String())
}
