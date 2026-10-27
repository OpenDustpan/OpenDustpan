// Package maintenance models the installation state of OpenDustpan (ADR 38
// decision 4): normal operation or maintenance mode for a restore.
//
// The state lives in the database, so every server instance reads the same
// value. In maintenance mode the tool APIs answer 503 while the installation
// API and the probes stay available. The server enters maintenance mode only
// after the restore checks pass, and leaves it when the restore ends, also
// after a failure.
//
// The package holds no SQL and no HTTP. Each store adapter implements the
// Store port, and the conformance suite store/storetest.RunMaintenance
// exercises it before any adapter does.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// States of an installation.
const (
	// StateNormal serves every API.
	StateNormal State = "normal"
	// StateMaintenance refuses tool calls with 503 while a restore runs.
	StateMaintenance State = "maintenance"
)

// maintenanceSubject names the principal of a force-unlock that maintenance
// mode makes, so the fence version of each released workspace says where it came from.
const maintenanceSubject = "maintenance"

// maintenanceReason is the reason in the audit entry of a force-unlock that
// maintenance mode makes.
const maintenanceReason = "maintenance mode started"

// DefaultCacheLimit bounds how long a Watcher trusts its read of the
// installation state. The limit only saves reads for the HTTP gate and the
// probes. A write never uses it: each write transaction reads the stored
// state itself (ErrInMaintenance).
const DefaultCacheLimit = 2 * time.Second

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalidState reports a state that is neither normal nor maintenance.
	ErrInvalidState = errors.New("invalid installation state")
	// ErrActiveWork reports that entering maintenance mode found a running
	// update or run. The error text names each one as a unit reference.
	ErrActiveWork = errors.New("cannot enter maintenance mode while work is active")
	// ErrInMaintenance reports a write that the store refused because the
	// installation is in maintenance mode. A store adapter checks the stored
	// state in each write transaction, so the refusal holds on every instance
	// at once, at installation scope too, and without a wait for a cache.
	// A store that cannot read the state refuses the write with
	// ErrUnavailable: it never admits the write.
	ErrInMaintenance = errors.New("the installation is in maintenance mode")
	// ErrStateChanged reports that a conditional write found another state
	// than expected: another instance entered or left first. The caller
	// re-reads the state instead of overwriting it.
	ErrStateChanged = errors.New("installation state changed")
	// ErrHeld reports a leave while the hold of the service keeps
	// maintenance mode: a change of the audit chain waits for its approval
	// or its migration.
	ErrHeld = errors.New("maintenance mode is held until the audit chain change ends")
	// ErrUnavailable reports that the installation state cannot be read or
	// stored. Store adapters translate driver failures to it, so callers
	// never see driver text.
	ErrUnavailable = errors.New("installation state unavailable")
	// ErrBusy reports an enter of maintenance mode that waited for the
	// writes in flight for the bound of the store, and gave up. The state
	// did not change. The caller tries again later.
	ErrBusy = errors.New("cannot enter maintenance mode: the writes in flight did not end in time")
)

// State is the installation state: normal or maintenance.
type State string

// String returns the state as a plain string.
func (s State) String() string {
	return string(s)
}

// ParseState validates s as an installation state.
func ParseState(s string) (State, error) {
	switch State(s) {
	case StateNormal:
		return StateNormal, nil
	case StateMaintenance:
		return StateMaintenance, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidState, s)
	}
}

// Store is the persistence port for the installation state. It describes the
// installation, not one namespace, so its rows carry no fence, as with the
// backup registry. Every SQL adapter serves it from one database connection.
type Store interface {
	// GetState returns the installation state. A fresh database reads
	// normal: the migration seeds the one row. It reports ErrUnavailable
	// when the state cannot be read, when the row is missing, and when the
	// stored value is outside the known set: an unknown stored value is a
	// server fault, never a bad request.
	GetState(ctx context.Context) (State, error)
	// SetState stores state in the seeded row. It reports ErrInvalidState
	// for any other value and ErrUnavailable when the state cannot be
	// stored, also when the row is missing: it never creates the row. A
	// store that waits for the writes in flight before it enters maintenance
	// mode bounds that wait, and reports ErrBusy and changes nothing when the
	// bound ends first.
	SetState(ctx context.Context, state State) error
	// SetStateIf stores next only when the stored state is expected: one
	// compare-and-set, so enters on several instances of one database stay
	// ordered. It reports ErrStateChanged when the stored state is not
	// expected and changes nothing, ErrInvalidState for any other expected
	// or next value, and ErrUnavailable when the state cannot be stored. It
	// reports ErrBusy as SetState does.
	SetStateIf(ctx context.Context, expected, next State) error
}

