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
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
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
	// gate serves the ports of gate.Store over this store.
	gate *gatestore.Store
	// stagingMu guards stagingSchema: one staged restore at a time.
	stagingMu     sync.Mutex
	stagingSchema string
	// keepAccessHook, when set, runs between the live access read and the
	// schema swap of a keep-access restore, inside the swap transaction.
	// Tests set it to prove the live access tables stay locked across that
	// window. Nil in production.
	keepAccessHook func(ctx context.Context, tx pgx.Tx, live string)
	// betweenProfileReads, when set, runs once in the next read of a profile
	// revision, between the read of the profile and the read of the revision,
	// inside the snapshot. Tests run a restore swap there. Nil in production.
	// The read takes it with an atomic swap, so a read on another goroutine
	// cannot race the test that sets it.
	betweenProfileReads atomic.Pointer[func()]
	// snapshotBegin is the statement that starts a snapshot read: BEGIN, then
	// the lock that makes the read wait for a restore swap (see inSnapshotTx).
	snapshotBegin string
	// beforeLockDelete, when set, runs in the removal of a manual lock between
	// the read of the lock and the delete, inside the transaction. Tests change
	// the lock there to reach the changed-lock check. Nil in production.
	beforeLockDelete func(ctx context.Context, q *db.Queries) error
	// stateLockWait, when it is above zero, replaces stateLockTimeout. Tests
	// set it to reach the timeout without a long wait. Zero in production.
	stateLockWait time.Duration
	// binaryVersion is the version of the binary that opens the store. The
	// migration check compares it with the minimum binary version of the
	// schema. Empty counts as a dev build.
	binaryVersion string
	// kind is the installation kind of the command that opens the store.
	// Empty means no check before the migrations.
	kind installation.Kind
	// noMigrate makes Open apply no migration and refuse a database with
	// a pending one.
	noMigrate bool
	// logger records the warnings of the store. It defaults to a discard
	// handler; pass WithLogger to record them.
	logger *slog.Logger
}

// Option tunes Open. Callers that pass none get the defaults.
type Option func(*Store)

// WithLogger sets the logger that records the warning for a stored preference
// that fails its check. The default is a discard logger, so the store stays
// silent unless the caller passes a logger.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Store) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithBinaryVersion sets the version of the binary, for example v0.1.0. Open
// refuses a schema whose minimum binary version is above it with a
// *schema.MinimumError. Without it, Open counts as a dev build, which no
// minimum refuses.
func WithBinaryVersion(version string) Option {
	return func(s *Store) { s.binaryVersion = version }
}

// WithInstallationKind sets the installation kind of the command that opens
// the database. When the database already holds a kind, Open compares it with
// kind before it applies a migration, and returns an error that wraps
// installation.ErrKindMismatch without a change when the two differ. A
// database that holds no kind yet passes the check: the claim decides.
func WithInstallationKind(kind installation.Kind) Option {
	return func(s *Store) { s.kind = kind }
}

// WithoutMigrate makes Open check the schema and apply no migration. Open
// then refuses a database with a pending migration with
// schema.ErrMigrationsPending. A command that only writes rows uses it, so
// the server stays the one process that changes the schema.
func WithoutMigrate() Option {
	return func(s *Store) { s.noMigrate = true }
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// liveSchemaName returns the live schema of a search_path setting: the first
// schema, or public when the setting is empty. The control schema appends
// _control to it, so each live schema keeps its own control tables.
func liveSchemaName(searchPath string) string {
	for part := range strings.SplitSeq(searchPath, ",") {
		if name := strings.Trim(strings.TrimSpace(part), `"`); name != "" {
			return name
		}
	}

	return "public"
}

// Open connects to the database at url and migrates it.
func Open(ctx context.Context, url string, opts ...Option) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	live := liveSchemaName(cfg.ConnConfig.RuntimeParams["search_path"])
	ctl := live + "_control"
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, execErr := conn.Exec(ctx, "SET search_path TO "+pgIdent(live)+", "+pgIdent(ctl)); execErr != nil {
			return fmt.Errorf("set postgres search path: %w", execErr)
		}

		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	s := &Store{
		pool: pool, queries: db.New(pool), stagingMu: sync.Mutex{}, stagingSchema: "", keepAccessHook: nil,
		betweenProfileReads: atomic.Pointer[func()]{}, snapshotBegin: snapshotBeginQuery(live),
		beforeLockDelete: nil, stateLockWait: 0,
		binaryVersion: "", gate: nil, kind: "", noMigrate: false, logger: slog.New(slog.DiscardHandler),
	}
	s.gate = gatestore.New(gateRunner{s: s}, gatestore.WithKindReader(s))

	for _, opt := range opts {
		opt(s)
	}

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
	return s.inTx(ctx, func(q *db.Queries) error {
		return insertNamespaceRow(ctx, q, codec.ToNamespaceRow(ns))
	})
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

		return publishStackInventory(
			ctx,
			q,
			st.Ref,
			unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeCreated},
		)
	})
}

