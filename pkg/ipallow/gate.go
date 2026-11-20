package ipallow

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Rules reads the lists of the namespaces. The Store port of this package
// satisfies it. NamespaceList reads the list of one namespace, for the check
// of one object. NamespacesWithList reads every list, for a scope that covers
// every object.
type Rules interface {
	NamespaceList(ctx context.Context, ns namespace.Name) (List, error)
	NamespacesWithList(ctx context.Context) ([]NamedList, error)
}

// Gate is an identity.Authorizer decorator that refuses requests from client
// addresses outside the allowlist of the namespace of the object. It runs in
// the authorization step of every route that calls Authorize, after
// authentication and before the handler. The routes with a capability token
// call CheckAddress instead, after they check the token.
type Gate struct {
	Next             identity.Authorizer
	Rules            Rules
	Enabled          bool
	IncludeBootstrap bool
}

var _ identity.Authorizer = Gate{Next: nil, Rules: nil, Enabled: false, IncludeBootstrap: false}

// Authorize asks Next first, then refuses the request when the namespace of
// obj holds a list that does not contain the client address in ctx.
func (g Gate) Authorize(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) error {
	if err := g.Next.Authorize(ctx, p, action, obj); err != nil {
		return err
	}

	if !g.Enabled {
		return nil
	}

	if g.bootstrapExempt(p) {
		return nil
	}

	return g.checkNamespace(ctx, obj.Namespace())
}

// ScopeExact reports that the scope is exact when the gate is off and the
// scope of Next is exact. An enabled gate says no: ListObjects and Authorize
// read the lists in separate reads, so a list change between the two reads
// can make Authorize refuse an object that the scope holds. A caller that
// needs an exact scope checks each object instead.
func (g Gate) ScopeExact() bool {
	return !g.Enabled && identity.HasExactScope(g.Next)
}

// ListObjects asks Next, then leaves out each object in a namespace whose
// list does not contain the client address in ctx, as Authorize refuses
// them. It filters the explicit sets of the scope object by object. A scope
// that covers every object gets each namespace whose list refuses the client
// address as a left-out namespace (identity.Scope.LeaveOut), so that a list
// of every namespace leaves it out too. A store error, a list of too many
// namespaces and an invalid stored name fail the whole list.
func (g Gate) ListObjects(
	ctx context.Context, p identity.Principal, action identity.Action,
) (identity.Scope, error) {
	none := identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}

	scope, err := g.Next.ListObjects(ctx, p, action)
	if err != nil {
		return none, fmt.Errorf("list objects: %w", err)
	}

	if !g.Enabled || g.bootstrapExempt(p) {
		return scope, nil
	}

	allowed := map[namespace.Name]bool{}
	sets := []*[]identity.Object{&scope.Namespaces, &scope.Collections, &scope.Units, &scope.Principals}

	for _, set := range sets {
		kept, keepErr := g.keepAllowed(ctx, *set, allowed)
		if keepErr != nil {
			return none, keepErr
		}

		*set = kept
	}

	if _, every := scope.EveryObjectExcept(); every {
		refused, refusedErr := g.refusedNamespaces(ctx)
		if refusedErr != nil {
			return none, refusedErr
		}

		scope = scope.LeaveOut(refused...)
	}

	return scope, nil
}

// CheckAddress refuses the request when the namespace ns holds a list that
// does not contain the client address in ctx. It is the address-only check
// for routes with no principal and no authorization object, such as the
// update-lease and upload-capability routes: there is no bootstrap exemption
// here because no principal exists on these routes. Callers check the
// capability token before they call it, so a caller without the token gets
// the same answer with and without a list. A store error fails closed, like
// in Authorize.
func (g Gate) CheckAddress(ctx context.Context, ns namespace.Name) error {
	if !g.Enabled {
		return nil
	}

	return g.checkNamespace(ctx, ns)
}

