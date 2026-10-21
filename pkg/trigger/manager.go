package trigger

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// IDPrefix starts every trigger ID that the server makes.
const IDPrefix = "trg-"

// MaxFireReasonLen is the most characters in the reason of a manual fire.
const MaxFireReasonLen = 200

// errNoUnit is the one text for a unit that does not exist and a unit that the
// caller may not change, so that the answer is no oracle for the names of units.
const errNoUnit = "the unit does not exist, or you cannot change its triggers"

// DeniedError reports a refusal of an action of the caller. It wraps
// identity.ErrDenied. Action and Object name what was refused, so that the
// adapter can write the audit entry of the refusal.
type DeniedError struct {
	Action identity.Action
	Object identity.Object
	Cause  error
}

// Error implements error.
func (e *DeniedError) Error() string {
	return fmt.Sprintf("%v: %s", identity.ErrDenied, errNoUnit)
}

// Unwrap lets errors.Is match identity.ErrDenied and the cause.
func (e *DeniedError) Unwrap() []error {
	if e.Cause == nil {
		return []error{identity.ErrDenied}
	}

	return []error{identity.ErrDenied, e.Cause}
}

// RequestInfo describes the request of a call of the Manager, for the audit
// entry of the change. The adapter bounds the values before it passes them.
type RequestInfo struct {
	Channel   audit.Channel
	SourceIP  string
	UserAgent string
	RequestID string
}

// SaveInput is what a caller gives to save a trigger. The unit and the source
// are stable unit IDs in Namespace.
type SaveInput struct {
	Namespace namespace.Name
	UnitID    string
	Kind      Kind
	Frequency Frequency
	// SourceID is the unit that a follows trigger follows, and empty for any
	// other kind.
	SourceID string
	Enabled  bool
}

// ManagerConfig holds the ports of a Manager.
type ManagerConfig struct {
	Store      Store
	Dispatcher Dispatcher
	Authorizer identity.Authorizer
	Savers     SaverCheck
	Units      Units
	Fences     Fences
	// Now is the clock, and time.Now when nil.
	Now func() time.Time
	// NewID makes the ID of a new trigger, and IDPrefix with random letters
	// and digits when nil.
	NewID func() ID
}

// Manager saves, reads, lists, deletes and fires triggers for a person, and
// checks the actions of that person at each call. The adapters of the REST API
// call it, and it is the only place that decides who may change a trigger.
type Manager struct {
	store      Store
	dispatcher Dispatcher
	authorizer identity.Authorizer
	savers     SaverCheck
	units      Units
	fences     Fences
	now        func() time.Time
	newID      func() ID
}

// NewManager returns a Manager. It returns an error that wraps ErrInvalid when
// a required port is nil.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil || cfg.Dispatcher == nil || cfg.Authorizer == nil || cfg.Savers == nil || cfg.Units == nil ||
		cfg.Fences == nil {
		return nil, fmt.Errorf("%w: a manager needs a store, a dispatcher, an authorizer, a saver check, units "+
			"and fences", ErrInvalid)
	}

	m := &Manager{
		store: cfg.Store, dispatcher: cfg.Dispatcher, authorizer: cfg.Authorizer, savers: cfg.Savers,
		units: cfg.Units, fences: cfg.Fences, now: cfg.Now, newID: cfg.NewID,
	}

	if m.now == nil {
		m.now = time.Now
	}

	if m.newID == nil {
		m.newID = func() ID { return ID(ident.NewID(IDPrefix)) }
	}

	return m, nil
}

// Create saves a new trigger with a new ID. The caller becomes its last saver.
// It returns:
//   - *DeniedError when the caller lacks the settings action or the start
//     action on the unit, or unit:read on the source of a follows trigger, and
//     for a unit that does not exist (the same answer);
//   - an error that wraps ErrSaverDenied when the caller has no current
//     principal record that holds those actions (a start would refuse the
//     trigger);
//   - ErrInvalid for a value that fails validation;
//   - ErrCycle (a *CycleError with the names of the units) for a loop;
//   - the errors of Store.Put.
func (m *Manager) Create(ctx context.Context, caller identity.Principal, info RequestInfo, in SaveInput,
) (Trigger, error) {
	return m.save(ctx, caller, info, m.newID(), in, false)
}

// SaverOK reports whether the last saver of t still holds the actions of a
// start now. A trigger whose saver does not needs a new save, and the console
// says so.
func (m *Manager) SaverOK(ctx context.Context, t *Trigger) bool {
	return m.savers.CheckStart(ctx, *t) == nil
}

