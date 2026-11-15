// Package triggerstore holds the rules of the trigger table that the SQLite and
// PostgreSQL adapters share: the mapping between a row and a trigger, the check
// of a stored row, and the steps of a write. A row is untrusted when it is
// read, because a backup, a restore or another version of the server can have
// written it. Both adapters call the same functions, so they apply one rule.
package triggerstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// ErrCorrupt reports a stored row that does not pass the checks of a trigger.
// The adapters do not serve such a row. The error also wraps trigger.ErrInvalid.
var ErrCorrupt = errors.New("stored trigger is not valid")

// Row is one row of the trigger table. Namespace is the name of the namespace
// of the row. The columns of the source are empty for a trigger with no
// source, and Preset and Cron are empty for a trigger with no frequency.
// Enabled is 1 or 0.
type Row struct {
	Namespace        string
	ID               string
	UnitTool         string
	UnitID           string
	Kind             string
	Preset           string
	Cron             string
	SourceNamespace  string
	SourceTool       string
	SourceUnitID     string
	Enabled          int64
	LastSaverSubject string
	LastSaverID      string
	CreatedAtMs      int64
	UpdatedAtMs      int64
	// NextSlotMs is the next slot of a schedule trigger in Unix milliseconds,
	// and 0 when the row records none.
	NextSlotMs int64
}

// RowOf returns the row of t, stamped with atMs.
func RowOf(t trigger.Trigger, atMs int64) Row {
	r := Row{
		Namespace:        t.Unit.Namespace.String(),
		ID:               string(t.ID),
		UnitTool:         t.Unit.Tool,
		UnitID:           t.Unit.ID,
		Kind:             string(t.Kind),
		Preset:           string(t.Frequency.Preset),
		Cron:             t.Frequency.Cron,
		SourceNamespace:  "",
		SourceTool:       "",
		SourceUnitID:     "",
		LastSaverSubject: t.LastSaver,
		LastSaverID:      t.LastSaverID,
		Enabled:          0,
		CreatedAtMs:      atMs,
		UpdatedAtMs:      atMs,
		NextSlotMs:       0,
	}

	if t.Source != (graph.Unit{Namespace: "", Tool: "", ID: ""}) {
		r.SourceNamespace, r.SourceTool, r.SourceUnitID = t.Source.Namespace.String(), t.Source.Tool, t.Source.ID
	}

	if t.Enabled {
		r.Enabled = 1
	}

	return r
}

// rebuild returns the trigger that r holds, with no check of the rules of a
// trigger. It refuses an enabled flag that is not 0 or 1 and a next slot below 0.
func rebuild(r Row) (trigger.Trigger, error) {
	if r.NextSlotMs < 0 {
		return trigger.Trigger{}, fmt.Errorf("%w: %w: the next slot is %d, below 0",
			ErrCorrupt, trigger.ErrInvalid, r.NextSlotMs)
	}

	// An enabled schedule trigger always has a next slot: Put sets it. A row
	// without one is not a row that this server wrote, and a claim of it fails.
	if r.Kind == string(trigger.KindSchedule) && r.Enabled == 1 && r.NextSlotMs == 0 {
		return trigger.Trigger{}, fmt.Errorf("%w: %w: an enabled schedule trigger has a next slot",
			ErrCorrupt, trigger.ErrInvalid)
	}

	if r.Enabled != 0 && r.Enabled != 1 {
		return trigger.Trigger{}, fmt.Errorf("%w: %w: the enabled flag is %d, not 0 or 1",
			ErrCorrupt, trigger.ErrInvalid, r.Enabled)
	}

	return Stored(r), nil
}

// Stored returns the trigger that r holds, with no check at all. An enabled
// flag other than 1 reads as off. Only a delete uses it, to find the unit of a
// row that fails its checks.
func Stored(r Row) trigger.Trigger {
	return trigger.Trigger{
		ID:        trigger.ID(r.ID),
		Unit:      graph.Unit{Namespace: namespace.Name(r.Namespace), Tool: r.UnitTool, ID: r.UnitID},
		Kind:      trigger.Kind(r.Kind),
		Frequency: trigger.Frequency{Preset: cron.Preset(r.Preset), Cron: r.Cron},
		Source: graph.Unit{
			Namespace: namespace.Name(r.SourceNamespace), Tool: r.SourceTool, ID: r.SourceUnitID,
		},
		Enabled:     r.Enabled == 1,
		LastSaver:   r.LastSaverSubject,
		LastSaverID: r.LastSaverID,
	}
}

