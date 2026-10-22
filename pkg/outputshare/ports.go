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
// of the producer, it changes nothing in the other namespace, and it writes
// no audit entry there.
//
// The store keys a unit and a collection of the producer namespace by their
// stable IDs, so a rename keeps the rule and a deleted and re-created name
// does not get it. A consumer of another namespace is kept as its reference
// by name, because the save does not resolve an object of another namespace:
// an answer must not tell whether such an object exists. Find matches that
// reference against the consumer at each check, so a rename in the other
// namespace ends the match, and a new unit or a new collection with the old
// name matches.
//
// The rule of a new unit is not a call of this port. When the setting of a
// namespace is DefaultNamespace, the SQL adapter that makes a unit of any
// tool writes the unit, its first rule and the audit entry of that rule in
// the one transaction that makes the unit. The three commit together, or
// none of them commits. No port of the SDK exposes that write.
//
// The store checks no grant. The caller checks the actions on the producer.
// The store binds each call that names a rule to the producer of the route
// (Get and Delete), so that a holder of an action on one producer cannot
// reach a rule of another producer.
type Store interface {
	// Put creates the rule r. In one transaction it checks the fence, runs
	// r.Validate, checks that each unit and each collection of the producer
	// namespace that r names exists with its stable ID, counts the rules of
	// r.Producer, writes the rule and appends the audit entry e. The caller
	// stamps the time and the actor on e. A consumer of another namespace is
	// not resolved: its reference is kept as it is.
	//
	// It returns:
	//   - ErrInvalid when r.Validate fails, for example for a unit or a
	//     collection of the producer namespace with no stable ID.
	//   - ErrUnknownObject when a unit or a collection of the producer
	//     namespace that r names does not exist, or when its stable ID and
	//     its name point to different objects.
	//   - ErrAlreadyExists when a rule with the same producer and the same
	//     consumer exists.
	//   - ErrLimit when r.Producer already has MaxRulesPerProducer rules.
	//   - namespace.ErrFenced when the fence is stale or belongs to another
	//     namespace than r.Producer.
	//
	// On every error the store changes nothing.
	Put(ctx context.Context, r Rule, fence namespace.Fence, e audit.Entry) error
	// Get returns the rule id when its producer is exactly producer: the same
	// unit, collection or namespace, by stable ID for a unit and a
	// collection. It returns ErrNotFound for an unknown rule and for a rule of
	// another producer, so that the answer does not tell them apart. It
	// returns ErrInvalid for an ID or a producer that fails validation.
	Get(ctx context.Context, producer identity.Object, id ID) (Rule, error)
	// List returns the rules whose producer is exactly producer, ordered by
	// ID. A producer with no rule gives an empty list and no error. It returns
	// ErrInvalid for an object that is not a unit, a collection or a namespace.
	List(ctx context.Context, producer identity.Object) ([]Rule, error)
	// Delete removes the rule id when its producer is exactly producer, as
	// Get matches it, and appends the audit entry e in the transaction. It
	// returns ErrNotFound for an unknown rule and for a rule of another
	// producer, ErrInvalid, and namespace.ErrFenced. On every error the store
	// changes nothing. The purge of a unit removes each rule whose producer
	// or consumer in the same namespace is that unit.
	Delete(ctx context.Context, producer identity.Object, id ID, fence namespace.Fence, e audit.Entry) error
	// Find returns one rule that covers the producer and names the consumer.
	// A rule covers the producer when its producer is the unit, the
	// collection of the unit or the namespace of the unit. A rule names the
	// consumer when its consumer is the unit, the collection of the unit or,
	// for a consumer of the producer namespace, that namespace. A consumer of
	// another namespace matches a rule that names its unit or its collection
	// by name. Of several matches it returns the one with the lowest ID.
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

// ReadKind is the way that a caller reads the outputs of a unit.
type ReadKind string

// The kinds of a read.
const (
	// ReadByPrincipal is a read with the token or the session of a principal.
	// The grants of the principal decide. No sharing rule applies.
	ReadByPrincipal ReadKind = "principal"
	// ReadByJob is a read with a job token of a job of another unit. A
	// sharing rule must name the unit of the job, and the principal that
	// started the job, or the last saver of the trigger that started it, must
	// hold the actions.
	ReadByJob ReadKind = "job"
	// ReadByAttachment is an open of the profile block outputs/opendustpan by
	// a job of another unit. A sharing rule must name the unit of the job,
	// and the attachment authority (ReadRequest.Authority) must hold the
	// actions. The grants of the starter of the job do not count.
	ReadByAttachment ReadKind = "attachment"
)

// ReadRequest is one read of the outputs of a unit.
type ReadRequest struct {
	// Caller is the principal of the request, with its credential and its
	// job-ID claim.
	Caller identity.Principal
	// Producer is the unit whose outputs the caller reads, as the tool
	// adapter resolved it from the route.
	Producer Placement
	// Authority is the ID of the attachment authority of an open of the
	// profile block outputs/opendustpan: the principal that made the
	// attachment, or the blueprint publisher of a Catalog attachment, as the
	// saved revision of the profile holds it. Only the profile service sets
	// it. A tool adapter leaves it empty, and never sets it from a value of
	// the request.
	//
	// For a job token, a set Authority makes the read a ReadByAttachment:
	// the grants of the authority decide check 2, and the producer must be in
	// the namespace of the unit of the job. For a principal, a direct open,
	// the authority is the caller: an Authority that is not the ID of the
	// caller is refused with ErrInvalid.
	Authority string
	// Secrets is true when the route cannot answer without a secret output,
	// as the decrypt of a secret output. A caller without unit:read-secrets
	// is then denied by CheckSecrets. When it is false, such a caller gets a
	// decision with Secrets false, and the answer leaves out each secret
	// output.
	Secrets bool
	// Latest is true when the route answers the latest state of the
	// producer: the current state version, or the latest checkpoint. A
	// request with Latest false is denied by CheckLatest. A route that reads
	// an older state, such as an old state version, an export of a version
	// or the history of updates, checks unit:read itself and does not call
	// the checker.
	Latest bool
}

// Decision is the answer of an allowed read.
type Decision struct {
	// Kind is the way of the read.
	Kind ReadKind
	// Rule is the rule that allowed a read by a job or by an attachment. It
	// is empty for a read by a principal.
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
	// the producer, or that the principal whose grants decide a read by a job
	// or by an attachment holds it.
	CheckGrant = "grant"
	// CheckSecrets is the check that the principal holds unit:read-secrets on
	// the producer for a read of a secret output.
	CheckSecrets = "secrets"
	// CheckCaller is the check that the caller, its job, and the principal
	// whose grants decide (the starter, the last saver of the trigger or the
	// attachment authority) are known and active.
	CheckCaller = "caller"
	// CheckLatest is the check that the read is of the latest state of the
	// producer.
	CheckLatest = "latest-state"
)

// DeniedError is a read that a check denied. It names the check, the
// producer and, for a read by a job, the unit of the job, so that the audit
// entry of the denial can name them. Its text is for the audit log of the
// producer namespace. A tool adapter never puts it in an answer to a job of
// another namespace.
//
// A denial of a read by a job of the producer namespace, and of a read by a
// principal, wraps identity.ErrDenied: the adapter answers 403. A hidden
// denial wraps ErrUnknownProducer and not identity.ErrDenied: the adapter
// answers it exactly as a producer that does not exist (404, the same body),
// so that a job cannot probe for the units of another namespace.
type DeniedError struct {
	// Err is the cause, for example ErrNoRule. It can be nil.
	Err error
	// Check is one of CheckRule, CheckGrant, CheckSecrets, CheckCaller and
	// CheckLatest.
	Check string
	// Producer is the unit whose outputs the caller asked for.
	Producer identity.Object
	// Consumer is the unit of the job, or the zero object for a read by a
	// principal.
	Consumer identity.Object
	// Hidden is true for a denial that must answer as an unknown producer:
	// a read by a job whose unit the checker could not read. A denial whose
	// consumer is in another namespace than the producer is hidden also when
	// Hidden is false.
	Hidden bool
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

// IsHidden reports whether the denial must answer as an unknown producer.
//
// Defense in depth: the checker sets Hidden for a read by a job of another
// namespace too; the namespace comparison stays, so that a checker that
// forgets the flag cannot make a denial across namespaces answer 403.
func (e *DeniedError) IsHidden() bool {
	if e.Hidden {
		return true
	}

	return e.Consumer.Namespace() != "" && e.Consumer.Namespace() != e.Producer.Namespace()
}

// Unwrap returns the cause and identity.ErrDenied, or ErrUnknownProducer for
// a hidden denial, so that errors.Is finds both.
func (e *DeniedError) Unwrap() []error {
	if e.IsHidden() {
		return []error{ErrUnknownProducer, e.Err}
	}

	return []error{identity.ErrDenied, e.Err}
}

// Checker decides each read of the outputs of a unit. Each tool adapter calls
// it on each route that answers outputs of the latest state: the routes that
// answer outputs only, and, for a job token of another unit, the routes that
// answer a whole state, which then answer the root outputs only. The profile
// service calls it for each open of the profile block outputs/opendustpan.
// No adapter decides a read of outputs without it.
//
// For a read by a principal, the checker allows the read when the principal
// holds unit:read-outputs on the producer, and unit:read-secrets for a secret
// output. A grant of unit:read alone does not allow it: unit:read does not
// imply unit:read-outputs. A route that answers a whole state checks unit:read
// itself and does not call the checker for a principal.
//
// For a read by a job token, the checker reads the job of the claim and its
// unit, and the principal whose grants decide check 2: the attachment
// authority when ReadRequest.Authority is set, else the last saver of the
// trigger that started the job, else the principal that started it. It
// allows the read only when both checks pass at this call: Store.Find returns
// a rule that names the unit of the job, and that principal holds
// unit:read-outputs on the producer with its own current grants, and
// unit:read-secrets for a secret output. A job of the producer itself is not
// a read of another unit and does not reach the checker.
//
// The checker fails closed. A job, a starter, a saver or an authority that is
// missing, deleted or disabled denies. A request with Latest false denies. An
// error of a port denies. Each denial writes an audit entry in the producer
// namespace only, with the check that denied, the producer and the unit of
// the job. For a job of another namespace, the entry names the unit of the
// job by its reference, the namespace of the job gets no entry, and the
// denial is hidden (DeniedError.IsHidden).
type Checker interface {
	// CheckRead returns the decision for req. It returns a *DeniedError when
	// a check denies: it wraps identity.ErrDenied, or ErrUnknownProducer
	// when it is hidden. It returns ErrInvalid when req.Producer fails
	// Placement.Validate or req.Authority is set on a read by a principal
	// that is not the authority, and an error of a port that it could not
	// read. On each error the caller answers no output.
	CheckRead(ctx context.Context, req ReadRequest) (Decision, error)
}
