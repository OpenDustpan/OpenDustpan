package outputshare

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store keeps the sharing rules and the output sharing setting of each
// namespace. A SQL adapter serves it from the database of the namespace of
// the producer. Every write carries the fence of that namespace, and the
// store checks it in the transaction of the write. A write appends its audit
// entry in that transaction (fail closed). A rule whose consumer is in another
// namespace is a write of the producer namespace only: it carries the fence
// of the producer and changes nothing in the other namespace.
//
// The store keys a unit and a collection of the producer namespace by their
// stable IDs, so a rename keeps the rule and a deleted and re-created name
// does not get it. A consumer of another namespace is kept as its reference,
// because the save does not resolve an object of another namespace: an answer
// must not tell whether such an object exists. Find matches that reference
// against the consumer at each check, so a rename in the other namespace ends
// the match, and a new unit with the old name matches.
//
// The store checks no access. The caller checks the actions on the producer.
type Store interface {
	// Put creates the rule r. In one transaction it checks the fence, runs
	// r.Validate, counts the rules of r.Producer, writes the rule and appends
	// the audit entry e. The caller stamps the time and the actor on e.
	//
	// It returns:
	//   - ErrInvalid when r.Validate fails.
	//   - ErrAlreadyExists when a rule with the same producer and the same
	//     consumer exists.
	//   - ErrLimit when r.Producer already has MaxRulesPerProducer rules.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Producer.
	//
	// On every error the store changes nothing.
	Put(ctx context.Context, r Rule, fence namespace.Fence, e audit.Entry) error
	// Get returns the rule id of the namespace ns, or ErrNotFound. It returns
	// ErrInvalid for an ID or a namespace that fails validation.
	Get(ctx context.Context, ns namespace.Name, id ID) (Rule, error)
	// List returns the rules whose producer is exactly producer, ordered by
	// ID. A producer with no rule gives an empty list and no error. It returns
	// ErrInvalid for an object that is not a unit, a collection or a namespace.
	List(ctx context.Context, producer identity.Object) ([]Rule, error)
	// Delete removes the rule id of the namespace ns and appends the audit
	// entry e in the transaction. It returns ErrNotFound for an unknown rule,
	// ErrInvalid, and namespace.ErrFenced. The purge of a unit removes each
	// rule whose producer or consumer in the same namespace is that unit.
	Delete(ctx context.Context, ns namespace.Name, id ID, fence namespace.Fence, e audit.Entry) error
	// Find returns one rule that covers the producer and names the consumer.
	// A rule covers the producer when its producer is the unit, the
	// collection of the unit or the namespace of the unit. A rule names the
	// consumer when its consumer is the unit, the collection of the unit or,
	// for a consumer of the producer namespace, that namespace. Of several
	// matches it returns the one with the lowest ID.
	//
	// It returns ErrNoRule when no rule matches, and ErrInvalid when a
	// placement fails Placement.Validate. A stored rule that fails
	// Rule.Validate never matches.
	Find(ctx context.Context, producer, consumer Placement) (Rule, error)
	// Setting returns the output sharing setting of the namespace ns. A
	// namespace that never set it gives DefaultNobody. It returns
	// namespace.ErrNotFound for an unknown namespace and ErrInvalid for a
	// stored value that fails Default.Validate, so the caller fails closed.
	Setting(ctx context.Context, ns namespace.Name) (Default, error)
	// SetSetting stores d as the setting of the namespace ns and appends the
	// audit entry e in the transaction. It returns ErrInvalid when d fails
	// Default.Validate, and namespace.ErrFenced. The change applies to the
	// units that the namespace makes later. It changes no rule of a unit that
	// exists.
	SetSetting(ctx context.Context, ns namespace.Name, d Default, fence namespace.Fence, e audit.Entry) error
}

// NewUnitRules is the part of the store that the tool stores call in the
// transaction that makes a unit, so that the unit and the rule of the setting
// are one write.
type NewUnitRules interface {
	// DefaultRule returns the rule that the setting of the namespace of unit
	// gives to that new unit, and true. With DefaultNobody it returns false
	// and no rule. The rule names the namespace of the unit as its consumer,
	// with id as its ID and actor as CreatedBy. It returns the errors of
	// Setting, and ErrInvalid for a unit reference with no stable ID.
	DefaultRule(ctx context.Context, unit identity.Object, id ID, actor string) (Rule, bool, error)
}

// ReadKind is the way that a caller reads the outputs of a unit.
type ReadKind string

