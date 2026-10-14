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

// Rules reads the list of a namespace. The Store port of this package satisfies it.
type Rules interface {
	NamespaceList(ctx context.Context, ns namespace.Name) (List, error)
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

// checkAddr refuses the request when the client address in ctx is outside
// list. An unknown address is denied. The text names the scope and never the
// list or the address.
func checkAddr(ctx context.Context, ns namespace.Name, list List) error {
	addr := clientip.FromContext(ctx)
	if !list.Contains(addr) {
		return fmt.Errorf("%w: the client address is not allowed for namespace %s", identity.ErrDenied, ns)
	}

	return nil
}
