package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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

// gateRunner opens the transactions of the gate store. Every write of the
// configuration takes the advisory lock of the scope @server first, as the
// other audited changes do, so a check across scopes cannot race another
// write. The lock comes before the namespace row, so the lock order cannot
// deadlock.
type gateRunner struct {
	s *Store
}

var _ gatestore.Runner = gateRunner{s: nil}

// Write implements gatestore.Runner.
func (r gateRunner) Write(
	ctx context.Context, scope gate.Scope, fence namespace.Fence, e audit.Entry, fn func(gatestore.Tx) error,
) error {
	run := func(q *db.Queries) error { return fn(newGateTx(q)) }

	if scope.Level() == gate.LevelInstallation {
		return r.s.inAuditedTx(ctx, e, run)
	}

	return r.s.inLockedFencedAuditedTx(ctx, fence, e, run)
}

// WriteRecord implements gatestore.Runner.
func (r gateRunner) WriteRecord(
	ctx context.Context, ns namespace.Name, fence namespace.Fence, fn func(gatestore.Tx) error,
) error {
	run := func(q *db.Queries) error { return fn(newGateTx(q)) }

	if ns == audit.ServerScope {
		return r.s.inTx(ctx, run)
	}

	return r.s.inFencedTx(ctx, fence, run)
}

// Read implements gatestore.Runner.
func (r gateRunner) Read(ctx context.Context, fn func(gatestore.Tx) error) error {
	return r.s.inSnapshotTx(ctx, func(q *db.Queries) error { return fn(newGateTx(q)) })
}

// gateTx reads and writes the gate rows of one transaction.
type gateTx struct {
	q   *db.Queries
	ids map[namespace.Name]string
}

var _ gatestore.Tx = (*gateTx)(nil)

func newGateTx(q *db.Queries) *gateTx {
	return &gateTx{q: q, ids: map[namespace.Name]string{}}
}

func (t *gateTx) Get(ctx context.Context, scope gate.Scope, kind, id string) (gatestore.Row, bool, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return noRow(), false, err
	}

	row, err := t.q.GetGateObject(ctx, db.GetGateObjectParams{
		NamespaceID: nsID, ScopeKey: scope.Key(), Kind: kind, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return noRow(), false, nil
	}

	if err != nil {
		return gatestore.Row{}, false, fmt.Errorf("select gate object: %w", err)
	}

	return gatestore.Row{Kind: kind, ID: id, Version: row.Version, Body: row.Body}, true, nil
}

func (t *gateTx) List(ctx context.Context, scope gate.Scope, kind string) ([]gatestore.Row, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateObjects(
		ctx,
		db.ListGateObjectsParams{NamespaceID: nsID, ScopeKey: scope.Key(), Kind: kind},
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
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateObjectsOfScope(
		ctx,
		db.ListGateObjectsOfScopeParams{NamespaceID: nsID, ScopeKey: scope.Key()},
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
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return 0, err
	}

	n, err := t.q.CountGateObjects(ctx, db.CountGateObjectsParams{NamespaceID: nsID, ScopeKey: scope.Key(), Kind: kind})
	if err != nil {
		return 0, fmt.Errorf("count gate objects: %w", err)
	}

	return int(n), nil
}

func (t *gateTx) Insert(ctx context.Context, scope gate.Scope, row gatestore.Row, at time.Time) error {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil {
		return err
	}

	if !ok {
		return namespace.ErrNotFound
	}

	unit := gatestore.UnitPartsOf(scope)

	err = t.q.InsertGateObject(ctx, db.InsertGateObjectParams{
		NamespaceID: nsID, ScopeKey: scope.Key(), Kind: row.Kind, ID: row.ID, Level: string(scope.Level()),
		Tool: unit.Tool, UnitName: unit.Name, Version: row.Version, Body: row.Body, UpdatedAtMs: at.UnixMilli(),
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
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return false, err
	}

	n, err := t.q.UpdateGateObject(ctx, db.UpdateGateObjectParams{
		Version: row.Version, Body: row.Body, UpdatedAtMs: at.UnixMilli(), NamespaceID: nsID, ScopeKey: scope.Key(),
		Kind: row.Kind, ID: row.ID, PrevVersion: prev,
	})
	if err != nil {
		return false, fmt.Errorf("update gate object: %w", err)
	}

	return n == 1, nil
}

func (t *gateTx) Delete(ctx context.Context, scope gate.Scope, kind, id string, prev int64) (bool, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return false, err
	}

	n, err := t.q.DeleteGateObject(ctx, db.DeleteGateObjectParams{
		NamespaceID: nsID, ScopeKey: scope.Key(), Kind: kind, ID: id, PrevVersion: prev,
	})
	if err != nil {
		return false, fmt.Errorf("delete gate object: %w", err)
	}

	return n == 1, nil
}

func (t *gateTx) SetReferences(ctx context.Context, scope gate.Scope, kind, id string, refs []gatestore.Ref) error {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return err
	}

	err = t.q.DeleteGateReferences(ctx, db.DeleteGateReferencesParams{
		NamespaceID: nsID, ScopeKey: scope.Key(), OwnerKind: kind, OwnerID: id,
	})
	if err != nil {
		return fmt.Errorf("delete gate references: %w", err)
	}

	for _, ref := range refs {
		err = t.q.InsertGateReference(ctx, db.InsertGateReferenceParams{
			RefKind: ref.Kind, RefID: ref.ID, NamespaceID: nsID, ScopeKey: scope.Key(), OwnerKind: kind, OwnerID: id,
		})
		if err != nil {
			return fmt.Errorf("insert gate reference: %w", err)
		}
	}

	return nil
}

