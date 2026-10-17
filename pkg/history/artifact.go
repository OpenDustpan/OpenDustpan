package history

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// Sentinel errors of the plan and job log ports.
var (
	// ErrPlanNotFound reports a plan that does not exist.
	ErrPlanNotFound = errors.New("plan not found")
	// ErrPlanExists reports a second upload of a plan ID.
	ErrPlanExists = errors.New("plan already exists")
	// ErrJobLogNotFound reports a job log that does not exist.
	ErrJobLogNotFound = errors.New("job log not found")
	// ErrJobLogExists reports a second job log for one job.
	ErrJobLogExists = errors.New("job log already exists")
	// ErrJobLogClosed reports a chunk for a job log that the end of its job
	// closed.
	ErrJobLogClosed = errors.New("job log is closed")
	// ErrChunkConflict reports a chunk with the sequence number of a stored
	// chunk and other bytes.
	ErrChunkConflict = errors.New("job log chunk conflicts with a stored chunk")
	// ErrChunkOutOfOrder reports a chunk whose sequence number is not the
	// next one.
	ErrChunkOutOfOrder = errors.New("job log chunk is out of order")
	// ErrInvalidRetention reports a retention period below MinRetention.
	ErrInvalidRetention = errors.New("invalid retention period")
)

// Retention periods of plans and job logs.
const (
	// DefaultPlanRetention is the time that the server keeps a plan from its
	// creation: 400 days.
	DefaultPlanRetention = 400 * 24 * time.Hour
	// DefaultJobLogRetention is the time that the server keeps a job log
	// from the start of its job: 400 days.
	DefaultJobLogRetention = 400 * 24 * time.Hour
	// MinRetention is the shortest period that a setting accepts.
	MinRetention = 24 * time.Hour
)

// Retention holds the retention periods of plans and job logs for an
// installation. The settings --plan-retention and --job-log-retention set
// them.
type Retention struct {
	Plans   time.Duration
	JobLogs time.Duration
}

// DefaultRetention returns the default periods.
func DefaultRetention() Retention {
	return Retention{Plans: DefaultPlanRetention, JobLogs: DefaultJobLogRetention}
}

// Validate reports whether each period is at least MinRetention. It returns
// ErrInvalidRetention otherwise.
func (r Retention) Validate() error {
	if r.Plans < MinRetention {
		return fmt.Errorf("%w: plan retention %v is below %v", ErrInvalidRetention, r.Plans, MinRetention)
	}

	if r.JobLogs < MinRetention {
		return fmt.Errorf("%w: job log retention %v is below %v", ErrInvalidRetention, r.JobLogs, MinRetention)
	}

	return nil
}

// PlanExpiry returns the expiry time of a plan made at created. The server
// stores it with the plan, so a later change of the period changes only
// new plans.
func (r Retention) PlanExpiry(created time.Time) time.Time {
	return created.Add(r.Plans)
}

// JobLogExpiry returns the expiry time of the log of a job that started at
// started.
func (r Retention) JobLogExpiry(started time.Time) time.Time {
	return started.Add(r.JobLogs)
}

// PlanID is the stable, opaque ID of one plan, with the prefix "pln-".
type PlanID string

// planPrefix starts every plan ID.
const planPrefix = "pln-"

// NewPlanID returns a new random plan ID.
func NewPlanID() PlanID {
	return PlanID(ident.NewID(planPrefix))
}

// Part is one blob of a plan. The tool package names the parts and owns
// their format: for OpenTofu "plan" (the plan file) and "json" (its JSON
// rendering), for the Pulumi CLI "plan" (the update plan) and "events" (the
// engine events of the preview). Secret is true when the part holds plain
// secret values, so a read of it also needs the action that reads secret
// values, and writes an audit entry.
type Part struct {
	Name   string
	Blob   blob.Key
	Size   int64
	SHA256 [sha256.Size]byte
	Secret bool
}

// Plan is the record of one stored plan. Its content key seals every part.
// Redaction is empty while the content exists.
type Plan struct {
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Unit       Unit
	ID         PlanID
	JobID      string
	By         Actor
	ContentKey secret.ContentKeyID
	Redaction  RedactionID
	Parts      []Part
}

// Item returns the history item reference of the plan.
func (p Plan) Item() ItemRef {
	return ItemRef{Unit: p.Unit, Kind: secret.ContentKindPlan, Item: string(p.ID)}
}

// PlanCursor is the position after which a page of plans starts: the order
// key of the last plan of the page before.
type PlanCursor struct {
	CreatedAt time.Time
	ID        PlanID
}

