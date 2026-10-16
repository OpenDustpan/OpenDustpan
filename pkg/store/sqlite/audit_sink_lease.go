package sqlite

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.SinkLeaser = (*Store)(nil)

// AcquireSinkLease implements audit.SinkLeaser. It takes or renews the lease
// of sink for holder for ttl from the database clock, in one conditional
// write in a transaction. It answers true when the caller holds the lease
// after the call and false while another instance holds a live lease.
func (s *Store) AcquireSinkLease(ctx context.Context, sink, holder string, ttl time.Duration) (bool, error) {
	if sink == "" || holder == "" || ttl <= 0 {
		return false, fmt.Errorf("%w: sink %q holder %q ttl %s", audit.ErrInvalidSinkLease, sink, holder, ttl)
	}

	ttlMs := ttl.Milliseconds()
	if ttlMs <= 0 {
		return false, fmt.Errorf("%w: ttl %s is below one millisecond", audit.ErrInvalidSinkLease, ttl)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var changed int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		n, err := q.AcquireAuditSinkLease(ctx, db.AcquireAuditSinkLeaseParams{
			Sink:   sink,
			Holder: holder,
			TtlMs:  ttlMs,
		})
		if err != nil {
			return fmt.Errorf("acquire audit sink lease: %w", err)
		}

		changed = n

		return nil
	})
	if err != nil {
		return false, err
	}

	return changed == 1, nil
}

// ReleaseSinkLease implements audit.SinkLeaser. It releases the lease of sink
// only when holder holds it, in one conditional write in a transaction. It
// changes nothing when no row exists or another instance holds the lease.
func (s *Store) ReleaseSinkLease(ctx context.Context, sink, holder string) error {
	if sink == "" || holder == "" {
		return fmt.Errorf("%w: sink %q holder %q", audit.ErrInvalidSinkLease, sink, holder)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.inTx(ctx, func(q *db.Queries) error {
		_, err := q.ReleaseAuditSinkLease(ctx, db.ReleaseAuditSinkLeaseParams{
			Sink:   sink,
			Holder: holder,
		})
		if err != nil {
			return fmt.Errorf("release audit sink lease: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
