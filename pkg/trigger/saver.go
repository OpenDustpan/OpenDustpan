package trigger

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Errors of the checks on the last saver of a trigger.
var (
	// ErrSaverDenied reports a trigger whose last saver no longer holds the
	// actions that a start needs, or whose principal is missing, deleted or
	// disabled. The start is refused (fail closed). The error wraps
	// identity.ErrDenied. The trigger needs a new save.
	ErrSaverDenied = fmt.Errorf("the last saver of the trigger no longer has access: %w", identity.ErrDenied)
	// ErrSaverGone reports a subject that names no current principal: the
	// principal does not exist, was deleted, or is disabled. A SaverResolver
	// returns it, and the checks turn it into a refusal.
	ErrSaverGone = errors.New("the saver is missing, deleted or disabled")
	// ErrUnitGone reports a unit that no longer exists, or never existed, in
	// the namespace and the tool of the trigger. A Units port returns it.
	ErrUnitGone = errors.New("unit not found")
)

// SettingsAction is the action that saving, replacing and deleting a trigger
// needs on the unit of the trigger. It is the action that changes the settings
// of the unit.
const SettingsAction = identity.ActionUnitManage

// StartAction is the action that a start of a plan needs on the unit.
const StartAction = identity.ActionUnitRunPlan

// Units finds units for the checks of this package. The server backs it with
// the inventory.
type Units interface {
	// Object returns the current reference of the live unit u, with its unit ID
	// set. It returns ErrUnitGone for a unit that does not exist or is deleted,
	// and for a unit whose namespace or tool differs from those of u.
	Object(ctx context.Context, u graph.Unit) (identity.Object, error)
	// Find returns the live unit of the namespace ns with the stable ID
	// unitID. It returns ErrUnitGone for a unit that does not exist, that is
	// deleted, or that belongs to another namespace.
	Find(ctx context.Context, ns namespace.Name, unitID string) (graph.Unit, error)
}

// SaverRecord is the answer of a SaverResolver: the current principal and the
// stable ID of its record. The ID tells a principal from a later principal
// that reuses the subject.
type SaverRecord struct {
	Principal identity.Principal
	ID        string
}

// SaverResolver turns the subject of a saver into the current principal. The
// server backs it with the lookup by subject of the principal record store.
type SaverResolver interface {
	// Saver returns the current principal for subject, with the ID of its
	// record, as the records hold them now. It returns ErrSaverGone when no
	// principal with the subject exists, when it was deleted, and when it is
	// disabled. The Admin flag of the principal is always false: the server
	// never rebuilds the flag of an administrator from a stored subject, so
	// the grants alone decide.
	Saver(ctx context.Context, subject string) (SaverRecord, error)
}

// SaverCheck checks the last saver of a trigger against the actions that the
// trigger needs. A check reads the current records and grants each time.
type SaverCheck interface {
	// SaverID returns the ID of the principal record of the caller with the
	// subject subject. A manager stores it with the trigger as LastSaverID. It
	// returns an error that wraps ErrSaverDenied when the principal is
	// missing, deleted or disabled, for example a bootstrap token with no
	// record.
	SaverID(ctx context.Context, subject string) (string, error)
	// CheckSave checks that the saver of t holds the settings action and the
	// start action on t.Unit, and, for a follows trigger, unit:read on
	// t.Source. It returns an error that wraps ErrSaverDenied (and so
	// identity.ErrDenied) when a check fails, and any other error for a failure
	// of a port, which also denies.
	CheckSave(ctx context.Context, t Trigger) error
	// CheckStart checks that the saver of t still holds the settings action
	// and the start action on t.Unit. It returns the errors of CheckSave.
	CheckStart(ctx context.Context, t Trigger) error
}

// SaverCheckConfig holds the ports of a SaverCheck.
type SaverCheckConfig struct {
	Resolver   SaverResolver
	Authorizer identity.Authorizer
	Units      Units
}

type saverCheck struct {
	resolver   SaverResolver
	authorizer identity.Authorizer
	units      Units
}

