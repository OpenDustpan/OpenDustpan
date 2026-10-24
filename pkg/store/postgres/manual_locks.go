package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var _ manuallock.Store = (*Store)(nil)

// PutLock implements manuallock.Store. The fence is checked and the audit
// entry is appended in the same transaction as the insert.
func (s *Store) PutLock(ctx context.Context, l manuallock.Lock, fence namespace.Fence, e audit.Entry) error {
	if l.Target.Namespace() != fence.Namespace {
		return fmt.Errorf("%w: %s is not in namespace %q", manuallock.ErrTargetNotFound, l.Target, fence.Namespace)
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		id, err := manualLockObject(ctx, q, l.Target)
		if err != nil {
			return err
		}

		// A create of a workspace reads the manual lock of its namespace under
		// the shared form of this lock, so this put waits for a create that
		// runs, and a create that starts later reads the new lock.
		if l.Target.Kind == manuallock.KindNamespace {
			if lockErr := q.LockNamespaceCreatesExclusive(ctx, l.Target.Namespace().String()); lockErr != nil {
				return fmt.Errorf("lock the creates of namespace %q: %w", l.Target.Namespace(), lockErr)
			}
		}

		n, err := q.InsertManualLock(ctx, db.InsertManualLockParams{
			Kind:      string(l.Target.Kind),
			ObjectID:  id,
			Namespace: l.Target.Namespace().String(),
			Holder:    l.Holder,
			Reason:    l.Reason,
			SinceMs:   codec.Millis(l.Since),
		})
		if err != nil {
			return fmt.Errorf("insert manual lock: %w", err)
		}

		if n == 0 {
			return manuallock.ErrAlreadyLocked
		}

		e.ObjectID = id

		return nil
	})
}

// RemoveLock implements manuallock.Store. The holder check, the delete and the
// audit entry run in one transaction. It refuses an empty holder: the removal
// of a lock by a principal that does not hold it goes through ForceRemoveLock.
func (s *Store) RemoveLock(
	ctx context.Context, t manuallock.Target, holder string, fence namespace.Fence, e audit.Entry,
) (manuallock.Lock, error) {
	if holder == "" {
		return manuallock.Lock{}, fmt.Errorf("%w: a removal without a holder", manuallock.ErrNotHolder)
	}

	return s.endLock(ctx, t, holder, fence, e)
}

// ForceRemoveLock implements manuallock.Store. It removes the lock whoever
// holds it, in the same transaction as the delete and the audit entry.
func (s *Store) ForceRemoveLock(
	ctx context.Context, t manuallock.Target, fence namespace.Fence, e audit.Entry,
) (manuallock.Lock, error) {
	return s.endLock(ctx, t, "", fence, e)
}

// endLock removes the lock of t. A holder that is not empty must hold the
// lock; an empty holder is the forced removal, which only ForceRemoveLock
// reaches.
func (s *Store) endLock(
	ctx context.Context, t manuallock.Target, holder string, fence namespace.Fence, e audit.Entry,
) (manuallock.Lock, error) {
	if t.Namespace() != fence.Namespace {
		return manuallock.Lock{}, fmt.Errorf("%w: %s is not in namespace %q",
			manuallock.ErrTargetNotFound, t, fence.Namespace)
	}

	var removed manuallock.Lock

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		id, err := manualLockObject(ctx, q, t)
		if err != nil {
			return err
		}

		held, err := readManualLockForRemoval(ctx, q, t, id)
		if err != nil {
			return err
		}

		if holder != "" && held.Holder != holder {
			return fmt.Errorf("%w: held by %s", manuallock.ErrNotHolder, held.Holder)
		}

		if delErr := s.deleteHeldLock(ctx, q, t, id, held); delErr != nil {
			return delErr
		}

		e.ObjectID = id
		e.Changes = append(e.Changes, endedLockChanges(held)...)
		removed = held

		return nil
	})
	if err != nil {
		return manuallock.Lock{}, err
	}

	return removed, nil
}

