package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ gate.Store = (*Store)(nil)

// Gates implements gate.Store.
func (s *Store) Gates() gate.Objects[gate.Gate] { return s.gate.Gates() }

// AccessLimits implements gate.Store.
func (s *Store) AccessLimits() gate.Objects[gate.AccessLimit] { return s.gate.AccessLimits() }

// ApprovalRules implements gate.Store.
func (s *Store) ApprovalRules() gate.Objects[gate.ApprovalRule] { return s.gate.ApprovalRules() }

// OwnershipRules implements gate.Store.
func (s *Store) OwnershipRules() gate.Objects[gate.OwnershipRule] { return s.gate.OwnershipRules() }

// PathSets implements gate.Store.
func (s *Store) PathSets() gate.Objects[gate.PathSet] { return s.gate.PathSets() }

// Settings implements gate.Store.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *Store) Settings() gate.Settings { return s.gate.Settings() }

// Records implements gate.Store.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *Store) Records() gate.Records { return s.gate.Records() }

// Snapshot implements gate.Store.
func (s *Store) Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error) {
	return s.gate.Snapshot(ctx, scopes)
}

// gateRunner opens the transactions of the gate store.
type gateRunner struct {
	s *Store
}

var _ gatestore.Runner = gateRunner{s: nil}

// Write implements gatestore.Runner.
func (r gateRunner) Write(
	ctx context.Context, scope gate.Scope, fence namespace.Fence, e audit.Entry, fn func(gatestore.Tx) error,
) error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	run := func(q *db.Queries) error { return fn(newGateTx(q)) }

	if scope.Level() == gate.LevelInstallation {
		return r.s.inAuditedTx(ctx, e, run)
	}

	return r.s.inFencedAuditedTx(ctx, fence, &e, run)
}

// WriteRecord implements gatestore.Runner.
func (r gateRunner) WriteRecord(
	ctx context.Context, ns namespace.Name, fence namespace.Fence, fn func(gatestore.Tx) error,
) error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	run := func(q *db.Queries) error { return fn(newGateTx(q)) }

	if ns == audit.ServerScope {
		return r.s.inTx(ctx, run)
	}

	return r.s.inFencedTx(ctx, fence, run)
}

// Read implements gatestore.Runner.
func (r gateRunner) Read(ctx context.Context, fn func(gatestore.Tx) error) error {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	return r.s.inReadTx(ctx, func(q *db.Queries) error { return fn(newGateTx(q)) })
}

// gateTx reads and writes the gate rows of one transaction.
type gateTx struct {
	q   *db.Queries
	ids map[namespace.Name]string
	// units holds the stable unit ID of each unit scope key that the
	// transaction resolved, and names holds the scope key of each unit ID.
	units map[string]string
	names map[string]string
}

var _ gatestore.Tx = (*gateTx)(nil)

func newGateTx(q *db.Queries) *gateTx {
	return &gateTx{q: q, ids: map[namespace.Name]string{}, units: map[string]string{}, names: map[string]string{}}
}

func (t *gateTx) ScopeExists(ctx context.Context, scope gate.Scope) (bool, error) {
	_, ok, err := t.rowScopeOf(ctx, scope)

	return ok, err
}

func (t *gateTx) Get(ctx context.Context, scope gate.Scope, kind, id string) (gatestore.Row, bool, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return noRow(), false, err
	}

	row, err := t.q.GetGateObject(ctx, db.GetGateObjectParams{
		NamespaceID: rs.nsID, ScopeKey: rs.key, Kind: kind, ID: id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return noRow(), false, nil
	}

	if err != nil {
		return gatestore.Row{}, false, fmt.Errorf("select gate object: %w", err)
	}

	return gatestore.Row{Kind: kind, ID: id, Version: row.Version, Body: row.Body}, true, nil
}