func (t *gateTx) Narrower(ctx context.Context, scope gate.Scope, kind, id string) ([]gatestore.Located, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListNarrowerGateObjects(ctx, db.ListNarrowerGateObjectsParams{
		Kind: kind, ID: id, Level: string(scope.Level()), NamespaceID: nsID, ScopeKey: scope.Key(),
		CollectionKey: scope.Collection(),
	})
	if err != nil {
		return nil, fmt.Errorf("select narrower gate objects: %w", err)
	}

	out := make([]gatestore.Located, len(rows))
	for i, r := range rows {
		out[i] = gatestore.Located{
			ScopeKey: r.ScopeKey, Row: gatestore.Row{Kind: kind, ID: id, Version: r.Version, Body: r.Body},
		}
	}

	return out, nil
}

func (t *gateTx) Referrers(ctx context.Context, scope gate.Scope, ref gatestore.Ref) ([]gatestore.Located, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return nil, err
	}

	rows, err := t.q.ListGateReferrers(ctx, db.ListGateReferrersParams{
		RefKind: ref.Kind, RefID: ref.ID, Level: string(scope.Level()), NamespaceID: nsID, ScopeKey: scope.Key(),
		CollectionKey: scope.Collection(),
	})
	if err != nil {
		return nil, fmt.Errorf("select gate referrers: %w", err)
	}

	out := make([]gatestore.Located, len(rows))
	for i, r := range rows {
		out[i] = gatestore.Located{
			ScopeKey: r.ScopeKey, Row: gatestore.Row{Kind: r.Kind, ID: r.ID, Version: r.Version, Body: r.Body},
		}
	}

	return out, nil
}

func (t *gateTx) UnitCollection(ctx context.Context, scope gate.Scope) (string, bool, error) {
	nsID, ok, err := t.scopeNamespaceID(ctx, scope)
	if err != nil || !ok {
		return "", false, err
	}

	unit := gatestore.UnitPartsOf(scope)

	key, err := t.q.GateUnitCollection(
		ctx,
		db.GateUnitCollectionParams{NamespaceID: nsID, Tool: unit.Tool, UnitName: unit.Name},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select unit collection: %w", err)
	}

	return key, true, nil
}