// FencedStore is the part of the installation state port that a durable
// operation uses: a change of the state under the claim of the operation. A
// holder that lost its claim can then never enter or leave maintenance mode.
// Each SQL store adapter implements it on the database of its durable.Store,
// and runs the conformance cases of package storetest.
type FencedStore interface {
	// SetStateIfFenced stores next only when the stored state is expected,
	// as SetStateIf does, and checks and records the step of f with the
	// rules of durable.Store.RecordStep, in one transaction. It checks the
	// fence first, and returns the errors of RecordStep. Then it returns the
	// errors of SetStateIf. On an error it changes nothing: a changed state
	// records no step.
	SetStateIfFenced(ctx context.Context, expected, next State, f durable.StepFence) error
}

// Service enters and leaves maintenance mode. The zero value is not usable:
// call NewService. Enter and Leave hold one mutex, so calls in one process
// run one after another instead of canceling the same work twice or
// interleaving a read-modify-write with an enter. Enters on different
// instances serialize on the stored state: each enter writes maintenance
// only when the stored state is still normal, so a loser re-reads instead
// of overwriting the winner; the non-force rollback writes normal only when
// the row is still the maintenance this enter wrote, so it never undoes
// another instance's enter. The winner always re-checks for active work
// after its write, so work that started before the write is still caught.
type Service struct {
	store      Store
	namespaces *namespace.Service
	stacks     *stack.Service
	updates    *stack.Updates
	workspaces *workspace.Service
	hold       func(ctx context.Context) (bool, error)
	mu         sync.Mutex
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces *namespace.Service
	Stacks     *stack.Service
	Updates    *stack.Updates
	Workspaces *workspace.Service
	// Hold, when it is not nil, reports whether maintenance mode must stay:
	// Leave refuses with ErrHeld while it reports true. Nil holds nothing.
	Hold func(ctx context.Context) (bool, error)
}

// NewService returns a Service. Every dependency is required, except Hold.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: maintenance store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack service", ErrMissingDependency)
	case cfg.Updates == nil:
		return nil, fmt.Errorf("%w: update service", ErrMissingDependency)
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: workspace service", ErrMissingDependency)
	}

	//nolint:exhaustruct_v5 // The enter mutex starts zero; only the dependencies are set.
	return &Service{
		store:      cfg.Store,
		namespaces: cfg.Namespaces,
		stacks:     cfg.Stacks,
		updates:    cfg.Updates,
		workspaces: cfg.Workspaces,
		hold:       cfg.Hold,
	}, nil
}

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// maxForceAttempts bounds the attempts of a forced Enter. Work that starts
// between the cancel and the write of the state makes an attempt end with
// ErrActiveWork. A forced Enter tries again, and each new attempt cancels
// that work. A writer that keeps starting work then fails the call after
// this many attempts instead of looping without end.
const maxForceAttempts = 3

// ActiveWorkError is the ErrActiveWork answer. It names the work that blocked
// the enter and, for a forced enter, the work that the attempts canceled
// before the enter gave up.
type ActiveWorkError struct {
	// Work holds the unit references of the work that blocked the enter, in
	// a stable order.
	Work []string
	// Canceled holds the unit references that a forced enter canceled
	// before it gave up, in a stable order. It is empty without force.
	Canceled []string
}

// Error returns the sentinel text with the unit references of Work.
func (e *ActiveWorkError) Error() string {
	return fmt.Sprintf("%s: %s", ErrActiveWork, strings.Join(e.Work, ", "))
}

// Unwrap lets errors.Is match ErrActiveWork.
func (e *ActiveWorkError) Unwrap() error { return ErrActiveWork }

