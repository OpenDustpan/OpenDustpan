// Package outputshare is the tool-neutral model of output sharing: the sharing
// rules that let the units of a job read the outputs of another unit, the
// namespace setting that decides the rule of a new unit, and the ports that
// store the rules and check a read of outputs.
//
// The unit that makes the outputs decides who reads them. A sharing rule is
// set on the producer: a unit, a collection or a namespace. It names the
// consumer: a unit, a collection, or the namespace of the producer. A rule can
// name a unit or a collection of another namespace. It never names a whole
// other namespace, and it never names a principal. A person reads outputs with
// a grant of the action unit:read-outputs, not with a rule. A job token reads
// outputs only when a rule names the unit of the job and the principal that
// started the job holds unit:read-outputs on the producer.
//
// The package is tool-neutral. A unit is a unit reference: the tool, the
// namespace and the tool's own name. The package never reads a state: the
// States port tells the checker the ID of the latest state of a unit and
// whether the server can read that state, and the Outputs port gives the root
// outputs of that state. The package has no type that exists for one tool
// only.
package outputshare

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the output sharing model and its ports.
var (
	// ErrInvalid reports a value that fails validation. The error text names
	// the field.
	ErrInvalid = errors.New("invalid sharing rule")
	// ErrNotFound reports a sharing rule that does not exist in the namespace
	// that a call names.
	ErrNotFound = errors.New("sharing rule not found")
	// ErrAlreadyExists reports a second rule with the same producer and the
	// same consumer.
	ErrAlreadyExists = errors.New("sharing rule already exists")
	// ErrLimit reports a producer that already has MaxRulesPerProducer rules.
	ErrLimit = errors.New("too many sharing rules on the producer")
	// ErrNoRule reports that no sharing rule of the producer names the
	// consumer. A check that gets it denies the read.
	ErrNoRule = errors.New("no sharing rule names the consumer")
	// ErrUnknownObject reports a unit or a collection of the producer
	// namespace that a rule names and that does not exist with that stable
	// ID and that name.
	ErrUnknownObject = errors.New("unit or collection of the sharing rule not found")
	// ErrUnknownProducer reports a hidden denial of a read by a job: a job of
	// another namespace, or a job whose unit the checker could not read. A
	// tool adapter answers it exactly as a producer that does not exist.
	ErrUnknownProducer = errors.New("producer not found")
	// ErrClientEncrypted reports a read of outputs only that the checker refused,
	// because the client encrypted the latest state of the producer. The
	// server cannot read such a state, so it cannot answer the outputs only.
	// A tool adapter answers 409 to a caller of the producer namespace. It is
	// a denial of one read, not a fact of the state: the fact is the
	// ErrClientEncrypted of the tool package, for example of pkg/workspace.
	// This package cannot import a tool package, so the two stay apart, and
	// an adapter gives both the same error code.
	ErrClientEncrypted = errors.New("state of the producer is encrypted by the client")
	// ErrStateMoved reports a read of outputs whose state is not the latest
	// state of the unit: a new state came in after the check of the read, or
	// the unit has no state now. The read fails closed and answers no output.
	// A new request reads the new state.
	ErrStateMoved = errors.New("latest state changed during the read")
	// ErrUnknownAttachment reports an attachment reference that the spec of the
	// job does not hold: no such attachment, revision or block
	// outputs/opendustpan reached through that attachment.
	ErrUnknownAttachment = errors.New("attachment of the profile block not found")
)

// Bounds of a sharing rule.
const (
	// MaxIDBytes is the longest rule ID.
	MaxIDBytes = 64
	// MaxRulesPerProducer is the most rules that one producer object has.
	MaxRulesPerProducer = 100
	// MaxBlockBytes is the longest name of a profile block in an attachment
	// reference.
	MaxBlockBytes = 100
)

// ID is the ID of a sharing rule. The server makes it, and it is unique in
// the namespace of the producer. It has 1 to MaxIDBytes characters from a-z,
// 0-9, "-" and "_".
type ID string

// Validate returns an error that wraps ErrInvalid when id is not an ID.
func (id ID) Validate() error {
	if id == "" || len(id) > MaxIDBytes {
		return fmt.Errorf("%w: rule ID length %d", ErrInvalid, len(id))
	}

	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("%w: rule ID has a byte that is not in a-z, 0-9, \"-\" or \"_\"", ErrInvalid)
		}
	}

	return nil
}

// AttachmentRef is a reference to one block outputs/opendustpan that a job
// reached through one profile attachment. The profile service puts it in a
// ReadRequest for an open of the block by a job. It names the attachment,
// because the authority belongs to the attachment: one profile can have more
// than one attachment that covers the unit of the job, each with its own
// authority. It is not trusted: the checker reads the attachment that it
// names through the Attachments port, for the job of the job token, and
// checks it.
type AttachmentRef struct {
	// Attachment is the stable ID of the profile attachment that the spec
	// of the job resolved, and through which the job reached the block. When
	// the job reaches a block of that name through more than one attachment,
	// it is the attachment whose block the merge of the profiles keeps: the
	// later one.
	Attachment string
	// Profile is the stable ID of the profile that holds the block: the
	// attached profile, or a profile that it imports or reads.
	Profile string
	// Block is the name of the block in the document of the revision.
	Block string
	// Revision is the number of the revision of Profile that the spec of the
	// job fixed, 1 or more.
	Revision int
}

