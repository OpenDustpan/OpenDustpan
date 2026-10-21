package drift

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
)

// JobStarter is the part of job.Service that a drift check uses.
type JobStarter interface {
	// Start starts the job of req for the actor.
	Start(ctx context.Context, actor job.Actor, req job.StartRequest) (job.Job, error)
}

// Service starts drift checks through the job service.
type Service struct {
	starter JobStarter
	now     func() time.Time
}

var _ Checker = (*Service)(nil)

// NewService returns the Service over starter. now is the clock, and time.Now
// when nil. It returns ErrInvalid for a nil starter.
func NewService(starter JobStarter, now func() time.Time) (*Service, error) {
	if starter == nil {
		return nil, fmt.Errorf("%w: a drift check needs a job service", ErrInvalid)
	}

	if now == nil {
		now = time.Now
	}

	return &Service{starter: starter, now: now}, nil
}

// Check implements Checker. The job is the operation refresh with the purpose
// drift check, and the source kind is api: only a feature of the server may
// set another source kind. The service takes the runner pool and the code
// location from the request or from the defaults of the job service.
func (s *Service) Check(ctx context.Context, actor job.Actor, req CheckRequest) (job.ID, error) {
	if !req.Unit.IsUnit() {
		return "", fmt.Errorf("%w: a drift check needs a unit", ErrInvalid)
	}

	j, err := s.starter.Start(ctx, actor, job.StartRequest{
		Unit: req.Unit, Operation: job.OperationRefresh, Purpose: job.PurposeDriftCheck, Executor: "",
		Location: req.Location, Pool: req.Pool, Edits: nil, Delivery: "", Source: job.SourceAPI,
		IdempotencyKey: checkKey(req.Unit.String(), s.now()), Reason: req.Reason, BreakGlass: nil,
		Channel: req.Channel, SourceIP: req.SourceIP, UserAgent: req.UserAgent, RequestID: req.RequestID,
	})
	if err != nil {
		return "", fmt.Errorf("start drift check: %w", err)
	}

	return j.ID, nil
}

// CheckKeyPrefix starts every idempotency key that a drift check takes. The
// jobs API refuses a key from a caller that starts with it, so a caller cannot
// take the key of a drift check first.
const CheckKeyPrefix = "drift-"

// checkKey returns the idempotency key of a drift check of the unit ref at
// time at: the same key for the same unit in the same minute, so a double click
// gives one job. The key is the hash of the reference and the minute, so it is
// short printable ASCII whatever the name of the unit holds.
func checkKey(ref string, at time.Time) string {
	sum := sha256.Sum256([]byte(ref))

	return CheckKeyPrefix + hex.EncodeToString(sum[:16]) + "-" + strconv.FormatInt(at.Unix()/60, 10)
}
