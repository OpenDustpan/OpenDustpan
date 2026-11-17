package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// HeaderDiscoveryGrant is the response header that names the stable ID of the
// grant that allowed a discovery answer, never the name of the grant. The
// header is tool-neutral: the Pulumi CLI and OpenTofu ignore it. A caller
// that no grant allowed, such as a site administrator, gets no header.
const HeaderDiscoveryGrant = "X-Dustpan-Grant-Id"

// DiscoveryGrantID returns the stable ID of one grant that lets p discover
// ns through authz, and whether authz can name its grants at all. It reports
// false when authz does not implement the check, so the caller falls back to
// the scope check with no grant header. It returns the error of the check
// with found true, never an empty ID with found true and a nil error.
func DiscoveryGrantID(
	ctx context.Context, authz identity.Authorizer, p identity.Principal, ns namespace.Name,
) (string, bool, error) {
	namer, ok := authz.(interface {
		DiscoveryGrantID(
			ctx context.Context, p identity.Principal, ns namespace.Name,
		) (string, error)
	})
	if !ok {
		return "", false, nil
	}

	id, err := namer.DiscoveryGrantID(ctx, p, ns)
	if err != nil {
		return "", true, err
	}

	return id, true, nil
}

// DiscoveryGrantID returns the stable ID of one grant (never a name of the
// grant) that lets p discover the namespace ns: a grant on a collection in it
// or on a unit in it, with a role that holds unit:read or unit:read-outputs. It returns the empty string when no such
// grant allows the read: the caller may still see the namespace through an
// action on it, which the Authorizer decides before this check runs. It
// returns the error of the read when the grants cannot be read, never an
// empty ID with a nil error. It reads the grants at each request.
func (a *Authorizer) DiscoveryGrantID(ctx context.Context, p identity.Principal, ns namespace.Name) (string, error) {
	if p.Admin {
		// The Authorizer allows an administrator before any grant runs.
		return "", nil
	}

	// The namespace service principal holds no stored grant.
	if identity.IsNamespaceServiceSubject(p.Subject) {
		return "", nil
	}

	subjects, err := a.subjectsOf(ctx, p)
	if err != nil || len(subjects) == 0 {
		return "", err
	}

	for _, action := range []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadOutputs} {
		id, found, findErr := a.findDiscoveryGrant(ctx, subjects, action, ns)
		if findErr != nil {
			return "", findErr
		}

		if found {
			return id, nil
		}
	}

	return "", nil
}

// findDiscoveryGrant returns the stable ID of one grant that allows the
// discovery read of ns through action, and whether it found one.
func (a *Authorizer) findDiscoveryGrant(
	ctx context.Context, subjects []Subject, action identity.Action, ns namespace.Name,
) (string, bool, error) {
	grants, err := a.reachGrants(ctx, subjects, action)
	if err != nil {
		return "", false, err
	}

	for i := range grants {
		if grantInNamespace(grants[i].Object, ns) {
			return grants[i].ID, true, nil
		}
	}

	return "", false, nil
}

// grantInNamespace reports whether obj is a collection in the namespace ns or
// a unit in it. A grant on the namespace itself is decided by the Authorizer
// before discovery runs. A grant on the installation or on a principal does
// not open discovery: an every-object scope proves no grant in ns.
func grantInNamespace(obj identity.Object, ns namespace.Name) bool {
	return (obj.IsCollection() || obj.IsUnit()) && obj.Namespace() == ns
}
