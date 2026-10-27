package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// Logger takes a warning for each record that a sweep ends with no
	// review, and for each expiry that fails. Nil discards them.
	Logger *slog.Logger
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
	logger  *slog.Logger
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

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Service{store: cfg.Store, reviews: cfg.Reviews, now: now, logger: logger}, nil
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

	for i := range all {
		if !all[i].ActiveAt(now) {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// Sweep ends every record that ends at or before the time of the Service
// clock and that did not end yet, each with an entry from e, and returns
// the principal IDs it ended, ordered by expiry. The entry names the
// subject of e, or SweepSubject when e names none. When the Service has
// Reviews, it opens the review of each record that it ends, and then of
// each earlier ended record whose review is not marked as opened, so a
// failed open retries at the next sweep. OpenReview makes no second review
// for a use, and a record leaves the retry once its mark is set. When the
// Service has no Reviews, it logs a warning for each record that it ends
// and retries nothing. A failed expiry or review of one record does not
// stop the sweep: Sweep logs it with no driver text, continues with the
// next record, and at the end returns ErrSweepIncomplete with the IDs it
// ended.
func (s *Service) Sweep(ctx context.Context, e audit.Entry) ([]string, error) {
	now := s.Now()

	expired, err := s.ExpiredAt(ctx, now)
	if err != nil {
		return nil, err
	}

	if e.Subject == "" {
		e.Subject = SweepSubject
	}

	var done []string

	failed := 0

	for i := range expired {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return done, fmt.Errorf("sweep temporary administrators: %w", ctxErr)
		}

		r := &expired[i]
		if !r.EndedAt.IsZero() {
			continue
		}

		err := s.store.ExpireTempAdmin(ctx, r.PrincipalID, expireEntry(e, now, Ref(r.PrincipalID)))

		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrExpired):
			// A delete or a use after the expiry ended the record first.
		case err != nil:
			failed++

			s.logger.WarnContext(ctx, "temporary administrator expiry failed",
				slog.String("principal", r.PrincipalID))
		default:
			done = append(done, r.PrincipalID)

			if s.reviews == nil {
				warnNoReview(ctx, s.logger, r.PrincipalID)
			}
		}
	}

	failed += s.retryReviews(ctx)

	if failed > 0 {
		return done, fmt.Errorf("%w: %d of %d records", ErrSweepIncomplete, failed, len(expired))
	}

	return done, nil
}

// retryReviews opens the review of each ended record whose review is not
// marked as opened, and marks the record after each open. It returns the
// count of records whose open or mark failed: the next sweep tries them
// again. With no Reviews it does nothing, because no review can open.
func (s *Service) retryReviews(ctx context.Context) int {
	if s.reviews == nil {
		return 0
	}

	pending, err := s.store.ListTempAdminsAwaitingReview(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "list the temporary administrators that await a break-glass review failed")

		return 1
	}

	failed := 0

	for i := range pending {
		if ctx.Err() != nil {
			return failed
		}

		r := &pending[i]

		if err := s.reviews.OpenReview(ctx, r.ReviewUse(r.ExpiresAt)); err != nil {
			failed++

			s.logger.WarnContext(ctx, "open the break-glass review of a temporary administrator failed",
				slog.String("principal", r.PrincipalID))

			continue
		}

		if err := s.store.MarkTempAdminReviewOpened(ctx, r.PrincipalID, s.Now()); err != nil &&
			!errors.Is(err, ErrNotFound) {
			failed++

			s.logger.WarnContext(ctx, "mark the break-glass review of a temporary administrator as opened failed",
				slog.String("principal", r.PrincipalID))
		}
	}

	return failed
}

// warnNoReview logs that a temporary administrator ended and that no
// break-glass review opens for it.
func warnNoReview(ctx context.Context, logger *slog.Logger, principalID string) {
	logger.WarnContext(ctx,
		"temporary administrator ended: no break-glass review opens for it yet: "+
			"review its audit entries by hand",
		slog.String("principal", principalID))
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
		CreatedAt:      at,
		ExpiresAt:      at.Add(lifetime),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    in.PrincipalID,
		Subject:        in.Subject,
		CreatedBy:      in.CreatedBy,
		Reason:         in.Reason,
	}

	if err := r.Validate(); err != nil {
		return TempAdmin{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	entry := change(e, at, Ref(r.PrincipalID))
	entry.Reason = r.Reason

	if err := s.store.CreateTempAdmin(ctx, r, entry); err != nil {
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

// expireEntry completes the entry e of the caller for the expiry of one
// record at the time at. The subject stays the subject of e: SweepSubject
// for the server, HostSubject for the host command.
func expireEntry(e audit.Entry, at time.Time, object string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = object
	e.Action = Action

	return e
}

// SweepSubject is the subject of the audit entries of an expiry that the
// server runs on its own schedule. It starts with '@', which no subject of
// a principal holds, so it never names a principal.
const SweepSubject = "@server"