// NewSaverCheck returns the SaverCheck over the ports of cfg. It returns an
// error that wraps ErrInvalid when a port is nil.
//
//nolint:ireturn // The port is the contract of the result.
func NewSaverCheck(cfg SaverCheckConfig) (SaverCheck, error) {
	if cfg.Resolver == nil || cfg.Authorizer == nil || cfg.Units == nil {
		return nil, fmt.Errorf("%w: a saver check needs a resolver, an authorizer and units", ErrInvalid)
	}

	return &saverCheck{resolver: cfg.Resolver, authorizer: cfg.Authorizer, units: cfg.Units}, nil
}

// errOtherSubject reports a resolver that answered a principal with another
// subject than the one it was asked for.
var errOtherSubject = errors.New("the resolver answered another subject")

// errOtherPrincipal reports a saver whose subject names a principal other than
// the one that saved the trigger: the principal was deleted, and a new
// principal took the subject.
var errOtherPrincipal = errors.New("the subject of the saver names another principal")

// SaverID implements SaverCheck.
func (c *saverCheck) SaverID(ctx context.Context, subject string) (string, error) {
	rec, err := c.resolver.Saver(ctx, subject)
	if err != nil {
		return "", fmt.Errorf("%w: saver %s: %w", ErrSaverDenied, quote(subject), err)
	}

	if rec.Principal.Subject != subject {
		return "", fmt.Errorf("%w: saver %s: %w", ErrSaverDenied, quote(subject), errOtherSubject)
	}

	if err := validateSaverID(rec.ID); err != nil {
		return "", fmt.Errorf("%w: saver %s: %w", ErrSaverDenied, quote(subject), err)
	}

	return rec.ID, nil
}

// CheckSave implements SaverCheck.
func (c *saverCheck) CheckSave(ctx context.Context, t Trigger) error {
	saver, err := c.checkUnitActions(ctx, t)
	if err != nil {
		return err
	}

	if t.Kind != KindFollows {
		return nil
	}

	source, err := c.units.Object(ctx, t.Source)
	if err != nil {
		return refusal(t, err)
	}

	err = c.authorizer.Authorize(ctx, saver, identity.ActionUnitRead, source)
	if err != nil {
		return refusal(t, err)
	}

	return nil
}

// CheckStart implements SaverCheck.
func (c *saverCheck) CheckStart(ctx context.Context, t Trigger) error {
	_, err := c.checkUnitActions(ctx, t)

	return err
}

// checkUnitActions resolves the last saver of t and checks the settings action
// and the start action on the unit of t. It returns the resolved saver.
func (c *saverCheck) checkUnitActions(ctx context.Context, t Trigger) (identity.Principal, error) {
	err := t.Validate()
	if err != nil {
		return identity.Principal{}, err
	}

	rec, err := c.resolver.Saver(ctx, t.LastSaver)
	if err != nil {
		return identity.Principal{}, refusal(t, err)
	}

	saver := rec.Principal

	// Defense in depth: the resolver returns a principal with the Admin flag
	// off. This line stays so that a resolver that sets the flag, from a
	// stored subject or a record of another version, never turns a saver
	// into an administrator.
	saver.Admin = false

	if saver.Subject != t.LastSaver {
		return identity.Principal{}, refusal(t, errOtherSubject)
	}

	// Defense in depth: the grants of a subject are not the grants of the
	// principal that saved the trigger. The check compares the ID of the
	// record, and does not rely on the resolver, on the subject check above
	// or on the removal of the triggers of a deleted principal.
	if rec.ID != t.LastSaverID {
		return identity.Principal{}, refusal(t, errOtherPrincipal)
	}

	unit, err := c.units.Object(ctx, t.Unit)
	if err != nil {
		return identity.Principal{}, refusal(t, err)
	}

	for _, action := range []identity.Action{SettingsAction, StartAction} {
		err = c.authorizer.Authorize(ctx, saver, action, unit)
		if err != nil {
			return identity.Principal{}, refusal(t, err)
		}
	}

	return saver, nil
}

// refusal wraps the cause of a failed check. Every cause denies, a failure of
// a port too: the check fails closed.
func refusal(t Trigger, cause error) error {
	return fmt.Errorf("%w: trigger %s, saver %s: %w", ErrSaverDenied, t.ID, quote(t.LastSaver), cause)
}
