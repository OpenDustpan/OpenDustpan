package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Reviews opens the second-person review of a temporary administrator
// after its end.
type Reviews interface {
	// OpenReview opens the review of the use u. A repeated open of the
	// same use makes no second review.
	OpenReview(ctx context.Context, u breakglass.Use) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Store Store
	// Reviews opens the review of each record that a sweep ends. Nil
	// opens no review.
	Reviews Reviews
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
	store   Store
	reviews Reviews
	now     func() time.Time
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

	return &Service{store: cfg.Store, reviews: cfg.Reviews, now: now}, nil
}

// Now returns the time of the Service clock, in UTC to the millisecond:
// the time a change and its audit entry carry.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Input names the fields of a new temporary administrator. Subject is the
// name of the person the operator passes on the host. CreatedBy names the
// host operator that runs the host command, and Reason holds the text that
// the host operator gave.
type Input struct {
	Subject     string
	PrincipalID string
	CreatedBy   string
	Reason      string
}

// Create records the principal principalID as a temporary administrator of
// the person subject, with the entry e of the host operator. The expiry is
// Lifetime after the create, and nothing extends it. The
// principal itself already exists: the host command makes it first, or
// reuses the record of a person that lost access.
func (s *Service) Create(ctx context.Context, e audit.Entry, in Input) (TempAdmin, error) {
	return s.insertRecord(ctx, e, in, Lifetime)
}

// CreateWithLifetime records the principal principalID as a temporary
// administrator of the person subject, with the entry e of the host
// operator. The expiry is lifetime after the create, which must be within
// one millisecond to Lifetime, and nothing extends it. The host command
// passes the token policy maximum when that is below Lifetime, so the
// record and the token end together.
func (s *Service) CreateWithLifetime(
	ctx context.Context, e audit.Entry, in Input, lifetime time.Duration,
) (TempAdmin, error) {
	return s.insertRecord(ctx, e, in, lifetime)
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
// it disabled, ordered by expiry. It opens the review of each record that
// it ends, when the Service has Reviews. A record that already ended is
// skipped: ExpireTempAdmin answers ErrExpired for it and writes no second
// entry.
func (s *Service) Sweep(ctx context.Context, e audit.Entry) ([]string, error) {
	now := s.Now()

	expired, err := s.ExpiredAt(ctx, now)
	if err != nil {
		return nil, err
	}

	var done []string

	for _, r := range expired {
		err := s.store.ExpireTempAdmin(ctx, r.PrincipalID, expireEntry(e, now, Ref(r.PrincipalID)))
		if errors.Is(err, ErrNotFound) {
			continue
		}

		if err != nil && !errors.Is(err, ErrExpired) {
			return done, fmt.Errorf("expire temporary administrator for %q: %w", r.Subject, err)
		}

		// A sign-in after the expiry can end the record first, so the
		// review opens for each ended record. An open of a review that
		// exists makes no second review.
		if openErr := s.openReview(ctx, r); openErr != nil {
			return done, openErr
		}

		if err == nil {
			done = append(done, r.PrincipalID)
		}
	}

	return done, nil
}

// openReview opens the review of the ended record r, when the Service has
// Reviews.
func (s *Service) openReview(ctx context.Context, r TempAdmin) error {
	if s.reviews == nil {
		return nil
	}

	if err := s.reviews.OpenReview(ctx, r.ReviewUse(r.ExpiresAt)); err != nil {
		return fmt.Errorf("open the review of temporary administrator %q: %w", r.Subject, err)
	}

	return nil
}

// insertRecord shares the create of Create and CreateWithLifetime.
func (s *Service) insertRecord(
	ctx context.Context, e audit.Entry, in Input, want time.Duration,
) (TempAdmin, error) {
	if err := principal.ValidateSubject(in.Subject); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	if in.PrincipalID == "" {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w: missing principal", ErrInvalid)
	}

	if in.CreatedBy == "" {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w: missing creator", ErrInvalid)
	}

	lifetime, err := lifetimeOf(want)
	if err != nil {
		return TempAdmin{}, err
	}

	at := s.Now()

	r := TempAdmin{
		CreatedAt:   at,
		ExpiresAt:   at.Add(lifetime),
		PrincipalID: in.PrincipalID,
		Subject:     in.Subject,
		CreatedBy:   in.CreatedBy,
		Reason:      in.Reason,
	}

	if err := r.Validate(); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	if err := s.store.CreateTempAdmin(ctx, r, change(e, at, Ref(r.PrincipalID))); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator for %q: %w", in.Subject, err)
	}

	return r, nil
}

// lifetimeOf answers the lifetime of a create: Lifetime when want is zero,
// or want when it is within one millisecond to Lifetime.
func lifetimeOf(want time.Duration) (time.Duration, error) {
	if want == 0 {
		return Lifetime, nil
	}

	if want <= 0 || want > Lifetime || want%time.Millisecond != 0 {
		return 0, fmt.Errorf("create temporary administrator: %w: lifetime %s is not 1ms to %s",
			ErrInvalid, want, Lifetime)
	}

	return want, nil
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
