package manuallock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Authorizer decides whether a principal may act on an object. identity.Authorizer
// satisfies it. The service asks it for each call, so a caller that skips the
// check of its own route still cannot lock, unlock or read without the action.
type Authorizer interface {
	// Authorize returns nil if allowed and an error wrapping identity.ErrDenied
	// otherwise.
	Authorize(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces namespace.Store
	Authorizer Authorizer
	// Phases lets a tool lock request of a job pass a manual lock that came
	// after its phase started. A nil Phases makes the service refuse every
	// such request, as it refuses the request of any other caller.
	Phases PhaseClock
	Now    func() time.Time
}

// PhaseClock tells when the phase of a job started. The job store implements it
// (job.PhaseClock). The manual lock service uses it to decide whether the job
// token of a lock request belongs to a phase that started before the lock.
type PhaseClock interface {
	// PhaseStartedAt returns when the current phase of the job jobID started,
	// if that phase is live (dispatched, running or canceling) and the job is
	// a job of unit. It compares the stable namespace ID and unit ID of unit,
	// never the name. An empty namespace ID matches no job. An empty unit ID is
	// for a unit that does not exist yet, and the job must then carry the same
	// namespace ID, tool and name. It returns ErrNoPhase for an unknown job,
	// for a job of another unit, for an empty namespace ID and for a job with
	// no live phase.
	PhaseStartedAt(ctx context.Context, jobID string, unit Unit) (time.Time, error)
}

// ErrNoPhase reports that a job has no live phase on the unit that a lock
// request names. A PhaseClock returns it.
var ErrNoPhase = errors.New("job has no live phase on this unit")

// Service implements the manual lock use cases. It reads the fence of the
// namespace before each write, so a write carries the failover version that
// the namespace has now.
type Service struct {
	store      Store
	namespaces namespace.Store
	authorizer Authorizer
	phases     PhaseClock
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when a port
// is missing. A nil Now uses time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: manual lock store, namespace store and authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store:      cfg.Store,
		namespaces: cfg.Namespaces,
		authorizer: cfg.Authorizer,
		phases:     cfg.Phases,
		now:        now,
	}, nil
}

// Lock puts a manual lock on t for the principal p, who needs unit:lock on the
// object. The holder is the subject of p. The reason is required and has at
// most 500 characters. The audit entry by names the caller and the request; the
// service sets its time and its reason. A lock on an object that has one
// returns ErrAlreadyLocked, and the message names the holder.
func (s *Service) Lock(
	ctx context.Context,
	p identity.Principal,
	t Target,
	reason string,
	by audit.Entry,
) (Lock, error) {
	if err := t.Valid(); err != nil {
		return Lock{}, err
	}

	if err := s.authorize(ctx, p, identity.ActionUnitLock, t); err != nil {
		return Lock{}, err
	}

	if strings.TrimSpace(reason) == "" {
		return Lock{}, ErrReasonRequired
	}

	if err := audit.ValidateReason(reason); err != nil {
		return Lock{}, fmt.Errorf("manual lock reason: %w", err)
	}

	fence, err := s.fence(ctx, t.Namespace())
	if err != nil {
		return Lock{}, err
	}

	l := Lock{Since: s.now().UTC(), Target: t, Holder: p.Subject, Reason: reason}

	by.At = l.Since
	by.Reason = reason

	if err := s.store.PutLock(ctx, l, fence, by); err != nil {
		return Lock{}, s.annotate(ctx, t, fmt.Errorf("lock %s: %w", t, err))
	}

	return l, nil
}

// Unlock removes the manual lock of t, which the principal p must hold. The
// principal needs unit:lock on the object. It returns ErrNotHolder when
// another principal holds the lock.
func (s *Service) Unlock(ctx context.Context, p identity.Principal, t Target, by audit.Entry) error {
	if err := t.Valid(); err != nil {
		return err
	}

	if err := s.authorize(ctx, p, identity.ActionUnitLock, t); err != nil {
		return err
	}

	// Defense in depth: the store refuses an empty holder too. This check stays
	// so that a principal with no subject never reaches the store as the holder.
	if p.Subject == "" {
		return fmt.Errorf("unlock %s: %w", t, ErrNotHolder)
	}

	fence, err := s.fence(ctx, t.Namespace())
	if err != nil {
		return err
	}

	by.At = s.now().UTC()

	if _, err := s.store.RemoveLock(ctx, t, p.Subject, fence, by); err != nil {
		return fmt.Errorf("unlock %s: %w", t, err)
	}

	return nil
}