// Replace saves the trigger id of the namespace ns again. The unit and the kind
// do not change. The caller becomes the last saver, and so takes over the
// trigger. It returns the errors of Create, and ErrNotFound for an unknown
// trigger.
func (m *Manager) Replace(
	ctx context.Context, caller identity.Principal, info RequestInfo, ns namespace.Name, id ID, in SaveInput,
) (Trigger, error) {
	if in.Namespace != ns {
		return Trigger{}, fmt.Errorf("%w: the body names another namespace than the path", ErrInvalid)
	}

	old, err := m.store.Get(ctx, ns, id)
	if err != nil {
		return Trigger{}, fmt.Errorf("trigger %s: %w", quote(string(id)), err)
	}

	// The caller needs the actions on the unit that the trigger has, whatever
	// the body names. A body for another unit or kind fails below.
	if _, err := m.authorizeUnit(ctx, caller, old.Unit, SettingsAction, StartAction); err != nil {
		return Trigger{}, err
	}

	return m.save(ctx, caller, info, id, in, true)
}

// save is Create and Replace.
func (m *Manager) save(
	ctx context.Context, caller identity.Principal, info RequestInfo, id ID, in SaveInput, replace bool,
) (Trigger, error) {
	if err := checkCaller(caller); err != nil {
		return Trigger{}, err
	}

	unit, obj, err := m.findUnit(ctx, in.Namespace, in.UnitID)
	if err != nil {
		return Trigger{}, err
	}

	if err := m.authorize(ctx, caller, obj, SettingsAction, StartAction); err != nil {
		return Trigger{}, err
	}

	t := Trigger{
		ID: id, Unit: unit, Kind: in.Kind, Frequency: in.Frequency,
		Source: graph.Unit{Namespace: "", Tool: "", ID: ""}, Enabled: in.Enabled, LastSaver: caller.Subject,
	}

	if in.SourceID != "" {
		source, srcObj, err := m.findUnit(ctx, in.Namespace, in.SourceID)
		if err != nil {
			return Trigger{}, err
		}

		// A person learns when a unit applies only with the right to read it.
		if err := m.authorize(ctx, caller, srcObj, identity.ActionUnitRead); err != nil {
			return Trigger{}, err
		}

		t.Source = source
	}

	if err := t.Validate(); err != nil {
		return Trigger{}, m.named(ctx, err)
	}

	// The saver must be able to start the trigger later: a principal with
	// a stored record that holds the actions now. A caller that fails this, for
	// example a bootstrap token with no record, would save a trigger that
	// never starts.
	if err := m.savers.CheckSave(ctx, t); err != nil {
		return Trigger{}, err
	}

	fence, err := m.fences.Fence(ctx, unit.Namespace)
	if err != nil {
		return Trigger{}, fmt.Errorf("fence of %s: %w", unit.Namespace, err)
	}

	if err := m.store.Put(ctx, t, fence, m.entry(caller, info, &t, obj, replace)); err != nil {
		return Trigger{}, m.named(ctx, fmt.Errorf("save trigger %s: %w", t.ID, err))
	}

	return t, nil
}

// Get returns the trigger id of the namespace ns, and the reference of its
// unit. A caller without unit:read on the unit gets ErrNotFound, the answer for
// a trigger that does not exist.
func (m *Manager) Get(ctx context.Context, caller identity.Principal, ns namespace.Name, id ID,
) (Trigger, identity.Object, error) {
	t, err := m.store.Get(ctx, ns, id)
	if err != nil {
		return Trigger{}, identity.Object{}, fmt.Errorf("trigger %s: %w", quote(string(id)), err)
	}

	obj, err := m.readable(ctx, caller, t.Unit)
	if err != nil {
		return Trigger{}, identity.Object{}, err
	}

	return t, obj, nil
}

// List returns the triggers of the unit unitID of the namespace ns, ordered by
// ID. A caller without unit:read on the unit, and a unit that does not exist,
// give ErrNotFound.
func (m *Manager) List(ctx context.Context, caller identity.Principal, ns namespace.Name, unitID string,
) ([]Trigger, error) {
	unit, err := m.units.Find(ctx, ns, unitID)
	if err != nil {
		return nil, notFound(err)
	}

	if _, err := m.readable(ctx, caller, unit); err != nil {
		return nil, err
	}

	out, err := m.store.List(ctx, unit)
	if err != nil {
		return nil, fmt.Errorf("list triggers: %w", err)
	}

	return out, nil
}

