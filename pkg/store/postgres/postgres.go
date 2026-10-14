// Package postgres is the PostgreSQL store adapter, built on pgx.
//
// Fenced writes lock the namespace row with FOR SHARE inside the write
// transaction, so a concurrent promotion (which updates that row) waits until
// the write commits or aborts. Migrations run under a transaction-scoped
// advisory lock, so several servers can start at once.
//
// AppliedMigrations reports the rows of schema_migrations ordered by
// version. It is the one place `opendustpan upgrade check` reads, through
// the installation API: only the server opens the database.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID is the advisory lock key held while migrating ("odpn" in ASCII).
const migrationLockID int64 = 0x6f64706e

// Store implements namespace.Store, stack.Store and the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) on PostgreSQL.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	// stagingMu guards stagingSchema: one staged restore at a time.
	stagingMu     sync.Mutex
	stagingSchema string
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// Open connects to the database at url and migrates it.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	s := &Store{pool: pool, queries: db.New(pool), stagingMu: sync.Mutex{}, stagingSchema: ""}
	if err := s.migrate(ctx); err != nil {
		pool.Close()

		return nil, err
	}

	return s, nil
}

// Close drops a live staging schema (best effort) and closes every connection
// in the pool, so no scratch schema survives the handle. A caller that holds
// a context uses CloseContext instead.
func (s *Store) Close() {
	s.CloseContext(context.Background())
}

// CloseContext is Close with the caller's context. The staging cleanup runs
// detached from ctx (context.WithoutCancel), so a canceled context still drops
// the scratch schema.
func (s *Store) CloseContext(ctx context.Context) {
	s.stagingMu.Lock()
	staging := s.stagingSchema
	s.stagingSchema = ""
	s.stagingMu.Unlock()

	if staging != "" {
		// Best-effort cleanup of the scratch schema after Close.
		_ = s.dropStagingSchema(context.WithoutCancel(ctx), staging) //nolint:errcheck // Best-effort cleanup.
	}

	s.pool.Close()
}

// Ping implements namespace.Store. It maps any driver failure to
// ErrUnavailable without driver text.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres database: %w", namespace.ErrUnavailable)
	}

	return nil
}

// CreateNamespace implements namespace.Store.
func (s *Store) CreateNamespace(ctx context.Context, ns namespace.Namespace) error {
	row := codec.ToNamespaceRow(ns)

	n, err := s.queries.CreateNamespace(ctx, db.CreateNamespaceParams{
		Name:            row.Name,
		HomeRegion:      row.HomeRegion,
		FailoverVersion: row.FailoverVersion,
		CreatedAtMs:     row.CreatedAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert namespace: %w", err)
	}

	if n == 0 {
		return namespace.ErrAlreadyExists
	}

	return nil
}

// GetNamespace implements namespace.Store.
func (s *Store) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	row, err := s.queries.GetNamespace(ctx, name.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
}

// SetOverrides implements namespace.Store. It replaces the override switches
// of the namespace (ADR 44, decision 6).
func (s *Store) SetOverrides(
	ctx context.Context, name namespace.Name, ov namespace.Overrides, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.SetNamespaceOverrides(ctx, db.SetNamespaceOverridesParams{
			AllowForcedWrites:   ov.Forced,
			AllowUnlockedWrites: ov.Unlocked,
			Name:                name.String(),
		})
		if err != nil {
			return fmt.Errorf("update namespace overrides: %w", err)
		}

		if n == 0 {
			return namespace.ErrNotFound
		}

		return nil
	})
}

// ListNamespaces implements namespace.Store.
func (s *Store) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	rows, err := s.queries.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("select namespaces: %w", err)
	}

	out := make([]namespace.Namespace, 0, len(rows))
	for _, row := range rows {
		out = append(out, codec.FromNamespaceRow(codec.NamespaceRow(row)))
	}

	return out, nil
}

