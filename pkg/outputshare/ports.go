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
	// A producer whose unit has no stable ID is a producer that the adapter
	// could not resolve. Find runs the same query for it and returns
	// ErrNoRule, because a stored rule never names a unit of its namespace
	// with no stable ID. So an unknown producer takes the same work as a
	// known one.
	//
	// It returns ErrNoRule when no rule matches, and ErrInvalid when the
	// consumer fails Placement.Validate, or the producer fails it for a
	// reason other than a unit with no stable ID. A stored rule that fails
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
	// The grants of the principal decide. No sharing rule applies. A direct
	// open of the profile block outputs/opendustpan is a read by the
	// principal.
	ReadByPrincipal ReadKind = "principal"
	// ReadByJob is a read with a job token of a job of another unit. A
	// sharing rule must name the unit of the job, and the principal that
	// started the job, or the last saver of the trigger that started it, must
	// hold the actions.
	ReadByJob ReadKind = "job"
	// ReadByAttachment is an open of the profile block outputs/opendustpan by
	// a job of another unit. A sharing rule must name the unit of the job,
	// and the attachment authority must hold the actions. The checker reads
	// the authority from the saved attachment that ReadRequest.Attachment
	// names. The grants of the starter of the job do not count.
	ReadByAttachment ReadKind = "attachment"
)

// ReadRequest is one read of the outputs of a unit. No field of it gives a
// grant: the checker reads each principal, each grant, the attachment and the
// latest state through its ports.
type ReadRequest struct {
	// Caller is the principal of the request, with its credential and its
	// job-ID claim.
	Caller identity.Principal
	// Producer is the unit whose outputs the caller reads, as the tool
	// adapter resolved it from the route. When the route names a unit that
	// does not exist, the adapter still calls the checker, with the unit
	// reference of the route, no stable ID and no collection, so that an
	// unknown producer takes the same path as a known one.
	Producer Placement
	// Attachment names the saved attachment of an open of the profile block
	// outputs/opendustpan by a job. Only the profile service sets it, from
	// the revision that the job opens. A tool adapter leaves it zero. The
	// checker does not trust it: it reads the attachment through the
	// Attachments port, and denies unless the attachment is attached to the
	// unit of the job and its block names the producer.
	//
	// For a job token, a set Attachment makes the read a ReadByAttachment.
	// For a principal, a set Attachment is refused with ErrInvalid: a direct
	// open is a read by the principal with the grants of the caller.
	Attachment AttachmentRef
	// Latest is the ID of the state that the route answers, as State.ID
	// names it: the current state version, or the version of the latest
	// checkpoint. The checker reads the latest state of the producer through
	// the States port and denies with CheckLatest when Latest is empty or is
	// not the ID of that state. A route that reads an older state, such as an
	// old state version, an export of a version or the history of updates,
	// checks unit:read itself and does not call the checker.
	Latest string
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
	// Rule is the rule that allowed a read by a job or by an attachment. It
	// is empty for a read by a principal.
	Rule ID
	// Secrets is true when the answer may hold the secret outputs. When it
	// is false, the answer leaves out each secret output.
	Secrets bool
}

// The checks of a read that a DeniedError can name. When more than one check
// fails, the error names the first of them in this order.
const (
	// CheckCaller is the check that the caller, its job, and the principal
	// whose grants decide (the starter, the last saver of the trigger or the
	// attachment authority) are known and active.
	CheckCaller = "caller"
	// CheckProducer is the check that the producer of the route exists.
	CheckProducer = "producer"
	// CheckAttachment is the check that the attachment of a ReadByAttachment
	// is a saved attachment of the unit of the job whose block names the
	// producer.
	CheckAttachment = "attachment"
	// CheckRule is the check that a sharing rule names the unit of the job.
	CheckRule = "sharing-rule"
	// CheckGrant is the check that the principal holds unit:read-outputs on
	// the producer, or that the principal whose grants decide a read by a job
	// or by an attachment holds it.
	CheckGrant = "grant"
	// CheckSecrets is the check that the principal holds unit:read-secrets on
	// the producer for a read of a secret output.
	CheckSecrets = "secrets"
	// CheckLatest is the check that the read is of the latest state of the
	// producer.
	CheckLatest = "latest-state"
	// CheckClientEncrypted is the check that the server can read the latest
	// state of the producer. A state that the client encrypted fails it.
	CheckClientEncrypted = "client-encrypted"
)