func (t *gateTx) List(ctx context.Context, scope gate.Scope, kind string) ([]gatestore.Row, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateObjects(
		ctx,
		db.ListGateObjectsParams{NamespaceID: rs.nsID, ScopeKey: rs.key, Kind: kind},
	)
	if err != nil {
		return nil, fmt.Errorf("select gate objects: %w", err)
	}

	out := make([]gatestore.Row, len(rows))
	for i, r := range rows {
		out[i] = gatestore.Row{Kind: kind, ID: r.ID, Version: r.Version, Body: r.Body}
	}

	return out, nil
}

func (t *gateTx) ListScope(ctx context.Context, scope gate.Scope) ([]gatestore.Row, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateObjectsOfScope(
		ctx,
		db.ListGateObjectsOfScopeParams{NamespaceID: rs.nsID, ScopeKey: rs.key},
	)
	if err != nil {
		return nil, fmt.Errorf("select gate objects of scope: %w", err)
	}

	out := make([]gatestore.Row, len(rows))
	for i, r := range rows {
		out[i] = gatestore.Row{Kind: r.Kind, ID: r.ID, Version: r.Version, Body: r.Body}
	}

	return out, nil
}

func (t *gateTx) Count(ctx context.Context, scope gate.Scope, kind string) (int, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return 0, err
	}

	n, err := t.q.CountGateObjects(ctx, db.CountGateObjectsParams{NamespaceID: rs.nsID, ScopeKey: rs.key, Kind: kind})
	if err != nil {
		return 0, fmt.Errorf("count gate objects: %w", err)
	}

	return int(n), nil
}

func (t *gateTx) Insert(ctx context.Context, scope gate.Scope, row gatestore.Row, at time.Time) error {
	rs, err := t.writeScopeOf(ctx, scope)
	if err != nil {
		return err
	}

	err = t.q.InsertGateObject(ctx, db.InsertGateObjectParams{
		NamespaceID: rs.nsID, ScopeKey: rs.key, Kind: row.Kind, ID: row.ID, Level: string(scope.Level()),
		UnitID: rs.unitID, Version: row.Version, Body: row.Body, UpdatedAtMs: at.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("insert gate object: %w", err)
	}

	return nil
}

func (t *gateTx) Update(
	ctx context.Context,
	scope gate.Scope,
	row gatestore.Row,
	prev int64,
	at time.Time,
) (bool, error) {
	rs, err := t.writeScopeOf(ctx, scope)
	if err != nil {
		return false, err
	}

	n, err := t.q.UpdateGateObject(ctx, db.UpdateGateObjectParams{
		Version: row.Version, Body: row.Body, UpdatedAtMs: at.UnixMilli(), NamespaceID: rs.nsID, ScopeKey: rs.key,
		Kind: row.Kind, ID: row.ID, PrevVersion: prev,
	})
	if err != nil {
		return false, fmt.Errorf("update gate object: %w", err)
	}

	return n == 1, nil
}

func (t *gateTx) Delete(ctx context.Context, scope gate.Scope, kind, id string, prev int64) (bool, error) {
	rs, err := t.writeScopeOf(ctx, scope)
	if err != nil {
		return false, err
	}

	n, err := t.q.DeleteGateObject(ctx, db.DeleteGateObjectParams{
		NamespaceID: rs.nsID, ScopeKey: rs.key, Kind: kind, ID: id, PrevVersion: prev,
	})
	if err != nil {
		return false, fmt.Errorf("delete gate object: %w", err)
	}

	return n == 1, nil
}

func (t *gateTx) SetReferences(ctx context.Context, scope gate.Scope, kind, id string, refs []gatestore.Ref) error {
	rs, err := t.writeScopeOf(ctx, scope)
	if err != nil {
		return err
	}

	err = t.q.DeleteGateReferences(ctx, db.DeleteGateReferencesParams{
		NamespaceID: rs.nsID, ScopeKey: rs.key, OwnerKind: kind, OwnerID: id,
	})
	if err != nil {
		return fmt.Errorf("delete gate references: %w", err)
	}

	for _, ref := range refs {
		err = t.q.InsertGateReference(ctx, db.InsertGateReferenceParams{
			RefKind: ref.Kind, RefID: ref.ID, NamespaceID: rs.nsID, ScopeKey: rs.key, OwnerKind: kind, OwnerID: id,
		})
		if err != nil {
			return fmt.Errorf("insert gate reference: %w", err)
		}
	}

	return nil
}

func (t *gateTx) Narrower(ctx context.Context, scope gate.Scope, kind, id string) ([]gatestore.Located, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListNarrowerGateObjects(ctx, db.ListNarrowerGateObjectsParams{
		Kind: kind, ID: id, Level: string(scope.Level()), NamespaceID: rs.nsID, ScopeKey: rs.key,
		CollectionKey: scope.Collection(),
	})
	if err != nil {
		return nil, fmt.Errorf("select narrower gate objects: %w", err)
	}

	out := make([]gatestore.Located, len(rows))
	for i, r := range rows {
		key, kerr := t.locatedKey(ctx, r.NamespaceID, r.ScopeKey, r.UnitID)
		if kerr != nil {
			return nil, kerr
		}

		out[i] = gatestore.Located{
			ScopeKey: key, Row: gatestore.Row{Kind: kind, ID: id, Version: r.Version, Body: r.Body},
		}
	}

	return out, nil
}

func (t *gateTx) Referrers(ctx context.Context, scope gate.Scope, ref gatestore.Ref) ([]gatestore.Located, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateReferrers(ctx, db.ListGateReferrersParams{
		RefKind: ref.Kind, RefID: ref.ID, Level: string(scope.Level()), NamespaceID: rs.nsID, ScopeKey: rs.key,
		CollectionKey: scope.Collection(),
	})
	if err != nil {
		return nil, fmt.Errorf("select gate referrers: %w", err)
	}

	out := make([]gatestore.Located, len(rows))
	for i, r := range rows {
		key, kerr := t.locatedKey(ctx, r.NamespaceID, r.ScopeKey, r.UnitID)
		if kerr != nil {
			return nil, kerr
		}

		out[i] = gatestore.Located{
			ScopeKey: key, Row: gatestore.Row{Kind: r.Kind, ID: r.ID, Version: r.Version, Body: r.Body},
		}
	}

	return out, nil
}

func (t *gateTx) UnitCollection(ctx context.Context, scope gate.Scope) (string, bool, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil || !ok || rs.unitID == "" {
		return "", false, err
	}

	key, err := t.q.GateUnitCollection(
		ctx,
		db.GateUnitCollectionParams{NamespaceID: rs.nsID, UnitID: rs.unitID},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select unit collection: %w", err)
	}

	return key, true, nil
}

func (t *gateTx) RecordExists(ctx context.Context, id string) (bool, error) {
	_, err := t.q.GetGateRecordByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("select gate record: %w", err)
	}

	return true, nil
}

