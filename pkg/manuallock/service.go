package manuallock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces namespace.Store
	Now        func() time.Time
}

// Service implements the manual lock use cases. It reads the fence of the
// namespace before each write, so a write carries the failover version that
// the namespace has now.
type Service struct {
	store      Store
	namespaces namespace.Store
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when a port
// is missing. A nil Now uses time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: manual lock store and namespace store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, now: now}, nil
}

// Lock puts a manual lock on t for holder, the subject of the calling
// principal. The reason is required and has at most 500 characters. The audit
// entry by names the caller and the request; the service sets its time and its
// reason. A lock on an object that has one returns ErrAlreadyLocked, and the
// message names the holder.
func (s *Service) Lock(ctx context.Context, t Target, holder, reason string, by audit.Entry) (Lock, error) {
	if err := t.Valid(); err != nil {
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

	l := Lock{Since: s.now().UTC(), Target: t, Holder: holder, Reason: reason}

	by.At = l.Since
	by.Reason = reason

	if err := s.store.PutLock(ctx, l, fence, by); err != nil {
		return Lock{}, s.annotate(ctx, t, fmt.Errorf("lock %s: %w", t, err))
	}

	return l, nil
}

// Unlock removes the manual lock of t, which holder must hold. It returns
// ErrNotHolder when another principal holds it.
func (s *Service) Unlock(ctx context.Context, t Target, holder string, by audit.Entry) error {
	if holder == "" {
		return fmt.Errorf("unlock %s: %w", t, ErrNotHolder)
	}

	return s.remove(ctx, t, holder, by)
}

// ForceUnlock removes the manual lock of t whoever holds it. The caller has
// checked unit:manage on t. The audit entry by records the principal that
// forced the removal.
func (s *Service) ForceUnlock(ctx context.Context, t Target, by audit.Entry) error {
	return s.remove(ctx, t, "", by)
}

// Get returns the manual lock of t, or ErrNotLocked.
func (s *Service) Get(ctx context.Context, t Target) (Lock, error) {
	if err := t.Valid(); err != nil {
		return Lock{}, err
	}

	return s.store.GetLock(ctx, t)
}

// Covering returns the manual lock that covers the unit, or ErrNotLocked. A
// tool calls it before it takes its own lock.
func (s *Service) Covering(ctx context.Context, unit collection.UnitRef) (Lock, error) {
	return s.store.CoveringLock(ctx, unit)
}

func (s *Service) remove(ctx context.Context, t Target, holder string, by audit.Entry) error {
	if err := t.Valid(); err != nil {
		return err
	}

	fence, err := s.fence(ctx, t.Namespace())
	if err != nil {
		return err
	}

	by.At = s.now().UTC()

	if _, err := s.store.RemoveLock(ctx, t, holder, fence, by); err != nil {
		return fmt.Errorf("unlock %s: %w", t, err)
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

	return fmt.Errorf("%w: held by %s since %s: %s",
		ErrAlreadyLocked, held.Holder, held.Since.UTC().Format(time.RFC3339), held.Reason)
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}