// DeniedError is a read that a check denied. It names the check, the
// producer and, for a read by a job, the unit of the job, so that the audit
// entry of the denial can name them. Its text is for the audit log of the
// producer namespace. A tool adapter never puts it in an answer to a job of
// another namespace.
//
// It unwraps to exactly one sentinel, and never to its cause:
//   - A hidden denial (IsHidden) unwraps to ErrUnknownProducer only. The
//     adapter answers it exactly as a producer that does not exist (404, the
//     same body), so that a job cannot probe for the units of another
//     namespace.
//   - A denial by CheckProducer unwraps to ErrUnknownProducer only.
//   - A denial by CheckClientEncrypted unwraps to ErrClientEncrypted only.
//     The adapter answers 409 with a message that names the reason.
//   - Each other denial unwraps to identity.ErrDenied only. The adapter
//     answers 403.
//
// The cause stays in the field Err for the audit entry. So a cause that
// wraps identity.ErrDenied, as the error of identity.Authorizer does, cannot
// make a hidden denial match identity.ErrDenied.
type DeniedError struct {
	// Err is the cause, for example ErrNoRule. It can be nil. Unwrap does not
	// return it.
	Err error
	// Check is one of the Check constants.
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
// forgets the flag cannot make a denial across namespaces answer 403 or 409.
func (e *DeniedError) IsHidden() bool {
	if e.Hidden {
		return true
	}

	return e.Consumer.Namespace() != "" && e.Consumer.Namespace() != e.Producer.Namespace()
}

// Unwrap returns the one sentinel of the denial: ErrUnknownProducer for a
// hidden denial and for CheckProducer, ErrClientEncrypted for
// CheckClientEncrypted, and identity.ErrDenied for each other check. It never
// returns the cause.
func (e *DeniedError) Unwrap() error {
	switch {
	case e.IsHidden(), e.Check == CheckProducer:
		return ErrUnknownProducer
	case e.Check == CheckClientEncrypted:
		return ErrClientEncrypted
	default:
		return identity.ErrDenied
	}
}

// Attachment is a saved attachment of a profile block outputs/opendustpan,
// as the Attachments port reads it from the saved revision.
type Attachment struct {
	// Unit is the unit that the revision of the profile is attached to, with
	// its stable ID.
	Unit identity.Object
	// Producer is the unit that the block names, with the stable ID that the
	// save of the revision resolved.
	Producer identity.Object
	// Authority is the principal ID of the attachment authority: the
	// principal that made the attachment, or the blueprint publisher of a
	// Catalog attachment.
	Authority string
}

// Attachments reads the saved attachments of the profile block
// outputs/opendustpan for the checker. The profile store serves it. It is the
// only source of the attachment authority: no caller passes an authority to
// the checker.
type Attachments interface {
	// Attachment returns the attachment that ref names in the namespace ns:
	// the block ref.Block of the saved revision ref.Revision of the profile
	// ref.Profile. It returns ErrInvalid when ref fails
	// AttachmentRef.Validate, and ErrUnknownAttachment for an unknown
	// profile, revision or block, for a block that is not of the provider
	// outputs/opendustpan, and for a revision that is attached to no unit.
	//
	// The port does not compare the attachment with the read. The checker
	// compares Unit with the unit of the job and Producer with the producer,
	// by stable ID.
	Attachment(ctx context.Context, ns namespace.Name, ref AttachmentRef) (Attachment, error)
}

// State is the latest state of a unit, as the States port tells it to the
// checker.
type State struct {
	// ID names the latest state: the ID of the current state version, or the
	// version of the latest checkpoint in decimal. It is empty for a unit
	// with no state.
	ID string
	// ClientEncrypted is true when the client encrypted the latest state, so
	// that the server cannot read its outputs or reduce it. A tool whose client never
	// encrypts the state always answers false.
	ClientEncrypted bool
}

// States tells the checker the latest state of a unit. The store of each tool
// serves it.
type States interface {
	// Latest returns the latest state of the unit u. It looks up u by its
	// reference (tool, namespace and name), and also by its stable ID when u
	// has one. It returns ErrUnknownProducer when no unit has that reference,
	// when the stable ID names another unit, and for a reference with no
	// stable ID, after the same lookup, so that an unknown producer takes the
	// same work as a known one. It returns ErrInvalid for an object that is
	// not a unit.
	Latest(ctx context.Context, u identity.Object) (State, error)
}

// Checker decides each read of the outputs of a unit. Each tool adapter calls
// it on each route that answers outputs of the latest state: the routes that
// answer outputs only, and, for a job token of another unit, the routes that
// answer a whole state, which then answer the root outputs only. The profile
// service calls it for each open of the profile block outputs/opendustpan.
// No adapter decides a read of outputs without it. An adapter calls it also
// when the route names a producer that does not exist.
//
// For a read by a principal, the checker allows the read when the principal
// holds unit:read-outputs on the producer, and unit:read-secrets for a secret
// output. A grant of unit:read alone does not allow it: unit:read does not
// imply unit:read-outputs. A route that answers a whole state checks unit:read
// itself and does not call the checker for a principal.
//
// For a read by a job token, the checker reads the job of the claim and its
// unit, and the principal whose grants decide check 2: the attachment
// authority of the saved attachment that ReadRequest.Attachment names, else
// the last saver of the trigger that started the job, else the principal that
// started it. It allows the read only when every check passes at this call:
//   - For a ReadByAttachment, the Attachments port returns the attachment,
//     its Unit is the unit of the job and its Producer is the producer, by
//     stable ID, and the producer is in the namespace of the unit of the job.
//   - Store.Find returns a rule that names the unit of the job.
//   - That principal holds unit:read-outputs on the producer with its own
//     current grants, and unit:read-secrets for a secret output.
//
// For each kind, the checker also reads the latest state of the producer
// through the States port. It denies with CheckLatest when ReadRequest.Latest
// is not the ID of that state. It denies with CheckClientEncrypted when the
// client encrypted the state and the caller reads outputs only: a job token,
// or a principal that does not hold unit:read on the producer. The server cannot answer the
// outputs only of a state that the client encrypted. A principal that holds
// unit:read reads such a state as before. The checker reads that grant
// itself. A job of the producer itself is not a read of another unit and
// does not reach the checker.
//
// The checker runs each check and each port call of the kind of the read
// before it decides, also after a check fails and also for a producer that
// does not exist. So an unknown producer and a hidden denial run the same
// code, call the same ports and write the same audit entry, and the work does
// not tell them apart.
//
// The checker fails closed. A job, a starter, a saver or an authority that is
// missing, deleted or disabled denies. An unknown or mismatched attachment
// denies. A stale or empty Latest denies. An error of a port denies. Each
// denial writes one audit entry in the producer namespace (the namespace of
// the route) only, with the check that denied, the producer and the unit of
// the job, when that namespace exists. For a job of another namespace, the
// entry names the unit of the job by its reference, the namespace of the job
// gets no entry, and the denial is hidden (DeniedError.IsHidden).
type Checker interface {
	// CheckRead returns the decision for req. It returns a *DeniedError when
	// a check denies. The error unwraps to one sentinel only, as
	// DeniedError says. It returns ErrInvalid when req.Producer holds no
	// unit reference, when a producer with a stable ID fails
	// Placement.Validate, when req.Attachment is set on a read by a
	// principal, and when req.Attachment fails AttachmentRef.Validate. It
	// returns an error of a port that it could not read. On each error the
	// caller answers no output.
	CheckRead(ctx context.Context, req ReadRequest) (Decision, error)
}
