// Package cost is the pluggable cost port of OpenDustpan: the estimate of
// what a planned change costs, for each resource and for the change, and
// the actual spend of a namespace from a spend tool. Budgets compare the
// two with a limit for each month and give warnings. A budget never blocks
// a change by itself: only a policy that reads the cost can block.
//
// Estimator and SpendSource are the ports to the tools. Adapters live
// outside this package and outside the core. The functions of this
// package are pure.
package cost

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by the ports.
var (
	// ErrInvalid reports a value that fails validation.
	ErrInvalid = errors.New("invalid cost data")
	// ErrNotFound reports an unknown estimate or budget.
	ErrNotFound = errors.New("cost data not found")
	// ErrExists reports a create with an ID that a budget already has.
	ErrExists = errors.New("budget already exists")
	// ErrConflict reports a write that names an older version.
	ErrConflict = errors.New("budget changed since it was read")
	// ErrCurrencyMismatch reports two amounts in different currencies.
	ErrCurrencyMismatch = errors.New("amounts are in different currencies")
	// ErrUnsupported reports a tool, a plan or a query that an adapter
	// cannot price.
	ErrUnsupported = errors.New("cost adapter cannot price this")
	// ErrUnavailable reports an adapter whose price source or spend tool
	// does not answer.
	ErrUnavailable = errors.New("cost adapter unavailable")
)

// Bounds of an estimate.
const (
	// EstimateIDPrefix starts every estimate ID.
	EstimateIDPrefix = "est-"
	// MaxResources caps the resources of an estimate.
	MaxResources = 5000
	// MaxNoteLen caps the note of a resource, in bytes.
	MaxNoteLen = 500
	// MaxSourceLen caps the name of the source of an estimate.
	MaxSourceLen = 100
)

// Action is what a planned change does to a resource.
type Action string

// Actions of a resource in a plan.
const (
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionReplace Action = "replace"
	ActionDelete  Action = "delete"
	ActionNoOp    Action = "no-op"
)

// ResourceCost is the estimate of one resource of a plan, as an amount for
// one month before and after the change. Each amount is 0 or more, and at
// most MaxMicros. Key is the key of the resource in the state of its unit,
// and the server keeps it opaque. A resource that the estimator cannot
// price is Unpriced, with zero amounts and a note.
type ResourceCost struct {
	Key      string
	Type     string
	Action   Action
	Before   Money
	After    Money
	Unpriced bool
	Note     string
}

// Subject names what an estimate is for: the plan of a phase of a job, or
// a version of a blueprint in the Catalog. Exactly one of Job and
// Blueprint is set.
type Subject struct {
	Job              string
	Phase            int
	Blueprint        string
	BlueprintVersion string
}

// Validate returns ErrInvalid for a subject that names both a job and a
// blueprint, or neither.
func (s Subject) Validate() error {
	if (s.Job == "") == (s.Blueprint == "") {
		return fmt.Errorf("%w: an estimate is for a plan or for a blueprint", ErrInvalid)
	}

	if s.Blueprint != "" && s.BlueprintVersion == "" {
		return fmt.Errorf("%w: an estimate of a blueprint names its version", ErrInvalid)
	}

	return nil
}

// Estimate is the cost of a planned change, for one month. Before and
// After are the sums of the priced resources. Source names the adapter
// and its version, or "api" for an estimate that a client sent. Unit is
// the unit reference of the plan, and empty for a blueprint. Namespace is
// the namespace of the row: the namespace of the unit of a plan, or the
// namespace of a blueprint, or audit.ServerScope for a blueprint of the
// installation.
type Estimate struct {
	ID        string
	Subject   Subject
	Namespace namespace.Name
	Unit      string
	Source    string
	Currency  string
	Before    Money
	After     Money
	Resources []ResourceCost
	CreatedAt time.Time
}

// Summed returns e with Before and After as the sums of its priced
// resources. The caller sets ID, Subject, Unit, Source, Currency,
// Resources and CreatedAt. It returns ErrInvalid for an estimate that
// fails Validate or for a sum that does not fit, and ErrCurrencyMismatch
// for a resource in another currency.
func Summed(e Estimate) (Estimate, error) {
	e.Before, e.After = Zero(e.Currency), Zero(e.Currency)

	for i := range e.Resources {
		r := &e.Resources[i]
		if r.Unpriced {
			continue
		}

		var err error
		if e.Before, err = e.Before.Add(r.Before); err != nil {
			return Estimate{}, fmt.Errorf("resource %s: %w", r.Key, err)
		}

		if e.After, err = e.After.Add(r.After); err != nil {
			return Estimate{}, fmt.Errorf("resource %s: %w", r.Key, err)
		}
	}

	return e, e.Validate()
}

