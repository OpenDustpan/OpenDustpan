package dustpan

import (
	"context"
	"errors"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// AuthorizeWired runs newAuthPorts on the default settings, with authz as
// the configured authorizer and no token store, and then checks the action
// with the authorizer that the server uses.
func AuthorizeWired(
	authz identity.Authorizer, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	cfg := defaultSettings()
	cfg.authorizer = authz

	ports, err := newAuthPorts(cfg, nil)
	if err != nil {
		return err
	}

	return ports.authorizer.Authorize(context.Background(), p, action, obj)
}

// AuthorizeWiredAllowlisted is AuthorizeWired with the IP allowlist feature on.
// The namespace acme holds the list 198.51.100.0/24; every other namespace has
// no rule. The client address comes from ctx.
func AuthorizeWiredAllowlisted(
	ctx context.Context, authz identity.Authorizer, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	list, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		return err
	}

	cfg := defaultSettings()
	cfg.authorizer = authz
	cfg.ipAllowlists = &ipAllowlistSettings{
		store:            fixedAllowlists{namespace.Name("acme"): list},
		enabled:          true,
		includeBootstrap: false,
	}

	ports, err := newAuthPorts(cfg, nil)
	if err != nil {
		return err
	}

	return ports.authorizer.Authorize(ctx, p, action, obj)
}

// fixedAllowlists is an ipallow.Store that holds one list per namespace and
// never writes.
type fixedAllowlists map[namespace.Name]ipallow.List

// NamespaceList returns the list of ns, or no rule for another namespace.
func (f fixedAllowlists) NamespaceList(_ context.Context, ns namespace.Name) (ipallow.List, error) {
	return f[ns], nil
}

// errFixedReadOnly is returned by the write of fixedAllowlists.
var errFixedReadOnly = errors.New("fixed allowlists are read only")

// SetNamespaceList refuses every write: the fixture is read only.
func (fixedAllowlists) SetNamespaceList(
	context.Context, namespace.Name, ipallow.List, namespace.Fence, audit.Entry,
) error {
	return errFixedReadOnly
}

// NamespacesWithList lists each namespace of the fixture whose list is not
// the zero List, in name order.
func (f fixedAllowlists) NamespacesWithList(context.Context) ([]ipallow.NamedList, error) {
	out := make([]ipallow.NamedList, 0, len(f))
	for ns, list := range f {
		if !list.IsEmpty() {
			out = append(out, ipallow.NamedList{Namespace: ns, List: list})
		}
	}

	slices.SortFunc(
		out,
		func(a, b ipallow.NamedList) int { return strings.Compare(a.Namespace.String(), b.Namespace.String()) },
	)

	return out, nil
}