// Trigger returns the trigger that r holds and checks it again with
// trigger.Trigger.Validate. It returns ErrCorrupt for a row that fails. The
// error also wraps trigger.ErrInvalid.
func Trigger(r Row) (trigger.Trigger, error) {
	t, err := rebuild(r)
	if err != nil {
		return trigger.Trigger{}, err
	}

	if err := t.Validate(); err != nil {
		return trigger.Trigger{}, fmt.Errorf("%w: %w: %w", ErrCorrupt, trigger.ErrInvalid, err)
	}

	return t, nil
}

// CheckStaged checks the trigger row r that a restore stages, with now as the
// clock of the store. It checks the row with Trigger, and then refuses an
// enabled schedule trigger whose next slot is not a slot of its schedule, or is
// after the first slot of the schedule after now plus trigger.MaxClaimSkew. A
// claim accepts a slot up to now plus trigger.MaxClaimSkew, so a backup from an
// instance whose clock is ahead inside the skew can hold such a next slot. A
// later next slot would answer ErrNotDue to every claim of a real slot, so the
// schedule would stop. A trigger that is off, or a trigger of another kind, has
// no slot, and this rule does not apply to it. It returns ErrCorrupt for a row
// that fails.
//
// Defense in depth: the store checks a row again when it reads it, and Put
// writes the slot from the same clock. This check stays so that a bad row fails
// the restore and does not wait for a claim.
func CheckStaged(r Row, now time.Time) error {
	t, err := Trigger(r)
	if err != nil {
		return err
	}

	if t.Kind != trigger.KindSchedule || !t.Enabled {
		return nil
	}

	next := NextSlot(r)

	if !trigger.IsSlot(t, next) {
		return fmt.Errorf("%w: %w: the next slot %v is not a slot of the schedule",
			ErrCorrupt, trigger.ErrInvalid, next)
	}

	after, err := trigger.NextSlot(t, now.Add(trigger.MaxClaimSkew))
	if err != nil {
		return fmt.Errorf("%w: %w: %w", ErrCorrupt, trigger.ErrInvalid, err)
	}

	if next.After(after) {
		return fmt.Errorf(
			"%w: %w: the next slot %v is after the first slot after the clock of the store plus the claim skew, %v",
			ErrCorrupt,
			trigger.ErrInvalid,
			next,
			after,
		)
	}

	return nil
}

// Triggers returns the triggers of rows, in the order of rows. A row that
// fails its checks fails the call (fail closed): no row after it is served.
func Triggers(rows []Row) ([]trigger.Trigger, error) {
	out := make([]trigger.Trigger, 0, len(rows))

	for i := range rows {
		t, err := Trigger(rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, t)
	}

	return out, nil
}

// Usable returns the triggers of rows that pass their checks, in the order of
// rows. It calls onSkip, when set, for each row that does not. The cycle check
// uses it: a row that fails its checks starts no plan, so it is no edge.
func Usable(rows []Row, onSkip func(Row)) []trigger.Trigger {
	out := make([]trigger.Trigger, 0, len(rows))

	for i := range rows {
		t, err := Trigger(rows[i])
		if err != nil {
			if onSkip != nil {
				onSkip(rows[i])
			}

			continue
		}

		out = append(out, t)
	}

	return out
}

// OfUnit returns the triggers of rows, which must be the triggers of unit. It
// fails closed as Triggers does. Defense in depth: the query selects the rows of
// the unit. This check stays so that a change of the query cannot list a trigger
// of another unit.
func OfUnit(rows []Row, unit graph.Unit) ([]trigger.Trigger, error) {
	out, err := Triggers(rows)
	if err != nil {
		return nil, err
	}

	for i := range out {
		if out[i].Unit != unit {
			return nil, fmt.Errorf("%w: %w: trigger %q is not a trigger of the unit",
				ErrCorrupt, trigger.ErrInvalid, out[i].ID)
		}
	}

	return out, nil
}

// FollowersOf returns the triggers of rows, which must be the enabled follows
// triggers of source. It checks that each row is a follows trigger, that it is
// on, and that it names source. A row that has a value that the rules of a
// trigger refuse is still returned: the dispatcher validates each follower
// and answers an outcome with trigger.ErrInvalid for it and starts no plan.
// A refusal of the whole read would stop the valid followers of the same
// source for one bad row. Only a row that cannot be a follower of source at all
// fails the call.
func FollowersOf(rows []Row, source graph.Unit) ([]trigger.Trigger, error) {
	out := make([]trigger.Trigger, 0, len(rows))

	for i := range rows {
		t, err := rebuild(rows[i])
		if err != nil {
			return nil, err
		}

		// Defense in depth: the query selects the enabled follows triggers of
		// the source. This check stays so that a change of the query cannot
		// start a plan for a trigger that does not follow the source.
		if t.Kind != trigger.KindFollows || !t.Enabled || t.Source != source {
			return nil, fmt.Errorf("%w: %w: trigger %q does not follow the unit",
				ErrCorrupt, trigger.ErrInvalid, t.ID)
		}

		out = append(out, t)
	}

	return out, nil
}

