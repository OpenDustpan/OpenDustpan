// Package settingsstore holds the rules of the unit settings tables that the
// SQLite and PostgreSQL adapters share: the check of a unit, the version rules
// of a write, the no-change rule and the audit entry of each write. An adapter
// gives it a Tx, which reads and writes the rows of one transaction, so the
// adapters differ in their queries only.
//
// A stored row is untrusted when it is read, because a backup, a restore or
// another version of the server can have written it.
package settingsstore

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// Row is one stored settings record of a unit.
type Row struct {
	UnitID      string
	Namespace   string
	Body        string
	Version     int64
	UpdatedAtMs int64
}

// DefaultsRow is the stored unit defaults of a namespace.
type DefaultsRow struct {
	Namespace   string
	Pool        string
	Version     int64
	UpdatedAtMs int64
}

// Tx reads and writes the rows of one transaction. The adapter has checked the
// fence of the namespace in that transaction before it calls a function of
// this package, and it holds the namespace row, so two writers of one namespace
// run one after the other.
type Tx interface {
	RecordTx
	DefaultsTx
}

// RecordTx reads and writes the settings rows of units.
type RecordTx interface {
	// UnitID returns the stable ID of the live unit obj, and false when the
	// unit does not exist.
	UnitID(ctx context.Context, obj identity.Object) (string, bool, error)
	// Get returns the row of the unit in the namespace, and false when none.
	Get(ctx context.Context, ns namespace.Name, unitID string) (Row, bool, error)
	// Insert stores r as the first row of its unit. It returns false when a
	// row of the unit exists.
	Insert(ctx context.Context, r Row) (bool, error)
	// Update replaces the row of r.UnitID when its version is expected, and
	// sets the version to expected + 1. It returns false when no row matches.
	Update(ctx context.Context, r Row, expected int64) (bool, error)
	// Delete removes the row of the unit when its version is expected. It
	// returns false when no row matches.
	Delete(ctx context.Context, ns namespace.Name, unitID string, expected int64) (bool, error)
}

// DefaultsTx reads and writes the defaults rows of namespaces.
type DefaultsTx interface {
	// Defaults returns the defaults row of the namespace, and false when none.
	Defaults(ctx context.Context, ns namespace.Name) (DefaultsRow, bool, error)
	// InsertDefaults stores r as the first defaults row of its namespace. It
	// returns false when a row exists.
	InsertDefaults(ctx context.Context, r DefaultsRow) (bool, error)
	// UpdateDefaults replaces the defaults row when its version is expected,
	// and sets the version to expected + 1. It returns false when no row
	// matches.
	UpdateDefaults(ctx context.Context, r DefaultsRow, expected int64) (bool, error)
	// NamespaceExists reports whether the namespace exists.
	NamespaceExists(ctx context.Context, ns namespace.Name) (bool, error)
}

// CheckEntry returns an error that wraps unitsettings.ErrInvalid and
// audit.ErrInvalidEntry when e fails validation or names a namespace other
// than ns.
//
// Defense in depth: the fenced transaction checks the namespace of the fence,
// and the append validates the entry. This check stays so that a refusal needs
// no transaction and a new caller cannot write an entry of another namespace.
func CheckEntry(e *audit.Entry, ns namespace.Name) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: audit entry: %w", unitsettings.ErrInvalid, err)
	}

	if e.Namespace != ns {
		return fmt.Errorf("%w: entry names %q, want %q: %w", unitsettings.ErrInvalid, e.Namespace, ns,
			audit.ErrInvalidEntry)
	}

	return nil
}