// IsZero reports whether r names no attachment.
func (r AttachmentRef) IsZero() bool {
	return r == AttachmentRef{Attachment: "", Profile: "", Block: "", Revision: 0}
}

// Validate returns an error that wraps ErrInvalid when r is not a reference
// to an attachment: an attachment ID, a profile ID or a block name that is
// empty, too long or has a byte that is not in the allowlist, or a revision
// below 1.
func (r AttachmentRef) Validate() error {
	if !validRefPart(r.Attachment, MaxIDBytes, "") {
		return fmt.Errorf("%w: the attachment ID of the attachment reference", ErrInvalid)
	}

	if !validRefPart(r.Profile, MaxIDBytes, "") {
		return fmt.Errorf("%w: the profile ID of the attachment reference", ErrInvalid)
	}

	if !validRefPart(r.Block, MaxBlockBytes, "._") {
		return fmt.Errorf("%w: the block name of the attachment reference", ErrInvalid)
	}

	if r.Revision < 1 {
		return fmt.Errorf("%w: the revision %d of the attachment reference is below 1", ErrInvalid, r.Revision)
	}

	return nil
}

// ValidStableID reports whether s has the format of the stable ID of a unit or
// a collection: 1 to MaxIDBytes bytes from a-z, 0-9, "-" and "_". It checks the
// format only. It does not tell whether such an object exists.
func ValidStableID(s string) bool {
	return validRefPart(s, MaxIDBytes, "_")
}

// validRefPart reports whether s has 1 to limit bytes from a-z, 0-9, "-" and
// the bytes of extra.
func validRefPart(s string, limit int, extra string) bool {
	if s == "" || len(s) > limit {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && !strings.ContainsRune(extra, rune(c)) {
			return false
		}
	}

	return true
}

// Default is the namespace setting that decides the sharing rule of a new
// unit.
type Default string

// The values of the namespace setting.
const (
	// DefaultNobody gives a new unit no sharing rule. No job of another unit
	// reads its outputs until a person saves a rule. It is the value of a
	// namespace that never set the setting.
	DefaultNobody Default = "nobody"
	// DefaultNamespace gives a new unit one sharing rule that names the
	// namespace of the unit. The store writes the rule in the transaction
	// that makes the unit.
	DefaultNamespace Default = "namespace"
)

// Validate returns an error that wraps ErrInvalid when d is not a value of
// the setting. The empty value is not valid: a store answers DefaultNobody
// for a namespace that never set it.
func (d Default) Validate() error {
	switch d {
	case DefaultNobody, DefaultNamespace:
		return nil
	default:
		return fmt.Errorf("%w: output sharing default %q", ErrInvalid, d)
	}
}

// ConsumerKind is the kind of object that a rule names as its consumer.
type ConsumerKind string

// The kinds of consumer.
const (
	// ConsumerUnit is one unit, by its stable ID.
	ConsumerUnit ConsumerKind = "unit"
	// ConsumerCollection is one collection, by its stable ID.
	ConsumerCollection ConsumerKind = "collection"
	// ConsumerNamespace is the namespace of the producer. It has no ID.
	ConsumerNamespace ConsumerKind = "namespace"
)

// Consumer is the object whose units read the outputs. It is always a stable
// reference: a name never decides a match. A unit or a collection of another
// namespace is the namespace plus the stable ID that the caller gives. The
// store checks the format of that ID only, because an answer must not tell
// whether an object of another namespace exists.
type Consumer struct {
	// Kind is the kind of the consumer.
	Kind ConsumerKind
	// Namespace is the namespace of the consumer. For ConsumerNamespace it is
	// the namespace of the producer.
	Namespace namespace.Name
	// ID is the stable ID of the unit or of the collection. It is empty for
	// ConsumerNamespace.
	ID string
}

// String returns a short form of c for messages.
func (c Consumer) String() string {
	if c.ID == "" {
		return string(c.Kind) + " " + c.Namespace.String()
	}

	return string(c.Kind) + " " + c.Namespace.String() + "/" + c.ID
}

