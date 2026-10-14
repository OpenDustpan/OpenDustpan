// Package identity defines who is calling (authentication) and what they may
// do (authorization) as two separate ports.
//
// OpenDustpan always issues its own access tokens; single sign-on providers
// (OIDC, SAML, CAS, LDAP, local users) only authenticate humans so that a token
// can be minted. Requests from tools are therefore always token-based.
package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	// TokenID names the stored access token that authenticated the caller
	// (ADR 53). It is empty for a bootstrap admin token (ADR 6), which has
	// no stored record.
	TokenID string
}

// Action is something a principal wants to do.
type Action string

// Actions known to the core. Adapters may define their own.
const (
	ActionNamespaceRead  Action = "namespace:read"
	ActionNamespaceAdmin Action = "namespace:admin"
	// ActionNamespaceReadAudit allows reading the audit entries of a
	// namespace. Its object is a namespace.
	ActionNamespaceReadAudit Action = "namespace:read-audit"
	ActionUnitRead           Action = "unit:read"
	ActionUnitWrite          Action = "unit:write"
	ActionUnitManage         Action = "unit:manage"
	ActionUnitEncrypt        Action = "unit:encrypt"
	ActionUnitDecrypt        Action = "unit:decrypt"
	// ActionUnitWriteForced allows a forced state write (state push -force)
	// and an unfenced force-unlock of a client-encrypted state. Its object
	// is a unit. The unit and namespace override switches must allow it too
	// (ADR 44, decisions 4 and 6).
	ActionUnitWriteForced Action = "unit:write-forced"
	// ActionUnitWriteUnlocked allows a write to an unlocked unit, as with
	// apply -lock=false. Its object is a unit. The override switches must
	// allow it too (ADR 44, decisions 5 and 6).
	ActionUnitWriteUnlocked Action = "unit:write-unlocked"
	// ActionNamespaceSetOverrides allows changing the override switches of a
	// namespace. Its object is a namespace.
	ActionNamespaceSetOverrides Action = "namespace:set-overrides"
	// ActionUnitSetOverrides allows changing the override switches of a unit.
	// Its object is a unit.
	ActionUnitSetOverrides Action = "unit:set-overrides"
	// ActionUnitReadSecrets allows reading secret values in plain text. Its
	// object is a unit.
	ActionUnitReadSecrets Action = "unit:read-secrets"
	// ActionCallerRead allows reading the caller's own record, for example
	// the current-user endpoint of a tool API. Its object is CallerObject.
	ActionCallerRead Action = "caller:read"
	// ActionServerBackup allows starting and reading installation backups.
	// Its object is ServerObject.
	ActionServerBackup Action = "server:backup"
	// ActionServerObserve allows reading installation metrics. Its object
	// is ServerObject.
	ActionServerObserve Action = "server:observe"
	// ActionServerMaintenance allows reading and changing the installation
	// state between normal operation and maintenance mode. Its object is
	// ServerObject.
	ActionServerMaintenance Action = "server:maintenance"
	// ActionServerRestore allows starting and reading installation restores.
	// Its object is ServerObject.
	ActionServerRestore Action = "server:restore"
	// ActionServerReadPrincipals allows reading the principals and the
	// groups of the installation, and the members of each group (ADR 52).
	// Its object is ServerObject.
	ActionServerReadPrincipals Action = "server:read-principals"
	// ActionServerManagePrincipals allows creating, changing and
	// deleting principals (ADR 52). Its object is ServerObject.
	ActionServerManagePrincipals Action = "server:manage-principals"
	// ActionServerManageGroups allows creating, changing and deleting groups,
	// and adding and removing their members (ADR 52). Its object is
	// ServerObject.
	ActionServerManageGroups Action = "server:manage-groups"
	// ActionServerReadTokens allows reading the access tokens of every
	// principal, never their secrets (ADR 53). Its object is ServerObject.
	ActionServerReadTokens Action = "server:read-tokens"
	// ActionServerManageTokens allows creating and rotating the personal
	// tokens of other persons, and revoking any access token (ADR 53,
	// decision 3). Its object is ServerObject. A create or a rotation also
	// runs the escalation check against the grants of the person.
	ActionServerManageTokens Action = "server:manage-tokens"
	// ActionServerManageServiceTokens allows creating and rotating service
	// tokens (ADR 53, decisions 3 and 7). Its object is ServerObject. A
	// create or a rotation also runs the escalation check against the
	// grants of the service principal.
	ActionServerManageServiceTokens Action = "server:manage-service-tokens"
	// ActionTokenManageOwn allows a person to list, create, rotate and
	// revoke its own personal tokens (ADR 53, decisions 3 and 10). Its
	// object is CallerObject. Every authenticated person holds it with no
	// grant.
	ActionTokenManageOwn Action = "token:manage-own"
)

// Object limits for the tool and unit parts of a unit reference.
const (
	maxToolLength = 100
	maxUnitLength = 300
)

// Object is the thing an action applies to: a namespace, a unit reference,
// the authenticated caller itself, or the installation itself. A namespace
// reference is the namespace name. A unit reference is the tool, the
// namespace and the tool's own name for the unit, written as
// <tool>:<namespace>/<name>. The core keeps the tool-specific name opaque
// and never parses it. The caller reference names the principal in the
// request and has no namespace or unit. The server reference names the
// installation and has no namespace or unit. Their wire forms start with
// '@', which namespace names never contain, so ParseObject rejects them and
// they never equal a namespace. Use IsCaller and IsServer to test for them;
// String alone also differs, but the methods state the intent.
type Object struct {
	raw      string
	ns       namespace.Name
	tool     string
	unit     string
	isUnit   bool
	isCaller bool
	isServer bool
}