// ForceUnlock removes the manual lock of t whoever holds it. The principal p
// needs unit:manage on the object. The audit entry by records the principal
// that forced the removal.
func (s *Service) ForceUnlock(ctx context.Context, p identity.Principal, t Target, by audit.Entry) error {
	if err := t.Valid(); err != nil {
		return err
	}

	if err := s.authorize(ctx, p, identity.ActionUnitManage, t); err != nil {
		return err
	}

	fence, err := s.fence(ctx, t.Namespace())
	if err != nil {
		return err
	}

	by.At = s.now().UTC()

	if _, err := s.store.ForceRemoveLock(ctx, t, fence, by); err != nil {
		return fmt.Errorf("force-unlock %s: %w", t, err)
	}

	return nil
}

// Get returns the manual lock of t, or ErrNotLocked. The principal p needs
// read access to the object: unit:read on a unit, collection:read on a
// collection, namespace:read on a namespace.
func (s *Service) Get(ctx context.Context, p identity.Principal, t Target) (Lock, error) {
	if err := t.Valid(); err != nil {
		return Lock{}, err
	}

	var action identity.Action

	switch t.Kind {
	case KindCollection:
		action = identity.ActionCollectionRead
	case KindNamespace:
		action = identity.ActionNamespaceRead
	case KindUnit:
		action = identity.ActionUnitRead
	default:
		return Lock{}, fmt.Errorf("get %s: %w", t, ErrInvalidTarget)
	}

	if err := s.authorize(ctx, p, action, t); err != nil {
		return Lock{}, err
	}

	return s.store.GetLock(ctx, t)
}

// Covering returns the manual lock that covers the unit, or ErrNotLocked. A
// tool calls it before it takes its own lock.
func (s *Service) Covering(ctx context.Context, unit collection.UnitRef) (Lock, error) {
	return s.store.CoveringLock(ctx, unit)
}

// LockOn returns the manual lock that t itself holds, or ErrNotLocked. Unlike
// Covering it does not look at the lock of a wider target, and unlike Get it
// does not check the access of a caller: it is for a server check that has
// authorized the caller on its own action, such as the move of a unit out of a
// locked collection.
func (s *Service) LockOn(ctx context.Context, t Target) (Lock, error) {
	if err := t.Valid(); err != nil {
		return Lock{}, err
	}

	return s.store.GetLock(ctx, t)
}

// NamespaceLock returns the manual lock of the namespace ns, or ErrNotLocked.
// The restore of a deleted unit asks it: a deleted unit has no inventory entry,
// so Covering cannot find the lock of its namespace.
func (s *Service) NamespaceLock(ctx context.Context, ns namespace.Name) (Lock, error) {
	return s.LockOn(ctx, NamespaceTarget(ns))
}

// CoveringFor is Covering for the lock request of the caller that holds the job
// token of the job jobID (empty for any other caller). It covers a unit that
// exists, and so has a unit ID. A lock request of a job whose phase on the unit
// started before the manual lock passes: it answers ErrNotLocked. The phase
// runs to its end, and so do its tool locks. Every other case answers as
// Covering does. The service fails closed: a missing phase clock, an error of
// the clock, a unit with an empty namespace ID, a unit with an empty unit ID, a
// phase that started at or after the lock and a time of zero all keep the
// lock. When several locks cover the unit, the phase passes only if it started
// before each of them, and the answer is the first lock that it did not start
// before.
//
// Defense in depth: the authenticator sets the job ID only for a verified job
// token, and the clock checks that the job is live on this unit. This check
// stays so that a caller which sets a job ID by mistake cannot pass a lock.
func (s *Service) CoveringFor(ctx context.Context, unit Unit, jobID string) (Lock, error) {
	locks, err := s.store.CoveringLocks(ctx, unit.Ref)
	if err != nil {
		return Lock{}, err
	}

	if len(locks) == 0 {
		return Lock{}, ErrNotLocked
	}

	// An existing unit always has a unit ID. An empty ID fails closed: it keeps
	// the first lock and never asks the clock.
	if unit.UnitID == "" {
		return locks[0], nil
	}

	return s.afterPhase(ctx, locks, unit, jobID)
}