// Validate returns an error that wraps ErrInvalid when c is not a consumer: a
// kind that is not in the allowlist, a namespace that namespace.ParseName
// refuses, an ID that is empty, too long or has a byte outside a-z, 0-9, "-"
// and "_" for a unit or a collection, or an ID on a namespace consumer.
func (c Consumer) Validate() error {
	if _, err := namespace.ParseName(c.Namespace.String()); err != nil {
		return fmt.Errorf("%w: the namespace of the consumer", ErrInvalid)
	}

	switch c.Kind {
	case ConsumerUnit, ConsumerCollection:
		if !ValidStableID(c.ID) {
			return fmt.Errorf("%w: the stable ID of the consumer %s", ErrInvalid, c.Kind)
		}

		return nil
	case ConsumerNamespace:
		if c.ID != "" {
			return fmt.Errorf("%w: a namespace consumer has no ID", ErrInvalid)
		}

		return nil
	default:
		return fmt.Errorf("%w: the consumer kind %q is not a unit, a collection or a namespace", ErrInvalid, c.Kind)
	}
}

// Rule is one sharing rule.
type Rule struct {
	// CreatedAt is the time of the save. The store sets it.
	CreatedAt time.Time
	// ID is the ID of the rule.
	ID ID
	// CreatedBy is the ID of the principal that saved the rule.
	CreatedBy string
	// Producer is the object whose units share their outputs: a unit, a
	// collection or a namespace. A collection or a namespace covers each unit
	// that it holds at the time of a check.
	Producer identity.Object
	// Consumer is the object whose units read the outputs: a unit or a
	// collection by stable ID, in any namespace, or the namespace of the
	// producer. A whole other namespace is not valid.
	Consumer Consumer
}

// Validate returns an error that wraps ErrInvalid when r is not a valid rule.
// It checks the ID, the kind of the producer, that the producer has its
// stable ID, and the consumer: an allowlisted kind, a valid namespace and a
// valid stable ID, and that a namespace consumer is the namespace of the
// producer. It refuses each other kind, so a new kind of object is refused
// until a decision adds it.
//
// Defense in depth: the HTTP adapter checks the same request, the store runs
// Validate at each write, and the checker runs it on each rule that it reads.
// Each check stays, so that a new caller, a row that another version wrote or
// a restored backup cannot make a rule that names a principal, a whole other
// namespace or an object by name.
func (r Rule) Validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}

	if r.CreatedBy == "" {
		return fmt.Errorf("%w: a rule names the principal that saved it", ErrInvalid)
	}

	if !scopeObject(r.Producer) {
		return fmt.Errorf("%w: the producer %q is not a unit, a collection or a namespace", ErrInvalid, r.Producer)
	}

	if !stableIn(r.Producer) {
		return fmt.Errorf("%w: the producer %q has no stable ID", ErrInvalid, r.Producer)
	}

	if err := r.Consumer.Validate(); err != nil {
		return err
	}

	if r.Consumer.Kind == ConsumerNamespace && r.Consumer.Namespace != r.Producer.Namespace() {
		return fmt.Errorf(
			"%w: the consumer %s is another namespace; name a unit or a collection of it", ErrInvalid, r.Consumer,
		)
	}

	return nil
}

// stableIn reports whether o, an object of the producer namespace, has the
// stable ID that its kind needs: a unit and a collection have one, a
// namespace needs none.
func stableIn(o identity.Object) bool {
	switch {
	case o.IsUnit():
		return o.UnitID() != ""
	case o.IsCollection():
		return o.CollectionID() != ""
	default:
		return true
	}
}

// CrossNamespace reports whether the consumer of r is in another namespace
// than the producer.
func (r Rule) CrossNamespace() bool {
	return r.Consumer.Namespace != r.Producer.Namespace()
}

// scopeObject reports whether o is a unit, a collection or a namespace. It
// refuses a principal, the caller, the installation and the zero object.
func scopeObject(o identity.Object) bool {
	if o.IsPrincipal() || o.IsCaller() || o.IsServer() || o.Namespace() == "" {
		return false
	}

	return o.IsUnit() || o.IsCollection() || o.IsNamespace()
}

// Placement is a unit with the collection that holds it at the time of a
// check. Unit is a unit reference with its stable ID. Collection is the
// reference of its collection with its stable ID, or the zero object when
// the unit has no collection. A rule matches a unit or a collection by its stable ID in
// every namespace, so a unit or a collection that someone deletes and makes
// again with the same name does not get the rules of the old one.
type Placement struct {
	Unit       identity.Object
	Collection identity.Object
}

// Validate returns an error that wraps ErrInvalid when p holds no unit with
// a stable ID, or a collection of another namespace than the unit.
func (p Placement) Validate() error {
	if !p.Unit.IsUnit() || p.Unit.UnitID() == "" {
		return fmt.Errorf("%w: the placement %q is not a unit with a stable ID", ErrInvalid, p.Unit)
	}

	if p.Collection.String() == "" {
		return nil
	}

	if !p.Collection.IsCollection() || p.Collection.CollectionID() == "" ||
		p.Collection.Namespace() != p.Unit.Namespace() {
		return fmt.Errorf("%w: the collection %q of the unit %q", ErrInvalid, p.Collection, p.Unit)
	}

	return nil
}
