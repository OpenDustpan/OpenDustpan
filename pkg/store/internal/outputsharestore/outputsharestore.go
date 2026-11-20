// Package outputsharestore holds the part of the SQL adapters of
// outputshare.Store that does not depend on the dialect: the checks before a
// write, the flow of a write over a small transaction port, the conversion of
// a stored row to a rule, and the arguments of the cover query of Find. Each
// dialect keeps its own queries and its own transaction.
package outputsharestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// The kinds of object that a rule names, as the table stores them.
const (
	KindUnit       = "unit"
	KindCollection = "collection"
	KindNamespace  = "namespace"
)

// Row is one rule row with the names that its stable IDs have now. The fields
// and their order are those of the rows that sqlc makes for the queries of
// each dialect, so each adapter converts its row type to Row directly.
type Row struct {
	ID                        string
	ProducerKind              string
	ProducerID                string
	ConsumerKind              string
	ConsumerNamespace         string
	ConsumerID                string
	ConsumerTool              string
	ConsumerName              string
	CreatedBy                 string
	CreatedAtMs               int64
	ProducerUnitTool          string
	ProducerUnitName          string
	ProducerCollectionName    string
	ConsumerUnitToolNow       string
	ConsumerUnitNameNow       string
	ConsumerCollectionNameNow string
}

// Insert is the columns of a new rule row.
type Insert struct {
	Namespace         string
	ID                string
	ProducerKind      string
	ProducerID        string
	ConsumerKind      string
	ConsumerNamespace string
	ConsumerID        string
	ConsumerTool      string
	ConsumerName      string
	CreatedBy         string
	CreatedAtMs       int64
}

// Key is a producer as the queries take it.
type Key struct {
	Namespace namespace.Name
	Kind      string
	ID        string
}

// ProducerKey checks the producer of a Get, a List or a Delete and returns its
// key. A unit and a collection need their stable ID. Any other object is
// invalid.
func ProducerKey(producer identity.Object) (Key, error) {
	ns := producer.Namespace()

	if _, err := namespace.ParseName(ns.String()); err != nil {
		return Key{}, fmt.Errorf("%w: the namespace of the producer", outputshare.ErrInvalid)
	}

	switch {
	case producer.IsPrincipal() || producer.IsCaller() || producer.IsServer():
		return Key{}, fmt.Errorf("%w: the producer is not a unit, a collection or a namespace", outputshare.ErrInvalid)
	case producer.IsUnit():
		if producer.UnitID() == "" {
			return Key{}, fmt.Errorf("%w: the producer unit has no stable ID", outputshare.ErrInvalid)
		}

		return Key{Namespace: ns, Kind: KindUnit, ID: producer.UnitID()}, nil
	case producer.IsCollection():
		if producer.CollectionID() == "" {
			return Key{}, fmt.Errorf("%w: the producer collection has no stable ID", outputshare.ErrInvalid)
		}

		return Key{Namespace: ns, Kind: KindCollection, ID: producer.CollectionID()}, nil
	case producer.IsNamespace():
		return Key{Namespace: ns, Kind: KindNamespace, ID: ""}, nil
	default:
		return Key{}, fmt.Errorf("%w: the producer is not a unit, a collection or a namespace", outputshare.ErrInvalid)
	}
}

// CheckKeyID returns ErrInvalid when id is not a rule ID.
func CheckKeyID(id outputshare.ID) error {
	return id.Validate()
}

// Lookup resolves the objects of the producer namespace inside the
// transaction of a write.
type Lookup interface {
	// UnitID returns the stable ID of the unit, and false when the namespace
	// holds no such unit.
	UnitID(ctx context.Context, ns namespace.Name, tool, name string) (string, bool, error)
	// CollectionID returns the stable ID of the collection with the key, and
	// false when the namespace holds no such collection.
	CollectionID(ctx context.Context, ns namespace.Name, nameKey string) (string, bool, error)
	// Count returns the number of rules of the producer.
	Count(ctx context.Context, k Key) (int64, error)
	// Insert writes the row and returns the number of rows written.
	Insert(ctx context.Context, row Insert) (int64, error)
}