// GetStack implements stack.Store. One statement reads the stack and its
// tags, so the read needs no transaction.
func (s *Store) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	row, err := s.queries.GetStackWithTags(ctx, db.GetStackWithTagsParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Stack{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.Stack{}, fmt.Errorf("select stack: %w", readConflict(err))
	}

	// The query builds both arrays from the same rows in the same order, so
	// arrays of two lengths are a corrupt row.
	if len(row.TagNames) != len(row.TagValues) {
		return stack.Stack{}, fmt.Errorf("%w: stack %q has %d tag names and %d tag values",
			codec.ErrCorruptRow, row.ID, len(row.TagNames), len(row.TagValues))
	}

	tags := make(map[string]string, len(row.TagNames))
	for i, name := range row.TagNames {
		tags[name] = row.TagValues[i]
	}

	st := codec.FromStackRow(codec.StackRow{
		Namespace:   row.Namespace,
		Project:     row.Project,
		Name:        row.Name,
		ID:          row.ID,
		Version:     row.Version,
		CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	}, tags)
	st.NamespaceID = namespace.ID(row.NamespaceID)

	return st, nil
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

		return publishStackInventory(
			ctx,
			q,
			ref,
			unitWrite{unitID: id, atMs: codec.Millis(updatedAt), kind: inventory.ChangeChanged},
		)
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
		from := unitGate{ns: oldRef.Namespace, tool: inventoryTool, name: stackUnitName(oldRef)}

		if gateErr := moveUnitGate(ctx, q, from, stackUnitName(newRef)); gateErr != nil {
			return gateErr
		}

		row, err := q.GetStack(ctx, db.GetStackParams{
			Namespace: newRef.Namespace.String(),
			Project:   newRef.Project.String(),
			Name:      newRef.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("read renamed stack: %w", err)
		}

		return publishStackInventory(
			ctx,
			q,
			newRef,
			unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeRenamed},
		)
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
// changes nothing. It answers from guard, which the service counted outside
// the transaction. A guard naming another checkpoint returns
// ErrConcurrentModification so the service recounts once. It returns
// ErrNotFound if no live stack has ref, and ErrUpdateConflict while a
// mutating update of the stack runs. While a manual lock covers the stack,
// its collection or its namespace, the error also matches manuallock.ErrLocked.
// The stack row is locked before the running check, so a concurrent start
// or save either wins the lock and the delete conflicts, or waits and then
// finds no stack. A concurrent create of an update takes no stack lock. When
// it read the stack before the delete committed, its row stays with the
// deleted stack and never starts, because a start finds the stack by name.
func (s *Store) DeleteStack(
	ctx context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	guard stack.DeleteGuard,
	d inventory.Deletion,
) error {
	e := d.Entry

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, ref)
		if errors.Is(err, stack.ErrUpdateNotFound) {
			return stack.ErrNotFound
		}

		if err != nil {
			return err
		}

		if err := refuseStackDeleteUnderLock(ctx, q, ref); err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, locked.ID); err != nil {
			return err
		}

		if err := refuseResourcedStack(ctx, q, locked.ID, guard); err != nil {
			return err
		}

		e.ObjectID = locked.ID

		return softDeleteStack(ctx, q, ref, locked.ID, d)
	})
}