func (t *gateTx) InsertRecord(ctx context.Context, ns namespace.Name, r gatestore.RecordRow) error {
	nsID, ok, err := t.namespaceID(ctx, ns)
	if err != nil {
		return err
	}

	if !ok {
		return namespace.ErrNotFound
	}

	err = t.q.InsertGateRecord(ctx, db.InsertGateRecordParams{
		ID: r.ID, NamespaceID: nsID, Point: r.Point, ChangeKind: r.ChangeKind, ChangeID: r.ChangeID, AtMs: r.AtMs,
		LastAtMs: r.LastAtMs, Count: r.Count, RefusalKey: r.RefusalKey, Body: r.Body,
	})
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: the record ID %s is used", gate.ErrConflict, r.ID)
	}

	if err != nil {
		return fmt.Errorf("insert gate record: %w", err)
	}

	return nil
}

func (t *gateTx) Record(ctx context.Context, ns namespace.Name, id string) (string, bool, error) {
	nsID, ok, err := t.namespaceID(ctx, ns)
	if err != nil || !ok {
		return "", false, err
	}

	body, err := t.q.GetGateRecord(ctx, db.GetGateRecordParams{ID: id, NamespaceID: nsID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select gate record: %w", err)
	}

	return body, true, nil
}

func (t *gateTx) RecordsByChange(ctx context.Context, ns namespace.Name, kind, id string) ([]string, error) {
	nsID, ok, err := t.namespaceID(ctx, ns)
	if err != nil || !ok {
		return nil, err
	}

	bodies, err := t.q.ListGateRecordsByChange(ctx, db.ListGateRecordsByChangeParams{
		NamespaceID: nsID, ChangeKind: kind, ChangeID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("select gate records of a change: %w", err)
	}

	return bodies, nil
}

func (t *gateTx) QueryRecords(ctx context.Context, ns namespace.Name, w gatestore.Window) ([]string, error) {
	nsID, ok, err := t.namespaceID(ctx, ns)
	if err != nil || !ok {
		return nil, err
	}

	bodies, err := t.q.QueryGateRecords(ctx, db.QueryGateRecordsParams{
		NamespaceID: nsID, Point: w.Point, FromMs: w.From.UnixMilli(), ToMs: w.To.UnixMilli(), MaxRows: int64(w.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select gate records: %w", err)
	}

	return bodies, nil
}

func (t *gateTx) FindRefusal(
	ctx context.Context, ns namespace.Name, key string, since time.Time,
) (gatestore.RecordRow, bool, error) {
	nsID, ok, err := t.namespaceID(ctx, ns)
	if err != nil || !ok {
		return noRecordRow(), false, err
	}

	row, err := t.q.FindGateRefusal(ctx, db.FindGateRefusalParams{
		NamespaceID: nsID, RefusalKey: key, SinceMs: since.UnixMilli(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return noRecordRow(), false, nil
	}

	if err != nil {
		return noRecordRow(), false, fmt.Errorf("select gate refusal: %w", err)
	}

	return gatestore.RecordRow{
		ID: row.ID, Point: "", ChangeKind: "", ChangeID: "", AtMs: 0, LastAtMs: 0, Count: row.Count,
		RefusalKey: key, Body: row.Body,
	}, true, nil
}

func (t *gateTx) UpdateRefusal(ctx context.Context, r gatestore.RecordRow) error {
	err := t.q.UpdateGateRefusal(
		ctx,
		db.UpdateGateRefusalParams{ID: r.ID, Count: r.Count, LastAtMs: r.LastAtMs, Body: r.Body},
	)
	if err != nil {
		return fmt.Errorf("update gate refusal: %w", err)
	}

	return nil
}

// rowScope is where the rows of a scope are: the ID of its namespace, the key
// that the rows hold, and the stable ID of the unit of a unit scope.
type rowScope struct {
	nsID   string
	key    string
	unitID string
}

// rowScopeOf returns the rows of scope: the scope key for each level but a
// unit, and the key of the stable ID of the live unit that holds the name for
// a unit. It reports false for a namespace that does not exist and for a unit
// scope whose name no live unit holds.
func (t *gateTx) rowScopeOf(ctx context.Context, scope gate.Scope) (rowScope, bool, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return rowScope{nsID: "", key: "", unitID: ""}, false, err
	}

	if scope.Level() != gate.LevelUnit {
		return rowScope{nsID: nsID, key: scope.Key(), unitID: ""}, true, nil
	}

	id, ok, err := t.unitID(ctx, scope)
	if err != nil || !ok {
		return rowScope{nsID: "", key: "", unitID: ""}, false, err
	}

	return rowScope{nsID: nsID, key: gatestore.UnitRowKey(id), unitID: id}, true, nil
}

// unitID returns the stable ID of the live unit that holds the name of the
// unit scope, and false when no live unit holds it. A deleted unit never
// holds a name, so a new unit with the name of a deleted unit has none of its
// rows.
func (t *gateTx) unitID(ctx context.Context, scope gate.Scope) (string, bool, error) {
	if id, ok := t.units[scope.Key()]; ok {
		return id, true, nil
	}

	obj, ok := scope.Unit()
	if !ok {
		return "", false, fmt.Errorf("%w: %s is not a unit scope", gate.ErrInvalid, scope)
	}

	id, err := t.q.GetInventoryUnit(ctx, db.GetInventoryUnitParams{
		Namespace: obj.Namespace().String(), Tool: obj.Tool(), Name: obj.UnitName(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select inventory unit: %w", err)
	}

	// Defense in depth: the inventory never holds an empty unit ID, and the
	// table checks that a unit row holds one. This check stays so that an
	// empty ID never becomes the key of the rows of every such unit.
	if id == "" {
		return "", false, fmt.Errorf("%w: the inventory holds no ID for %s", gate.ErrInvalid, scope)
	}

	t.units[scope.Key()] = id

	return id, true, nil
}

// writeScopeOf is rowScopeOf for a write: it returns namespace.ErrNotFound for
// a namespace that does not exist and the error of gatestore.NoUnitError for a
// unit scope whose name no live unit holds.
func (t *gateTx) writeScopeOf(ctx context.Context, scope gate.Scope) (rowScope, error) {
	rs, ok, err := t.rowScopeOf(ctx, scope)
	if err != nil {
		return rowScope{nsID: "", key: "", unitID: ""}, err
	}

	if ok {
		return rs, nil
	}

	// Defense in depth: the runner refuses a write at a unit scope with no
	// live unit before the write runs. This check stays so that a write path
	// that skips the runner cannot store a row of no unit.
	if scope.Level() == gate.LevelUnit {
		return rowScope{nsID: "", key: "", unitID: ""}, gatestore.NoUnitError(scope)
	}

	return rowScope{nsID: "", key: "", unitID: ""}, namespace.ErrNotFound
}

// locatedKey returns the scope key of a row that a query across scopes
// found: the stored key, or for a unit row the scope key of the name of its
// unit, live or deleted. A unit row of no unit fails closed.
func (t *gateTx) locatedKey(ctx context.Context, nsID, key, unitID string) (string, error) {
	if unitID == "" {
		return key, nil
	}

	if name, ok := t.names[unitID]; ok {
		return name, nil
	}

	row, err := t.q.GetGateUnitName(ctx, db.GetGateUnitNameParams{NamespaceID: nsID, UnitID: unitID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: the gate row %s names no unit", gate.ErrInvalid, key)
	}

	if err != nil {
		return "", fmt.Errorf("select gate unit name: %w", err)
	}

	scope, err := gate.UnitScope(namespace.Name(row.Namespace), row.Tool, row.Name)
	if err != nil {
		return "", fmt.Errorf("scope of the unit of the gate row %s: %w", key, err)
	}

	t.names[unitID] = scope.Key()

	return scope.Key(), nil
}

// scopeNamespaceID is namespaceID for the namespace of a scope: the scope of
// the installation is in audit.ServerScope.
func (t *gateTx) scopeNamespaceID(ctx context.Context, scope gate.Scope) (string, bool, error) {
	if scope.Level() == gate.LevelInstallation {
		return t.namespaceID(ctx, audit.ServerScope)
	}

	return t.namespaceID(ctx, scope.Namespace())
}

// namespaceID returns the stable ID of the namespace of ns, the empty ID for
// audit.ServerScope (the installation), and false for a namespace that does
// not exist. It returns gate.ErrInvalid for the empty name.
func (t *gateTx) namespaceID(ctx context.Context, ns namespace.Name) (string, bool, error) {
	if ns == "" {
		return "", false, fmt.Errorf("%w: the namespace is empty", gate.ErrInvalid)
	}

	if ns == audit.ServerScope {
		return "", true, nil
	}

	if id, ok := t.ids[ns]; ok {
		return id, true, nil
	}

	id, err := t.q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select namespace ID: %w", err)
	}

	t.ids[ns] = id

	return id, true, nil
}

// checkGateJoin returns gate.ErrFinal when the unit of the move m holds a gate
// with the ID of a final gate of the collection that it joins.
func checkGateJoin(ctx context.Context, q *db.Queries, m collection.Move) error {
	unit, err := gate.UnitScope(m.Unit.Namespace, m.Unit.Tool, m.Unit.Name)
	if err != nil {
		return fmt.Errorf("unit scope: %w", err)
	}

	err = gatestore.CheckJoin(ctx, newGateTx(q), unit, gate.CollectionScope(m.To.Namespace, m.To.Name))
	if err != nil {
		return fmt.Errorf("check the final gates of the collection: %w", err)
	}

	return nil
}

// unitGate names the unit of a purge: its namespace, its tool, the name that
// the delete left it, and its stable ID.
type unitGate struct {
	ns   namespace.Name
	tool string
	name string
	id   string
}

// purgeUnitGate deletes the gates, access limits, rules and path sets of the
// unit u, when a purge removes the unit. The rows key on the stable ID of the
// unit, so a live unit with the same name keeps its own rows. It returns the
// audit changes that name the removed objects, with the scope key of the name
// that the delete left the unit.
func purgeUnitGate(ctx context.Context, q *db.Queries, u unitGate, room int) ([]audit.Change, error) {
	// Defense in depth: the purge names the unit by its stable ID, which is
	// never empty. This check stays so that an empty ID never selects the rows
	// of another scope.
	if u.id == "" {
		return nil, fmt.Errorf("%w: the purge names no unit ID", gate.ErrInvalid)
	}

	nsID, err := q.GetNamespaceID(ctx, u.ns.String())
	if err != nil {
		return nil, fmt.Errorf("select namespace ID: %w", err)
	}

	keys, err := q.ListUnitGateObjectKeys(ctx, db.ListUnitGateObjectKeysParams{NamespaceID: nsID, UnitID: u.id})
	if err != nil {
		return nil, fmt.Errorf("select unit gate objects: %w", err)
	}

	scopeKey := gatestore.UnitRowKey(u.id)
	if scope, serr := gate.UnitScope(u.ns, u.tool, u.name); serr == nil {
		scopeKey = scope.Key()
	}

	removed := make([]gatestore.Removed, len(keys))
	for i, k := range keys {
		removed[i] = gatestore.Removed{Kind: k.Kind, ID: k.ID, ScopeKey: scopeKey}
	}

	if err = q.DeleteUnitGateObjects(ctx, db.DeleteUnitGateObjectsParams{NamespaceID: nsID, UnitID: u.id}); err != nil {
		return nil, fmt.Errorf("delete unit gate objects: %w", err)
	}

	return gatestore.RemovedChanges(removed, room), nil
}

// purgeCollectionGate deletes the configuration that the collection ref holds,
// when a delete removes the collection. It returns the audit changes that name
// the removed objects.
func purgeCollectionGate(ctx context.Context, q *db.Queries, ref collection.Ref, room int) ([]audit.Change, error) {
	nsID, err := q.GetNamespaceID(ctx, ref.Namespace.String())
	if err != nil {
		return nil, fmt.Errorf("select namespace ID: %w", err)
	}

	scope := gate.CollectionScope(ref.Namespace, ref.Name)

	keys, err := q.ListScopeGateObjectKeys(ctx, db.ListScopeGateObjectKeysParams{
		NamespaceID: nsID, ScopeKey: scope.Key(),
	})
	if err != nil {
		return nil, fmt.Errorf("select collection gate objects: %w", err)
	}

	removed := make([]gatestore.Removed, len(keys))
	for i, k := range keys {
		removed[i] = gatestore.Removed{Kind: k.Kind, ID: k.ID, ScopeKey: k.ScopeKey}
	}

	params := db.DeleteScopeGateObjectsParams{NamespaceID: nsID, ScopeKey: scope.Key()}
	if err = q.DeleteScopeGateObjects(ctx, params); err != nil {
		return nil, fmt.Errorf("delete collection gate objects: %w", err)
	}

	return gatestore.RemovedChanges(removed, room), nil
}

// noRow is the row that a read returns with false.
func noRow() gatestore.Row {
	return gatestore.Row{Kind: "", ID: "", Version: 0, Body: ""}
}

// noRecordRow is the record row that a read returns with false.
func noRecordRow() gatestore.RecordRow {
	return gatestore.RecordRow{
		ID: "", Point: "", ChangeKind: "", ChangeID: "", AtMs: 0, LastAtMs: 0, Count: 0, RefusalKey: "", Body: "",
	}
}