// CheckPut runs the checks of Put that need no transaction: the validity of
// the rule, the fence and the audit entry. It returns the row to write.
func CheckPut(r outputshare.Rule, fence namespace.Fence, e audit.Entry) (Insert, error) {
	// Defense in depth: the service validates the rule too. This check stays
	// so that a new caller of the store cannot write a rule that names a
	// principal, a whole other namespace or an object with no stable ID. It
	// does not rely on the service or on the handler.
	if err := r.Validate(); err != nil {
		return Insert{}, fmt.Errorf("put sharing rule: %w", err)
	}

	// Defense in depth: the fence check of the transaction compares the
	// failover version of the namespace of the fence only. This check stays so
	// that a rule of one namespace cannot be written under the fence of
	// another namespace.
	if fence.Namespace != r.Producer.Namespace() {
		return Insert{}, fmt.Errorf("%w: the rule names %q, the fence names %q",
			namespace.ErrFenced, r.Producer.Namespace(), fence.Namespace)
	}

	if e.At.IsZero() {
		return Insert{}, fmt.Errorf("%w: the audit entry has no time", outputshare.ErrInvalid)
	}

	producer, err := ProducerKey(r.Producer)
	if err != nil {
		return Insert{}, err
	}

	row := Insert{
		Namespace:         producer.Namespace.String(),
		ID:                string(r.ID),
		ProducerKind:      producer.Kind,
		ProducerID:        producer.ID,
		ConsumerKind:      "",
		ConsumerNamespace: r.Consumer.Namespace().String(),
		ConsumerID:        "",
		ConsumerTool:      "",
		ConsumerName:      "",
		CreatedBy:         r.CreatedBy,
		CreatedAtMs:       e.At.UnixMilli(),
	}

	cross := r.CrossNamespace()

	switch {
	case r.Consumer.IsUnit():
		row.ConsumerKind = KindUnit

		if cross {
			row.ConsumerTool = r.Consumer.Tool()
			row.ConsumerName = r.Consumer.UnitName()
		} else {
			row.ConsumerID = r.Consumer.UnitID()
		}
	case r.Consumer.IsCollection():
		row.ConsumerKind = KindCollection

		if cross {
			// The name of a collection compares in lower case.
			row.ConsumerName = strings.ToLower(r.Consumer.CollectionName())
		} else {
			row.ConsumerID = r.Consumer.CollectionID()
		}
	default:
		row.ConsumerKind = KindNamespace
	}

	return row, nil
}

// Put runs the part of Put that needs the transaction: it checks that each
// object of the producer namespace that the rule names exists under its
// stable ID, counts the rules of the producer and writes the row.
func Put(ctx context.Context, tx Lookup, r outputshare.Rule, row Insert) error {
	if err := checkObject(ctx, tx, r.Producer); err != nil {
		return err
	}

	if !r.CrossNamespace() {
		if err := checkObject(ctx, tx, r.Consumer); err != nil {
			return err
		}
	}

	key := Key{Namespace: namespace.Name(row.Namespace), Kind: row.ProducerKind, ID: row.ProducerID}

	count, err := tx.Count(ctx, key)
	if err != nil {
		return fmt.Errorf("count the sharing rules of the producer: %w", err)
	}

	if count >= outputshare.MaxRulesPerProducer {
		return fmt.Errorf("%w: the producer has %d rules", outputshare.ErrLimit, outputshare.MaxRulesPerProducer)
	}

	n, err := tx.Insert(ctx, row)
	if err != nil {
		return fmt.Errorf("insert sharing rule: %w", err)
	}

	if n == 0 {
		return outputshare.ErrAlreadyExists
	}

	return nil
}

// checkObject returns ErrUnknownObject unless the unit or the collection o of
// a namespace exists and its name and its stable ID point to the same object.
// A namespace needs no check: the fence names it.
func checkObject(ctx context.Context, tx Lookup, o identity.Object) error {
	switch {
	case o.IsUnit():
		id, found, err := tx.UnitID(ctx, o.Namespace(), o.Tool(), o.UnitName())
		if err != nil {
			return fmt.Errorf("look up the unit of the sharing rule: %w", err)
		}

		if !found || id != o.UnitID() {
			return outputshare.ErrUnknownObject
		}
	case o.IsCollection():
		id, found, err := tx.CollectionID(ctx, o.Namespace(), strings.ToLower(o.CollectionName()))
		if err != nil {
			return fmt.Errorf("look up the collection of the sharing rule: %w", err)
		}

		if !found || id != o.CollectionID() {
			return outputshare.ErrUnknownObject
		}
	default:
		// A namespace needs no check.
	}

	return nil
}

// ErrDangling reports a row whose unit or collection has no name now. A reader
// leaves such a row out, so it matches nothing.
var ErrDangling = errors.New("sharing rule names an object that no longer exists")

// Rule converts a stored row of the namespace ns to a rule. The row names its
// unit and its collection by the names that their stable IDs have now. It
// returns an error for a row that fails Validate and for a row that names an
// object that no longer exists, so a caller leaves such a row out.
func (row Row) Rule(ns namespace.Name) (outputshare.Rule, error) {
	producer, err := row.producer(ns)
	if err != nil {
		return outputshare.Rule{}, err
	}

	consumer, err := row.consumer(ns)
	if err != nil {
		return outputshare.Rule{}, err
	}

	r := outputshare.Rule{
		CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(),
		ID:        outputshare.ID(row.ID),
		CreatedBy: row.CreatedBy,
		Producer:  producer,
		Consumer:  consumer,
	}

	// Defense in depth: the CHECK constraints of the table refuse a row of the
	// wrong shape, and Put validates each rule. This check stays so that a row
	// of another version or a restored backup never reads as a rule.
	if err := r.Validate(); err != nil {
		return outputshare.Rule{}, err
	}

	return r, nil
}

