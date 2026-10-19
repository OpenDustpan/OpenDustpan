package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/unitimport"
)

var (
	_ unitimport.Store     = (*Imports)(nil)
	_ unitimport.UnitStore = (*Imports)(nil)
)

// Imports is the store of imports on the database of a Store. It implements
// unitimport.Store and unitimport.UnitStore. A separate type keeps the method
// names of the two ports apart from the other ports of the Store.
type Imports struct {
	store *Store
}

// Imports returns the store of imports on s.
func (s *Store) Imports() *Imports {
	return &Imports{store: s}
}

// Create implements unitimport.Store.
func (i *Imports) Create(
	ctx context.Context, fence namespace.Fence, imp unitimport.Import, e audit.Entry,
) (unitimport.Import, error) {
	var stored unitimport.Import

	err := i.store.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		var err error

		stored, err = insertImport(ctx, q, fence, imp)

		return err
	})
	if err != nil {
		return unitimport.Import{}, err
	}

	return stored, nil
}

// Get implements unitimport.Store.
func (i *Imports) Get(ctx context.Context, ns namespace.Name, id string) (unitimport.Import, error) {
	_, row, err := i.readImport(ctx, ns, id)
	if err != nil {
		return unitimport.Import{}, err
	}

	return codec.ImportFromRow(ns, codec.ImportRow(row))
}

