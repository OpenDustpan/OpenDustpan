package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/settingsstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// UnitSettings returns the store of the settings of each unit and of the unit
// defaults of each namespace. Each write checks the installation state and the
// fence of its namespace, and appends its audit entry, in its own transaction.
func (s *Store) UnitSettings() UnitSettingsStore {
	return UnitSettingsStore{s: s}
}

// UnitSettingsStore is unitsettings.Store over the database of a Store.
type UnitSettingsStore struct {
	s *Store
}

var _ unitsettings.Store = UnitSettingsStore{s: nil}

// Get implements unitsettings.Store.
func (u UnitSettingsStore) Get(ctx context.Context, unit identity.Object) (unitsettings.Settings, error) {
	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return settingsstore.Read(ctx, settingsTx{q: u.s.queries}, unit)
}

// Put implements unitsettings.Store.
func (u UnitSettingsStore) Put(
	ctx context.Context, s *unitsettings.Settings, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.PutResult, error) {
	if err := s.Validate(); err != nil {
		return unitsettings.PutResult{}, err
	}

	if err := settingsstore.CheckFence(s.Unit.Namespace(), fence); err != nil {
		return unitsettings.PutResult{}, err
	}

	if err := settingsstore.CheckEntry(&e, fence.Namespace); err != nil {
		return unitsettings.PutResult{}, err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	var out unitsettings.PutResult

	err := u.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		tx := settingsTx{q: q}

		stored, entry, err := settingsstore.Write(ctx, tx, s, expected, e)
		if err != nil {
			return err
		}

		// The defaults are read in the transaction of the write, so the
		// effective value that the caller computes from them matches the
		// record that this write stores.
		defaults, err := settingsstore.ReadDefaults(ctx, tx, s.Unit.Namespace())
		if err != nil {
			return err
		}

		out = unitsettings.PutResult{Settings: stored, Defaults: defaults}

		return appendAndFinish(ctx, q, entry, out, nil)
	})
	if err != nil {
		return unitsettings.PutResult{}, err
	}

	return out, nil
}

// Delete implements unitsettings.Store.
func (u UnitSettingsStore) Delete(
	ctx context.Context, unit identity.Object, expected int64, fence namespace.Fence, e audit.Entry,
) error {
	if err := settingsstore.CheckFence(unit.Namespace(), fence); err != nil {
		return err
	}

	if err := settingsstore.CheckEntry(&e, fence.Namespace); err != nil {
		return err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return u.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		entry, err := settingsstore.Remove(ctx, settingsTx{q: q}, unit, expected, e)
		if err != nil {
			return err
		}

		var gone unitsettings.Settings

		gone.Unit = unit

		return appendAndFinish(ctx, q, &entry, gone, nil)
	})
}

// Defaults implements unitsettings.Store.
func (u UnitSettingsStore) Defaults(ctx context.Context, ns namespace.Name) (unitsettings.NamespaceDefaults, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return unitsettings.NamespaceDefaults{}, fmt.Errorf("%w: namespace name", unitsettings.ErrInvalid)
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	return settingsstore.ReadDefaults(ctx, settingsTx{q: u.s.queries}, ns)
}

// SetDefaults implements unitsettings.Store.
func (u UnitSettingsStore) SetDefaults(
	ctx context.Context, d unitsettings.NamespaceDefaults, expected int64, fence namespace.Fence, e audit.Entry,
) (unitsettings.NamespaceDefaults, error) {
	if err := d.Validate(); err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	if err := settingsstore.CheckFence(d.Namespace, fence); err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	if err := settingsstore.CheckEntry(&e, fence.Namespace); err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	u.s.mu.RLock()
	defer u.s.mu.RUnlock()

	stored := d

	err := u.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		var (
			entry *audit.Entry
			err   error
		)

		stored, entry, err = settingsstore.WriteDefaults(ctx, settingsTx{q: q}, d, expected, e)

		return appendAndFinish(ctx, q, entry, stored, err)
	})
	if err != nil {
		return unitsettings.NamespaceDefaults{}, err
	}

	return stored, nil
}

// appendAndFinish appends entry in the transaction of q, and then stores the
// answer of the request when ctx carries an idempotency.Completion. result is
// what the write passes to the Completion. The answer is the last step of the
// transaction. A nil entry is a write that changed nothing, and it appends
// nothing. An error err of the write comes back as it is.
func appendAndFinish(ctx context.Context, q *db.Queries, entry *audit.Entry, result any, err error) error {
	if err != nil {
		return err
	}

	if entry != nil {
		if _, appendErr := appendAuditEntry(ctx, q, *entry); appendErr != nil {
			return fmt.Errorf("append audit entry: %w", appendErr)
		}
	}

	return finishCompletion(ctx, q, result)
}

// settingsTx is the settingsstore.Tx over one SQLite transaction or the
// database.
type settingsTx struct {
	q *db.Queries
}

var _ settingsstore.Tx = settingsTx{q: nil}