func (row Row) producer(ns namespace.Name) (identity.Object, error) {
	switch row.ProducerKind {
	case KindUnit:
		if row.ProducerUnitName == "" {
			return identity.Object{}, ErrDangling
		}

		o, err := identity.UnitObject(row.ProducerUnitTool, ns, row.ProducerUnitName)
		if err != nil {
			return identity.Object{}, fmt.Errorf("%w: producer unit", outputshare.ErrInvalid)
		}

		return o.WithUnitID(row.ProducerID), nil
	case KindCollection:
		if row.ProducerCollectionName == "" {
			return identity.Object{}, ErrDangling
		}

		return identity.CollectionObject(ns, row.ProducerCollectionName).WithCollectionID(row.ProducerID), nil
	case KindNamespace:
		return identity.NamespaceObject(ns), nil
	default:
		return identity.Object{}, fmt.Errorf("%w: producer kind", outputshare.ErrInvalid)
	}
}

func (row Row) consumer(ns namespace.Name) (identity.Object, error) {
	cns, err := namespace.ParseName(row.ConsumerNamespace)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: consumer namespace", outputshare.ErrInvalid)
	}

	cross := cns != ns

	switch row.ConsumerKind {
	case KindUnit:
		if cross {
			o, unitErr := identity.UnitObject(row.ConsumerTool, cns, row.ConsumerName)
			if unitErr != nil {
				return identity.Object{}, fmt.Errorf("%w: consumer unit", outputshare.ErrInvalid)
			}

			return o, nil
		}

		if row.ConsumerUnitNameNow == "" {
			return identity.Object{}, ErrDangling
		}

		o, unitErr := identity.UnitObject(row.ConsumerUnitToolNow, ns, row.ConsumerUnitNameNow)
		if unitErr != nil {
			return identity.Object{}, fmt.Errorf("%w: consumer unit", outputshare.ErrInvalid)
		}

		return o.WithUnitID(row.ConsumerID), nil
	case KindCollection:
		if cross {
			return identity.CollectionObject(cns, row.ConsumerName), nil
		}

		if row.ConsumerCollectionNameNow == "" {
			return identity.Object{}, ErrDangling
		}

		return identity.CollectionObject(ns, row.ConsumerCollectionNameNow).WithCollectionID(row.ConsumerID), nil
	case KindNamespace:
		return identity.NamespaceObject(cns), nil
	default:
		return identity.Object{}, fmt.Errorf("%w: consumer kind", outputshare.ErrInvalid)
	}
}

// CoverArgs are the arguments of the cover query of Find.
type CoverArgs struct {
	Namespace              string
	ProducerUnitID         string
	ProducerCollectionID   string
	ConsumerNamespace      string
	ConsumerUnitID         string
	ConsumerCollectionID   string
	ConsumerTool           string
	ConsumerUnitName       string
	ConsumerCollectionName string
}

// FindArgs checks the producer and the consumer of Find and returns the
// arguments of the cover query and the namespace of the producer.
//
// A producer unit with no stable ID is one that the adapter could not
// resolve. It gets the same query with an empty namespace, which matches no
// row, so the caller gets ErrNoRule after the same work as for a known unit.
func FindArgs(producer, consumer outputshare.Placement) (CoverArgs, namespace.Name, error) {
	unresolved := producer.Unit.IsUnit() && producer.Unit.UnitID() == ""

	if !unresolved {
		if err := producer.Validate(); err != nil {
			return CoverArgs{}, "", err
		}
	}

	if err := consumer.Validate(); err != nil {
		return CoverArgs{}, "", err
	}

	ns := producer.Unit.Namespace()

	if _, err := namespace.ParseName(ns.String()); err != nil {
		return CoverArgs{}, "", fmt.Errorf("%w: the namespace of the producer", outputshare.ErrInvalid)
	}

	args := CoverArgs{
		Namespace:              ns.String(),
		ProducerUnitID:         producer.Unit.UnitID(),
		ProducerCollectionID:   producer.Collection.CollectionID(),
		ConsumerNamespace:      consumer.Unit.Namespace().String(),
		ConsumerUnitID:         consumer.Unit.UnitID(),
		ConsumerCollectionID:   consumer.Collection.CollectionID(),
		ConsumerTool:           consumer.Unit.Tool(),
		ConsumerUnitName:       consumer.Unit.UnitName(),
		ConsumerCollectionName: "",
	}

	if consumer.Collection.IsCollection() {
		args.ConsumerCollectionName = strings.ToLower(consumer.Collection.CollectionName())
	}

	if unresolved {
		args.Namespace = ""
	}

	return args, ns, nil
}