// CreateStack implements stack.Store with a fenced insert. Tags go to the
// stack_tags table, keyed by the new stack ID. The inventory entry is written
// in the same transaction.
func (s *Store) CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error {
	row := codec.ToStackRow(st)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.CreateStack(ctx, db.CreateStackParams(row))
		if err != nil {
			return fmt.Errorf("insert stack: %w", err)
		}

		if n == 0 {
			return stack.ErrAlreadyExists
		}

		if err := insertStackTags(ctx, q, row.ID, st.Tags); err != nil {
			return err
		}

		return publishStackInventory(ctx, q, st.Ref, row.ID, row.UpdatedAtMs)
	})
}

// GetStack implements stack.Store.
func (s *Store) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	row, err := s.queries.GetStack(ctx, db.GetStackParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Stack{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.Stack{}, fmt.Errorf("select stack: %w", err)
	}

	tags, err := stackTags(ctx, s.queries, row.ID)
	if err != nil {
		return stack.Stack{}, err
	}

	return codec.FromStackRow(codec.StackRow{
		Namespace:   row.Namespace,
		Project:     row.Project,
		Name:        row.Name,
		ID:          row.ID,
		Version:     row.Version,
		CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	}, tags), nil
}

// UpdateStackTags implements stack.Store: it replaces every tag row of the
// stack, keyed by its stable ID, and stamps updatedAt. The inventory entry is
// written in the same transaction.
func (s *Store) UpdateStackTags(
	ctx context.Context, ref stack.Ref, tags map[string]string, updatedAt time.Time, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		id, err := q.GetStackID(ctx, db.GetStackIDParams{
			Namespace: ref.Namespace.String(),
			Project:   ref.Project.String(),
			Name:      ref.Stack.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return stack.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select stack ID: %w", err)
		}

		if err := q.DeleteStackTags(ctx, id); err != nil {
			return fmt.Errorf("delete stack tags: %w", err)
		}

		touchErr := q.TouchStack(ctx, db.TouchStackParams{
			UpdatedAtMs: codec.Millis(updatedAt),
			ID:          id,
		})
		if touchErr != nil {
			return fmt.Errorf("stamp stack: %w", touchErr)
		}

		if err := insertStackTags(ctx, q, id, tags); err != nil {
			return err
		}

		return publishStackInventory(ctx, q, ref, id, codec.Millis(updatedAt))
	})
}

// insertStackTags writes one row per tag, keyed by the stack ID. A nil or
// empty map writes nothing, which clears the tags.
func insertStackTags(ctx context.Context, q *db.Queries, id string, tags map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(tags)) {
		tagErr := q.CreateStackTag(ctx, db.CreateStackTagParams{
			StackID: id,
			Name:    name,
			Value:   tags[name],
		})
		if tagErr != nil {
			return fmt.Errorf("insert stack tag: %w", tagErr)
		}
	}

	return nil
}

// stackTags reads the tag rows of one stack ID into a map.
func stackTags(ctx context.Context, q *db.Queries, id string) (map[string]string, error) {
	rows, err := q.GetStackTags(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("select stack tags: %w", err)
	}

	tags := make(map[string]string, len(rows))
	for _, row := range rows {
		tags[row.Name] = row.Value
	}

	return tags, nil
}

// RenameStack implements stack.Store. It renames the stack without moving its
// history, checkpoint or events: child rows key on the stable stack ID. The
// inventory keeps one entry for the same stack ID, under the new name, in the
// same transaction.
func (s *Store) RenameStack(
	ctx context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if newName == oldRef.Stack {
			return confirmStackPresent(ctx, q, oldRef)
		}

		if err := rejectTakenName(ctx, q, oldRef, newName); err != nil {
			return err
		}

		n, err := q.RenameStack(ctx, db.RenameStackParams{
			Name:      newName.String(),
			Namespace: oldRef.Namespace.String(),
			Project:   oldRef.Project.String(),
			Name_2:    oldRef.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("rename stack: %w", err)
		}

		if n == 0 {
			return stack.ErrNotFound
		}

		if removeErr := removeStackInventory(ctx, q, oldRef); removeErr != nil {
			return removeErr
		}

		newRef := stack.Ref{Namespace: oldRef.Namespace, Project: oldRef.Project, Stack: newName}

		row, err := q.GetStack(ctx, db.GetStackParams{
			Namespace: newRef.Namespace.String(),
			Project:   newRef.Project.String(),
			Name:      newRef.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("read renamed stack: %w", err)
		}

		return publishStackInventory(ctx, q, newRef, row.ID, row.UpdatedAtMs)
	})
}