// Delta returns After minus Before: the change of the cost of a month,
// "+EUR 31.20 a month" in the work order and the Catalog. It returns
// ErrCurrencyMismatch when the sums are in two currencies, and ErrInvalid
// when the difference does not fit.
func (e Estimate) Delta() (Money, error) {
	return e.After.Sub(e.Before)
}

// Unpriced returns the count of resources that the estimator could not
// price. A reader shows it next to the delta, because the delta does not
// count them.
func (e Estimate) Unpriced() int {
	n := 0

	for _, r := range e.Resources {
		if r.Unpriced {
			n++
		}
	}

	return n
}

// Validate returns ErrInvalid for an estimate that breaks a bound, whose
// sums do not match its resources, or with an unpriced resource that has
// an amount.
func (e Estimate) Validate() error {
	if err := e.validateFields(); err != nil {
		return err
	}

	return e.validateSums()
}

func (r ResourceCost) validate(currency string) error {
	switch {
	case r.Key == "":
		return fmt.Errorf("%w: a resource needs a key", ErrInvalid)
	case len(r.Note) > MaxNoteLen:
		return fmt.Errorf("%w: resource %s has a note above %d bytes", ErrInvalid, r.Key, MaxNoteLen)
	case r.Unpriced && (r.Before.Micros != 0 || r.After.Micros != 0):
		return fmt.Errorf("%w: unpriced resource %s has an amount", ErrInvalid, r.Key)
	case !r.Unpriced && (r.Before.Currency != currency || r.After.Currency != currency):
		return fmt.Errorf("%w: resource %s is not in %s", ErrCurrencyMismatch, r.Key, currency)
	}

	switch r.Action {
	case ActionCreate, ActionUpdate, ActionReplace, ActionDelete, ActionNoOp:
		return r.validateAmounts()
	default:
		return fmt.Errorf("%w: resource %s has an unknown action %q", ErrInvalid, r.Key, r.Action)
	}
}

// validateAmounts returns ErrInvalid for an amount of a priced resource
// below 0 or above MaxMicros.
func (r ResourceCost) validateAmounts() error {
	if r.Unpriced {
		return nil
	}

	if err := r.Before.ValidateCost(); err != nil {
		return fmt.Errorf("resource %s, before: %w", r.Key, err)
	}

	if err := r.After.ValidateCost(); err != nil {
		return fmt.Errorf("resource %s, after: %w", r.Key, err)
	}

	return nil
}

// validateFields returns ErrInvalid for a field of the estimate that
// breaks a bound.
func (e Estimate) validateFields() error {
	switch {
	case !strings.HasPrefix(e.ID, EstimateIDPrefix):
		return fmt.Errorf("%w: an estimate ID starts with %q", ErrInvalid, EstimateIDPrefix)
	case !ValidCurrency(e.Currency):
		return fmt.Errorf("%w: currency %q is not an ISO 4217 code", ErrInvalid, e.Currency)
	case e.Source == "" || len(e.Source) > MaxSourceLen:
		return fmt.Errorf("%w: an estimate names its source in 1 to %d bytes", ErrInvalid, MaxSourceLen)
	case len(e.Resources) > MaxResources:
		return fmt.Errorf("%w: at most %d resources", ErrInvalid, MaxResources)
	case e.Namespace == "":
		return fmt.Errorf("%w: an estimate names its namespace", ErrInvalid)
	case e.Subject.Job != "" && e.Namespace == audit.ServerScope:
		return fmt.Errorf("%w: an estimate of a plan is in the namespace of its unit", ErrInvalid)
	case e.Subject.Job != "" && e.Unit == "":
		return fmt.Errorf("%w: an estimate of a plan names its unit", ErrInvalid)
	}

	return e.Subject.Validate()
}

// validateSums returns ErrInvalid for a resource that fails validation, or
// for sums that do not match the resources.
func (e Estimate) validateSums() error {
	before, after := Zero(e.Currency), Zero(e.Currency)

	for i := range e.Resources {
		r := &e.Resources[i]
		if err := r.validate(e.Currency); err != nil {
			return err
		}

		if r.Unpriced {
			continue
		}

		var err error
		if before, err = before.Add(r.Before); err != nil {
			return fmt.Errorf("resource %s: %w", r.Key, err)
		}

		if after, err = after.Add(r.After); err != nil {
			return fmt.Errorf("resource %s: %w", r.Key, err)
		}
	}

	if before != e.Before || after != e.After {
		return fmt.Errorf("%w: the sums do not match the resources", ErrInvalid)
	}

	return nil
}