// CanceledError wraps a failure of a forced enter, other than ErrActiveWork,
// that came after the enter canceled work. The cancel stays: the error names
// the units so that the caller can tell the operator what stopped. It
// unwraps to Err.
type CanceledError struct {
	// Err is the failure of the enter.
	Err error
	// Canceled holds the unit references that the forced enter canceled
	// before it failed, in a stable order.
	Canceled []string
}

// Error returns the text of Err and the unit references of Canceled.
func (e *CanceledError) Error() string {
	return fmt.Sprintf("%v (canceled before the failure: %s)", e.Err, strings.Join(e.Canceled, ", "))
}

// Unwrap returns Err, so errors.Is and errors.As reach the failure.
func (e *CanceledError) Unwrap() error { return e.Err }

// EnterResult names the work that entering maintenance mode canceled. Each
// entry is a unit reference, for example "pulumi:acme/web/prod".
type EnterResult struct {
	Canceled []string
}

// Get returns the installation state.
func (s *Service) Get(ctx context.Context) (State, error) {
	state, err := s.store.GetState(ctx)
	if err != nil {
		return "", fmt.Errorf("get installation state: %w", err)
	}

	return state, nil
}

// Enter moves the installation to maintenance mode. While an update or a run
// is active it reports ErrActiveWork naming each one, unless force cancels
// them first and the result names what it canceled. Entering twice keeps maintenance
// mode: the result holds only what this call canceled, empty otherwise.
//
// Enter holds the service mutex, so two concurrent enters cannot cancel the
// same work twice: the second one sees maintenance mode and answers empty.
// The write is a compare-and-set on normal, so two enters on different
// instances cannot both win: the loser re-reads, sees maintenance, and
// answers what it canceled, if anything. When the re-read finds normal (a
// leave or a rollback landed in between), the enter tries again from the
// fresh state. After the write, Enter lists the active work again: an update
// or a run that started between the first check and the write is still
// active during maintenance. In that case Enter moves back to normal and
// reports ErrActiveWork naming it. With force Enter then tries again and the
// new attempt cancels that work, up to maxForceAttempts attempts. When
// writers keep starting work, the last attempt reports an *ActiveWorkError
// that names the work and every unit that the attempts canceled. A start
// after the second check meets the tool adapters, which answer 503.
//
//nolint:revive // The force flag is the API: it picks the bounded retry, and the route maps it from the request body.
func (s *Service) Enter(ctx context.Context, force bool) (EnterResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var canceled []string

	for attempt := 1; ; attempt++ {
		retry, res, err := s.enterAttempt(ctx, force)
		canceled = append(canceled, res.Canceled...)

		if retry {
			continue
		}

		blocked, isBlocked := errors.AsType[*ActiveWorkError](err)
		isBlocked = isBlocked && blocked != nil

		if force && attempt < maxForceAttempts && isBlocked {
			continue
		}

		if err == nil {
			slices.Sort(canceled)

			return EnterResult{Canceled: canceled}, nil
		}

		if len(canceled) > 0 {
			slices.Sort(canceled)

			if isBlocked {
				blocked.Canceled = canceled
			} else {
				err = &CanceledError{Err: err, Canceled: canceled}
			}
		}

		return EnterResult{Canceled: nil}, err
	}
}

// Leave moves the installation back to normal operation. Leaving twice keeps
// normal operation. It holds the service mutex, the same critical section as
// Enter, so an overlapping enter and leave run one after another: the final
// state is the one the last call wrote, and each caller reports it. While
// the hold reports true, Leave reports ErrHeld and keeps maintenance mode.
func (s *Service) Leave(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.store.GetState(ctx)
	if err != nil {
		return fmt.Errorf("get installation state: %w", err)
	}

	if state == StateNormal {
		return nil
	}

	if s.hold != nil {
		held, holdErr := s.hold(ctx)
		if holdErr != nil {
			return fmt.Errorf("read the maintenance hold: %w", holdErr)
		}

		if held {
			return ErrHeld
		}
	}

	return s.setState(ctx, StateNormal)
}