// GetLock implements manuallock.Store.
func (s *Store) GetLock(ctx context.Context, t manuallock.Target) (manuallock.Lock, error) {
	var lock manuallock.Lock

	// One snapshot, so the object and its lock come from one state.
	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		id, err := manualLockObject(ctx, q, t)
		if err != nil {
			return err
		}

		lock, err = readManualLock(ctx, q, t, id)

		return err
	})
	if err != nil {
		return manuallock.Lock{}, err
	}

	return lock, nil
}

// CoveringLock implements manuallock.Store. The lock of the unit wins over the
// lock of its collection.
func (s *Store) CoveringLock(ctx context.Context, unit collection.UnitRef) (manuallock.Lock, error) {
	return coveringLock(ctx, s.queries, unit)
}

// coveringLock is CoveringLock in the transaction of q. A restore reads the lock
// of the unit it has just brought back, in its own transaction.
func coveringLock(ctx context.Context, q *db.Queries, unit collection.UnitRef) (manuallock.Lock, error) {
	locks, err := coveringLocks(ctx, q, unit)
	if err != nil {
		return manuallock.Lock{}, err
	}

	return locks[0], nil
}

// CoveringLocks implements manuallock.Store. It reads the row of the unit once,
// so the answer is one consistent view of the three locks.
func (s *Store) CoveringLocks(ctx context.Context, unit collection.UnitRef) ([]manuallock.Lock, error) {
	return coveringLocks(ctx, s.queries, unit)
}

// coveringLocks returns every manual lock that covers the unit, the lock of the
// unit first, then the lock of its collection, then the lock of its namespace.
// It returns ErrNotLocked, and never an empty list, when none covers the unit.
func coveringLocks(ctx context.Context, q *db.Queries, unit collection.UnitRef) ([]manuallock.Lock, error) {
	row, err := q.GetCoveringManualLocks(ctx, db.GetCoveringManualLocksParams{
		Namespace: unit.Namespace.String(),
		Tool:      unit.Tool,
		Name:      unit.Name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, manuallock.ErrNotLocked
	}

	if err != nil {
		return nil, fmt.Errorf("select covering manual lock: %w", err)
	}

	var locks []manuallock.Lock

	if row.UnitHolder.Valid {
		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.UnitSinceMs.Int64),
			Target: manuallock.UnitTarget(unit),
			Holder: row.UnitHolder.String,
			Reason: row.UnitReason.String,
		})
	}

	if row.CollectionHolder.Valid {
		ref := collection.Ref{Namespace: unit.Namespace, Name: collection.Name(row.CollectionName.String)}

		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.CollectionSinceMs.Int64),
			Target: manuallock.CollectionTarget(ref),
			Holder: row.CollectionHolder.String,
			Reason: row.CollectionReason.String,
		})
	}

	if row.NamespaceHolder.Valid {
		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.NamespaceSinceMs.Int64),
			Target: manuallock.NamespaceTarget(unit.Namespace),
			Holder: row.NamespaceHolder.String,
			Reason: row.NamespaceReason.String,
		})
	}

	if len(locks) == 0 {
		return nil, manuallock.ErrNotLocked
	}

	return locks, nil
}

// coveringLocksOfUnitID returns every manual lock that covers the unit unitID of
// the namespace ns, in the order of coveringLocks. It finds the unit by its
// stable ID, so a rename of the unit does not hide its locks. It returns
// ErrNotLocked when none covers the unit, and ErrUnitNotInInventory when the
// inventory holds no such unit.
func coveringLocksOfUnitID(
	ctx context.Context, q *db.Queries, ns namespace.Name, unitID string,
) ([]manuallock.Lock, error) {
	row, err := q.GetCoveringManualLocksByUnitID(ctx, db.GetCoveringManualLocksByUnitIDParams{
		Namespace: ns.String(),
		UnitID:    unitID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, jobstore.ErrUnitNotInInventory
	}

	if err != nil {
		return nil, fmt.Errorf("select covering manual lock of unit: %w", err)
	}

	unit := collection.UnitRef{Namespace: ns, Tool: row.UnitTool, Name: row.UnitName}

	var locks []manuallock.Lock

	if row.UnitHolder.Valid {
		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.UnitSinceMs.Int64),
			Target: manuallock.UnitTarget(unit),
			Holder: row.UnitHolder.String,
			Reason: row.UnitReason.String,
		})
	}

	if row.CollectionHolder.Valid {
		ref := collection.Ref{Namespace: ns, Name: collection.Name(row.CollectionName.String)}

		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.CollectionSinceMs.Int64),
			Target: manuallock.CollectionTarget(ref),
			Holder: row.CollectionHolder.String,
			Reason: row.CollectionReason.String,
		})
	}

	if row.NamespaceHolder.Valid {
		locks = append(locks, manuallock.Lock{
			Since:  codec.FromMillis(row.NamespaceSinceMs.Int64),
			Target: manuallock.NamespaceTarget(ns),
			Holder: row.NamespaceHolder.String,
			Reason: row.NamespaceReason.String,
		})
	}

	if len(locks) == 0 {
		return nil, manuallock.ErrNotLocked
	}

	return locks, nil
}