// confirmStackPresent returns nil when ref exists and ErrNotFound otherwise.
// It answers a rename that keeps the current name.
func confirmStackPresent(ctx context.Context, q *db.Queries, ref stack.Ref) error {
	params := db.GetStackParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	}

	_, err := q.GetStack(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read stack: %w", err)
	}

	return nil
}

// rejectTakenName returns ErrAlreadyExists when newName already names a stack
// of the same project.
func rejectTakenName(ctx context.Context, q *db.Queries, oldRef stack.Ref, newName stack.Name) error {
	params := db.GetStackParams{
		Namespace: oldRef.Namespace.String(),
		Project:   oldRef.Project.String(),
		Name:      newName.String(),
	}

	_, err := q.GetStack(ctx, params)
	if err == nil {
		return stack.ErrAlreadyExists
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check new stack name: %w", err)
	}

	return nil
}

// DeleteStack implements stack.Store. It soft-deletes the stack in one fenced
// transaction (ADR 47): it frees the name, keeps the updates, checkpoints,
// events, tags and wrapped data key, and removes the inventory entry. When
// the latest checkpoint manages resources, it returns ErrHasResources and
// changes nothing: rows with a stored count use it, rows with SQL deployment
// content count it, and rows with a NULL count and blob content use guard,
// which the service counted outside the transaction. A guard naming another
// checkpoint returns ErrConcurrentModification so the service recounts once. It returns
// ErrNotFound if no live stack has ref, and ErrUpdateConflict while a
// mutating update of the stack runs.
// The stack row is locked before the running check, so a concurrent start
// or save either wins the lock and the delete conflicts, or waits and then
// finds no stack. A concurrent create of an update takes no stack lock. When
// it read the stack before the delete committed, its row stays with the
// deleted stack and never starts, because a start finds the stack by name.
func (s *Store) DeleteStack(
	ctx context.Context, ref stack.Ref, fence namespace.Fence, guard stack.DeleteGuard, at time.Time,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, ref)
		if errors.Is(err, stack.ErrUpdateNotFound) {
			return stack.ErrNotFound
		}

		if err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, locked.ID); err != nil {
			return err
		}

		if err := refuseResourcedStack(ctx, q, locked.ID, guard); err != nil {
			return err
		}

		return softDeleteStack(ctx, q, ref, locked.ID, at)
	})
}

// ForceDeleteStack implements stack.Store. It soft-deletes the stack as
// DeleteStack does, even when the latest checkpoint manages resources. It
// returns ErrNotFound if no live stack has ref, and ErrUpdateConflict while a
// mutating update of the stack runs.
func (s *Store) ForceDeleteStack(
	ctx context.Context, ref stack.Ref, fence namespace.Fence, at time.Time,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, ref)
		if errors.Is(err, stack.ErrUpdateNotFound) {
			return stack.ErrNotFound
		}

		if err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, locked.ID); err != nil {
			return err
		}

		return softDeleteStack(ctx, q, ref, locked.ID, at)
	})
}

// refuseRunningStack returns ErrUpdateConflict while a mutating update of the
// stack with stackID runs. The caller holds the stack lock.
func refuseRunningStack(ctx context.Context, q *db.Queries, stackID string) error {
	running, err := q.RunningMutatingUpdates(ctx, stackID)
	if err != nil {
		return fmt.Errorf("count running updates: %w", err)
	}

	if running > 0 {
		return stack.ErrUpdateConflict
	}

	return nil
}

