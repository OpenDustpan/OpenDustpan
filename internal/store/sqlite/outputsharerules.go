package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/outputsharestore"
	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// OutputSharingStore returns the store of the sharing rules and of the output
// sharing setting. Each write checks the fence of the namespace of the
// producer and appends its audit entry in one transaction.
func (s *Store) OutputSharingStore() OutputSharingStore {
	return OutputSharingStore{outputSharingSettings{s: s}}
}

// OutputSharingStore is outputshare.Store over the database of a Store. It is
// a concrete type, so that the method that returns it needs no interface
// return.
type OutputSharingStore struct {
	outputSharingSettings
}

var _ outputshare.Store = OutputSharingStore{outputSharingSettings{s: nil}}

// Put implements outputshare.Store.
func (o OutputSharingStore) Put(ctx context.Context, r outputshare.Rule, fence namespace.Fence, e audit.Entry) error {
	row, err := outputsharestore.CheckPut(r, fence, e)
	if err != nil {
		return err
	}

	o.s.mu.RLock()
	defer o.s.mu.RUnlock()

	return o.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return outputsharestore.Put(ctx, ruleTx{q: q}, r, row)
	})
}

// Get implements outputshare.Store.
func (o OutputSharingStore) Get(
	ctx context.Context, producer identity.Object, id outputshare.ID,
) (outputshare.Rule, error) {
	key, err := outputsharestore.ProducerKey(producer)
	if err != nil {
		return outputshare.Rule{}, err
	}

	if idErr := outputsharestore.CheckKeyID(id); idErr != nil {
		return outputshare.Rule{}, idErr
	}

	o.s.mu.RLock()
	defer o.s.mu.RUnlock()

	row, err := o.s.queries.GetOutputSharingRule(ctx, db.GetOutputSharingRuleParams{
		Namespace: key.Namespace.String(), ID: string(id), ProducerKind: key.Kind, ProducerID: key.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return outputshare.Rule{}, outputshare.ErrNotFound
	}

	if err != nil {
		return outputshare.Rule{}, fmt.Errorf("select sharing rule: %w", err)
	}

	r, err := outputsharestore.Row(row).Rule(key.Namespace)
	if err != nil {
		// A row that fails its checks is not served.
		return outputshare.Rule{}, outputshare.ErrNotFound
	}

	return r, nil
}

// List implements outputshare.Store. A row that fails its checks is left out.
func (o OutputSharingStore) List(ctx context.Context, producer identity.Object) ([]outputshare.Rule, error) {
	key, err := outputsharestore.ProducerKey(producer)
	if err != nil {
		return nil, err
	}

	o.s.mu.RLock()
	defer o.s.mu.RUnlock()

	rows, err := o.s.queries.ListOutputSharingRulesOfProducer(ctx, db.ListOutputSharingRulesOfProducerParams{
		Namespace: key.Namespace.String(), ProducerKind: key.Kind, ProducerID: key.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("select sharing rules: %w", err)
	}

	out := make([]outputshare.Rule, 0, len(rows))

	for i := range rows {
		r, ruleErr := outputsharestore.Row(rows[i]).Rule(key.Namespace)
		if ruleErr != nil {
			continue
		}

		out = append(out, r)
	}

	return out, nil
}

// Delete implements outputshare.Store.
func (o OutputSharingStore) Delete(
	ctx context.Context, producer identity.Object, id outputshare.ID, fence namespace.Fence, e audit.Entry,
) error {
	key, err := outputsharestore.ProducerKey(producer)
	if err != nil {
		return err
	}

	if idErr := outputsharestore.CheckKeyID(id); idErr != nil {
		return idErr
	}

	// Defense in depth: the fence check of the transaction compares the
	// failover version only. This check stays so that a rule of one namespace
	// cannot be removed under the fence of another namespace.
	if fence.Namespace != key.Namespace {
		return fmt.Errorf(
			"%w: the rule names %q, the fence names %q", namespace.ErrFenced, key.Namespace, fence.Namespace,
		)
	}

	o.s.mu.RLock()
	defer o.s.mu.RUnlock()

	return o.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, delErr := q.DeleteOutputSharingRule(ctx, db.DeleteOutputSharingRuleParams{
			Namespace: key.Namespace.String(), ID: string(id), ProducerKind: key.Kind, ProducerID: key.ID,
		})
		if delErr != nil {
			return fmt.Errorf("delete sharing rule: %w", delErr)
		}

		if n == 0 {
			return outputshare.ErrNotFound
		}

		return nil
	})
}