// Delete removes the trigger id of the namespace ns. The caller needs the
// settings action on its unit.
func (m *Manager) Delete(
	ctx context.Context, caller identity.Principal, info RequestInfo, ns namespace.Name, id ID,
) error {
	if err := checkCaller(caller); err != nil {
		return err
	}

	t, err := m.store.Get(ctx, ns, id)
	if err != nil {
		return fmt.Errorf("trigger %s: %w", quote(string(id)), err)
	}

	obj, err := m.authorizeUnit(ctx, caller, t.Unit, SettingsAction)
	if err != nil {
		return err
	}

	fence, err := m.fences.Fence(ctx, ns)
	if err != nil {
		return fmt.Errorf("fence of %s: %w", ns, err)
	}

	e := m.entry(caller, info, &t, obj, false)
	e.Changes = []audit.Change{
		{Key: "trigger", Old: ptr(string(t.ID)), New: "", Layer: "", TakesEffect: ""},
		{Key: "kind", Old: ptr(string(t.Kind)), New: "", Layer: "", TakesEffect: ""},
	}

	if err := m.store.Delete(ctx, ns, id, fence, e); err != nil {
		return fmt.Errorf("delete trigger %s: %w", id, err)
	}

	return nil
}

// Fire starts the plan of the trigger id now, as the caller. The job service
// checks the start action of the caller and applies the gates, the approval
// rules, the freezes and the manual locks. A caller without unit:read on the
// unit gets ErrNotFound.
func (m *Manager) Fire(
	ctx context.Context, caller identity.Principal, ns namespace.Name, id ID, reason string,
) (job.ID, error) {
	if err := checkCaller(caller); err != nil {
		return "", err
	}

	// The reason joins a text of the dispatcher in the audit entry of the
	// start, so it keeps well below the bound of an audit reason.
	if utf8.RuneCountInString(reason) > MaxFireReasonLen {
		return "", fmt.Errorf("%w: a reason has at most %d characters", ErrInvalid, MaxFireReasonLen)
	}

	if err := audit.ValidateReason(reason); err != nil {
		return "", fmt.Errorf("%w: reason: %w", ErrInvalid, err)
	}

	t, err := m.store.Get(ctx, ns, id)
	if err != nil {
		return "", fmt.Errorf("trigger %s: %w", quote(string(id)), err)
	}

	if _, err := m.readable(ctx, caller, t.Unit); err != nil {
		return "", err
	}

	actor := job.Actor{Principal: caller, Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{}}

	jobID, err := m.dispatcher.Manual(ctx, actor, ManualRequest{
		Namespace: ns, Trigger: id, Now: m.now(), Reason: reason,
	})
	if err != nil {
		return "", err //nolint:wrapcheck // The dispatcher wraps its errors with the trigger.
	}

	return jobID, nil
}

// checkCaller refuses a caller that cannot change or fire a trigger by kind of
// credential. A job token cannot: a job that could save a trigger would widen
// what its steps run, and a job that could fire one would start a job.
func checkCaller(caller identity.Principal) error {
	if caller.Subject == "" {
		return fmt.Errorf("%w: a call needs a principal", ErrInvalid)
	}

	// Defense in depth: the grants of a job token hold none of these actions.
	// The check stays so that a role with too many actions never lets a job
	// token reach a trigger.
	if caller.Credential == identity.CredentialJobToken {
		return fmt.Errorf("%w: a job token cannot change or fire a trigger", identity.ErrDenied)
	}

	// Defense in depth: the principal API refuses this subject and the
	// Authorizer denies it every action but the start of a plan. The server
	// principal never saves or fires a trigger.
	if identity.IsNamespaceServiceSubject(caller.Subject) {
		return fmt.Errorf("%w: the namespace service principal cannot change or fire a trigger", identity.ErrDenied)
	}

	return nil
}

// findUnit finds the unit with the stable ID unitID in ns and its current
// reference.
func (m *Manager) findUnit(ctx context.Context, ns namespace.Name, unitID string) (graph.Unit, identity.Object, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil || unitID == "" {
		return graph.Unit{}, identity.Object{}, fmt.Errorf("%w: a unit is a namespace and a unit ID", ErrInvalid)
	}

	unit, err := m.units.Find(ctx, ns, unitID)
	if err != nil {
		return graph.Unit{}, identity.Object{}, m.unitError(ns, err)
	}

	obj, err := m.units.Object(ctx, unit)
	if err != nil {
		return graph.Unit{}, identity.Object{}, m.unitError(ns, err)
	}

	return unit, obj, nil
}

