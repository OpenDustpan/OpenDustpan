package trigger

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// OutputRead describes one read of the outputs of a source unit by a job that
// a trigger started. The handler of the read builds it from the job, and asks
// OutputReadCheck.Allow at each read.
type OutputRead struct {
	// Namespace is the namespace of the job and of the trigger.
	Namespace namespace.Name
	// Trigger is the ID of the trigger that the job records.
	Trigger ID
	// Unit is the unit of the job. It must be the unit of the trigger.
	Unit graph.Unit
	// Source is the unit whose outputs the job reads.
	Source identity.Object
	// Secret is true when the read returns a secret output.
	Secret bool
	// Caller is the person who fired the trigger, for the job of a manual
	// fire, and nil for any other start. The read then needs the grants of
	// the caller as well as those of the last saver: the job gets the
	// intersection of both, so a caller without access to the source gains
	// none through the trigger.
	Caller *identity.Principal
}

// Causes of a refused read. The error text of a refusal repeats one of them.
var (
	errSourceNotUnit = errors.New("the source is not a unit")
	errOtherUnit     = errors.New("the trigger does not start the unit of the job")
)

// OutputReadCheck decides check 2 of the read of an output for a job that a
// trigger started. The namespace service principal that started the job
// holds no read action, so the grants of the last saver of the trigger decide,
// as they are now. The decision fails closed: a trigger that is gone, a
// saver that is missing, deleted or disabled, and any error of a port deny.
type OutputReadCheck struct {
	store      Store
	resolver   SaverResolver
	authorizer identity.Authorizer
}

// NewOutputReadCheck returns an OutputReadCheck over the ports. It returns an
// error that wraps ErrInvalid when a port is nil.
func NewOutputReadCheck(store Store, resolver SaverResolver, authorizer identity.Authorizer,
) (*OutputReadCheck, error) {
	if store == nil || resolver == nil || authorizer == nil {
		return nil, fmt.Errorf("%w: an output read check needs a store, a resolver and an authorizer", ErrInvalid)
	}

	return &OutputReadCheck{store: store, resolver: resolver, authorizer: authorizer}, nil
}

// Allow returns nil when the read is allowed and an error that wraps
// identity.ErrDenied otherwise. It does not decide check 1 (the sharing rule
// of the source), which the caller applies as well.
func (c *OutputReadCheck) Allow(ctx context.Context, in OutputRead) error {
	if err := in.Trigger.Validate(); err != nil {
		return denyRead(in, err)
	}

	if !in.Source.IsUnit() {
		return denyRead(in, errSourceNotUnit)
	}

	t, err := c.store.Get(ctx, in.Namespace, in.Trigger)
	if err != nil {
		return denyRead(in, err)
	}

	// Defense in depth: the job records the trigger ID, and the ID alone must
	// not let a job borrow the grants of another trigger. The trigger must
	// start the unit of the job, in the namespace of the job.
	if t.Unit != in.Unit || t.Unit.Namespace != in.Namespace || in.Source.Namespace() != in.Namespace {
		return denyRead(in, errOtherUnit)
	}

	rec, err := c.resolver.Saver(ctx, t.LastSaver)
	if err != nil {
		return denyRead(in, err)
	}

	saver := rec.Principal

	// Defense in depth: the resolver answers a principal with the Admin flag
	// off. The flag is never rebuilt from a stored subject, so this line
	// stays against a resolver that sets it.
	saver.Admin = false

	if saver.Subject != t.LastSaver {
		return denyRead(in, errOtherSubject)
	}

	// Defense in depth: a subject that a new principal reuses does not carry
	// the reads of the principal that saved the trigger. The ID of the record
	// must match, whatever the resolver and the subject check say.
	if rec.ID != t.LastSaverID {
		return denyRead(in, errOtherPrincipal)
	}

	if err := c.authorize(ctx, saver, in); err != nil {
		return err
	}

	if in.Caller == nil {
		return nil
	}

	return c.authorize(ctx, *in.Caller, in)
}

// authorize checks the read actions of in for p.
func (c *OutputReadCheck) authorize(ctx context.Context, p identity.Principal, in OutputRead) error {
	actions := []identity.Action{identity.ActionUnitRead}
	if in.Secret {
		actions = append(actions, identity.ActionUnitReadSecrets)
	}

	for _, action := range actions {
		if err := c.authorizer.Authorize(ctx, p, action, in.Source); err != nil {
			return denyRead(in, err)
		}
	}

	return nil
}

// denyRead returns the error of a refused read. It wraps identity.ErrDenied
// and the cause.
func denyRead(in OutputRead, cause error) error {
	return fmt.Errorf("%w: trigger %s: the read of the outputs of %s is refused: %w",
		identity.ErrDenied, quote(string(in.Trigger)), in.Source.String(), cause)
}