// CheckFence returns namespace.ErrFenced unless the fence names ns.
//
// Defense in depth: the fence check of the transaction compares the failover
// version of the namespace row of the fence. Only this comparison stops the
// settings of a unit of one namespace from being written under a valid fence
// of another namespace.
func CheckFence(ns namespace.Name, fence namespace.Fence) error {
	if fence.Namespace != ns {
		return fmt.Errorf("%w: write names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return nil
}

// resolve returns the stable ID of unit after it checked that the live unit has
// that ID. It returns unitsettings.ErrInvalid for a reference that is not a unit
// with an ID, and unitsettings.ErrUnknownUnit for a unit that does not exist or
// whose ID is another one.
func resolve(ctx context.Context, tx RecordTx, unit identity.Object) (string, error) {
	if !unit.IsUnit() || unit.UnitID() == "" {
		return "", fmt.Errorf("%w: the unit must be a unit reference with a stable unit ID", unitsettings.ErrInvalid)
	}

	id, found, err := tx.UnitID(ctx, unit)
	if err != nil {
		return "", err
	}

	// The ID and the name must point to the same unit: a deleted and created
	// again name has another ID and does not get the record of the old unit.
	if !found || id != unit.UnitID() {
		return "", unitsettings.ErrUnknownUnit
	}

	return id, nil
}

// settingsOf parses the stored row of unit. It fails closed: a body that fails
// the checks gives unitsettings.ErrInvalid, and the error never holds the body.
func settingsOf(unit identity.Object, row Row) (unitsettings.Settings, error) {
	// Defense in depth: the query matched the namespace and the unit ID. This
	// check stays so that a change of the query cannot serve the record of
	// another unit.
	if row.UnitID != unit.UnitID() || row.Namespace != unit.Namespace().String() {
		return zero(), unitsettings.ErrNotFound
	}

	s, err := unitsettings.Parse([]byte(row.Body))
	if err != nil {
		return zero(), fmt.Errorf("stored settings of a unit: %w", err)
	}

	s.Unit = unit
	s.Version = row.Version
	s.UpdatedAt = codec.FromMillis(row.UpdatedAtMs)

	return s, nil
}

func zero() unitsettings.Settings {
	var s unitsettings.Settings

	return s
}

// Read returns the record of unit.
func Read(ctx context.Context, tx RecordTx, unit identity.Object) (unitsettings.Settings, error) {
	id, err := resolve(ctx, tx, unit)
	if err != nil {
		return zero(), err
	}

	row, found, err := tx.Get(ctx, unit.Namespace(), id)
	if err != nil {
		return zero(), err
	}

	if !found {
		return zero(), unitsettings.ErrNotFound
	}

	return settingsOf(unit, row)
}

// previous reads the record that a write replaces. It returns nil for a unit
// with no record and for a stored record that fails the checks: a valid write
// repairs such a record, as the audit entry then names no old value.
func previous(unit identity.Object, row *Row) *unitsettings.Settings {
	if row == nil {
		return nil
	}

	s, err := settingsOf(unit, *row)
	if err != nil {
		return nil
	}

	return &s
}

// Write stores s as the record of s.Unit, as unitsettings.Store.Put says. It
// returns the stored record and the audit entry that the adapter must append in
// the same transaction. The entry is nil when the write changes nothing.
func Write(
	ctx context.Context, tx RecordTx, s *unitsettings.Settings, expected int64, e audit.Entry,
) (unitsettings.Settings, *audit.Entry, error) {
	if err := s.Validate(); err != nil {
		return zero(), nil, err
	}

	id, err := resolve(ctx, tx, s.Unit)
	if err != nil {
		return zero(), nil, err
	}

	ns := s.Unit.Namespace()

	row, found, err := tx.Get(ctx, ns, id)
	if err != nil {
		return zero(), nil, err
	}

	var existing *Row
	if found {
		existing = &row
	}

	if versionMismatch(existing, expected) {
		return zero(), nil, unitsettings.ErrConflict
	}

	old := previous(s.Unit, existing)
	if old != nil && unitsettings.Equal(old, s) {
		return *old, nil, nil
	}

	body, err := unitsettings.Marshal(s)
	if err != nil {
		return zero(), nil, err
	}

	next := *s
	next.Version = expected + 1
	next.UpdatedAt = codec.FromMillis(codec.Millis(e.At))

	stored := Row{
		UnitID: id, Namespace: ns.String(), Body: string(body), Version: next.Version,
		UpdatedAtMs: codec.Millis(e.At),
	}
	if err := persist(ctx, tx, existing, stored, expected); err != nil {
		return zero(), nil, err
	}

	entry := unitsettings.ChangeEntry(e, old, &next)
	entry.ObjectID = id

	if err := entry.Validate(); err != nil {
		return zero(), nil, fmt.Errorf("%w: audit entry: %w", unitsettings.ErrInvalid, err)
	}

	return next, &entry, nil
}

// versionMismatch reports whether expected is not the version of the stored
// row: a row at another version, or no row and a version other than 0.
func versionMismatch(existing *Row, expected int64) bool {
	if existing == nil {
		return expected != 0
	}

	return existing.Version != expected
}

// persist inserts r as the first row of its unit when existing is nil, and
// replaces the row at expected otherwise. A row that changed under the write
// gives unitsettings.ErrConflict.
func persist(ctx context.Context, tx RecordTx, existing *Row, r Row, expected int64) error {
	var (
		ok  bool
		err error
	)

	if existing == nil {
		ok, err = tx.Insert(ctx, r)
	} else {
		ok, err = tx.Update(ctx, r, expected)
	}

	if err != nil {
		return err
	}

	if !ok {
		return unitsettings.ErrConflict
	}

	return nil
}

// Remove deletes the record of unit, as unitsettings.Store.Delete says. It
// returns the audit entry that the adapter must append in the same transaction.
func Remove(
	ctx context.Context, tx RecordTx, unit identity.Object, expected int64, e audit.Entry,
) (audit.Entry, error) {
	var none audit.Entry

	if expected < 1 {
		return none, fmt.Errorf("%w: a delete names the version that the caller read, one or more",
			unitsettings.ErrInvalid)
	}

	id, err := resolve(ctx, tx, unit)
	if err != nil {
		return none, err
	}

	ns := unit.Namespace()

	row, found, err := tx.Get(ctx, ns, id)
	if err != nil {
		return none, err
	}

	if !found {
		return none, unitsettings.ErrNotFound
	}

	if row.Version != expected {
		return none, unitsettings.ErrConflict
	}

	ok, err := tx.Delete(ctx, ns, id, expected)
	if err != nil {
		return none, err
	}

	if !ok {
		return none, unitsettings.ErrConflict
	}

	entry := unitsettings.ResetEntry(e, previous(unit, &row))
	entry.ObjectID = id

	if err := entry.Validate(); err != nil {
		return none, fmt.Errorf("%w: audit entry: %w", unitsettings.ErrInvalid, err)
	}

	return entry, nil
}

// ReadDefaults returns the unit defaults of ns. A namespace with no row gives
// the empty defaults at version 0, and an unknown namespace gives
// namespace.ErrNotFound.
func ReadDefaults(ctx context.Context, tx DefaultsTx, ns namespace.Name) (unitsettings.NamespaceDefaults, error) {
	none := emptyDefaults(ns)

	row, found, err := tx.Defaults(ctx, ns)
	if err != nil {
		return none, err
	}

	if !found {
		exists, existsErr := tx.NamespaceExists(ctx, ns)
		if existsErr != nil {
			return none, existsErr
		}

		if !exists {
			return none, namespace.ErrNotFound
		}

		return none, nil
	}

	d := defaultsOf(ns, row)
	if err := d.Validate(); err != nil {
		return none, fmt.Errorf("stored unit defaults: %w", err)
	}

	return d, nil
}

// emptyDefaults returns the defaults of a namespace that never set them.
func emptyDefaults(ns namespace.Name) unitsettings.NamespaceDefaults {
	return unitsettings.NamespaceDefaults{Namespace: ns, Pool: "", Version: 0, UpdatedAt: time.Time{}}
}

func defaultsOf(ns namespace.Name, row DefaultsRow) unitsettings.NamespaceDefaults {
	return unitsettings.NamespaceDefaults{
		Namespace: ns, Pool: row.Pool, Version: row.Version, UpdatedAt: codec.FromMillis(row.UpdatedAtMs),
	}
}

// WriteDefaults stores d as the unit defaults of d.Namespace, as
// unitsettings.Store.SetDefaults says. It returns the stored defaults and the
// audit entry that the adapter must append in the same transaction. The entry
// is nil when the write changes nothing.
func WriteDefaults(
	ctx context.Context, tx DefaultsTx, d unitsettings.NamespaceDefaults, expected int64, e audit.Entry,
) (unitsettings.NamespaceDefaults, *audit.Entry, error) {
	if err := d.Validate(); err != nil {
		return d, nil, err
	}

	row, found, err := tx.Defaults(ctx, d.Namespace)
	if err != nil {
		return d, nil, err
	}

	var existing *DefaultsRow
	if found {
		existing = &row
	}

	old := emptyDefaults(d.Namespace)
	if existing != nil {
		old = defaultsOf(d.Namespace, *existing)
	}

	if old.Version != expected {
		return d, nil, unitsettings.ErrConflict
	}

	// A write of the pool that is stored changes nothing. A namespace with no
	// row and an empty pool has nothing to store.
	if old.Pool == d.Pool && (existing != nil || d.Pool == "") {
		return old, nil, nil
	}

	stored := DefaultsRow{
		Namespace: d.Namespace.String(), Pool: d.Pool, Version: expected + 1, UpdatedAtMs: codec.Millis(e.At),
	}
	if err := persistDefaults(ctx, tx, existing, stored, expected); err != nil {
		return d, nil, err
	}

	next := defaultsOf(d.Namespace, stored)
	entry := unitsettings.DefaultsEntry(e, old, next)

	if err := entry.Validate(); err != nil {
		return d, nil, fmt.Errorf("%w: audit entry: %w", unitsettings.ErrInvalid, err)
	}

	return next, &entry, nil
}

// persistDefaults inserts r as the first defaults row of its namespace when
// existing is nil, and replaces the row at expected otherwise. A row that
// changed under the write gives unitsettings.ErrConflict.
func persistDefaults(ctx context.Context, tx DefaultsTx, existing *DefaultsRow, r DefaultsRow, expected int64) error {
	var (
		ok  bool
		err error
	)

	if existing == nil {
		ok, err = tx.InsertDefaults(ctx, r)
	} else {
		ok, err = tx.UpdateDefaults(ctx, r, expected)
	}

	if err != nil {
		return err
	}

	if !ok {
		return unitsettings.ErrConflict
	}

	return nil
}