// unitError maps a failed lookup of a unit. A unit that does not exist gets the
// same refusal as a unit that the caller may not change.
func (m *Manager) unitError(ns namespace.Name, err error) error {
	if errors.Is(err, ErrUnitGone) {
		return &DeniedError{Action: SettingsAction, Object: identity.NamespaceObject(ns), Cause: nil}
	}

	return fmt.Errorf("find unit: %w", err)
}

// authorize checks that caller holds each of actions on obj.
func (m *Manager) authorize(ctx context.Context, caller identity.Principal, obj identity.Object,
	actions ...identity.Action,
) error {
	for _, action := range actions {
		err := m.authorizer.Authorize(ctx, caller, action, obj)

		switch {
		case err == nil:
		case errors.Is(err, identity.ErrDenied):
			return &DeniedError{Action: action, Object: obj, Cause: nil}
		default:
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// authorizeUnit finds the current reference of unit and checks actions on it.
func (m *Manager) authorizeUnit(ctx context.Context, caller identity.Principal, unit graph.Unit,
	actions ...identity.Action,
) (identity.Object, error) {
	obj, err := m.units.Object(ctx, unit)
	if err != nil {
		return identity.Object{}, m.unitError(unit.Namespace, err)
	}

	if err := m.authorize(ctx, caller, obj, actions...); err != nil {
		return identity.Object{}, err
	}

	return obj, nil
}

// readable checks unit:read on unit for a read. Every refusal, and a unit that
// is gone, is ErrNotFound.
func (m *Manager) readable(ctx context.Context, caller identity.Principal, unit graph.Unit) (identity.Object, error) {
	obj, err := m.units.Object(ctx, unit)
	if err != nil {
		return identity.Object{}, notFound(err)
	}

	err = m.authorizer.Authorize(ctx, caller, identity.ActionUnitRead, obj)

	switch {
	case err == nil:
		return obj, nil
	case errors.Is(err, identity.ErrDenied):
		return identity.Object{}, ErrNotFound
	default:
		return identity.Object{}, fmt.Errorf("authorize %s: %w", identity.ActionUnitRead, err)
	}
}

// notFound maps the error of a lookup of a unit for a read: a unit that is gone
// is ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, ErrUnitGone) {
		return ErrNotFound
	}

	return fmt.Errorf("find unit: %w", err)
}

// named fills the names of the units of a cycle error with the names that the
// tool of each unit uses. Any other error passes unchanged.
func (m *Manager) named(ctx context.Context, err error) error {
	var cycle *CycleError
	if !errors.As(err, &cycle) {
		return err
	}

	names := make(map[graph.Unit]string, len(cycle.Path))

	for _, u := range cycle.Path {
		if obj, lookup := m.units.Object(ctx, u); lookup == nil && obj.UnitName() != "" {
			names[u] = obj.UnitName()
		}
	}

	return &CycleError{Path: cycle.Path, Names: names}
}

// entry returns the audit entry of a save of t by caller.
func (m *Manager) entry(
	caller identity.Principal, info RequestInfo, t *Trigger, obj identity.Object, replace bool,
) audit.Entry {
	changes := []audit.Change{
		{Key: "trigger", Old: nil, New: string(t.ID), Layer: "", TakesEffect: ""},
		{Key: "kind", Old: nil, New: string(t.Kind), Layer: "", TakesEffect: ""},
		{Key: "enabled", Old: nil, New: fmt.Sprint(t.Enabled), Layer: "", TakesEffect: ""},
	}

	if t.Kind == KindSchedule {
		expr := t.Frequency.Cron
		if expr == "" {
			expr = string(t.Frequency.Preset)
		}

		changes = append(changes, audit.Change{Key: "frequency", Old: nil, New: expr, Layer: "", TakesEffect: ""})
	}

	if t.Kind == KindFollows {
		changes = append(changes, audit.Change{Key: "source", Old: nil, New: t.Source.ID, Layer: "", TakesEffect: ""})
	}

	reason := "save trigger"
	if replace {
		reason = "replace trigger"
	}

	channel := info.Channel
	if channel == "" {
		channel = audit.ChannelAPI
	}

	return audit.Entry{
		At: m.now().UTC(), Namespace: t.Unit.Namespace, Subject: caller.Subject, IDPSubject: "",
		TokenID: caller.TokenID, Action: string(SettingsAction), Object: obj.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: changes, SourceIP: info.SourceIP, UserAgent: info.UserAgent, Channel: channel,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: info.RequestID, ObjectID: t.Unit.ID,
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: string(caller.Kind), JobID: "",
		Reason: reason,
	}
}

func ptr[T any](v T) *T { return &v }
