package drift

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the latest drift result of each unit. A SQL adapter serves it
// from the database of the namespace. Every write carries the fence of the
// namespace, and the store checks it in the transaction of the write, as every
// other store does.
type Store interface {
	// Record stores r as the latest result of r.Unit and returns the change of
	// the drift status. In one transaction it checks the fence, checks the
	// job, writes the result, updates the drift status in the inventory
	// projection of the unit, and appends the notification of a change of the
	// status. The dedupe key of the notification is the unit ID and the job ID,
	// so a repeated Record sends nothing twice.
	//
	// It returns:
	//   - ErrInvalid when r.Validate fails, and when the unit ID of r.Unit
	//     holds the result of another namespace or another tool (for example
	//     after a restore or a move).
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Unit.
	//   - ErrJobMismatch when r.JobID is not a job of r.Unit with the purpose
	//     drift check, in the state succeeded (a failed, canceled or running
	//     job is refused), and when r.CheckedAt is outside the run of the job,
	//     from its start to its end. A runner is not trusted, so a time far in
	//     the future cannot freeze the status.
	//   - ErrStale when r.CheckedAt is not after the stored CheckedAt. A second
	//     Record of the same job also returns ErrStale and changes nothing.
	//
	// On every error the store changes nothing.
	Record(ctx context.Context, r Result, fence namespace.Fence) (Transition, error)
	// GetLatest returns the latest result of unit. It returns ErrNotFound when no
	// check has been recorded. A reader treats that as the status
	// StatusUnknown. GetLatest does not check access. The caller does.
	GetLatest(ctx context.Context, unit graph.Unit) (Latest, error)
	// DeleteLatest removes the latest result of unit and clears the drift status in
	// the inventory projection. A unit with no result is not an error. The
	// purge of a unit calls it. It returns namespace.ErrFenced.
	DeleteLatest(ctx context.Context, unit graph.Unit, fence namespace.Fence) error
}

// PolicyStore keeps the drift schedule settings. Its method names differ from
// those of Store, so one adapter type can implement both. A write carries the fence of
// the namespace and the audit entry e of the change. The caller stamps the
// time and the actor on e. The store appends it in the transaction of the
// write (fail closed).
type PolicyStore interface {
	// GetPolicy returns the policy that the scope sets itself. It returns
	// ErrNotFound when the scope sets none, and ErrInvalid for a scope that
	// fails Scope.Validate.
	GetPolicy(ctx context.Context, scope Scope) (Policy, error)
	// PutPolicy sets the policy of the scope and replaces an earlier one. It returns
	// ErrInvalid when the scope or the policy fails validation, and
	// namespace.ErrFenced.
	PutPolicy(ctx context.Context, scope Scope, p Policy, fence namespace.Fence, e audit.Entry) error
	// DeletePolicy removes the policy of the scope, so the scope inherits again. It
	// returns ErrNotFound when the scope sets none, ErrInvalid, and
	// namespace.ErrFenced.
	DeletePolicy(ctx context.Context, scope Scope, fence namespace.Fence, e audit.Entry) error
	// EffectivePolicy returns the policy that applies to unit, which is in the
	// collection named collection (empty when it has none). It reads the three
	// scopes in one transaction and applies Resolve. It returns the zero
	// Policy, which is off, when no scope sets a policy. It returns ErrInvalid
	// for a unit or a collection name that fails validation.
	EffectivePolicy(ctx context.Context, unit graph.Unit, collection string) (Policy, error)
}

// CheckRequest asks for one drift check of a unit that a person starts.
type CheckRequest struct {
	// Unit is the unit reference: the tool, the namespace and the name of the
	// unit. A stable unit ID on it is ignored: the job service finds its own.
	Unit identity.Object
	// Location and Pool are empty to take the defaults of the job service. A
	// request that names them needs the extra actions that job.StartRequest
	// names.
	Location job.Location
	Pool     string
	// Reason is the text that the caller gives. The audit entry of the start
	// holds it.
	Reason string
	// Channel, SourceIP, UserAgent and RequestID describe the request for the
	// audit entry. The caller bounds them.
	Channel   audit.Channel
	SourceIP  string
	UserAgent string
	RequestID string
}

// Validate reports whether r names a unit and has a reason that the server can
// keep. An empty reason is valid. It returns an error that wraps ErrInvalid.
func (r CheckRequest) Validate() error {
	if !r.Unit.IsUnit() {
		return fmt.Errorf("%w: a drift check needs a unit", ErrInvalid)
	}

	if err := validateText(r.Reason); err != nil {
		return fmt.Errorf("reason: %w", err)
	}

	return nil
}

// Checker starts a drift check that a person asks for, through the job
// service. The REST route calls it, so the rules of a job (the gates, the
// manual lock and the limits of open jobs) apply to a drift check as to any
// job. A change freeze never holds a drift check, because a freeze holds only
// the phases that change something.
//
// The checks that a person starts need no setting. A setting only turns on the
// checks that start on their own.
type Checker interface {
	// Check starts a drift check of req.Unit for the principal of actor and
	// returns the job ID. The idempotency key is the unit and the minute, so
	// a double click gives one job. A manual lock on the unit does not make
	// Check fail: the job service queues the job with the wait reason
	// locked. Check returns the other errors of job.Service.Start, for
	// example job.ErrLimit, job.ErrUnsupported when the tool of the unit has
	// no refresh-only plan, and job.ErrPolicyDenied.
	Check(ctx context.Context, actor job.Actor, req CheckRequest) (job.ID, error)
}

// ScheduledChecker starts the drift checks that the schedule owes. The slice
// of the drift schedule implements it, and no adapter does yet.
type ScheduledChecker interface {
	// Scheduled starts the drift check of unit that the schedule owes for
	// slot. The job has the source kind schedule and the principal of the
	// schedule. The idempotency key is the unit ID and slot, so two instances
	// that fire the same slot start one job. It returns the errors of Check,
	// and ErrDisabled when the policy that applies to the unit is off.
	Scheduled(ctx context.Context, unit graph.Unit, slot time.Time) (job.ID, error)
}