// refuseResourcedStack returns ErrHasResources when the latest checkpoint of
// the stack with stackID manages resources. Rows with a stored count use it;
// rows with SQL deployment content count it with stack.CountManagedResources;
// rows with a NULL count and blob content use guard, which the service counted
// outside the transaction: a guard naming another checkpoint returns
// ErrConcurrentModification so the service recounts once. It reads only the
// latest row, never a blob. The caller holds the stack lock.
func refuseResourcedStack(
	ctx context.Context, q *db.Queries, stackID string, guard stack.DeleteGuard,
) error {
	row, err := q.GetLatestCheckpoint(ctx, stackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkNoCheckpoint(guard)
	}

	if err != nil {
		return fmt.Errorf("read latest checkpoint: %w", err)
	}

	if row.ResourceCount.Valid {
		return checkStoredCount(row.ResourceCount.Int64)
	}

	if len(row.Deployment) > 0 || !row.BlobKey.Valid || row.BlobKey.String == "" {
		return checkDeployment(row.Deployment)
	}

	return checkBlobGuard(guard, row.UpdateID, row.Digest)
}

// checkNoCheckpoint answers a delete with no checkpoint row: a guard that
// counted one means a checkpoint landed after the count.
func checkNoCheckpoint(guard stack.DeleteGuard) error {
	if guard.HasCheckpoint {
		return stack.ErrConcurrentModification
	}

	return nil
}

// checkStoredCount answers a delete from the stored count of an older binary.
func checkStoredCount(count int64) error {
	if count > 0 {
		return stack.ErrHasResources
	}

	return nil
}

// checkDeployment answers a delete by counting the SQL deployment column.
func checkDeployment(deployment []byte) error {
	if stack.CountManagedResources(deployment) > 0 {
		return stack.ErrHasResources
	}

	return nil
}

// checkBlobGuard answers a delete of a blob row from the domain count in
// guard: a guard naming another checkpoint means a save landed after the
// count, so the caller recounts once.
func checkBlobGuard(guard stack.DeleteGuard, updateID string, digest []byte) error {
	if !guard.HasCheckpoint || string(guard.UpdateID) != updateID || !equalDigest(guard.Digest, digest) {
		return stack.ErrConcurrentModification
	}

	if guard.HasResources {
		return stack.ErrHasResources
	}

	return nil
}

// equalDigest reports whether digest holds the checkpoint digest column.
func equalDigest(digest [32]byte, column []byte) bool {
	if len(column) != len(digest) {
		return false
	}

	for i := range digest {
		if digest[i] != column[i] {
			return false
		}
	}

	return true
}

// softDeleteStack marks the stack with stackID deleted at the time at (ADR
// 47). It moves the name to deleted_name, so the name is free at once, and
// keeps the updates, checkpoints, events, tags and wrapped data key until the
// final delete. It moves the inventory entry to the deleted units, so the
// stack leaves the live inventory. It returns ErrNotFound when no live stack
// has stackID.
func softDeleteStack(ctx context.Context, q *db.Queries, ref stack.Ref, stackID string, at time.Time) error {
	n, err := q.SoftDeleteStack(ctx, db.SoftDeleteStackParams{
		DeletedAtMs: pgtype.Int8{Int64: at.UnixMilli(), Valid: true},
		ID:          stackID,
	})
	if err != nil {
		return fmt.Errorf("delete stack: %w", err)
	}

	if n == 0 {
		return stack.ErrNotFound
	}

	return moveStackInventoryToDeleted(ctx, q, ref, stackID, at)
}