// ForceDeleteStack implements stack.Store. It soft-deletes the stack as
// DeleteStack does, even when the latest checkpoint manages resources. It
// returns ErrNotFound if no live stack has ref, and ErrUpdateConflict while a
// mutating update of the stack runs. While a manual lock covers the stack,
// its collection or its namespace, the error also matches manuallock.ErrLocked.
func (s *Store) ForceDeleteStack(
	ctx context.Context, ref stack.Ref, fence namespace.Fence, d inventory.Deletion,
) error {
	e := d.Entry

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, ref)
		if errors.Is(err, stack.ErrUpdateNotFound) {
			return stack.ErrNotFound
		}

		if err != nil {
			return err
		}

		if err := refuseStackDeleteUnderLock(ctx, q, ref); err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, locked.ID); err != nil {
			return err
		}

		e.ObjectID = locked.ID

		return softDeleteStack(ctx, q, ref, locked.ID, d)
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
// the stack with stackID manages resources. It answers from guard, which the
// service counted outside the transaction: a guard naming another checkpoint
// returns ErrConcurrentModification so the service recounts once. It reads
// only the latest row, never a blob. The caller holds the stack lock.
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

	return checkGuard(guard, row.UpdateID, row.Digest)
}

// checkNoCheckpoint answers a delete with no checkpoint row: a guard that
// counted one means a checkpoint landed after the count.
func checkNoCheckpoint(guard stack.DeleteGuard) error {
	if guard.HasCheckpoint {
		return stack.ErrConcurrentModification
	}

	return nil
}

