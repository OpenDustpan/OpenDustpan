// Package sqlite is the SQLite store adapter: the zero-dependency default for
// single-node installs. It uses a pure-Go SQLite build, so binaries need no cgo.
//
// SQLite allows one writer at a time. Transactions start as IMMEDIATE so a
// fenced write takes the write lock before it reads the failover version.
//
// Inspect opens a database read-only and reports its schema state. It is the
// one place `opendustpan upgrade check` reads.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"time"

	// Registers the "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// fileScheme is the URL scheme of a SQLite database file DSN.
const fileScheme = "file"

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements namespace.Store, stack.Store and the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) on SQLite.
type Store struct {
	conn    *sql.DB
	queries *db.Queries
	path    string
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// Open opens (creating if needed) the database file at path and migrates it.
//
// Open leaves running backup rows alone: crash recovery inspects them before
// the server marks them failed with FailRunningBackups at start, and offline
// commands open stores that no server owns.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	conn, err := openDatabase(ctx, abs)
	if err != nil {
		return nil, err
	}

	s := &Store{conn: conn, queries: db.New(conn), path: abs}
	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(err, conn.Close())
	}

	return s, nil
}

// openDatabase opens the database file at abs with the store pragmas.
func openDatabase(ctx context.Context, abs string) (*sql.DB, error) {
	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(wal)")
	query.Add("_pragma", "synchronous(normal)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := conn.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("connect to sqlite database: %w", err), conn.Close())
	}

	return conn, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}

	return nil
}

// Ping implements namespace.Store. It maps any driver failure to
// ErrUnavailable without driver text.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.conn.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", namespace.ErrUnavailable)
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
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
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
		n, err := q.CreateStack(ctx, db.CreateStackParams{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		})
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
	if errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
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

	if _, err := q.GetStack(ctx, params); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return stack.ErrNotFound
		}

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

	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check new stack name: %w", err)
	}

	return nil
}

// DeleteStack implements stack.Store. It removes the stack with its updates,
// checkpoints, events and wrapped data key in one fenced transaction: child
// rows go through foreign keys, the data key through an explicit delete. It
// removes the inventory entry in the same transaction. When the latest
// checkpoint manages resources, it returns ErrHasResources and removes
// nothing: each checkpoint stores its managed resource count, so the check
// reads only the count, or the one latest deployment when the checkpoint
// predates the count. It returns ErrNotFound if the stack does not
// exist, and ErrUpdateConflict while a mutating update of the stack runs.
// The transaction holds the write lock from the start, so a concurrent start
// or finish of an update either runs before the checks or waits until the
// delete commits.
func (s *Store) DeleteStack(ctx context.Context, ref stack.Ref, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, ref)
		if err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, stackID); err != nil {
			return err
		}

		if err := refuseResourcedStack(ctx, q, stackID); err != nil {
			return err
		}

		return removeStack(ctx, q, ref, stackID)
	})
}

// ForceDeleteStack implements stack.Store. It removes the stack with its
// updates, checkpoints, events and wrapped data key in one fenced
// transaction, even when the latest checkpoint manages resources. It returns
// ErrNotFound if the stack does not exist, and ErrUpdateConflict while a
// mutating update of the stack runs.
func (s *Store) ForceDeleteStack(ctx context.Context, ref stack.Ref, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, ref)
		if err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, stackID); err != nil {
			return err
		}

		return removeStack(ctx, q, ref, stackID)
	})
}

// refuseRunningStack returns ErrUpdateConflict while a mutating update of the
// stack with stackID runs.
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
// the stack with stackID manages resources. It reads the stored count; a
// NULL count predates the count column, so it loads that one deployment and
// counts it instead.
func refuseResourcedStack(ctx context.Context, q *db.Queries, stackID string) error {
	count, err := q.LatestResourceCount(ctx, stackID)
	if err != nil {
		return fmt.Errorf("count managed resources: %w", err)
	}

	if count.Valid {
		if count.Int64 > 0 {
			return stack.ErrHasResources
		}

		return nil
	}

	deployment, err := q.LatestDeployment(ctx, stackID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read latest deployment: %w", err)
	}

	if stack.CountManagedResources(deployment) > 0 {
		return stack.ErrHasResources
	}

	return nil
}

// removeStack deletes the stack with stackID with its wrapped data key and
// inventory entry: child rows go through foreign keys. It returns
// ErrNotFound when the delete removes no row.
func removeStack(ctx context.Context, q *db.Queries, ref stack.Ref, stackID string) error {
	if err := q.DeleteSecretKeysOfStack(ctx, stackID); err != nil {
		return fmt.Errorf("delete secret key: %w", err)
	}

	n, err := q.DeleteStack(ctx, db.DeleteStackParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if err != nil {
		return fmt.Errorf("delete stack: %w", err)
	}

	if n == 0 {
		return stack.ErrNotFound
	}

	return removeStackInventory(ctx, q, ref)
}

// ListStacks implements stack.Store. Tags come from one query for the whole
// namespace, not one query per stack.
func (s *Store) ListStacks(ctx context.Context, filter stack.Filter) ([]stack.Stack, error) {
	if filter.Project == "" {
		rows, err := s.queries.ListStacksInNamespace(ctx, filter.Namespace.String())
		if err != nil {
			return nil, fmt.Errorf("select stacks: %w", err)
		}

		return s.hydrateStacks(ctx, rows)
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

// hydrateStacks attaches the tag rows of every stack row, keyed by stack ID.
// The tags come from one query for the whole namespace.
func (s *Store) hydrateStacks(
	ctx context.Context, rows []db.ListStacksInNamespaceRow,
) ([]stack.Stack, error) {
	out := make([]stack.Stack, 0, len(rows))

	if len(rows) == 0 {
		return out, nil
	}

	tags, err := stackTagMap(ctx, s.queries, rows[0].Namespace)
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
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// inFencedTx runs fn in a transaction that first checks fence. The write lock
// is held from the start, so the failover version cannot change before commit.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		version, err := q.GetFailoverVersion(ctx, fence.Namespace.String())
		if errors.Is(err, sql.ErrNoRows) {
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

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, execErr := s.conn.ExecContext(ctx, createTable); execErr != nil {
		return fmt.Errorf("create schema_migrations: %w", execErr)
	}

	recorded, recordedErr := s.recordedVersions(ctx)
	if recordedErr != nil {
		return recordedErr
	}

	if unknown := migrate.UnknownRecorded(recorded, all); len(unknown) > 0 {
		return schema.NewSchemaNewerError(recorded, embeddedVersions(all))
	}

	for _, m := range all {
		if applyErr := s.apply(ctx, m); applyErr != nil {
			return applyErr
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
func (s *Store) recordedVersions(ctx context.Context) ([]int64, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var out []int64

	for rows.Next() {
		var version int64

		if scanErr := rows.Scan(&version); scanErr != nil {
			return nil, fmt.Errorf("read schema versions: %w", scanErr)
		}

		out = append(out, version)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema versions: %w", iterErr)
	}

	return out, nil
}

func (s *Store) apply(ctx context.Context, m migrate.Migration) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.Name, err)
	}

	var applied int64

	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&applied)
	if err != nil {
		return errors.Join(fmt.Errorf("check migration %s: %w", m.Name, err), rollback(tx))
	}

	if applied > 0 {
		return rollback(tx)
	}

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return errors.Join(fmt.Errorf("apply migration %s: %w", m.Name, err), rollback(tx))
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name); err != nil {
		return errors.Join(fmt.Errorf("record migration %s: %w", m.Name, err), rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Name, err)
	}

	return nil
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("roll back transaction: %w", err)
	}

	return nil
}
