package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// CredentialServer is the credential of a principal that the server builds
// itself, in its own process, for work that no person starts: the namespace
// service principal. No client can present it. The authenticators never give
// it, and no token or session maps to it.
const CredentialServer Credential = "server"

// NamespaceServicePrefix starts the subject of every namespace service
// principal. The principal API refuses a subject with this prefix, so only
// NamespaceService makes one.
const NamespaceServicePrefix = "system:namespace-service:"

// NamespaceServiceSubject returns the subject of the namespace service
// principal of ns. It does not check ns.
func NamespaceServiceSubject(ns namespace.Name) string {
	return NamespaceServicePrefix + ns.String()
}

// IsNamespaceServiceSubject reports whether subject has the form of the
// subject of a namespace service principal. The check uses the prefix only,
// ignoring case, so that no spelling of the prefix gets past a caller that
// refuses these subjects.
func IsNamespaceServiceSubject(subject string) bool {
	return len(subject) >= len(NamespaceServicePrefix) &&
		strings.EqualFold(subject[:len(NamespaceServicePrefix)], NamespaceServicePrefix)
}

// NamespaceServiceOf returns the namespace that subject names, and true when
// subject is exactly the subject of the namespace service principal of a
// valid namespace.
func NamespaceServiceOf(subject string) (namespace.Name, bool) {
	rest, ok := strings.CutPrefix(subject, NamespaceServicePrefix)
	if !ok {
		return "", false
	}

	ns, err := namespace.ParseName(rest)
	if err != nil {
		return "", false
	}

	return ns, true
}

// NamespaceService returns the namespace service principal of ns: the
// principal that the server uses for a start that no person makes, for example
// a start by a run trigger. It holds one action, ActionUnitRunPlan, on the
// units of ns, and no other grant. The grant is a rule of the Authorizer, not
// a stored row, so no API can widen it. It returns an error that wraps
// ErrInvalidObject when ns is not a valid namespace name.
func NamespaceService(ns namespace.Name) (Principal, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return Principal{}, fmt.Errorf("%w: namespace service principal: %w", ErrInvalidObject, err)
	}

	return Principal{
		Subject:                NamespaceServiceSubject(ns),
		DisplayName:            "Namespace service principal of " + ns.String(),
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Kind:                   KindService,
		Credential:             CredentialServer,
		JobID:                  "",
	}, nil
}

// IsNamespaceService reports whether p is a namespace service principal that
// the server built: the form of the subject and the server credential both
// hold. A principal with the subject and any other credential is not one,
// and the Authorizer denies it every action.
func (p Principal) IsNamespaceService() bool {
	_, ok := NamespaceServiceOf(p.Subject)

	return ok && p.Credential == CredentialServer && p.Kind == KindService
}

// NamespaceServiceRule decides every check of a principal whose subject has
// the form of the subject of a namespace service principal, and asks Next for
// every other principal. Such a principal holds one action,
// ActionUnitRunPlan, on the units of its own namespace, and only when
// IsNamespaceService holds. Every other check of such a subject is denied,
// also with the Admin flag set, and Next never sees it.
//
// Defense in depth: the built-in authorizer of grants applies the same rule,
// and the principal API refuses the subject. This decorator stays so that an
// Authorizer of an embedder that does not know the rule, for example one that
// allows by the Admin flag, never gives such a principal more.
type NamespaceServiceRule struct {
	Next Authorizer
}

var _ Authorizer = NamespaceServiceRule{Next: nil}

// Authorize implements Authorizer.
func (r NamespaceServiceRule) Authorize(ctx context.Context, p Principal, action Action, obj Object) error {
	if !IsNamespaceServiceSubject(p.Subject) {
		return r.Next.Authorize(ctx, p, action, obj)
	}

	ns, valid := NamespaceServiceOf(p.Subject)

	allowed := valid && p.IsNamespaceService() &&
		action == ActionUnitRunPlan &&
		obj.IsUnit() && obj.Namespace() == ns

	if !allowed {
		return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
	}

	return nil
}

// ListObjects gives a principal with the subject of a namespace service
// principal no object: its one action is a decision on one object, not a list
// of grants. It asks Next for every other principal.
func (r NamespaceServiceRule) ListObjects(ctx context.Context, p Principal, action Action) (Scope, error) {
	if IsNamespaceServiceSubject(p.Subject) {
		return Scope{
			Namespaces:  nil,
			Collections: nil,
			Units:       nil,
			Principals:  nil,
			every:       everyObject{set: false, except: nil},
		}, nil
	}

	scope, err := r.Next.ListObjects(ctx, p, action)
	if err != nil {
		return Scope{
			Namespaces:  nil,
			Collections: nil,
			Units:       nil,
			Principals:  nil,
			every:       everyObject{set: false, except: nil},
		}, fmt.Errorf(
			"list objects: %w",
			err,
		)
	}

	return scope, nil
}

// ScopeExact reports that the scope is exact when the scope of Next is exact.
func (r NamespaceServiceRule) ScopeExact() bool {
	return HasExactScope(r.Next)
}