// checkGuard answers a delete of a checkpoint row from the domain count in
// guard: a guard naming another checkpoint means a save landed after the
// count, so the caller recounts once.
func checkGuard(guard stack.DeleteGuard, updateID string, digest []byte) error {
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
func softDeleteStack(
	ctx context.Context,
	q *db.Queries,
	ref stack.Ref,
	stackID string,
	d inventory.Deletion,
) error {
	n, err := q.SoftDeleteStack(ctx, db.SoftDeleteStackParams{
		DeletedAtMs: pgtype.Int8{Int64: d.Entry.At.UnixMilli(), Valid: true},
		ID:          stackID,
	})
	if err != nil {
		return fmt.Errorf("delete stack: %w", err)
	}

	if n == 0 {
		return stack.ErrNotFound
	}

	return moveStackInventoryToDeleted(ctx, q, ref, stackID, d)
}

// ListStacks implements stack.Store. Tags come from one query for the whole
// namespace, not one query per stack.
func (s *Store) ListStacks(ctx context.Context, filter stack.Filter) ([]stack.Stack, error) {
	var out []stack.Stack

	// One snapshot, so the stacks and their tags come from one state.
	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		var err error

		out, err = listStacks(ctx, q, filter)

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// listStacks is ListStacks on the queries of one snapshot.
func listStacks(ctx context.Context, q *db.Queries, filter stack.Filter) ([]stack.Stack, error) {
	if filter.Project == "" {
		rows, err := q.ListStacksInNamespace(ctx, filter.Namespace.String())
		if err != nil {
			return nil, fmt.Errorf("select stacks: %w", err)
		}

		out := make([]stack.Stack, 0, len(rows))

		if len(rows) == 0 {
			return out, nil
		}

		tags, err := stackTagMap(ctx, q, filter.Namespace.String())
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

	rows, err := q.ListStacksInProject(ctx, db.ListStacksInProjectParams{
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

	tags, err := stackTagMap(ctx, q, filter.Namespace.String())
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

// writeLockClass is the first key of the advisory lock that orders the
// writes against a change of the installation state. Each write transaction
// takes the shared form, and an enter or a leave of maintenance mode takes the
// exclusive form, so an enter waits for the writes in flight. The second key
// is the schema, so two installations in one database do not wait for each
// other ("main" in ASCII).
const writeLockClass int32 = 0x6d61696e

// writeLockSQL takes the shared form of the write lock for this transaction.
const writeLockSQL = `SELECT pg_advisory_xact_lock_shared($1, hashtext(current_schema()))`

// readStateSQL reads the installation state. A later statement than the
// lock, so a READ COMMITTED snapshot cannot be older than the lock: a write
// that waited for an enter reads maintenance mode.
const readStateSQL = `SELECT state FROM installation WHERE id = 1`

// readStateAndFenceSQL reads the installation state and the failover version
// of the namespace in one statement, and holds the namespace row FOR SHARE so
// a promotion waits until the transaction ends.
const readStateAndFenceSQL = `SELECT
	(SELECT state FROM installation WHERE id = 1),
	(SELECT failover_version FROM namespaces WHERE name = $1 FOR SHARE)`

// inTx runs fn in a write transaction. It first takes the shared write lock
// and reads the installation state, so a write fails with
// maintenance.ErrInMaintenance on every instance as soon as one instance
// enters maintenance mode, without a wait for a cache. It fails closed: a
// read error fails the write with maintenance.ErrUnavailable. The writes of a
// restore and of the control tables use inControlTx instead.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the shared write lock; the inner one asks for it again,
// and it queues behind an enter that waits for the outer one: a deadlock.
// Take the state check once, in the outermost transaction.
func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.runTx(ctx, fn, func(tx pgx.Tx) error {
		_, err := readWritable(ctx, tx, readStateSQL, nil)

		return err
	})
}

// inControlTx runs fn in a transaction with no installation state check. The
// writes of a restore and of the control tables (the installation state,
// backups, restores, durable operations, schedules, heartbeats and the audit
// chain) must run while the installation is in maintenance mode. The control
// path is for the writes that keep the installation able to enter, leave and
// restore from maintenance mode, and that record what happens. Each caller is
// a write that maintenance mode must not refuse.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the shared write lock; the inner one asks for it again,
// and it queues behind an enter that waits for the outer one: a deadlock.
// Take the state check once, in the outermost transaction.
func (s *Store) inControlTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.runTx(ctx, fn, nil)
}

// runTx runs before, when set, and then fn in one transaction.
func (s *Store) runTx(ctx context.Context, fn func(q *db.Queries) error, before func(tx pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if before != nil {
			if err := before(tx); err != nil {
				return err
			}
		}

		return fn(s.queries.WithTx(tx))
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}

	return nil
}

// readWritable takes the shared write lock and then reads readSQL in one
// round trip, as two statements. Each statement has its own READ COMMITTED
// snapshot, so the read comes after the lock: a write that waited for an
// enter reads maintenance mode. readSQL returns the installation state in its
// first column. With fenceName set, it takes the namespace name as its only
// argument and returns the failover version in a second column, which
// readWritable returns (nil for a missing namespace row).
//
// It returns maintenance.ErrInMaintenance in maintenance mode, and
// maintenance.ErrUnavailable when the state cannot be read, the row is
// missing or the stored value is unknown: a write never passes on a read error.
func readWritable(ctx context.Context, tx pgx.Tx, readSQL string, fenceName *string) (*int64, error) {
	var batch pgx.Batch

	batch.Queue(writeLockSQL, writeLockClass)

	if fenceName != nil {
		batch.Queue(readSQL, *fenceName)
	} else {
		batch.Queue(readSQL)
	}

	var (
		state   *string
		version *int64
	)

	results := tx.SendBatch(ctx, &batch)

	_, lockErr := results.Exec()

	var scanErr error

	if lockErr == nil {
		row := results.QueryRow()

		if fenceName != nil {
			scanErr = row.Scan(&state, &version)
		} else {
			scanErr = row.Scan(&state)
		}
	}

	if err := errors.Join(lockErr, scanErr, results.Close()); err != nil {
		return nil, fmt.Errorf("read installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	if state == nil {
		return nil, fmt.Errorf(
			"read installation state postgres database: row is missing: %w",
			maintenance.ErrUnavailable,
		)
	}

	parsed, err := maintenance.ParseState(*state)
	if err != nil {
		return nil, fmt.Errorf("read installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	if parsed == maintenance.StateMaintenance {
		return nil, maintenance.ErrInMaintenance
	}

	return version, nil
}

// inSnapshotTx runs fn in a REPEATABLE READ, read-only transaction, so each
// read of fn sees one snapshot: a page and its total agree.
//
// The snapshot alone does not protect the read from a restore swap. The read
// names its tables through the search path, and PostgreSQL resolves a name
// with the latest catalog, not with the snapshot. So the transaction first
// takes the swap lock in ACCESS SHARE mode, in the same message as BEGIN and
// before any statement that takes the snapshot (a LOCK statement takes no
// snapshot). The swap takes the same lock in ACCESS EXCLUSIVE mode before it
// touches a live table (see lockSwap). So the read and the swap never overlap:
//
//   - A read that started first holds the lock, and the swap waits for the
//     read to end. The swap holds no live table while it waits, so the read
//     can lock its tables in any order without a deadlock.
//   - A read that starts during the swap waits for the swap to commit, and
//     then takes its snapshot. It sees the committed result of the swap, with
//     the changes that the swap itself made.
//
// A read with one statement needs neither the transaction nor the lock: run
// it on the pool. The SQLite adapter needs no such lock, because its swap
// and its reads hold the same mutex of the store.
func (s *Store) inSnapshotTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.inSnapshotConn(ctx, func(tx pgx.Tx) error {
		return fn(s.queries.WithTx(tx))
	})
}

// inSnapshotConn is inSnapshotTx for a read that also runs a hand-written
// statement: fn gets the transaction itself.
func (s *Store) inSnapshotConn(ctx context.Context, fn func(tx pgx.Tx) error) error {
	err := pgx.BeginTxFunc(ctx, s.pool, s.snapshotOptions(), fn)
	if err != nil {
		return fmt.Errorf("snapshot transaction: %w", readConflict(err))
	}

	return nil
}

// snapshotOptions returns the options of a snapshot read: a REPEATABLE READ,
// read-only transaction that takes the swap lock before its snapshot (see
// inSnapshotTx). Every REPEATABLE READ read of the store uses them.
func (s *Store) snapshotOptions() pgx.TxOptions {
	// BeginQuery replaces the isolation and access fields: snapshotBegin
	// starts the same REPEATABLE READ, read-only transaction.
	return pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly, DeferrableMode: pgx.NotDeferrable,
		BeginQuery: s.snapshotBegin, CommitQuery: "",
	}
}

// snapshotBeginQuery returns the statement that starts a snapshot read on the
// live schema live: BEGIN, then the swap lock in ACCESS SHARE mode. pgx sends
// a statement with no arguments in the simple protocol, so the two
// statements go in one message and cost no extra round trip. If the lock
// statement fails (for example, the context ends while it waits for a swap),
// pgx closes the connection and does not roll it back. The read fails closed.
func snapshotBeginQuery(live string) string {
	return "BEGIN READ ONLY ISOLATION LEVEL REPEATABLE READ; " +
		"LOCK TABLE " + swapLockTable(live) + " IN ACCESS SHARE MODE"
}

// SQLSTATE codes of a transaction that PostgreSQL ended to resolve a
// conflict. The same call can work when the caller tries again.
const (
	deadlockDetected     = "40P01"
	serializationFailure = "40001"
)

// readConflict returns namespace.ErrUnavailable, with no driver text, when err
// is a deadlock or a serialization failure, and err otherwise. Callers report
// namespace.ErrUnavailable as a temporary failure, so the client tries again.
func readConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == deadlockDetected || pgErr.Code == serializationFailure) {
		return fmt.Errorf("read ended by a conflict with another transaction: %w", namespace.ErrUnavailable)
	}

	return err
}

// inFencedTx runs fn in a write transaction that first takes the shared write
// lock, then reads the installation state and the failover version of the
// namespace in one statement and checks both: maintenance mode fails the
// write with maintenance.ErrInMaintenance, and a stale fence with
// namespace.ErrFenced. The namespace row is held FOR SHARE so a promotion
// waits until the transaction ends.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the shared write lock; the inner one asks for it again,
// and it queues behind an enter that waits for the outer one: a deadlock.
// Take the state check once, in the outermost transaction.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	name := fence.Namespace.String()

	return s.runTx(ctx, fn, func(tx pgx.Tx) error {
		version, err := readWritable(ctx, tx, readStateAndFenceSQL, &name)
		if err != nil {
			return err
		}

		if version == nil {
			return namespace.ErrNotFound
		}

		if *version != fence.FailoverVersion {
			return namespace.ErrFenced
		}

		return nil
	})
}

func (s *Store) migrate(ctx context.Context) error {
	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return err
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return migrateTx(
			ctx,
			tx,
			all,
			migrateRules{binaryVersion: s.binaryVersion, kind: s.kind, noMigrate: s.noMigrate},
		)
	})
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