// ListStacks implements stack.Store. Tags come from one query for the whole
// namespace, not one query per stack.
func (s *Store) ListStacks(ctx context.Context, filter stack.Filter) ([]stack.Stack, error) {
	if filter.Project == "" {
		rows, err := s.queries.ListStacksInNamespace(ctx, filter.Namespace.String())
		if err != nil {
			return nil, fmt.Errorf("select stacks: %w", err)
		}

		out := make([]stack.Stack, 0, len(rows))

		if len(rows) == 0 {
			return out, nil
		}

		tags, err := stackTagMap(ctx, s.queries, filter.Namespace.String())
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			out = append(out, codec.FromStackRow(codec.StackRow{
				Namespace:   row.Namespace,
				Project:     row.Project,
				Name:        row.Name,
				ID:          row.ID,
				Version:     row.Version,
				CreatedAtMs: row.CreatedAtMs,
				UpdatedAtMs: row.UpdatedAtMs,
			}, tags[row.ID]))
		}

		return out, nil
	}

	rows, err := s.queries.ListStacksInProject(ctx, db.ListStacksInProjectParams{
		Namespace: filter.Namespace.String(),
		Project:   filter.Project.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("select stacks: %w", err)
	}

	out := make([]stack.Stack, 0, len(rows))

	if len(rows) == 0 {
		return out, nil
	}

	tags, err := stackTagMap(ctx, s.queries, filter.Namespace.String())
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		out = append(out, codec.FromStackRow(codec.StackRow{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		}, tags[row.ID]))
	}

	return out, nil
}

// stackTagMap reads every tag row of the namespace in one query, keyed by
// stack ID.
func stackTagMap(ctx context.Context, q *db.Queries, ns string) (map[string]map[string]string, error) {
	rows, err := q.ListStackTagsInNamespace(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("select stack tags: %w", err)
	}

	out := make(map[string]map[string]string, len(rows))

	for _, row := range rows {
		m := out[row.StackID]
		if m == nil {
			m = make(map[string]string)
			out[row.StackID] = m
		}

		m[row.Name] = row.Value
	}

	return out, nil
}

func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(s.queries.WithTx(tx))
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}

	return nil
}

// inSnapshotTx runs fn in a REPEATABLE READ, read-only transaction, so each
// read of fn sees one snapshot: a page and its total agree.
func (s *Store) inSnapshotTx(ctx context.Context, fn func(q *db.Queries) error) error {
	opts := pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly, DeferrableMode: pgx.NotDeferrable, BeginQuery: "",
		CommitQuery: "",
	}

	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		return fn(s.queries.WithTx(tx))
	})
	if err != nil {
		return fmt.Errorf("snapshot transaction: %w", err)
	}

	return nil
}

// inFencedTx runs fn in a transaction that first checks fence, holding the
// namespace row FOR SHARE so a promotion waits until the transaction ends.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		version, err := q.GetFailoverVersionForShare(ctx, fence.Namespace.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read failover version: %w", err)
		}

		if version != fence.FailoverVersion {
			return namespace.ErrFenced
		}

		return fn(q)
	})
}

func (s *Store) migrate(ctx context.Context) error {
	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return err
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return migrateTx(ctx, tx, all)
	})
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

// migrateTx applies pending migrations while holding the advisory lock.
func migrateTx(ctx context.Context, tx pgx.Tx, all []migrate.Migration) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := tx.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	recorded, err := recordedVersions(ctx, tx)
	if err != nil {
		return err
	}

	if unknown := migrate.UnknownRecorded(recorded, all); len(unknown) > 0 {
		return schema.NewSchemaNewerError(recorded, embeddedVersions(all))
	}

	for _, m := range all {
		if err := apply(ctx, tx, m); err != nil {
			return err
		}
	}

	return nil
}

// embeddedVersions returns the versions the binary embeds, for the
// newer-schema refusal.
func embeddedVersions(all []migrate.Migration) []int64 {
	out := make([]int64, 0, len(all))
	for _, m := range all {
		out = append(out, m.Version)
	}

	return out
}

// recordedVersions returns the versions schema_migrations records, ordered.
// The caller holds the migration lock in the same transaction.
func recordedVersions(ctx context.Context, tx pgx.Tx) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer rows.Close()

	var out []int64

	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("read schema versions: %w", err)
		}

		out = append(out, version)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	return out, nil
}

func apply(ctx context.Context, tx pgx.Tx, m migrate.Migration) error {
	var applied bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, m.Version).
		Scan(&applied)
	if err != nil {
		return fmt.Errorf("check migration %s: %w", m.Name, err)
	}

	if applied {
		return nil
	}

	// No arguments, so pgx uses the simple protocol, which allows several statements.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Name, err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, record, m.Version, m.Name); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}

	return nil
}