// refusedNamespaces returns each namespace whose list does not contain the
// client address in ctx, in the order of the store. An unknown address is
// outside every list. A namespace with the zero List has no rule, as in
// checkNamespace, so it is not refused. Any error fails closed: the caller
// does not get a scope that covers every object.
func (g Gate) refusedNamespaces(ctx context.Context) ([]namespace.Name, error) {
	lists, err := g.Rules.NamespacesWithList(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the namespaces with an IP allowlist: %w", err)
	}

	addr := clientip.FromContext(ctx)
	out := make([]namespace.Name, 0, len(lists))

	for i := range lists {
		// Defense in depth: both SQL adapters parse each stored name too; this
		// check stays so that a Rules port that does not parse cannot put an
		// empty or invalid name into the scope, where it would leave out
		// nothing.
		name, parseErr := namespace.ParseName(lists[i].Namespace.String())
		if parseErr != nil {
			return nil, fmt.Errorf("list the namespaces with an IP allowlist: %w", parseErr)
		}

		if lists[i].List.IsEmpty() {
			continue
		}

		if !lists[i].List.Contains(addr) {
			out = append(out, name)
		}
	}

	return out, nil
}

// checkNamespace refuses the request when ns holds a list that does not
// contain the client address in ctx. An empty namespace or a namespace
// without a rule allows the request.
func (g Gate) checkNamespace(ctx context.Context, ns namespace.Name) error {
	if ns == "" {
		return nil
	}

	list, err := g.rulesList(ctx, ns)
	if err != nil {
		return err
	}

	if list.IsEmpty() {
		return nil
	}

	return checkAddr(ctx, ns, list)
}

// bootstrapExempt reports whether p is the bootstrap admin and the gate
// exempts it: an administrator cannot lock themselves out.
func (g Gate) bootstrapExempt(p identity.Principal) bool {
	return p.Admin && principal.IsBootstrapSubject(p.Subject) && !g.IncludeBootstrap
}

// rulesList returns the list of ns. An unknown namespace reads as no rule:
// Next allowed an object of it, so the allowlist adds nothing.
func (g Gate) rulesList(ctx context.Context, ns namespace.Name) (List, error) {
	list, err := g.Rules.NamespaceList(ctx, ns)
	if err == nil {
		return list, nil
	}

	if errors.Is(err, namespace.ErrNotFound) {
		return List{prefixes: nil}, nil
	}

	return List{prefixes: nil}, fmt.Errorf("check IP allowlist of namespace %q: %w", ns, err)
}

// ErrAddressNotAllowed marks the refusal of a client address that is outside
// the allowlist of a namespace. The refusal also matches identity.ErrDenied, so
// a caller that does not know this error still answers 403. An adapter tests
// this error first to tell the allowlist from a missing permission.
var ErrAddressNotAllowed = errors.New("the client address is not allowed")

// addressError is the refusal that checkAddr returns. It keeps the text of an
// identity.ErrDenied wrap and matches both identity.ErrDenied and
// ErrAddressNotAllowed.
type addressError struct{ text string }

func (e addressError) Error() string { return e.text }

func (e addressError) Is(target error) bool {
	return target == identity.ErrDenied || target == ErrAddressNotAllowed
}

// checkAddr refuses the request when the client address in ctx is outside
// list. An unknown address is denied. The text names the scope and never the
// list or the address.
func checkAddr(ctx context.Context, ns namespace.Name, list List) error {
	addr := clientip.FromContext(ctx)
	if !list.Contains(addr) {
		return addressError{
			text: fmt.Errorf("%w: the client address is not allowed for namespace %s", identity.ErrDenied, ns).Error(),
		}
	}

	return nil
}

// keepAllowed returns the objects of objs whose namespace allows the client
// address in ctx. allowed holds the answer of each namespace already
// checked.
func (g Gate) keepAllowed(
	ctx context.Context, objs []identity.Object, allowed map[namespace.Name]bool,
) ([]identity.Object, error) {
	out := make([]identity.Object, 0, len(objs))

	for i := range objs {
		ns := objs[i].Namespace()

		ok, seen := allowed[ns]
		if !seen {
			err := g.checkNamespace(ctx, ns)
			if err != nil && !errors.Is(err, identity.ErrDenied) {
				return nil, err
			}

			ok = err == nil
			allowed[ns] = ok
		}

		if ok {
			out = append(out, objs[i])
		}
	}

	return out, nil
}
