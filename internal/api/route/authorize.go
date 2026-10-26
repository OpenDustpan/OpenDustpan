package route

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// Checks are the checks that AuthorizeObject runs on one object of a route.
type Checks struct {
	// Namespace is the namespace object of the object that is checked. The
	// read of the namespace comes first.
	Namespace identity.Object
	// Reads are the read actions on the object. A refused read answers
	// NotFound.
	Reads []identity.Action
	// Actions are the other actions on the object. A refused action answers
	// its refusal.
	Actions []identity.Action
	// NotFound is the answer of a refused read, so that a caller without read
	// gets the answer of an unknown object. Each API passes its own not-found
	// error, which its error writer answers with its own code.
	NotFound error
}

// AuthorizeObject checks the declared actions of a route for p on obj, at the
// point of use. It runs three groups of checks, in this order, and returns the
// first refusal:
//
//  1. read on c.Namespace;
//  2. each action of c.Reads on obj;
//  3. each action of c.Actions on obj.
//
// A refused read answers c.NotFound. A refused read on an address that the IP
// allowlist of the namespace refuses answers that refusal, which is 403, as on
// the other routes of the namespace. A refused action answers the refusal of
// the authorizer, wrapped with its action name.
//
// The helper does not change ctx or r. Each caller passes the context that it
// has at this point, so the checks see the same values as before the helper.
//
// Defense in depth: the caller checks the object before it calls the helper,
// and the route test of each API checks that each route declares its actions.
// The helper runs the checks anyway, for a caller that skips either one.
func (g Guard) AuthorizeObject(
	ctx context.Context,
	r *http.Request,
	p identity.Principal,
	obj identity.Object,
	c Checks,
) error {
	nsRead := identity.ActionNamespaceRead

	err := readRefusal(nsRead, g.Refusals.Authorize(ctx, r, p, nsRead, c.Namespace), c.NotFound)
	if err != nil {
		return err
	}

	for _, action := range c.Reads {
		err = readRefusal(action, g.Refusals.Authorize(ctx, r, p, action, obj), c.NotFound)
		if err != nil {
			return err
		}
	}

	for _, action := range c.Actions {
		if err = g.Refusals.Authorize(ctx, r, p, action, obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// readRefusal maps the result of a read check. A refusal is notFound, except
// the refusal of an address that the IP allowlist refuses: that one keeps its
// own 403 code, and it names no object, so it does not tell whether the object
// exists. Any other error is wrapped with its action name.
func readRefusal(action identity.Action, err, notFound error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ipallow.ErrAddressNotAllowed):
		return fmt.Errorf("authorize %s: %w", action, err)
	case errors.Is(err, identity.ErrDenied):
		return notFound
	default:
		return fmt.Errorf("authorize %s: %w", action, err)
	}
}