// enterAttempt runs one enter attempt: read the state, check for active
// work, cancel it with force, and write maintenance with a compare-and-set.
// It reports retry when the write lost to another instance and the state
// moved back to normal, so the caller tries again from the fresh state.
//
//nolint:revive // The force flag is the API: ADR 38 decision 5 names it, and the route maps it from the request body.
func (s *Service) enterAttempt(ctx context.Context, force bool) (bool, EnterResult, error) {
	state, err := s.store.GetState(ctx)
	if err != nil {
		return false, EnterResult{Canceled: nil}, fmt.Errorf("get installation state: %w", err)
	}

	if state == StateMaintenance {
		return false, EnterResult{Canceled: nil}, nil
	}

	active, err := s.activeWork(ctx)
	if err != nil {
		return false, EnterResult{Canceled: nil}, err
	}

	if len(active) > 0 && !force {
		return false, EnterResult{Canceled: nil}, activeWorkError(active)
	}

	var canceled []string

	if force {
		var cancelErr error

		canceled, cancelErr = s.cancelWork(ctx, active)
		if cancelErr != nil {
			return false, EnterResult{Canceled: nil}, cancelErr
		}
	}

	setErr := s.setStateIf(ctx, StateNormal, StateMaintenance)
	if setErr != nil {
		if !errors.Is(setErr, ErrStateChanged) {
			// The cancel stays: the result names it, and Enter attaches it to the error.
			return false, EnterResult{Canceled: canceled}, setErr
		}

		return s.enterRaced(ctx, canceled)
	}

	// With force too: once the state is maintenance, a cancel is a checked
	// write and maintenance mode refuses it. Work that raced the write moves
	// the installation back to normal and the caller enters again.
	var already activeWork
	if force {
		already = active
	}

	// The result holds what this attempt canceled also when work raced the
	// write: a forced Enter that gives up names it in the error.
	_, racedErr := s.refuseRaced(ctx, already)

	return false, EnterResult{Canceled: canceled}, racedErr
}

// enterRaced answers a lost compare-and-set: another instance changed the
// state after this enter read it. Maintenance means someone else entered:
// this enter owns nothing and answers what it canceled, if anything, while
// the winner's own re-check covers the raced work. Normal means a leave or
// a rollback landed in between: the caller tries again from it.
func (s *Service) enterRaced(ctx context.Context, canceled []string) (bool, EnterResult, error) {
	state, err := s.store.GetState(ctx)
	if err != nil {
		return false, EnterResult{Canceled: nil}, fmt.Errorf("get installation state: %w", err)
	}

	if state == StateMaintenance {
		return false, EnterResult{Canceled: canceled}, nil
	}

	return true, EnterResult{Canceled: nil}, nil
}

// refuseRaced answers work that started between the enter check and its
// write: it moves back to normal and reports ErrActiveWork naming it. The
// rollback is a compare-and-set on maintenance, so it moves back only when
// this enter still owns the row: it never undoes another instance's enter.
// When the row already moved on (ErrStateChanged), the raced work is still
// reported, but the state is left alone. Work in already is not raced: a
// forced enter met it before the write and left it (a client-encrypted
// state keeps its lock).
func (s *Service) refuseRaced(ctx context.Context, already activeWork) (EnterResult, error) {
	found, err := s.activeWork(ctx)
	if err != nil {
		return EnterResult{Canceled: nil}, err
	}

	raced := make(activeWork, 0, len(found))

	for _, u := range found {
		if !slices.ContainsFunc(already, func(a activeUnit) bool { return sameUnit(a, u) }) {
			raced = append(raced, u)
		}
	}

	if len(raced) == 0 {
		return EnterResult{Canceled: nil}, nil
	}

	if err := s.setStateIf(ctx, StateMaintenance, StateNormal); err != nil {
		if errors.Is(err, ErrStateChanged) {
			return EnterResult{Canceled: nil}, activeWorkError(raced)
		}

		return EnterResult{Canceled: nil}, err
	}

	return EnterResult{Canceled: nil}, activeWorkError(raced)
}

// activeWorkError reports ErrActiveWork naming each unit of work.
func activeWorkError(work activeWork) error {
	return &ActiveWorkError{Work: work.names(), Canceled: nil}
}