// Find implements outputshare.Store. A row that fails its checks never
// matches.
func (o OutputSharingStore) Find(
	ctx context.Context, producer, consumer outputshare.Placement,
) (outputshare.Rule, error) {
	args, ns, err := outputsharestore.FindArgs(producer, consumer)
	if err != nil {
		return outputshare.Rule{}, err
	}

	o.s.mu.RLock()
	defer o.s.mu.RUnlock()

	rows, err := o.s.queries.ListOutputSharingRulesThatCover(ctx, db.ListOutputSharingRulesThatCoverParams{
		Namespace:            args.Namespace,
		ProducerUnitID:       args.ProducerUnitID,
		ProducerCollectionID: args.ProducerCollectionID,
		ConsumerNamespace:    args.ConsumerNamespace,
		ConsumerUnitID:       args.ConsumerUnitID,
		ConsumerCollectionID: args.ConsumerCollectionID,
	})
	if err != nil {
		return outputshare.Rule{}, fmt.Errorf("select covering sharing rules: %w", err)
	}

	for i := range rows {
		r, ruleErr := outputsharestore.Row(rows[i]).Rule(ns)
		if ruleErr != nil {
			continue
		}

		return r, nil
	}

	return outputshare.Rule{}, outputshare.ErrNoRule
}

// ruleTx is the transaction port of the rule write over the queries of one
// transaction.
type ruleTx struct {
	q *db.Queries
}

var _ outputsharestore.Lookup = ruleTx{q: nil}

// newUnitRuleIDPrefix starts the ID of the rule that the store writes for a
// new unit.
const newUnitRuleIDPrefix = "osr-"

// newUnitRuleReason is the reason of the audit entry of the rule of a new unit.
const newUnitRuleReason = "the output sharing setting of the namespace is namespace"

// newUnit is a unit that the transaction of a create just wrote.
type newUnit struct {
	At        time.Time
	Namespace namespace.Name
	Tool      string
	Name      string
	ID        string
}

// writeNewUnitRule writes the first rule of the new unit in the transaction of
// q, from the setting that the same transaction reads. With the setting
// namespace it writes one rule whose consumer is the namespace of the unit,
// and its audit entry. With the setting nobody it writes nothing. A stored
// setting that fails Validate refuses the create with ErrInvalid. The create
// has no caller, so the server is the actor of the rule.
func writeNewUnitRule(ctx context.Context, q *db.Queries, u newUnit) error {
	raw, err := q.GetNamespaceOutputSharingDefault(ctx, u.Namespace.String())
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select output sharing setting: %w", err)
	}

	// Defense in depth: the setting store validates each value that it
	// writes. This check stays so that a row of another version or a restored
	// backup fails closed and never gives a unit a rule.
	setting := outputshare.Default(raw)
	if validErr := setting.Validate(); validErr != nil {
		return fmt.Errorf("read output sharing setting: %w", validErr)
	}

	if setting != outputshare.DefaultNamespace {
		return nil
	}

	producer, err := identity.UnitObject(u.Tool, u.Namespace, u.Name)
	if err != nil {
		return fmt.Errorf("%w: the new unit", outputshare.ErrInvalid)
	}

	r := outputshare.Rule{
		CreatedAt: u.At.UTC(),
		ID:        outputshare.ID(ident.NewID(newUnitRuleIDPrefix)),
		CreatedBy: history.ServerSubject,
		Producer:  producer.WithUnitID(u.ID),
		Consumer:  outputshare.Consumer{Kind: outputshare.ConsumerNamespace, Namespace: u.Namespace, ID: ""},
	}

	e := newUnitRuleEntry(r)

	row, err := outputsharestore.CheckPut(r, namespace.Fence{Namespace: u.Namespace, FailoverVersion: 0}, e)
	if err != nil {
		return err
	}

	if putErr := outputsharestore.Put(ctx, ruleTx{q: q}, r, row); putErr != nil {
		return putErr
	}

	if _, appendErr := appendAuditEntry(ctx, q, e); appendErr != nil {
		return fmt.Errorf("append audit entry: %w", appendErr)
	}

	return nil
}