// UnitID implements settingsstore.Tx.
func (t settingsTx) UnitID(ctx context.Context, obj identity.Object) (string, bool, error) {
	return unitIDWith(ctx, t.q, obj)
}

// Get implements settingsstore.Tx.
func (t settingsTx) Get(ctx context.Context, ns namespace.Name, unitID string) (settingsstore.Row, bool, error) {
	var none settingsstore.Row

	row, err := t.q.GetUnitSettings(ctx, db.GetUnitSettingsParams{Namespace: ns.String(), UnitID: unitID})
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select unit settings: %w", err)
	}

	return settingsstore.Row{
		UnitID: row.UnitID, Namespace: row.Namespace, Body: row.Body, Version: row.Version,
		UpdatedAtMs: row.UpdatedAtMs,
	}, true, nil
}

// Insert implements settingsstore.Tx.
func (t settingsTx) Insert(ctx context.Context, r settingsstore.Row) (bool, error) {
	n, err := t.q.InsertUnitSettings(ctx, db.InsertUnitSettingsParams{
		UnitID: r.UnitID, Namespace: r.Namespace, Body: r.Body, UpdatedAtMs: r.UpdatedAtMs,
	})
	if err != nil {
		return false, fmt.Errorf("insert unit settings: %w", err)
	}

	return n == 1, nil
}

// Update implements settingsstore.Tx.
func (t settingsTx) Update(ctx context.Context, r settingsstore.Row, expected int64) (bool, error) {
	n, err := t.q.UpdateUnitSettings(ctx, db.UpdateUnitSettingsParams{
		Body: r.Body, Expected: expected, UpdatedAtMs: r.UpdatedAtMs, UnitID: r.UnitID, Namespace: r.Namespace,
	})
	if err != nil {
		return false, fmt.Errorf("update unit settings: %w", err)
	}

	// Defense in depth: the query matches the version and the fenced
	// transaction serializes the writers. This check stays so that a statement
	// that matches no row, or more than one, never reads as a success, and it
	// relies on neither the version compare of the store nor the lock of the
	// namespace row.

	return n == 1, nil
}

// Delete implements settingsstore.Tx.
func (t settingsTx) Delete(ctx context.Context, ns namespace.Name, unitID string, expected int64) (bool, error) {
	n, err := t.q.DeleteUnitSettings(ctx, db.DeleteUnitSettingsParams{
		UnitID: unitID, Expected: expected, Namespace: ns.String(),
	})
	if err != nil {
		return false, fmt.Errorf("delete unit settings: %w", err)
	}

	// Defense in depth: the query matches the version and the fenced
	// transaction serializes the writers. This check stays so that a statement
	// that matches no row, or more than one, never reads as a success, and it
	// relies on neither the version compare of the store nor the lock of the
	// namespace row.

	return n == 1, nil
}

// Defaults implements settingsstore.Tx.
func (t settingsTx) Defaults(ctx context.Context, ns namespace.Name) (settingsstore.DefaultsRow, bool, error) {
	var none settingsstore.DefaultsRow

	row, err := t.q.GetUnitDefaults(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select unit defaults: %w", err)
	}

	return settingsstore.DefaultsRow{
		Namespace: row.Namespace, Pool: row.Pool, Version: row.Version, UpdatedAtMs: row.UpdatedAtMs,
	}, true, nil
}

// InsertDefaults implements settingsstore.Tx.
func (t settingsTx) InsertDefaults(ctx context.Context, r settingsstore.DefaultsRow) (bool, error) {
	n, err := t.q.InsertUnitDefaults(ctx, db.InsertUnitDefaultsParams{
		Namespace: r.Namespace, Pool: r.Pool, UpdatedAtMs: r.UpdatedAtMs,
	})
	if err != nil {
		return false, fmt.Errorf("insert unit defaults: %w", err)
	}

	return n == 1, nil
}

// UpdateDefaults implements settingsstore.Tx.
func (t settingsTx) UpdateDefaults(ctx context.Context, r settingsstore.DefaultsRow, expected int64) (bool, error) {
	n, err := t.q.UpdateUnitDefaults(ctx, db.UpdateUnitDefaultsParams{
		Pool: r.Pool, Expected: expected, UpdatedAtMs: r.UpdatedAtMs, Namespace: r.Namespace,
	})
	if err != nil {
		return false, fmt.Errorf("update unit defaults: %w", err)
	}

	// Defense in depth: the query matches the version and the fenced
	// transaction serializes the writers. This check stays so that a statement
	// that matches no row, or more than one, never reads as a success, and it
	// relies on neither the version compare of the store nor the lock of the
	// namespace row.

	return n == 1, nil
}

// NamespaceExists implements settingsstore.Tx.
func (t settingsTx) NamespaceExists(ctx context.Context, ns namespace.Name) (bool, error) {
	_, err := t.q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("select namespace ID: %w", err)
	}

	return true, nil
}