// PlanStore is the persistence port of stored plans. A plan is immutable
// after its upload, except for its redaction. The store redacts plans
// through Redactor.
type PlanStore interface {
	Redactor
	// CreatePlan stores p after its part blobs exist. It returns
	// ErrPlanExists for a plan ID that exists, and namespace.ErrFenced when
	// the fence is stale.
	CreatePlan(ctx context.Context, p Plan, fence namespace.Fence) error
	// GetPlan returns the plan id of ns, or ErrPlanNotFound.
	GetPlan(ctx context.Context, ns namespace.Name, id PlanID) (Plan, error)
	// ListPlans returns at most limit plans of unit, newest first by
	// creation time, then by ID. With a cursor, it returns only the plans
	// after the cursor in that order.
	ListPlans(ctx context.Context, unit Unit, after *PlanCursor, limit int) ([]Plan, error)
	// ListExpiredPlans returns at most limit plans of every namespace that
	// are not redacted and whose expiry time is at or before now, ordered by
	// expiry time, then by ID.
	ListExpiredPlans(ctx context.Context, now time.Time, limit int) ([]Plan, error)
}

// JobLog is the record of the log of one job. Its content key seals every
// chunk. ClosedAt is the zero time while the job runs. Size, SHA256 and
// Chunks hold the totals over the chunks in order, from the close.
// Redaction is empty while the content exists.
type JobLog struct {
	StartedAt  time.Time
	ClosedAt   time.Time
	ExpiresAt  time.Time
	Unit       Unit
	JobID      string
	By         Actor
	ContentKey secret.ContentKeyID
	Redaction  RedactionID
	Size       int64
	Chunks     int64
	SHA256     [sha256.Size]byte
}

// Closed reports whether the end of the job closed the log.
func (l JobLog) Closed() bool {
	return !l.ClosedAt.IsZero()
}

// Item returns the history item reference of the job log.
func (l JobLog) Item() ItemRef {
	return ItemRef{Unit: l.Unit, Kind: secret.ContentKindJobLog, Item: l.JobID}
}

// Chunk is one blob of a job log. Seq starts at 0 and has no gap.
type Chunk struct {
	Blob   blob.Key
	Seq    int64
	Size   int64
	SHA256 [sha256.Size]byte
}

// JobLogTotals closes a job log: the close time, and the size and the
// SHA-256 over its chunks in order.
type JobLogTotals struct {
	ClosedAt time.Time
	Size     int64
	SHA256   [sha256.Size]byte
}

// JobLogStore is the persistence port of job logs. The store redacts job
// logs through Redactor: a log that is not closed answers ErrInProgress.
type JobLogStore interface {
	Redactor
	// CreateJobLog stores l for a job that starts. It returns
	// ErrJobLogExists when the job has a log, and namespace.ErrFenced when
	// the fence is stale.
	CreateJobLog(ctx context.Context, l JobLog, fence namespace.Fence) error
	// AppendChunk stores c as the next chunk of the log of jobID, after its
	// blob exists. A chunk with the sequence number and the SHA-256 of a
	// stored chunk answers nil and changes nothing, so a runner can send a
	// chunk again. It returns ErrJobLogNotFound, ErrJobLogClosed,
	// ErrChunkConflict for a stored sequence number with another SHA-256,
	// ErrChunkOutOfOrder for a gap, and namespace.ErrFenced when the fence
	// is stale.
	AppendChunk(ctx context.Context, ns namespace.Name, jobID string, c Chunk, fence namespace.Fence) error
	// CloseJobLog closes the log of jobID with totals, and returns the
	// stored log. A second close answers the stored log and changes nothing.
	// It returns ErrJobLogNotFound, and namespace.ErrFenced when the fence is
	// stale.
	CloseJobLog(
		ctx context.Context, ns namespace.Name, jobID string, totals JobLogTotals, fence namespace.Fence,
	) (JobLog, error)
	// GetJobLog returns the log of jobID in ns, or ErrJobLogNotFound.
	GetJobLog(ctx context.Context, ns namespace.Name, jobID string) (JobLog, error)
	// ListChunks returns at most limit chunks of the log of jobID with a
	// sequence number above after (-1 for the first page), in order. It
	// returns ErrJobLogNotFound.
	ListChunks(ctx context.Context, ns namespace.Name, jobID string, after int64, limit int) ([]Chunk, error)
	// ListExpiredJobLogs returns at most limit closed logs of every
	// namespace that are not redacted and whose expiry time is at or before
	// now, ordered by expiry time, then by job ID.
	ListExpiredJobLogs(ctx context.Context, now time.Time, limit int) ([]JobLog, error)
}