// setStateIf stores next only when the stored state is expected.
func (s *Service) setStateIf(ctx context.Context, expected, next State) error {
	if err := s.store.SetStateIf(ctx, expected, next); err != nil {
		return fmt.Errorf("set installation state %q: %w", next, err)
	}

	return nil
}

// setState stores state.
func (s *Service) setState(ctx context.Context, state State) error {
	if err := s.store.SetState(ctx, state); err != nil {
		return fmt.Errorf("set installation state %q: %w", state, err)
	}

	return nil
}

// activeUnit is one running update or locked workspace with its unit
// reference. It holds only the references and IDs the cancel needs, not the
// full update or workspace, so listing the active work copies little. An
// update unit leaves workspaceID empty; a workspace unit leaves the update
// fields empty.
type activeUnit struct {
	reference   string
	updateRef   stack.Ref
	updateID    stack.UpdateID
	workspaceID workspace.ID
	isUpdate    bool
}

// sameUnit reports whether a and b name the same running update or the same
// locked workspace.
func sameUnit(a, b activeUnit) bool {
	return a.reference == b.reference && a.updateID == b.updateID && a.workspaceID == b.workspaceID &&
		a.isUpdate == b.isUpdate
}

// activeWork holds every running update and locked workspace of the
// installation.
type activeWork []activeUnit

// names returns the unit references in a stable order.
func (w activeWork) names() []string {
	out := make([]string, 0, len(w))
	for _, u := range w {
		out = append(out, u.reference)
	}

	slices.Sort(out)

	return out
}

// activeWork lists every running mutating update and every locked workspace
// of the installation. A locked workspace is an active run: OpenTofu holds
// the lock while it changes state.
func (s *Service) activeWork(ctx context.Context) (activeWork, error) {
	namespaces, err := s.namespaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}

	var out activeWork

	for i := range namespaces {
		ns := namespaces[i].Name

		updates, err := s.activeUpdates(ctx, ns)
		if err != nil {
			return nil, err
		}

		out = append(out, updates...)

		locked, err := s.lockedWorkspaces(ctx, ns)
		if err != nil {
			return nil, err
		}

		out = append(out, locked...)
	}

	slices.SortFunc(out, func(a, b activeUnit) int { return strings.Compare(a.reference, b.reference) })

	return out, nil
}

// activeUpdates returns the running mutating updates of one namespace.
func (s *Service) activeUpdates(ctx context.Context, ns namespace.Name) (activeWork, error) {
	stacks, err := s.stacks.List(ctx, stack.Filter{Namespace: ns, Project: ""})
	if err != nil {
		return nil, fmt.Errorf("list stacks in %q: %w", ns, err)
	}

	var out activeWork

	for i := range stacks {
		u, err := s.updates.Active(ctx, stacks[i].Ref)
		if errors.Is(err, stack.ErrUpdateNotFound) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("active update of %s: %w", stacks[i].Ref, err)
		}

		ref, err := unitObject(
			"pulumi",
			stacks[i].Ref.Namespace,
			stacks[i].Ref.Project.String()+"/"+stacks[i].Ref.Stack.String(),
		)
		if err != nil {
			return nil, err
		}

		out = append(out, activeUnit{
			reference:   ref,
			updateRef:   u.Ref,
			updateID:    u.ID,
			workspaceID: "",
			isUpdate:    true,
		})
	}

	return out, nil
}

// lockedWorkspaces returns the locked workspaces of one namespace.
func (s *Service) lockedWorkspaces(ctx context.Context, ns namespace.Name) (activeWork, error) {
	list, err := s.workspaces.List(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list workspaces in %q: %w", ns, err)
	}

	var out activeWork

	for i := range list {
		if list[i].Lock.Holder == "" {
			continue
		}

		ref, err := unitObject("opentofu", list[i].Ref.Namespace, list[i].Ref.Name.String())
		if err != nil {
			return nil, err
		}

		out = append(out, activeUnit{
			reference:   ref,
			updateRef:   stack.Ref{Namespace: "", Project: "", Stack: ""},
			updateID:    "",
			workspaceID: list[i].ID,
			isUpdate:    false,
		})
	}

	return out, nil
}

