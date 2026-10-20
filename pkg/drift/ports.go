package drift

import (
	"context"
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
// other store does (ADR 72).
type Store interface {
	// Record stores r as the latest result of r.Unit and returns the change of
	// the drift status. In one transaction it checks the fence, checks the
	// job, writes the result, updates the drift status in the inventory
	// projection of the unit, and appends the notification of a change of the
	// status (ADR 145 decision 7). The dedupe key of the notification is the
	// unit ID and the job ID, so a repeated Record sends nothing twice.
	//
	// It returns:
	//   - ErrInvalid when r.Validate fails.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Unit.
	//   - ErrJobMismatch when r.JobID is not a job of r.Unit with the purpose
	//     drift check, in the state succeeded.
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
// write (ADR 60, fail closed).
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

// Checker starts drift checks through the job service. The REST route and the
// schedule both call it, so the rules of a job (the gates, the freeze, the
// lock and the limits of open jobs) apply to a drift check as to any job.
//
// The checks that a person starts need no setting. A setting only turns on the
// checks that start on their own.
type Checker interface {
	// Check starts a drift check of unit for the principal of actor and
	// returns the job ID. The unit settings give the runner pool. The idempotency
	// key is the unit ID and the minute, so a double click gives one job. It
	// returns the errors of job.Service.Start, for example job.ErrLocked,
	// job.ErrFrozen, job.ErrLimit, job.ErrUnsupported when the tool of the unit
	// has no refresh-only plan, and job.ErrPolicyDenied.
	Check(ctx context.Context, actor job.Actor, unit identity.Object, reason string) (job.ID, error)
	// Scheduled starts the drift check of unit that the schedule owes for
	// slot. The job has the source kind schedule (ADR 226 decision 4) and the
	// principal of the schedule. The idempotency key is the unit ID and slot,
	// so two instances that fire the same slot start one job. It returns the
	// errors of Check, and ErrNotFound when the policy that applies to the unit is off.
	Scheduled(ctx context.Context, unit graph.Unit, slot time.Time) (job.ID, error)
}