// NamespaceObject returns the reference to the namespace ns.
func NamespaceObject(ns namespace.Name) Object {
	return Object{
		raw: ns.String(), ns: ns, tool: "", unit: "", isUnit: false, isCaller: false, isServer: false,
	}
}

// CallerObject returns the reference to the authenticated caller itself, for
// endpoints that read the caller's own record and act on no namespace or
// unit. Its wire form is "@caller", which ParseObject rejects: '@' is not a
// valid namespace character, so a namespace named "caller" never equals it.
// Every tool API needs it for its current-user endpoint, so it lives in the
// core.
func CallerObject() Object {
	return Object{
		raw: "@caller", ns: namespace.Name(""), tool: "", unit: "",
		isUnit: false, isCaller: true, isServer: false,
	}
}

// ServerObject returns the reference to the installation itself, for
// endpoints that act on no namespace or unit. Its wire form is "@server",
// which ParseObject rejects: '@' is not a valid namespace character, so a
// namespace named "server" never equals it. The installation API needs it,
// so it lives in the core.
func ServerObject() Object {
	return Object{
		raw: "@server", ns: namespace.Name(""), tool: "", unit: "",
		isUnit: false, isCaller: false, isServer: true,
	}
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

	return Object{
		raw: raw, ns: ns, tool: tool, unit: name, isUnit: true, isCaller: false, isServer: false,
	}, nil
}

// ParseObject parses s as a namespace or unit reference. It rejects the
// caller and server reference forms: use CallerObject and ServerObject for
// those.
func ParseObject(s string) (Object, error) {
	if !strings.Contains(s, ":") {
		ns, err := namespace.ParseName(s)
		if err != nil {
			return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
		}

		return Object{raw: s, ns: ns, tool: "", unit: "", isUnit: false, isCaller: false, isServer: false}, nil
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

	return Object{raw: s, ns: ns, tool: tool, unit: unitPart, isUnit: true, isCaller: false, isServer: false}, nil
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
// itself rather than a namespace, a unit or the installation.
func (o Object) IsCaller() bool {
	return o.isCaller
}

// IsServer reports whether the reference names the installation itself
// rather than a namespace, a unit or the caller.
func (o Object) IsServer() bool {
	return o.isServer
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
		principal: Principal{Subject: subject, DisplayName: subject, Admin: true, TokenID: ""},
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

// StaticTokens authenticates a fixed set of bootstrap tokens, one
// administrator per token. It exists to bootstrap a fresh install; replace
// it once real identity is configured. It stores only the SHA-256 of each
// token, so the comparison never handles raw tokens of different lengths.
type StaticTokens struct {
	principals []Principal
	hashes     [][32]byte
}

var _ Authenticator = (*StaticTokens)(nil)

// NewStaticTokens returns an authenticator accepting each of tokens as a
// distinct administrator. The first token signs in as admin, the next ones
// as admin-2, admin-3, and so on. Each token must hold at least 32 bytes.
// Empty and duplicate entries fail with ErrWeakToken, as does an empty
// list. White space around an entry is ignored.
func NewStaticTokens(tokens ...string) (*StaticTokens, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: need at least one token", ErrWeakToken)
	}

	principals := make([]Principal, 0, len(tokens))
	hashes := make([][32]byte, 0, len(tokens))
	seen := make(map[[32]byte]struct{}, len(tokens))

	for i, raw := range tokens {
		token := strings.TrimSpace(raw)
		if len(token) < minTokenLength {
			return nil, fmt.Errorf("%w: token %d needs at least %d bytes", ErrWeakToken, i+1, minTokenLength)
		}

		sum := sha256.Sum256([]byte(token))
		if _, dup := seen[sum]; dup {
			return nil, fmt.Errorf("%w: token %d is a duplicate", ErrWeakToken, i+1)
		}

		seen[sum] = struct{}{}

		subject := "admin"
		if i > 0 {
			subject = fmt.Sprintf("admin-%d", i+1)
		}

		principals = append(principals, Principal{Subject: subject, DisplayName: subject, Admin: true, TokenID: ""})
		hashes = append(hashes, sum)
	}

	return &StaticTokens{principals: principals, hashes: hashes}, nil
}

// AuthenticateToken compares the SHA-256 of token against every stored
// token in constant time, without an early exit.
func (s *StaticTokens) AuthenticateToken(_ context.Context, token string) (Principal, error) {
	sum := sha256.Sum256([]byte(token))

	found := 0

	var principal Principal

	for i := range s.hashes {
		match := subtle.ConstantTimeCompare(sum[:], s.hashes[i][:])
		found |= match

		if match == 1 {
			principal = s.principals[i]
		}
	}

	if found != 1 {
		return Principal{}, ErrUnauthenticated
	}

	return principal, nil
}

// Chain authenticates a token with each of its authenticators in order and
// answers the first principal. It lets the bootstrap admin tokens (ADR 6)
// keep working next to the stored access tokens (ADR 53).
type Chain []Authenticator

var _ Authenticator = Chain(nil)

// AuthenticateToken returns the principal of the first authenticator that
// accepts token. An authenticator that answers ErrUnauthenticated passes the
// token to the next one. Any other error stops the chain and is returned, so
// a failed store read fails closed. With no authenticator left, it answers
// the last ErrUnauthenticated.
func (c Chain) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	last := ErrUnauthenticated

	for _, a := range c {
		p, err := a.AuthenticateToken(ctx, token)
		if err == nil {
			return p, nil
		}

		if !errors.Is(err, ErrUnauthenticated) {
			return Principal{}, err
		}

		last = err
	}

	return Principal{}, last
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