// manualLockObject returns the stable ID of the object of t: the unit ID of
// the inventory, or the ID of the collection. It returns ErrTargetNotFound when
// the object does not exist.
func manualLockObject(ctx context.Context, q *db.Queries, t manuallock.Target) (string, error) {
	if err := t.Valid(); err != nil {
		return "", err
	}

	if t.Kind == manuallock.KindNamespace {
		row, err := q.GetNamespace(ctx, t.Space.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", manuallock.ErrTargetNotFound, t)
		}

		if err != nil {
			return "", fmt.Errorf("select namespace: %w", err)
		}

		return row.ID, nil
	}

	if t.Kind == manuallock.KindCollection {
		row, err := q.GetCollection(ctx, db.GetCollectionParams{
			Namespace: t.Collection.Namespace.String(),
			NameKey:   t.Collection.Name.Key(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", manuallock.ErrTargetNotFound, t)
		}

		if err != nil {
			return "", fmt.Errorf("select collection: %w", err)
		}

		return row.ID, nil
	}

	id, err := q.GetInventoryUnit(ctx, db.GetInventoryUnitParams{
		Namespace: t.Unit.Namespace.String(),
		Tool:      t.Unit.Tool,
		Name:      t.Unit.Name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", manuallock.ErrTargetNotFound, t)
	}

	if err != nil {
		return "", fmt.Errorf("select inventory unit: %w", err)
	}

	return id, nil
}

// readManualLock returns the lock of the object id of t, or ErrNotLocked.
func readManualLock(ctx context.Context, q *db.Queries, t manuallock.Target, id string) (manuallock.Lock, error) {
	row, err := q.GetManualLock(ctx, db.GetManualLockParams{Kind: string(t.Kind), ObjectID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	if err != nil {
		return manuallock.Lock{}, fmt.Errorf("select manual lock: %w", err)
	}

	return manuallock.Lock{Since: codec.FromMillis(row.SinceMs), Target: t, Holder: row.Holder, Reason: row.Reason}, nil
}

// readManualLockForRemoval is readManualLock that also locks the row until the
// transaction ends. The first of two concurrent removals deletes the row. The
// second waits, then finds no row and returns ErrNotLocked.
func readManualLockForRemoval(
	ctx context.Context, q *db.Queries, t manuallock.Target, id string,
) (manuallock.Lock, error) {
	row, err := q.GetManualLockForUpdate(ctx, db.GetManualLockForUpdateParams{Kind: string(t.Kind), ObjectID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return manuallock.Lock{}, manuallock.ErrNotLocked
	}

	if err != nil {
		return manuallock.Lock{}, fmt.Errorf("select manual lock for removal: %w", err)
	}

	return manuallock.Lock{Since: codec.FromMillis(row.SinceMs), Target: t, Holder: row.Holder, Reason: row.Reason}, nil
}

// endedLockChanges returns the audit changes of a removal: the holder, the
// reason and the time of the lock that ends, each with no value after.
func endedLockChanges(l manuallock.Lock) []audit.Change {
	one := func(key, old string) audit.Change {
		return audit.Change{Key: key, Old: &old, New: "", Layer: "", TakesEffect: ""}
	}

	return []audit.Change{
		one("lock-holder", l.Holder),
		one("lock-reason", l.Reason),
		one("locked-at", l.Since.UTC().Format(time.RFC3339)),
	}
}

// inventoryManualFlag converts the manual lock columns of an inventory row into
// the flag of the entry. The lock of the unit wins over the lock of its
// collection, and that wins over the lock of its namespace. It returns nil when no lock covers the unit.
func inventoryManualFlag(row db.ListInventoryRow) *inventory.ManualLock {
	switch {
	case row.UnitManualHolder.Valid:
		return &inventory.ManualLock{
			Since:  codec.FromMillis(row.UnitManualSinceMs.Int64),
			Kind:   string(manuallock.KindUnit),
			Object: row.Tool + ":" + row.Namespace + "/" + row.Name,
			Holder: row.UnitManualHolder.String,
			Reason: row.UnitManualReason.String,
		}
	case row.CollectionManualHolder.Valid:
		return &inventory.ManualLock{
			Since:  codec.FromMillis(row.CollectionManualSinceMs.Int64),
			Kind:   string(manuallock.KindCollection),
			Object: row.Namespace + "/" + row.Collection,
			Holder: row.CollectionManualHolder.String,
			Reason: row.CollectionManualReason.String,
		}
	case row.NamespaceManualHolder.Valid:
		return &inventory.ManualLock{
			Since:  codec.FromMillis(row.NamespaceManualSinceMs.Int64),
			Kind:   string(manuallock.KindNamespace),
			Object: row.Namespace,
			Holder: row.NamespaceManualHolder.String,
			Reason: row.NamespaceManualReason.String,
		}
	default:
		return nil
	}
}

// deleteHeldLock deletes the lock that the caller read. The delete matches the
// holder and the time of that read, so it removes no other lock.
func (s *Store) deleteHeldLock(
	ctx context.Context, q *db.Queries, t manuallock.Target, id string, held manuallock.Lock,
) error {
	if s.beforeLockDelete != nil {
		if err := s.beforeLockDelete(ctx, q); err != nil {
			return err
		}
	}

	n, err := q.DeleteManualLock(ctx, db.DeleteManualLockParams{
		Kind:     string(t.Kind),
		ObjectID: id,
		Holder:   held.Holder,
		SinceMs:  codec.Millis(held.Since),
	})
	if err != nil {
		return fmt.Errorf("delete manual lock: %w", err)
	}

	// Defense in depth: the read above already holds the lock against a
	// concurrent change, and the delete matches the holder and the time of
	// that read. This check stays so that a delete which removes no row or
	// another row is never reported as a removal, and so that no audit entry
	// "ended" is written for a lock that this call did not end.
	if n != 1 {
		return manuallock.ErrLockChanged
	}

	return nil
}

// refuseUnderNamespaceLock returns an error that matches workspace.ErrLocked
// and manuallock.ErrLocked while a manual lock covers the namespace of ref. The
// create of a workspace calls it in its transaction, so a lock that commits
// before the create is never missed. A new workspace is in no collection and
// has no lock of its own, so only the lock of the namespace covers it. The
// request passes when passFrom, the start of the phase of its job, is not zero
// and is before the start of the lock: the phase of a job runs to its end.
//
// Defense in depth: workspace.Service.Create checks the same lock before it
// calls the store. This check does not rely on that one, and it also refuses
// a lock that commits between that check and the create.
func refuseUnderNamespaceLock(ctx context.Context, q *db.Queries, ref workspace.Ref, passFrom time.Time) error {
	target := manuallock.NamespaceTarget(ref.Namespace)

	id, err := manualLockObject(ctx, q, target)
	if err != nil {
		return err
	}

	lock, err := readManualLock(ctx, q, target, id)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return err
	}

	if !passFrom.IsZero() && passFrom.Before(lock.Since) {
		return nil
	}

	return fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{
		Lock: lock,
		Unit: collection.UnitRef{Namespace: ref.Namespace, Tool: workspace.InventoryTool, Name: ref.Name.String()},
		Noun: "workspace",
		Name: ref.String(),
	})
}