// migrateRules holds what migrateTx checks the schema against: the version
// of the binary, and whether the caller applies no migration.
type migrateRules struct {
	binaryVersion string
	kind          installation.Kind
	noMigrate     bool
}

// migrateTx checks the schema and applies pending migrations while holding
// the advisory lock. A binary below the minimum binary version of the schema
// gets a *schema.MinimumError and applies nothing. With noMigrate, a pending
// migration gives schema.ErrMigrationsPending and applies nothing.
func migrateTx(ctx context.Context, tx pgx.Tx, all []migrate.Migration, rules migrateRules) error {
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

	recorded, err := recordedMigrations(ctx, tx)
	if err != nil {
		return err
	}

	if baselineErr := schema.CheckBaseline(recordedVersions(recorded)); baselineErr != nil {
		return fmt.Errorf("check schema versions: %w", baselineErr)
	}

	plan, err := migrate.Check(recorded, all, rules.binaryVersion)
	if err != nil {
		return fmt.Errorf("check schema: %w", err)
	}

	if err := checkKind(ctx, tx, rules.kind); err != nil {
		return err
	}

	if rules.noMigrate && len(plan.Pending) > 0 {
		return fmt.Errorf("%w: %d", schema.ErrMigrationsPending, len(plan.Pending))
	}

	for _, m := range plan.Pending {
		if err := apply(ctx, tx, m); err != nil {
			return err
		}
	}

	return nil
}