// newUnitRuleEntry returns the audit entry of the rule r of a new unit.
func newUnitRuleEntry(r outputshare.Rule) audit.Entry {
	return audit.Entry{
		At:         r.CreatedAt,
		Namespace:  r.Producer.Namespace(),
		Subject:    history.ServerSubject,
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:manage-sharing",
		Object:     r.Producer.String(),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes: []audit.Change{
			{Key: "output-sharing-rule", Old: nil, New: string(r.ID), Layer: "", TakesEffect: ""},
		},
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      string(r.ID),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        newUnitRuleReason,
		Origin:        nil,
	}
}

// UnitExists implements outputsharestore.Lookup.
func (t ruleTx) UnitExists(ctx context.Context, ns namespace.Name, id string) (bool, error) {
	found, err := t.q.ExistsOutputSharingUnit(ctx, db.ExistsOutputSharingUnitParams{Namespace: ns.String(), ID: id})
	if err != nil {
		return false, fmt.Errorf("select inventory unit by ID: %w", err)
	}

	return found, nil
}

// CollectionExists implements outputsharestore.Lookup.
func (t ruleTx) CollectionExists(ctx context.Context, ns namespace.Name, id string) (bool, error) {
	found, err := t.q.ExistsOutputSharingCollection(ctx, db.ExistsOutputSharingCollectionParams{
		Namespace: ns.String(), ID: id,
	})
	if err != nil {
		return false, fmt.Errorf("select collection by ID: %w", err)
	}

	return found, nil
}

// UnitID implements outputsharestore.Lookup.
func (t ruleTx) UnitID(ctx context.Context, ns namespace.Name, tool, name string) (string, bool, error) {
	id, err := t.q.GetInventoryUnit(ctx, db.GetInventoryUnitParams{Namespace: ns.String(), Tool: tool, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select inventory unit: %w", err)
	}

	return id, true, nil
}

// CollectionID implements outputsharestore.Lookup.
func (t ruleTx) CollectionID(ctx context.Context, ns namespace.Name, nameKey string) (string, bool, error) {
	row, err := t.q.GetCollection(ctx, db.GetCollectionParams{Namespace: ns.String(), NameKey: nameKey})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select collection: %w", err)
	}

	return row.ID, true, nil
}

// Count implements outputsharestore.Lookup.
func (t ruleTx) Count(ctx context.Context, k outputsharestore.Key) (int64, error) {
	n, err := t.q.CountOutputSharingRulesOfProducer(ctx, db.CountOutputSharingRulesOfProducerParams{
		Namespace: k.Namespace.String(), ProducerKind: k.Kind, ProducerID: k.ID,
	})
	if err != nil {
		return 0, fmt.Errorf("count sharing rules: %w", err)
	}

	return n, nil
}

// Insert implements outputsharestore.Lookup.
func (t ruleTx) Insert(ctx context.Context, row outputsharestore.Insert) (int64, error) {
	n, err := t.q.InsertOutputSharingRule(ctx, db.InsertOutputSharingRuleParams(row))
	// Defense in depth: Rule.Validate and the checks of Put refuse the rows that
	// the CHECK constraints and the foreign key refuse. This mapping stays so
	// that a refusal of the table reaches the caller as an invalid rule, and
	// never as a raw driver error.
	if isCheckViolation(err) || isForeignKeyViolation(err) {
		return 0, fmt.Errorf("%w: the table refused the rule", outputshare.ErrInvalid)
	}

	if err != nil {
		return 0, fmt.Errorf("insert sharing rule: %w", err)
	}

	return n, nil
}
