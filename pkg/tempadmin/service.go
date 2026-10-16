package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

// Service implements the temporary administrator use cases.
//
// Each change takes the audit entry of the caller: who acted, from where,
// and through which channel. The Service sets the time from its own clock,
// the scope, the kind change, the outcome success and the record, and the
// store appends the entry in the transaction of the change.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService returns a Service. Store is required.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: temporary administrator store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, now: now}, nil
}

// Now returns the time of the Service clock, in UTC to the millisecond:
// the time a change and its audit entry carry.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Input names the fields of a new temporary administrator. Subject is the
// name of the person the operator passes on the host. CreatedBy names the
// host operator that runs the host command.
type Input struct {
	Subject     string
	PrincipalID string
	CreatedBy   string
}

// Create records the principal principalID as a temporary administrator of
// the person subject, with the entry e of the host operator. The expiry is
// fixed: exactly Lifetime after the create, and nothing extends it. The
// principal itself already exists: the host command makes it first, or
// reuses the record of a person that lost access.
func (s *Service) Create(ctx context.Context, e audit.Entry, in Input) (TempAdmin, error) {
	if err := principal.ValidateSubject(in.Subject); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	if in.PrincipalID == "" {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w: missing principal", ErrInvalid)
	}

	if in.CreatedBy == "" {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w: missing creator", ErrInvalid)
	}

	at := s.Now()

	r := TempAdmin{
		CreatedAt:   at,
		ExpiresAt:   at.Add(Lifetime),
		PrincipalID: in.PrincipalID,
		Subject:     in.Subject,
		CreatedBy:   in.CreatedBy,
	}

	if err := r.Validate(); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	if err := s.store.CreateTempAdmin(ctx, r, change(e, at, Ref(r.PrincipalID))); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator for %q: %w", in.Subject, err)
	}

	return r, nil
}

// Get returns the record of the principal principalID.
func (s *Service) Get(ctx context.Context, principalID string) (TempAdmin, error) {
	r, err := s.store.GetTempAdmin(ctx, principalID)
	if err != nil {
		return TempAdmin{}, fmt.Errorf("get temporary administrator: %w", err)
	}

	return r, nil
}

// ExpiredAt reports the records that end at or before now, ordered by
// expiry, so the sweep disables each of them.
func (s *Service) ExpiredAt(ctx context.Context, now time.Time) ([]TempAdmin, error) {
	all, err := s.store.ListTempAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list temporary administrators: %w", err)
	}

	var out []TempAdmin

	for _, r := range all {
		if !r.ActiveAt(now) {
			out = append(out, r)
		}
	}

	return out, nil
}

// Sweep disables every record that ends at or before the time of the
// Service clock, each with an entry from e, and returns the principal IDs
// it disabled, ordered by expiry. A record that already ended is skipped:
// ExpireTempAdmin answers ErrExpired for it and writes no second entry.
func (s *Service) Sweep(ctx context.Context, e audit.Entry) ([]string, error) {
	now := s.Now()

	expired, err := s.ExpiredAt(ctx, now)
	if err != nil {
		return nil, err
	}

	var done []string

	for _, r := range expired {
		err := s.store.ExpireTempAdmin(ctx, r.PrincipalID, expireEntry(e, now, Ref(r.PrincipalID)))
		if errors.Is(err, ErrExpired) || errors.Is(err, ErrNotFound) {
			continue
		}

		if err != nil {
			return done, fmt.Errorf("expire temporary administrator for %q: %w", r.Subject, err)
		}

		done = append(done, r.PrincipalID)
	}

	return done, nil
}

// change completes the entry of the caller for one change of the record
// object at the time at.
func change(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object
	e.Action = Action

	return e
}

// expireEntry completes the entry of a sweep for the expiry of one record:
// the subject SweepSubject, because the server runs the sweep on its own
// schedule and no person acts.
func expireEntry(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object
	e.Action = Action
	e.Subject = SweepSubject

	return e
}

// SweepSubject is the subject of the audit entries of an expiry that the
// server runs on its own schedule. It starts with '@', which no subject of
// a principal holds, so it never names a principal.
const SweepSubject = "@server"