// The kinds of a read.
const (
	// ReadByPrincipal is a read with the token or the session of a principal.
	// The grants of the principal decide. No sharing rule applies.
	ReadByPrincipal ReadKind = "principal"
	// ReadByJob is a read with a job token of a job of another unit. A
	// sharing rule must name the unit of the job, and the principal that
	// started the job must hold the actions.
	ReadByJob ReadKind = "job"
)

// ReadRequest is one read of the outputs of a unit.
type ReadRequest struct {
	// Caller is the principal of the request, with its credential and its
	// job-ID claim.
	Caller identity.Principal
	// Producer is the unit whose outputs the caller reads, as the tool
	// adapter resolved it from the route.
	Producer Placement
	// Secrets is true when the route cannot answer without a secret output,
	// as the decrypt of a secret output. A caller without unit:read-secrets
	// is then denied by CheckSecrets. When it is false, such a caller gets a
	// decision with Secrets false, and the answer leaves out each secret
	// output.
	Secrets bool
}

// Decision is the answer of an allowed read.
type Decision struct {
	// Kind is the way of the read.
	Kind ReadKind
	// Rule is the rule that allowed a read by a job. It is empty for a read
	// by a principal.
	Rule ID
	// Secrets is true when the answer may hold the secret outputs. When it
	// is false, the answer leaves out each secret output.
	Secrets bool
}

// The checks of a read that a DeniedError can name.
const (
	// CheckRule is the check that a sharing rule names the unit of the job.
	CheckRule = "sharing-rule"
	// CheckGrant is the check that the principal holds unit:read-outputs on
	// the producer, or that the principal that started the job holds it.
	CheckGrant = "grant"
	// CheckSecrets is the check that the principal holds unit:read-secrets on
	// the producer for a read of a secret output.
	CheckSecrets = "secrets"
	// CheckCaller is the check that the caller, its job and the principal
	// that started the job are known and active.
	CheckCaller = "caller"
)

// DeniedError is a read that a check denied. It wraps identity.ErrDenied. It
// names the check, the producer and, for a read by a job, the unit of the
// job, so that the audit entry of the denial and the answer can name them.
type DeniedError struct {
	// Err is the cause, for example ErrNoRule. It can be nil.
	Err error
	// Check is one of CheckRule, CheckGrant, CheckSecrets and CheckCaller.
	Check string
	// Producer is the unit whose outputs the caller asked for.
	Producer identity.Object
	// Consumer is the unit of the job, or the zero object for a read by a
	// principal.
	Consumer identity.Object
}

// Error names the check and the units.
func (e *DeniedError) Error() string {
	if e.Consumer.String() == "" {
		return fmt.Sprintf("read of the outputs of %s denied by the %s check", e.Producer, e.Check)
	}

	return fmt.Sprintf(
		"read of the outputs of %s by a job of %s denied by the %s check",
		e.Producer,
		e.Consumer,
		e.Check,
	)
}

// Unwrap returns identity.ErrDenied and the cause, so that errors.Is finds
// both.
func (e *DeniedError) Unwrap() []error {
	return []error{identity.ErrDenied, e.Err}
}

// Checker decides each read of the outputs of a unit. Each tool adapter calls
// it on each route that answers outputs: the routes that answer outputs only,
// and, for a job token of another unit, the routes that answer a whole state,
// which then answer the root outputs only. The adapter never decides a read
// of outputs without it.
//
// For a read by a principal, the checker allows the read when the principal
// holds unit:read-outputs on the producer, and unit:read-secrets for a secret
// output. A route that answers a whole state checks unit:read itself and does
// not call the checker for a principal.
//
// For a read by a job token, the checker reads the job of the claim and its
// unit, the principal that started it, or the last saver of the trigger that
// started it. It allows the read only when both checks pass at this call:
// Store.Find returns a rule that names the unit of the job, and that principal
// holds unit:read-outputs on the producer with its own current grants, and
// unit:read-secrets for a secret output. A job of the producer itself is not a
// read of another unit and does not reach the checker.
//
// The checker fails closed. A job, a starter or a saver that is missing,
// deleted or disabled denies. An error of a port denies. Each denial writes
// an audit entry with the check that denied, the producer and the unit of the
// job.
type Checker interface {
	// CheckRead returns the decision for req. It returns a *DeniedError
	// (identity.ErrDenied) when a check denies, ErrInvalid when
	// req.Producer fails Placement.Validate, and an error of a port that it
	// could not read. On each error the caller answers no output.
	CheckRead(ctx context.Context, req ReadRequest) (Decision, error)
}