// List implements unitimport.Store.
func (i *Imports) List(ctx context.Context, ns namespace.Name, f unitimport.Filter) ([]unitimport.Import, error) {
	nsID, err := importNamespaceID(ctx, i.store.queries, ns)
	if errors.Is(err, unitimport.ErrNotFound) {
		return []unitimport.Import{}, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := i.store.queries.ListImports(ctx, nsID)
	if err != nil {
		return nil, fmt.Errorf("select imports: %w", err)
	}

	out := make([]unitimport.Import, 0, len(rows))

	for n := range rows {
		if len(f.States) > 0 && !slices.Contains(f.States, unitimport.State(rows[n].State)) {
			continue
		}

		imp, decodeErr := codec.ImportFromRow(ns, codec.ImportRow(rows[n]))
		if decodeErr != nil {
			return nil, decodeErr
		}

		out = append(out, imp)
	}

	return out, nil
}

// Transition implements unitimport.Store.
func (i *Imports) Transition(
	ctx context.Context, fence namespace.Fence, id string, m unitimport.Move, e audit.Entry,
) error {
	return i.writeImport(ctx, fence, id, e, func(q *db.Queries, nsID string, row db.Import) error {
		if unitimport.State(row.State) != m.From {
			return fmt.Errorf("%w: import is %q, not %q", unitimport.ErrTransition, row.State, m.From)
		}

		if err := unitimport.NextState(m.From, m.To); err != nil {
			return fmt.Errorf("next state: %w", err)
		}

		if err := checkReplan(ctx, q, nsID, id, m); err != nil {
			return err
		}

		var endedMs int64
		if m.To.Ended() {
			endedMs = codec.Millis(e.At)
		}

		n, err := q.SetImportState(ctx, db.SetImportStateParams{
			State: string(m.To), Reason: m.Reason, EndedAtMs: endedMs, NamespaceID: nsID, ID: id,
			FromState: string(m.From),
		})
		if err != nil {
			return fmt.Errorf("update import state: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: import is not in state %q", unitimport.ErrTransition, m.From)
		}

		return nil
	})
}

// checkReplan refuses the move of a paused import to planning when the import
// has committed versions.
func checkReplan(ctx context.Context, q *db.Queries, nsID, id string, m unitimport.Move) error {
	if m.From != unitimport.StatePaused || m.To != unitimport.StatePlanning {
		return nil
	}

	held, err := q.CountImportLedger(ctx, db.CountImportLedgerParams{NamespaceID: nsID, ImportID: id})
	if err != nil {
		return fmt.Errorf("count import ledger: %w", err)
	}

	if held > 0 {
		return fmt.Errorf(
			"%w: the import has committed versions and cannot plan again", unitimport.ErrTransition,
		)
	}

	return nil
}

// SetPlan implements unitimport.Store.
func (i *Imports) SetPlan(
	ctx context.Context, fence namespace.Fence, id string, plan unitimport.Plan, e audit.Entry,
) error {
	return i.writeImport(ctx, fence, id, e, func(q *db.Queries, nsID string, row db.Import) error {
		if st := unitimport.State(row.State); st != unitimport.StatePlanning && st != unitimport.StatePlanned {
			stored, err := codec.DecodePlan(row.Plan)
			if err != nil {
				return fmt.Errorf("decode stored plan: %w", err)
			}

			stored.Reservations = plan.Reservations
			plan = stored
		}

		text, err := codec.EncodePlan(plan)
		if err != nil {
			return fmt.Errorf("encode plan: %w", err)
		}

		if err = q.SetImportPlan(ctx, db.SetImportPlanParams{Plan: text, NamespaceID: nsID, ID: id}); err != nil {
			return fmt.Errorf("update import plan: %w", err)
		}

		return nil
	})
}

// Purge implements unitimport.Store.
func (i *Imports) Purge(ctx context.Context, fence namespace.Fence, id string, e audit.Entry) error {
	return i.writeImport(ctx, fence, id, e, func(q *db.Queries, nsID string, row db.Import) error {
		if !unitimport.State(row.State).Ended() {
			return fmt.Errorf("%w: import is %q, and only a released or canceled import is purged",
				unitimport.ErrTransition, row.State)
		}

		if err := q.DeleteImportUnits(ctx, db.DeleteImportUnitsParams{NamespaceID: nsID, ImportID: id}); err != nil {
			return fmt.Errorf("delete import units: %w", err)
		}

		if err := q.DeleteImport(ctx, db.DeleteImportParams{NamespaceID: nsID, ID: id}); err != nil {
			return fmt.Errorf("delete import: %w", err)
		}

		err := q.InsertPurgedImport(ctx, db.InsertPurgedImportParams{
			NamespaceID: nsID, ID: id, PurgedAtMs: codec.Millis(e.At),
		})
		if err != nil {
			return fmt.Errorf("record purged import: %w", err)
		}

		return nil
	})
}

// PutUnits implements unitimport.UnitStore.
func (i *Imports) PutUnits(
	ctx context.Context, fence namespace.Fence, id string, units []unitimport.ImportUnit, e audit.Entry,
) error {
	return i.writeImport(ctx, fence, id, e, func(q *db.Queries, nsID string, row db.Import) error {
		if st := unitimport.State(row.State); st != unitimport.StatePlanning && st != unitimport.StatePlanned {
			return fmt.Errorf("%w: import is %q, and only a planning or planned import takes units",
				unitimport.ErrTransition, st)
		}

		if err := unitimport.ValidateUnits(id, units); err != nil {
			return fmt.Errorf("units: %w", err)
		}

		if err := q.DeleteImportUnits(ctx, db.DeleteImportUnitsParams{NamespaceID: nsID, ImportID: id}); err != nil {
			return fmt.Errorf("delete import units: %w", err)
		}

		for n := range units {
			ur, err := codec.ImportUnitToRow(nsID, id, n, units[n])
			if err != nil {
				return fmt.Errorf("encode import unit: %w", err)
			}

			if err = q.InsertImportUnit(ctx, db.InsertImportUnitParams(ur)); err != nil {
				return fmt.Errorf("insert import unit: %w", err)
			}
		}

		return nil
	})
}

// Units implements unitimport.UnitStore.
func (i *Imports) Units(ctx context.Context, ns namespace.Name, id string) ([]unitimport.ImportUnit, error) {
	nsID, _, err := i.readImport(ctx, ns, id)
	if err != nil {
		return nil, err
	}

	rows, err := i.store.queries.ListImportUnits(ctx, db.ListImportUnitsParams{NamespaceID: nsID, ImportID: id})
	if err != nil {
		return nil, fmt.Errorf("select import units: %w", err)
	}

	out := make([]unitimport.ImportUnit, 0, len(rows))

	for n := range rows {
		u, decodeErr := codec.ImportUnitFromRow(ns, codec.ImportUnitRow(rows[n]))
		if decodeErr != nil {
			return nil, decodeErr
		}

		out = append(out, u)
	}

	return out, nil
}

// Unit implements unitimport.UnitStore.
func (i *Imports) Unit(ctx context.Context, ns namespace.Name, id, unitID string) (unitimport.ImportUnit, error) {
	nsID, _, err := i.readImport(ctx, ns, id)
	if err != nil {
		return unitimport.ImportUnit{}, err
	}

	row, err := findImportUnit(ctx, i.store.queries, nsID, unitimport.UnitKey{ImportID: id, UnitID: unitID})
	if err != nil {
		return unitimport.ImportUnit{}, err
	}

	return codec.ImportUnitFromRow(ns, codec.ImportUnitRow(row))
}

// TransitionUnit implements unitimport.UnitStore.
func (i *Imports) TransitionUnit(
	ctx context.Context, fence namespace.Fence, key unitimport.UnitKey, m unitimport.UnitMove, e audit.Entry,
) error {
	return i.writeImport(ctx, fence, key.ImportID, e, func(q *db.Queries, nsID string, imp db.Import) error {
		if unitimport.State(imp.State).Ended() && m.To != unitimport.UnitCanceled {
			return fmt.Errorf("%w: import is %q, and only a move to canceled changes a unit",
				unitimport.ErrTransition, imp.State)
		}

		row, err := findImportUnit(ctx, q, nsID, key)
		if err != nil {
			return err
		}

		if err = unitimport.CheckUnitMove(unitimport.UnitState(row.State), row.ErrorCode, m); err != nil {
			return fmt.Errorf("unit move: %w", err)
		}

		n, err := q.SetImportUnitState(ctx, db.SetImportUnitStateParams{
			State: string(m.To), ErrorCode: m.Code, NamespaceID: nsID, ImportID: key.ImportID, ID: key.UnitID,
			FromState: string(m.From),
		})
		if err != nil {
			return fmt.Errorf("update import unit state: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: unit is not in state %q", unitimport.ErrTransition, m.From)
		}

		return nil
	})
}

// SetCounts implements unitimport.UnitStore.
func (i *Imports) SetCounts(
	ctx context.Context, fence namespace.Fence, key unitimport.UnitKey, c unitimport.Counts, e audit.Entry,
) error {
	return i.writeImport(ctx, fence, key.ImportID, e, func(q *db.Queries, nsID string, imp db.Import) error {
		if unitimport.State(imp.State).Ended() {
			return fmt.Errorf(
				"%w: import is %q, and an ended import takes no counts",
				unitimport.ErrTransition,
				imp.State,
			)
		}

		if err := unitimport.ValidateCounts(c); err != nil {
			return fmt.Errorf("counts: %w", err)
		}

		n, err := q.SetImportUnitCounts(ctx, db.SetImportUnitCountsParams{
			Versions: int64(c.Versions), Committed: int64(c.Committed), NamespaceID: nsID,
			ImportID: key.ImportID, ID: key.UnitID,
		})
		if err != nil {
			return fmt.Errorf("update import unit counts: %w", err)
		}

		if n == 0 {
			return unitimport.ErrNotFound
		}

		return nil
	})
}

// Ledger implements unitimport.UnitStore. It answers the rows of a purged
// import too, and wraps unitimport.ErrNotFound for a namespace that does not
// exist.
func (i *Imports) Ledger(ctx context.Context, ns namespace.Name, id, unitID string) ([]unitimport.LedgerRow, error) {
	nsID, _, err := i.readImport(ctx, ns, id)
	if err != nil && !errors.Is(err, unitimport.ErrPurged) {
		return nil, err
	}

	rows, err := i.store.queries.ListImportLedger(ctx, db.ListImportLedgerParams{
		NamespaceID: nsID, ImportID: id, ImportUnitID: unitID,
	})
	if err != nil {
		return nil, fmt.Errorf("select import ledger: %w", err)
	}

	out := make([]unitimport.LedgerRow, 0, len(rows))
	for n := range rows {
		out = append(out, codec.LedgerFromRow(ns, codec.ImportLedgerRow(rows[n])))
	}

	return out, nil
}

// readImport resolves the namespace and the import in one read.
func (i *Imports) readImport(ctx context.Context, ns namespace.Name, id string) (string, db.Import, error) {
	nsID, err := importNamespaceID(ctx, i.store.queries, ns)
	if err != nil {
		return "", db.Import{}, err
	}

	row, err := findImport(ctx, i.store.queries, nsID, id)

	return nsID, row, err
}

// writeImport runs fn in a fenced, audited transaction with the import id
// found. It hands fn the stable ID of the namespace and the row.
func (i *Imports) writeImport(
	ctx context.Context, fence namespace.Fence, id string, e audit.Entry,
	fn func(q *db.Queries, nsID string, row db.Import) error,
) error {
	return i.store.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		nsID, err := importNamespaceID(ctx, q, fence.Namespace)
		if err != nil {
			return err
		}

		row, err := findImport(ctx, q, nsID, id)
		if err != nil {
			return err
		}

		return fn(q, nsID, row)
	})
}