// cancelWork cancels every active update and releases every held lock,
// returning the unit references it canceled in a stable order. Work that
// ended on its own stays out of the result: only canceled work is named.
func (s *Service) cancelWork(ctx context.Context, work activeWork) ([]string, error) {
	canceled := make([]string, 0, len(work))

	for _, u := range work {
		name, ok, err := s.cancelOne(ctx, u)
		if err != nil {
			return nil, err
		}

		if ok {
			canceled = append(canceled, name)
		}
	}

	slices.Sort(canceled)

	return canceled, nil
}

// cancelOne cancels one active update or releases one held lock. It reports
// whether it canceled anything: work that ended on its own reports false.
func (s *Service) cancelOne(ctx context.Context, u activeUnit) (string, bool, error) {
	if u.isUpdate {
		if _, err := s.updates.Cancel(ctx, u.updateRef, u.updateID); err != nil {
			if errors.Is(err, stack.ErrUpdateNotFound) || errors.Is(err, stack.ErrWrongUpdateState) {
				return "", false, nil
			}

			return "", false, fmt.Errorf("cancel update %s of %s: %w", u.updateID, u.updateRef, err)
		}

		return u.reference, true, nil
	}

	// Maintenance holds no override: a client-encrypted state keeps its lock.
	none := namespace.Overrides{Forced: false, Unlocked: false}

	entry := audit.Entry{
		At:         time.Time{},
		Namespace:  "",
		Subject:    "",
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionUnitWriteForced),
		Object:     u.reference,
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "",
		UserAgent:  "",
		// ChannelAPI: the request that enters maintenance mode is an API request,
		// and no channel names the installation as the actor.
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        maintenanceReason,
		PrincipalKind: "",
		JobID:         "",
	}

	if _, _, err := s.workspaces.ForceUnlock(ctx, u.workspaceID, maintenanceSubject, none, nil, entry); err != nil {
		// A client-encrypted state cannot be fenced, so its lock stays. The
		// enter goes on and does not report the unit as released.
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrNotLocked) ||
			errors.Is(err, workspace.ErrClientEncrypted) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("release lock of %s: %w", u.reference, err)
	}

	return u.reference, true, nil
}

// unitObject maps a tool and its unit name to the tool-neutral unit reference
// that authorization uses.
func unitObject(tool string, ns namespace.Name, name string) (string, error) {
	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		return "", fmt.Errorf("unit reference of %s:%s/%s: %w", tool, ns, name, err)
	}

	return obj.String(), nil
}

// Watcher reads the installation state with a short cache limit, so every
// request does not read the database. It only gives the HTTP gate a cheap
// early refusal. It never decides a write: the store checks the stored state
// in each write transaction, so a cached or kept state cannot admit a write.
// The zero value is not usable: call NewWatcher.
type Watcher struct {
	store Store
	now   func() time.Time
	limit time.Duration
	mu    sync.Mutex
	at    time.Time
	state State
	read  bool
}

// NewWatcher returns a Watcher over store. A nil now means time.Now; a
// non-positive limit means DefaultCacheLimit.
func NewWatcher(store Store, now func() time.Time, limit time.Duration) *Watcher {
	if now == nil {
		now = time.Now
	}

	if limit <= 0 {
		limit = DefaultCacheLimit
	}

	//nolint:exhaustruct_v5 // NewWatcher sets only the inputs; the mutex and the cache start zero.
	return &Watcher{store: store, now: now, limit: limit}
}

// State returns the installation state, reading the database at most once
// per cache limit. A read failure keeps the last state it read. Before the
// first good read, a read failure answers maintenance: the watcher does not
// know the state, so it refuses the request instead of letting it write
// during maintenance mode.
func (w *Watcher) State(ctx context.Context) State {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.read && w.now().Sub(w.at) < w.limit {
		return w.state
	}

	state, err := w.store.GetState(ctx)
	if err != nil {
		if !w.read {
			return StateMaintenance
		}

		return w.state
	}

	w.state, w.at, w.read = state, w.now(), true

	return state
}