func (t *gateTx) RecordExists(ctx context.Context, id string) (bool, error) {
	_, err := t.q.GetGateRecordByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
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
	if errors.Is(err, pgx.ErrNoRows) {
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
		NamespaceID: nsID, Point: w.Point, FromMs: w.From.UnixMilli(), ToMs: w.To.UnixMilli(),
		MaxRows: int32(w.Limit), //nolint:gosec // The port checks 1 to gate.MaxSimulateRecords.
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

	if err = t.q.LockGateRefusal(ctx, nsID+":"+key); err != nil {
		return noRecordRow(), false, fmt.Errorf("lock gate refusal: %w", err)
	}

	row, err := t.q.FindGateRefusal(ctx, db.FindGateRefusalParams{
		NamespaceID: nsID, RefusalKey: key, SinceMs: since.UnixMilli(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
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
	if errors.Is(err, pgx.ErrNoRows) {
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

// renameUnitGate moves the configuration of the unit oldRef to the scope of the
// name newName, in the transaction of the rename. It returns
// stack.ErrAlreadyExists when the scope of the new name holds configuration:
// it is the configuration of a deleted unit that kept the name, and the
// rename would give it to this unit.
func renameUnitGate(ctx context.Context, q *db.Queries, oldRef stack.Ref, newName stack.Name) error {
	ns := oldRef.Namespace
	oldName := stackUnitName(oldRef)
	newRef := stack.Ref{Namespace: ns, Project: oldRef.Project, Stack: newName}
	next := stackUnitName(newRef)

	oldScope, err := gate.UnitScope(ns, inventoryTool, oldName)
	if err != nil {
		return fmt.Errorf("scope of the stack: %w", err)
	}

	newScope, err := gate.UnitScope(ns, inventoryTool, next)
	if err != nil {
		return fmt.Errorf("scope of the renamed stack: %w", err)
	}

	nsID, err := q.GetNamespaceID(ctx, ns.String())
	if err != nil {
		return fmt.Errorf("select namespace ID: %w", err)
	}

	taken, err := q.CountUnitGateObjects(ctx, db.CountUnitGateObjectsParams{
		NamespaceID: nsID, Tool: inventoryTool, UnitName: next,
	})
	if err != nil {
		return fmt.Errorf("count unit gate objects: %w", err)
	}

	if taken > 0 {
		return fmt.Errorf("%w: the name %s holds the configuration of a deleted unit", stack.ErrAlreadyExists, next)
	}

	held, err := q.CountUnitGateObjects(ctx, db.CountUnitGateObjectsParams{
		NamespaceID: nsID, Tool: inventoryTool, UnitName: oldName,
	})
	if err != nil {
		return fmt.Errorf("count unit gate objects: %w", err)
	}

	if held == 0 {
		return nil
	}

	err = q.CopyUnitGateObjects(ctx, db.CopyUnitGateObjectsParams{
		NewScopeKey: newScope.Key(), NewUnitName: next, NamespaceID: nsID, Tool: inventoryTool, OldUnitName: oldName,
	})
	if err != nil {
		return fmt.Errorf("copy unit gate objects: %w", err)
	}

	err = q.CopyUnitGateReferences(ctx, db.CopyUnitGateReferencesParams{
		NewScopeKey: newScope.Key(), NamespaceID: nsID, OldScopeKey: oldScope.Key(),
	})
	if err != nil {
		return fmt.Errorf("copy unit gate references: %w", err)
	}

	params := db.DeleteUnitGateObjectsParams{NamespaceID: nsID, Tool: inventoryTool, UnitName: oldName}
	if err = q.DeleteUnitGateObjects(ctx, params); err != nil {
		return fmt.Errorf("delete unit gate objects: %w", err)
	}

	return nil
}

// purgeUnitGate deletes the gates, access limits, rules and path sets that the
// unit called name holds, when a purge removes the unit. A unit that a team
// creates again under the same name starts with none of them. When the
// inventory holds a live unit of the same tool and name, a team created it
// after the delete freed the name: the rows are the rows of that unit and
// stay. It returns the audit changes that name the removed objects.
func purgeUnitGate(
	ctx context.Context, q *db.Queries, ns namespace.Name, tool, name string,
) ([]audit.Change, error) {
	if name == "" {
		return nil, nil
	}

	_, err := q.GetInventoryUnit(ctx, db.GetInventoryUnitParams{Namespace: ns.String(), Tool: tool, Name: name})
	if err == nil {
		return nil, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("select inventory unit: %w", err)
	}

	nsID, err := q.GetNamespaceID(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select namespace ID: %w", err)
	}

	keys, err := q.ListUnitGateObjectKeys(ctx, db.ListUnitGateObjectKeysParams{
		NamespaceID: nsID, Tool: tool, UnitName: name,
	})
	if err != nil {
		return nil, fmt.Errorf("select unit gate objects: %w", err)
	}

	removed := make([]gatestore.Removed, len(keys))
	for i, k := range keys {
		removed[i] = gatestore.Removed{Kind: k.Kind, ID: k.ID, ScopeKey: k.ScopeKey}
	}

	params := db.DeleteUnitGateObjectsParams{NamespaceID: nsID, Tool: tool, UnitName: name}
	if err = q.DeleteUnitGateObjects(ctx, params); err != nil {
		return nil, fmt.Errorf("delete unit gate objects: %w", err)
	}

	return gatestore.RemovedChanges(removed), nil
}

// purgeCollectionGate deletes the configuration that the collection ref holds,
// when a delete removes the collection. It returns the audit changes that name
// the removed objects.
func purgeCollectionGate(ctx context.Context, q *db.Queries, ref collection.Ref) ([]audit.Change, error) {
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

	return gatestore.RemovedChanges(removed), nil
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
