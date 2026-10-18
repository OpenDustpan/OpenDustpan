package cost

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// EstimateInput is what an estimator reads. Plan is the machine-readable
// plan of the tool with every secret value masked, the same document that
// a person with the action job:read-plans can read. It never holds a
// secret value. Tool is the tool name, for example "OpenTofu".
type EstimateInput struct {
	Unit     string
	Tool     string
	Plan     []byte
	Currency string
}

// Estimator prices a plan. The server calls it after a plan phase stores
// its plan, in a durable operation, so a slow price source never holds a
// job. An adapter can call a price service, read a price list, or run an
// estimator program. A team that runs its estimator in a step of a runner
// job sends the result through the API instead, with the source "api".
type Estimator interface {
	// Name returns the name and the version of the adapter, for
	// Estimate.Source.
	Name() string
	// Estimate returns the cost of each resource of in.Plan, in
	// in.Currency. It returns ErrUnsupported for a tool or a plan format
	// that it cannot read, and ErrUnavailable when its price source does
	// not answer. A resource that it cannot price is in the result,
	// unpriced, with a note.
	Estimate(ctx context.Context, in EstimateInput) ([]ResourceCost, error)
}

// Period is one calendar month, in UTC.
type Period struct {
	Year  int
	Month time.Month
}

// PeriodOf returns the month of t, in UTC.
func PeriodOf(t time.Time) Period {
	y, m, _ := t.UTC().Date()

	return Period{Year: y, Month: m}
}

// Start returns the first moment of p.
func (p Period) Start() time.Time {
	return time.Date(p.Year, p.Month, 1, 0, 0, 0, 0, time.UTC)
}

// End returns the first moment after p.
func (p Period) End() time.Time {
	return time.Date(p.Year, p.Month+1, 1, 0, 0, 0, 0, time.UTC)
}

// UnitKey names a unit for a spend tool: its reference and its stable ID.
// A spend tool finds the resources of a unit by a tag that holds the ID,
// or by its own mapping.
type UnitKey struct {
	Ref string
	ID  string
}

// SpendQuery asks for the spend of the units of a namespace in a month.
type SpendQuery struct {
	Namespace namespace.Name
	Units     []UnitKey
	Period    Period
	Currency  string
}

// SpendLine is the spend of one unit in the month of a query. Unit is
// empty for spend that the tool gives to the namespace but to no unit.
// Actual is the spend so far. Forecast is the forecast of the tool for
// the whole month, or a zero amount when it gives none. AsOf is the time
// of the data of the tool.
type SpendLine struct {
	Unit     string
	Actual   Money
	Forecast Money
	AsOf     time.Time
}

// SpendSource reads actual spend from a spend or cost tool.
type SpendSource interface {
	// Spend returns the spend of q, one line for each unit with spend,
	// in q.Currency. It returns ErrUnsupported for a currency or a period
	// that the tool cannot give, and ErrUnavailable when the tool does
	// not answer.
	Spend(ctx context.Context, q SpendQuery) ([]SpendLine, error)
}

// EstimateStore is the persistence port for estimates.
//
// An estimate is a write in the namespace of its row, Estimate.Namespace,
// and carries the fence of that namespace. A zero fence is valid only for
// an estimate in audit.ServerScope, the estimate of a blueprint of the
// installation, and a fence is refused for such an estimate.
//
// A subject keeps one estimate of each origin (Estimate.Origin): the
// estimate that a client sent and the estimate of an Estimator adapter.
// An estimate replaces only the estimate of its subject and its origin,
// so an estimate of one origin never replaces the other silently.
type EstimateStore interface {
	// PutEstimate stores est and replaces the estimate of the same
	// subject and origin. It appends the audit entry when the estimate
	// came from a client through the API, and appends nothing when entry
	// is nil. It returns ErrInvalid for an estimate that fails Validate,
	// for a fence of another namespace than est.Namespace, for a zero
	// fence of an estimate in a namespace, for a fence of an estimate in
	// audit.ServerScope, for a subject whose stored estimate is in another
	// namespace, and for an entry whose Namespace is not est.Namespace. It
	// returns namespace.ErrFenced for a stale fence and
	// namespace.ErrNotFound for an unknown namespace.
	PutEstimate(ctx context.Context, est Estimate, fence namespace.Fence, entry *audit.Entry) error
	// GetEstimate returns the estimate of s and o, or ErrNotFound.
	GetEstimate(ctx context.Context, s Subject, o Origin) (Estimate, error)
	// PendingEstimates returns the estimates of the plans of the jobs of
	// ns that wait or run and that have not applied their plan: for each
	// job, the estimates of its highest phase with an estimate, one of
	// each origin. The budget of the namespace counts them as pending,
	// each job once.
	PendingEstimates(ctx context.Context, ns namespace.Name) ([]Estimate, error)
}

// BudgetStore is the persistence port for budgets and their last status.
//
// A budget and its status are writes in the namespace of the budget. Each
// write carries the fence of that namespace. A budget is never in
// audit.ServerScope, so a zero fence is always refused. A write of a
// stored budget, found by its ID, returns ErrNotFound when the fence
// belongs to another namespace than the budget, so that a caller cannot
// learn of a budget in a namespace that it does not hold. A stale fence
// returns namespace.ErrFenced. A write with an audit entry returns
// ErrInvalid for an entry whose Namespace is not the namespace of the
// budget, and changes nothing, so a write never appends to the audit log
// of another namespace.
type BudgetStore interface {
	// CreateBudget persists b with version 1 and appends the audit entry
	// e in its transaction. It returns ErrInvalid for a budget that fails
	// Validate, for a zero fence and for a fence of another namespace than
	// b.Namespace. It returns ErrExists, namespace.ErrFenced and
	// namespace.ErrNotFound for an unknown namespace.
	CreateBudget(ctx context.Context, b Budget, fence namespace.Fence, e audit.Entry) error
	// UpdateBudget replaces b.ID when b.Version is the stored version,
	// and appends the audit entry e in its transaction. It returns
	// ErrNotFound for an unknown budget, a zero fence or a fence of
	// another namespace than the stored budget. It returns ErrInvalid for
	// a budget that fails Validate or that names another namespace than
	// the stored budget, ErrConflict and namespace.ErrFenced.
	UpdateBudget(ctx context.Context, b Budget, fence namespace.Fence, e audit.Entry) error
	// DeleteBudget deletes the budget id and its statuses, and appends the
	// audit entry e in its transaction. It returns ErrNotFound for an
	// unknown budget, a zero fence or a fence of another namespace than
	// the budget, and namespace.ErrFenced.
	DeleteBudget(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Budgets returns the budgets of ns, ordered by ID.
	Budgets(ctx context.Context, ns namespace.Name) ([]Budget, error)
	// SaveStatus stores st, with its Warned thresholds, as the last status
	// of its budget and period, when version is the version of the stored
	// status, or 0 for none. It returns ErrNotFound for an unknown budget,
	// a zero fence or a fence of another namespace than the budget,
	// ErrConflict for another version, and namespace.ErrFenced. The
	// evaluation of a budget writes no audit entry.
	SaveStatus(ctx context.Context, st Status, version int64, fence namespace.Fence) error
	// LastStatus returns the last status of the budget id for p, with its
	// Warned thresholds and its version, or ErrNotFound.
	LastStatus(ctx context.Context, id string, p Period) (Status, int64, error)
}
