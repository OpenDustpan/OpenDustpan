package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// Triggers returns the store of run triggers. Each write checks the
// installation state and the fence of its namespace, and appends its audit
// entry in its own transaction. The write lock of that transaction orders the
// writes of a namespace, so two saves at the same time cannot both pass the
// check for a loop.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) Triggers() trigger.Store {
	return triggers{s: s}
}

// triggers is trigger.Store over the database of a Store.
type triggers struct {
	s *Store
}

var _ trigger.Store = triggers{s: nil}

// Put implements trigger.Store.
func (t triggers) Put(ctx context.Context, tr trigger.Trigger, fence namespace.Fence, e audit.Entry) error {
	if err := triggerstore.CheckPut(tr, fence, e); err != nil {
		return err
	}

	if e.ObjectID == "" {
		e.ObjectID = tr.Unit.ID
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return triggerstore.ApplyPut(ctx, triggerTx{q: q}, tr, e.At, func(r triggerstore.Row) {
			// The warning names the row by its ID and its namespace: the text of the
			// check can hold a cron expression or a subject, so it stays out of the log.
			t.s.logger.WarnContext(ctx, "stored trigger left out of the loop check: it fails its checks",
				slog.String("id", r.ID), slog.String("namespace", r.Namespace))
		})
	})
}

// Get implements trigger.Store. A row that fails its checks is not served.
func (t triggers) Get(ctx context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := triggerstore.CheckGet(ns, id); err != nil {
		return trigger.Trigger{}, err
	}

	row, err := t.read(ctx, ns, id)
	if err != nil {
		return trigger.Trigger{}, err
	}

	return triggerstore.Trigger(row)
}

// Stored implements trigger.Store. It serves a row that fails its checks.
func (t triggers) Stored(ctx context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	if err := triggerstore.CheckGet(ns, id); err != nil {
		return trigger.Trigger{}, err
	}

	row, err := t.read(ctx, ns, id)
	if err != nil {
		return trigger.Trigger{}, err
	}

	return triggerstore.Stored(row), nil
}