// CheckGet checks the arguments of a read of one trigger.
func CheckGet(ns namespace.Name, id trigger.ID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("get trigger: %w", err)
	}

	return checkNamespace(ns)
}

// CheckUnit checks the unit of a list or of a followers read.
func CheckUnit(u graph.Unit) error {
	if err := u.Validate(); err != nil {
		return fmt.Errorf("%w: unit: %w", trigger.ErrInvalid, err)
	}

	return nil
}

func checkNamespace(ns namespace.Name) error {
	if _, err := namespace.ParseName(string(ns)); err != nil {
		return fmt.Errorf("%w: namespace: %w", trigger.ErrInvalid, err)
	}

	return nil
}

// CheckPut checks the arguments of a Put before the transaction starts. The
// adapter calls it first and again checks the fence in the transaction.
//
// It returns trigger.ErrInvalid when t fails Validate, namespace.ErrFenced
// when the fence names another namespace than the unit of t, and
// audit.ErrInvalidEntry for an entry that fails Validate or names another
// namespace.
func CheckPut(t trigger.Trigger, fence namespace.Fence, e audit.Entry) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("put trigger: %w", err)
	}

	if err := checkFenceNamespace(t.Unit.Namespace, fence); err != nil {
		return err
	}

	return checkEntry(e, t.Unit.Namespace)
}

// CheckDelete checks the arguments of a Delete before the transaction starts,
// as CheckPut does for a Put.
func CheckDelete(ns namespace.Name, id trigger.ID, fence namespace.Fence, e audit.Entry) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("delete trigger: %w", err)
	}

	if err := checkNamespace(ns); err != nil {
		return fmt.Errorf("delete trigger: %w", err)
	}

	if err := checkFenceNamespace(ns, fence); err != nil {
		return err
	}

	return checkEntry(e, ns)
}

// Defense in depth: the transaction of the adapter compares the fence with the
// namespace row. This check stays so that a trigger of one namespace cannot be
// written under the fence of another namespace. It does not rely on the
// audit entry or on the caller.
func checkFenceNamespace(ns namespace.Name, fence namespace.Fence) error {
	if fence.Namespace != ns {
		return fmt.Errorf("%w: trigger names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return nil
}

// Defense in depth: the audited transaction of the adapter checks the entry
// too. This check stays so that an entry of another namespace never reaches a
// chain, whatever the adapter does.
func checkEntry(e audit.Entry, ns namespace.Name) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != ns {
		return fmt.Errorf("%w: the entry names namespace %q, not %q", audit.ErrInvalidEntry, e.Namespace, ns)
	}

	return nil
}

// Tx is what the steps of a write need from the transaction of an adapter. The
// adapter has already checked the fence in that transaction, and its
// transaction serializes the writes of the namespace: SQLite has one writer, and
// PostgreSQL holds the namespace row FOR UPDATE. So the reads below and the
// writes that follow them see no other write in between.
type Tx interface {
	// Find returns the row of the trigger id in ns, and false when none exists.
	Find(ctx context.Context, ns namespace.Name, id trigger.ID) (Row, bool, error)
	// EnabledFollows returns the rows of the enabled follows triggers of ns.
	EnabledFollows(ctx context.Context, ns namespace.Name) ([]Row, error)
	// Upsert writes r, and keeps the creation time of a row that exists.
	Upsert(ctx context.Context, r Row) error
	// Remove deletes the trigger id of ns, and reports whether a row existed.
	Remove(ctx context.Context, ns namespace.Name, id trigger.ID) (bool, error)
	// MoveNextSlot sets the next slot of the enabled schedule trigger id of ns
	// to next, in Unix milliseconds, only when it is old now. It reports whether
	// it changed a row.
	MoveNextSlot(ctx context.Context, ns namespace.Name, id trigger.ID, old, next int64) (bool, error)
	// Now returns the time of the clock of the store, read in the transaction.
	// PostgreSQL gives the clock of the database, so every instance agrees on it.
	// SQLite has one process, and gives its clock.
	Now(ctx context.Context) (time.Time, error)
}

// ApplyPut writes t in tx, stamped with at. It refuses a replace that changes
// the unit or the kind, reads the enabled follows triggers of the namespace in
// tx, applies trigger.CheckCycle, and writes the row. It returns
// trigger.ErrInvalid, a *trigger.CycleError, or an error of tx.
//
// A row of the namespace that fails its checks does not fail the call: no
// dispatcher starts a plan for that row, so it is no edge of the graph, and one
// bad row must not stop each save of the namespace. onSkip, when set, gets each
// such row, for a log entry. Get and List still fail closed for the row.
func ApplyPut(ctx context.Context, tx Tx, t trigger.Trigger, at time.Time, onSkip func(Row)) error {
	old, found, err := tx.Find(ctx, t.Unit.Namespace, t.ID)
	if err != nil {
		return err
	}

	if found && (old.UnitTool != t.Unit.Tool || old.UnitID != t.Unit.ID || old.Kind != string(t.Kind)) {
		return fmt.Errorf("%w: a trigger keeps its unit and its kind", trigger.ErrInvalid)
	}

	rows, err := tx.EnabledFollows(ctx, t.Unit.Namespace)
	if err != nil {
		return err
	}

	existing := Usable(rows, onSkip)

	if cycleErr := trigger.CheckCycle(existing, t); cycleErr != nil {
		return fmt.Errorf("put trigger: %w", cycleErr)
	}

	var prev *trigger.Trigger

	if found {
		stored := Stored(old)
		prev = &stored
	}

	// The first slot comes from the clock of the store, and not from the time of
	// the audit entry that the caller stamped: one instance with a wrong clock
	// must not choose the slot of a schedule.
	clock, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	slot, slotErr := trigger.PutSlot(t, clock, prev, fromMs(old.NextSlotMs))
	if slotErr != nil {
		return fmt.Errorf("put trigger: %w", slotErr)
	}

	r := RowOf(t, at.UnixMilli())
	r.NextSlotMs = toMs(slot)

	return tx.Upsert(ctx, r)
}

// fromMs returns the time of a slot in Unix milliseconds, and the zero time for 0.
func fromMs(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// toMs returns the Unix milliseconds of a slot, and 0 for the zero time.
func toMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// NextSlot returns the next slot that r records, and the zero time when it
// records none.
func NextSlot(r Row) time.Time {
	return fromMs(r.NextSlotMs)
}

// CheckClaim checks the arguments of a ClaimDue before the transaction starts,
// as CheckPut does for a Put.
func CheckClaim(c trigger.Claim, fence namespace.Fence, e audit.Entry) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("claim trigger: %w", err)
	}

	if err := checkFenceNamespace(c.Namespace, fence); err != nil {
		return err
	}

	return checkEntry(e, c.Namespace)
}