// insertImport validates imp and stores it in the state planning, with no
// plan, in the transaction of q.
func insertImport(
	ctx context.Context, q *db.Queries, fence namespace.Fence, imp unitimport.Import,
) (unitimport.Import, error) {
	var noPlan unitimport.Plan

	imp.State, imp.Reason, imp.Plan, imp.EndedAt = unitimport.StatePlanning, "", noPlan, time.Time{}

	if err := imp.ValidateNew(); err != nil {
		return unitimport.Import{}, fmt.Errorf("import: %w", err)
	}

	if imp.Namespace != fence.Namespace {
		return unitimport.Import{}, fmt.Errorf("%w: the import names %q, the fence names %q",
			unitimport.ErrInvalid, imp.Namespace, fence.Namespace)
	}

	nsID, err := importNamespaceID(ctx, q, fence.Namespace)
	if err != nil {
		return unitimport.Import{}, err
	}

	_, err = findImport(ctx, q, nsID, imp.ID)

	switch {
	case err == nil || errors.Is(err, unitimport.ErrPurged):
		return unitimport.Import{}, fmt.Errorf("%w: import %q exists", unitimport.ErrInvalid, imp.ID)
	case !errors.Is(err, unitimport.ErrNotFound):
		return unitimport.Import{}, err
	}

	row, err := codec.ImportToRow(nsID, imp)
	if err != nil {
		return unitimport.Import{}, fmt.Errorf("encode import: %w", err)
	}

	if err = q.InsertImport(ctx, db.InsertImportParams(row)); err != nil {
		if isUniqueViolation(err) {
			return unitimport.Import{}, fmt.Errorf("%w: import %q exists", unitimport.ErrInvalid, imp.ID)
		}

		return unitimport.Import{}, fmt.Errorf("insert import: %w", err)
	}

	return codec.ImportFromRow(fence.Namespace, row)
}