// NamespaceLockFor is CoveringFor for a unit that does not exist yet. A new
// unit has no inventory row, so it is in no collection and has no lock of its
// own: only the lock of its namespace covers it. Its unit ID is empty, so the
// phase clock matches the job by the namespace ID, the tool and the name. A job
// whose phase on the unit started before that lock passes it, as it does in
// CoveringFor.
func (s *Service) NamespaceLockFor(ctx context.Context, unit Unit, jobID string) (Lock, error) {
	lock, err := s.NamespaceLock(ctx, unit.Ref.Namespace)
	if err != nil {
		return Lock{}, err
	}

	return s.afterPhase(ctx, []Lock{lock}, unit, jobID)
}

// PhaseStart returns when the current phase of the job jobID on the unit
// started, for a caller that keeps a manual lock in a transaction of its own
// and applies the rule of CoveringFor there: the phase passes a lock that came
// after it. It returns the zero time, which passes no lock, for an empty job
// ID, for a unit with an empty namespace ID, for a service without a phase
// clock and for a clock that fails. An empty unit ID is for a unit that does
// not exist yet, and the clock still answers for it. The answer fails closed
// in the same way as CoveringFor.
func (s *Service) PhaseStart(ctx context.Context, unit Unit, jobID string) time.Time {
	if jobID == "" || s.phases == nil || unit.NamespaceID == "" {
		return time.Time{}
	}

	started, err := s.phases.PhaseStartedAt(ctx, jobID, unit)
	if err != nil {
		return time.Time{}
	}

	return started
}

// afterPhase applies the rule of the job to locks, which cover unit and are not
// empty. Without a job it answers the first lock. The request passes only on a
// positive answer of the clock: a clock that fails or answers zero leaves the
// lock in place. The phase passes only when it started before every lock that
// covers the unit, so a new lock on a wider object never lifts a refusal that an
// older lock gives.
func (s *Service) afterPhase(ctx context.Context, locks []Lock, unit Unit, jobID string) (Lock, error) {
	started := s.PhaseStart(ctx, unit, jobID)
	if started.IsZero() {
		return locks[0], nil
	}

	for i := range locks {
		if !started.Before(locks[i].Since) {
			return locks[i], nil
		}
	}

	return Lock{}, ErrNotLocked
}

// authorize checks that p may do action on the object of t. It fails closed:
// an object that cannot be named is refused.
func (s *Service) authorize(ctx context.Context, p identity.Principal, action identity.Action, t Target) error {
	var (
		obj identity.Object
		err error
	)

	switch t.Kind {
	case KindUnit:
		obj, err = identity.UnitObject(t.Unit.Tool, t.Unit.Namespace, t.Unit.Name)
	case KindCollection:
		obj = identity.CollectionObject(t.Collection.Namespace, string(t.Collection.Name))
	case KindNamespace:
		obj = identity.NamespaceObject(t.Space)
	default:
		err = ErrInvalidTarget
	}

	if err != nil {
		return fmt.Errorf("authorize %s on %s: %w: %w", action, t, ErrInvalidTarget, err)
	}

	if err := s.authorizer.Authorize(ctx, p, action, obj); err != nil {
		return fmt.Errorf("authorize %s on %s: %w", action, t, err)
	}

	return nil
}

// annotate adds the holder and the reason of the existing lock to an
// ErrAlreadyLocked, so that the caller sees who holds it.
func (s *Service) annotate(ctx context.Context, t Target, err error) error {
	if !errors.Is(err, ErrAlreadyLocked) {
		return err
	}

	held, getErr := s.store.GetLock(ctx, t)
	if getErr != nil {
		return err
	}

	return fmt.Errorf("%w: held %s", ErrAlreadyLocked, held.Detail())
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