// List implements trigger.Store. A row that fails its checks fails the read.
func (t triggers) List(ctx context.Context, unit graph.Unit) ([]trigger.Trigger, error) {
	if err := triggerstore.CheckUnit(unit); err != nil {
		return nil, err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	rows, err := t.s.queries.ListTriggersOfUnit(ctx, db.ListTriggersOfUnitParams{
		Namespace: unit.Namespace.String(), UnitTool: unit.Tool, UnitID: unit.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("select triggers of a unit: %w", err)
	}

	stored := make([]triggerstore.Row, 0, len(rows))
	for i := range rows {
		stored = append(stored, triggerstore.Row(rows[i]))
	}

	return triggerstore.OfUnit(stored, unit)
}

// Followers implements trigger.Store.
func (t triggers) Followers(ctx context.Context, source graph.Unit) ([]trigger.Trigger, error) {
	if err := triggerstore.CheckUnit(source); err != nil {
		return nil, err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	rows, err := t.s.queries.ListTriggerFollowers(ctx, db.ListTriggerFollowersParams{
		Namespace: source.Namespace.String(), SourceNamespace: source.Namespace.String(), SourceTool: source.Tool,
		SourceUnitID: source.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("select followers: %w", err)
	}

	stored := make([]triggerstore.Row, 0, len(rows))
	for i := range rows {
		stored = append(stored, triggerstore.Row(rows[i]))
	}

	return triggerstore.FollowersOf(stored, source)
}

// Delete implements trigger.Store.
func (t triggers) Delete(
	ctx context.Context, ns namespace.Name, id trigger.ID, fence namespace.Fence, e audit.Entry,
) error {
	if err := triggerstore.CheckDelete(ns, id, fence, e); err != nil {
		return err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		unitID, err := triggerstore.ApplyDelete(ctx, triggerTx{q: q}, ns, id)
		if err != nil {
			return err
		}

		if e.ObjectID == "" {
			e.ObjectID = unitID
		}

		return nil
	})
}

// NextSlot implements trigger.Store.
func (t triggers) NextSlot(ctx context.Context, ns namespace.Name, id trigger.ID) (time.Time, error) {
	if err := triggerstore.CheckGet(ns, id); err != nil {
		return time.Time{}, err
	}

	row, err := t.read(ctx, ns, id)
	if err != nil {
		return time.Time{}, err
	}

	return triggerstore.NextSlot(row), nil
}

// ClaimDue implements trigger.Store. The claim and the move of the next slot
// are one statement with a compare on the old slot, in the audited and fenced
// transaction of the namespace.
func (t triggers) ClaimDue(ctx context.Context, c trigger.Claim, fence namespace.Fence, e audit.Entry) error {
	if err := triggerstore.CheckClaim(c, fence, e); err != nil {
		return err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		unitID, err := triggerstore.ApplyClaim(ctx, triggerTx{q: q}, c)
		if err != nil {
			return err
		}

		if e.ObjectID == "" {
			e.ObjectID = unitID
		}

		return nil
	})
}

// read returns the row of the trigger id of ns, or trigger.ErrNotFound. It
// checks nothing about the row: the caller applies the rules that it needs.
func (t triggers) read(ctx context.Context, ns namespace.Name, id trigger.ID) (triggerstore.Row, error) {
	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	row, err := t.s.queries.GetTrigger(ctx, db.GetTriggerParams{Namespace: ns.String(), ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return triggerstore.Row{}, trigger.ErrNotFound
	}

	if err != nil {
		return triggerstore.Row{}, fmt.Errorf("select trigger: %w", err)
	}

	return triggerstore.Row(row), nil
}

// triggerTx is triggerstore.Tx over the queries of one transaction.
type triggerTx struct {
	q *db.Queries
}

var _ triggerstore.Tx = triggerTx{q: nil}

// Find implements triggerstore.Tx.
func (x triggerTx) Find(ctx context.Context, ns namespace.Name, id trigger.ID) (triggerstore.Row, bool, error) {
	var none triggerstore.Row

	row, err := x.q.GetTrigger(ctx, db.GetTriggerParams{Namespace: ns.String(), ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select trigger: %w", err)
	}

	return triggerstore.Row(row), true, nil
}

// EnabledFollows implements triggerstore.Tx.
func (x triggerTx) EnabledFollows(ctx context.Context, ns namespace.Name) ([]triggerstore.Row, error) {
	rows, err := x.q.ListEnabledFollowTriggers(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select follows triggers: %w", err)
	}

	out := make([]triggerstore.Row, 0, len(rows))
	for i := range rows {
		out = append(out, triggerstore.Row(rows[i]))
	}

	return out, nil
}

// Upsert implements triggerstore.Tx.
func (x triggerTx) Upsert(ctx context.Context, r triggerstore.Row) error {
	n, err := x.q.UpsertTrigger(ctx, db.UpsertTriggerParams{
		ID:               r.ID,
		UnitTool:         r.UnitTool,
		UnitID:           r.UnitID,
		Kind:             r.Kind,
		Preset:           r.Preset,
		Cron:             r.Cron,
		SourceNamespace:  r.SourceNamespace,
		SourceTool:       r.SourceTool,
		SourceUnitID:     r.SourceUnitID,
		Enabled:          r.Enabled,
		LastSaverSubject: r.LastSaverSubject,
		LastSaverID:      r.LastSaverID,
		CreatedAtMs:      r.CreatedAtMs,
		UpdatedAtMs:      r.UpdatedAtMs,
		NextSlotMs:       r.NextSlotMs,
		Namespace:        r.Namespace,
	})
	if err != nil {
		return fmt.Errorf("write trigger: %w", err)
	}

	if n == 0 {
		return namespace.ErrNotFound
	}

	return nil
}

// Now implements triggerstore.Tx. It reads the clock of the store.
func (x triggerTx) Now(ctx context.Context) (time.Time, error) {
	ms, err := storeNow(ctx, x.q)
	if err != nil {
		return time.Time{}, err
	}

	return time.UnixMilli(ms).UTC(), nil
}

// Remove implements triggerstore.Tx.
func (x triggerTx) Remove(ctx context.Context, ns namespace.Name, id trigger.ID) (bool, error) {
	n, err := x.q.DeleteTrigger(ctx, db.DeleteTriggerParams{Namespace: ns.String(), ID: string(id)})
	if err != nil {
		return false, fmt.Errorf("delete trigger: %w", err)
	}

	return n > 0, nil
}

// MoveNextSlot implements triggerstore.Tx.
func (x triggerTx) MoveNextSlot(
	ctx context.Context, ns namespace.Name, id trigger.ID, old, next int64,
) (bool, error) {
	n, err := x.q.MoveTriggerNextSlot(ctx, db.MoveTriggerNextSlotParams{
		NextSlotMs: next, ID: string(id), OldNextSlotMs: old, Namespace: ns.String(),
	})
	if err != nil {
		return false, fmt.Errorf("move next slot of trigger: %w", err)
	}

	return n > 0, nil
}