// importNamespaceID returns the stable ID of the namespace ns. It returns
// unitimport.ErrNotFound for a namespace that does not exist.
func importNamespaceID(ctx context.Context, q *db.Queries, ns namespace.Name) (string, error) {
	id, err := q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return "", unitimport.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select namespace ID: %w", err)
	}

	return id, nil
}

// findImport returns the row of an import. It returns unitimport.ErrPurged
// for a purged import and unitimport.ErrNotFound for an unknown one.
func findImport(ctx context.Context, q *db.Queries, nsID, id string) (db.Import, error) {
	row, err := q.GetImport(ctx, db.GetImportParams{NamespaceID: nsID, ID: id})
	if err == nil {
		return row, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Import{}, fmt.Errorf("select import: %w", err)
	}

	purged, err := q.CountPurgedImport(ctx, db.CountPurgedImportParams{NamespaceID: nsID, ID: id})
	if err != nil {
		return db.Import{}, fmt.Errorf("select purged import: %w", err)
	}

	if purged > 0 {
		return db.Import{}, unitimport.ErrPurged
	}

	return db.Import{}, unitimport.ErrNotFound
}

// findImportUnit returns the row of an import unit. It returns
// unitimport.ErrNotFound for an unknown one.
func findImportUnit(ctx context.Context, q *db.Queries, nsID string, key unitimport.UnitKey) (db.ImportUnit, error) {
	row, err := q.GetImportUnit(ctx, db.GetImportUnitParams{NamespaceID: nsID, ImportID: key.ImportID, ID: key.UnitID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ImportUnit{}, unitimport.ErrNotFound
	}

	if err != nil {
		return db.ImportUnit{}, fmt.Errorf("select import unit: %w", err)
	}

	return row, nil
}

// putLedgerRow stores a ledger row in the transaction of q. The Tool
// adapters call it in the transaction of the history item that the row
// names. It wraps unitimport.ErrInvalid for a bad row.
func putLedgerRow(ctx context.Context, q *db.Queries, ns namespace.Name, r unitimport.LedgerRow) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("ledger row: %w", err)
	}

	nsID, err := importNamespaceID(ctx, q, ns)
	if err != nil {
		return err
	}

	if err = q.InsertImportLedger(ctx, db.InsertImportLedgerParams(codec.LedgerToRow(nsID, r))); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: version %q of unit %q", unitimport.ErrLedgerExists, r.Version, r.ImportUnitID)
		}

		return fmt.Errorf("insert import ledger row: %w", err)
	}

	return nil
}

// setImportUnitTarget records the target unit of an import unit in the
// transaction of q. The Tool adapters call it in the transaction that creates
// the target. It wraps unitimport.ErrNotFound for an unknown import unit and
// unitimport.ErrTransition for an import that ended.
func setImportUnitTarget(
	ctx context.Context, q *db.Queries, key unitimport.UnitKey, target history.Unit,
) error {
	if err := target.Validate(); err != nil {
		return fmt.Errorf("%w: target unit: %w", unitimport.ErrInvalid, err)
	}

	nsID, err := importNamespaceID(ctx, q, target.Namespace)
	if err != nil {
		return err
	}

	imp, err := findImport(ctx, q, nsID, key.ImportID)
	if err != nil {
		return err
	}

	if unitimport.State(imp.State).Ended() {
		return fmt.Errorf("%w: import is %q, and an ended import takes no target", unitimport.ErrTransition, imp.State)
	}

	n, err := q.SetImportUnitTarget(ctx, db.SetImportUnitTargetParams{
		TargetTool: target.Tool, TargetUnitID: target.ID, NamespaceID: nsID, ImportID: key.ImportID, ID: key.UnitID,
	})
	if err != nil {
		return fmt.Errorf("update import unit target: %w", err)
	}

	if n == 0 {
		return unitimport.ErrNotFound
	}

	return nil
}
