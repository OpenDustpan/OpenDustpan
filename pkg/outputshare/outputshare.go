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
// whether the server can read that state. The package has no type that
// exists for one tool only.
package outputshare

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
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
	// A tool adapter answers 409 to a caller of the producer namespace.
	ErrClientEncrypted = errors.New("state of the producer is encrypted by the client")
	// ErrUnknownAttachment reports an attachment reference that names no
	// saved attachment of a profile block outputs/opendustpan.
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

// AttachmentRef is a reference to one block outputs/opendustpan of a saved
// revision of a profile. The profile service puts it in a ReadRequest for an
// open of the block by a job. It is not trusted: the checker reads the
// attachment that it names through the Attachments port, and checks it.
type AttachmentRef struct {
	// Profile is the stable ID of the profile.
	Profile string
	// Block is the name of the block in the document of the revision.
	Block string
	// Revision is the number of the saved revision, 1 or more.
	Revision int
}

// IsZero reports whether r names no attachment.
func (r AttachmentRef) IsZero() bool {
	return r == AttachmentRef{Profile: "", Block: "", Revision: 0}
}

// Validate returns an error that wraps ErrInvalid when r is not a reference
// to an attachment: a profile ID or a block name that is empty, too long or
// has a byte that is not in the allowlist, or a revision below 1.
func (r AttachmentRef) Validate() error {
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
	// Consumer is the object whose units read the outputs: a unit, a
	// collection, or the namespace of the producer. A unit or a collection
	// of another namespace is valid, as a reference by name with no stable
	// ID. A whole other namespace is not.
	Consumer identity.Object
}

// Validate returns an error that wraps ErrInvalid when r is not a valid rule.
// It checks the ID, the kinds of both objects, that a consumer namespace is
// the namespace of the producer, that each unit and each collection of the
// producer namespace has its stable ID, and that a consumer of another
// namespace has none. It allows the kinds of objects that it
// lists and refuses each other kind, so a new kind of object is refused until
// a decision adds it.
//
// Defense in depth: the HTTP adapter checks the same request, the store runs
// Validate at each write, and the checker runs it on each rule that it reads.
// Each check stays, so that a new caller, a row that another version wrote or
// a restored backup cannot make a rule that names a principal or a whole other
// namespace.
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

	if !scopeObject(r.Consumer) {
		return fmt.Errorf("%w: the consumer %q is not a unit, a collection or a namespace", ErrInvalid, r.Consumer)
	}

	if r.Consumer.IsNamespace() && r.Consumer.Namespace() != r.Producer.Namespace() {
		return fmt.Errorf(
			"%w: the consumer %q is another namespace; name a unit or a collection of it", ErrInvalid, r.Consumer,
		)
	}

	if !stableIn(r.Producer) {
		return fmt.Errorf("%w: the producer %q has no stable ID", ErrInvalid, r.Producer)
	}

	if r.CrossNamespace() {
		// A consumer of another namespace is a reference by name. A stable ID
		// on it would come from a resolve of an object of another namespace,
		// which the save never does.
		if r.Consumer.UnitID() != "" || r.Consumer.CollectionID() != "" {
			return fmt.Errorf("%w: the consumer %q of another namespace has a stable ID", ErrInvalid, r.Consumer)
		}

		return nil
	}

	if !stableIn(r.Consumer) {
		return fmt.Errorf("%w: the consumer %q has no stable ID", ErrInvalid, r.Consumer)
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
	return r.Consumer.Namespace() != r.Producer.Namespace()
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
// the unit has no collection. A rule that names a collection of the producer
// namespace matches by the stable ID, so a collection of that namespace that
// is deleted and made again with the same name does not get the rules of the
// old one. A rule that names a collection of another namespace matches by
// name, so a collection of that namespace made again with the old name gets
// the rule.
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