// recordedMigrations returns the rows of schema_migrations, ordered by
// version. The caller holds the migration lock in the same transaction.
func recordedMigrations(ctx context.Context, tx pgx.Tx) ([]schema.AppliedMigration, error) {
	query, err := appliedQuery(ctx, tx)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer rows.Close()

	var out []schema.AppliedMigration

	for rows.Next() {
		var row schema.AppliedMigration
		if err := rows.Scan(&row.Version, &row.Name, &row.MinBinaryVersion); err != nil {
			return nil, fmt.Errorf("read schema versions: %w", err)
		}

		out = append(out, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	return out, nil
}

// recordedVersions returns the versions of the recorded migrations,
// ordered as recorded, for the baseline check.
func recordedVersions(recorded []schema.AppliedMigration) []int64 {
	versions := make([]int64, 0, len(recorded))

	for _, r := range recorded {
		versions = append(versions, r.Version)
	}

	return versions
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

	return recordMigration(ctx, tx, m)
}

// hasMinimumColumn reports whether schema_migrations in the current schema
// has the min_binary_version column. A migration adds it. Before that
// migration, the schema records no minimum.
func hasMinimumColumn(ctx context.Context, tx pgx.Tx) (bool, error) {
	var exists bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
			AND column_name = 'min_binary_version'
	)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check schema_migrations columns: %w", err)
	}

	return exists, nil
}

// appliedQuery returns the query that reads the rows of schema_migrations
// ordered by version: with the minimum binary version when the column
// exists, and with the empty value before.
func appliedQuery(ctx context.Context, tx pgx.Tx) (string, error) {
	exists, err := hasMinimumColumn(ctx, tx)
	if err != nil {
		return "", err
	}

	if exists {
		return `SELECT version, name, min_binary_version FROM schema_migrations ORDER BY version`, nil
	}

	return `SELECT version, name, '' FROM schema_migrations ORDER BY version`, nil
}

// recordMigration records m in schema_migrations in tx, after its SQL ran.
// It records the minimum binary version of m when the column exists. A
// migration that sets a minimum before the column exists fails.
func recordMigration(ctx context.Context, tx pgx.Tx, m migrate.Migration) error {
	hasMinimum, err := hasMinimumColumn(ctx, tx)
	if err != nil {
		return err
	}

	if !hasMinimum {
		if m.MinBinaryVersion != "" {
			return fmt.Errorf("record migration %s: %w: schema_migrations has no min_binary_version column",
				m.Name, schema.ErrBadMinimum)
		}

		const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`
		if _, err := tx.Exec(ctx, record, m.Version, m.Name); err != nil {
			return fmt.Errorf("record migration %s: %w", m.Name, err)
		}

		return nil
	}

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES ($1, $2, $3)`
	if _, err := tx.Exec(ctx, record, m.Version, m.Name, m.MinBinaryVersion); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}

	return nil
}