// ApplyClaim takes the slot of c in tx. It reads the row, applies
// trigger.CheckClaim to it, and moves the next slot from the value that it read
// to the slot after c.Slot. It returns the stable ID of the unit of the trigger,
// for the audit entry. It returns trigger.ErrNotFound for an unknown trigger, the
// errors of trigger.CheckClaim, and trigger.ErrNotDue when the move changed no
// row because the slot moved since the read.
func ApplyClaim(ctx context.Context, tx Tx, c trigger.Claim) (string, error) {
	row, found, err := tx.Find(ctx, c.Namespace, c.Trigger)
	if err != nil {
		return "", err
	}

	if !found {
		return "", trigger.ErrNotFound
	}

	// A row that fails its checks is not claimed: Trigger fails closed, so a
	// row of another version or a restore never starts a plan at a rate that
	// the rules refuse.
	t, err := Trigger(row)
	if err != nil {
		return "", fmt.Errorf("claim trigger: %w", err)
	}

	clock, err := tx.Now(ctx)
	if err != nil {
		return "", err
	}

	next, err := trigger.CheckClaim(c, t, NextSlot(row), clock)
	if err != nil {
		return "", fmt.Errorf("claim trigger: %w", err)
	}

	moved, err := tx.MoveNextSlot(ctx, c.Namespace, c.Trigger, row.NextSlotMs, toMs(next))
	if err != nil {
		return "", err
	}

	if !moved {
		return "", trigger.ErrNotDue
	}

	return row.UnitID, nil
}

// ApplyDelete removes the trigger id of ns in tx. It returns the stable ID of
// the unit of the removed trigger, for the audit entry, and trigger.ErrNotFound
// when no trigger exists. It removes a row that fails its checks too, so that
// an operator can clean up a bad row.
func ApplyDelete(ctx context.Context, tx Tx, ns namespace.Name, id trigger.ID) (string, error) {
	row, found, err := tx.Find(ctx, ns, id)
	if err != nil {
		return "", err
	}

	if !found {
		return "", trigger.ErrNotFound
	}

	removed, err := tx.Remove(ctx, ns, id)
	if err != nil {
		return "", err
	}

	if !removed {
		return "", trigger.ErrNotFound
	}

	return row.UnitID, nil
}
