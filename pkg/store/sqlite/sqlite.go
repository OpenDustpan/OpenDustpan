// Package sqlite is the SQLite store adapter: the zero-dependency default for
// single-node installs. It uses a pure-Go SQLite build, so binaries need no cgo.
//
// SQLite allows one writer at a time. Transactions start as IMMEDIATE so a
// fenced write takes the write lock before it reads the failover version.
//
// AppliedMigrations reports the rows of schema_migrations ordered by
// version. It is the one place `opendustpan upgrade check` reads, through
// the installation API: only the server opens the database.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	// Registers the "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/gatestore"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// fileScheme is the URL scheme of a SQLite database file DSN.
const fileScheme = "file"

// walAutoCheckpointFrames holds the write-ahead log size that triggers an
// automatic checkpoint, in frames. The value is the SQLite default: the store
// sets it explicitly so a later default cannot change the bound silently.
// The automatic checkpoint is passive, so it never restarts the log while a
// reader holds it; the guard below forces a truncate checkpoint when the log
// grows past walGuardThresholdBytes.
const walAutoCheckpointFrames = 1000

// walJournalSizeLimitBytes caps the size the database keeps the write-ahead
// log file at after a restart, in bytes. The SQLite default keeps the peak
// size forever; 64 MiB bounds the file without extra checkpoints.
const walJournalSizeLimitBytes = 67108864

// walGuardSlowAttempt marks a truncate attempt as slow: an attempt that
// takes at least this long waited for readers (up to the short busy timeout),
// so it may stall writers. A fast busy attempt only met the checkpoint lock
// of an automatic checkpoint and stalled no writer. The split errs in the
// safe direction under CPU load. A fast busy attempt that takes longer counts
// as slow and stops the tick early.
const walGuardSlowAttempt = 10 * time.Millisecond

// walGuardMaxSlowAttempts caps the slow busy attempts of one guard tick: the
// guard tries at most 2 slow attempts, then waits for the next tick. Two slow
// attempts with the short busy timeout below bound the writer stall of one
// tick to about 105 ms in total (2 times 50 ms plus a 5 ms retry delay), plus
// the time of a successful checkpoint copy.
const walGuardMaxSlowAttempts = 2

// walGuardMaxAttempts caps all truncate attempts of one guard tick in total:
// fast busy attempts may repeat up to 20 attempts per tick, with 5 ms between
// them. Fast attempts stall no writer, so only the 2 slow attempts above
// bound the worst writer stall of one tick.
const walGuardMaxAttempts = 20

// walGuardRetryDelay waits between two truncate attempts of one guard tick,
// so a busy checkpoint lock can clear without stalling writers.
const walGuardRetryDelay = 5 * time.Millisecond

// walGuardBusyTimeoutMs bounds how long a truncate checkpoint waits for
// readers per attempt, in milliseconds. A truncate checkpoint blocks new
// writers while it waits, and a backup snapshot is a long reader; the short
// timeout limits a writer stall to about 50 ms for each attempt.
const walGuardBusyTimeoutMs = 50

// walGuardDefaultTimeoutMs restores the store busy timeout on a guard
// connection before the guard returns it to the pool, in milliseconds.
const walGuardDefaultTimeoutMs = 10000

// walGuardInterval ticks the write-ahead log guard. Tests replace it through
// the test hook; production keeps 200 ms.
//
//nolint:gochecknoglobals // The guard timing stays tunable in tests without a new setting; the hook restores it.
var walGuardInterval = 200 * time.Millisecond

// walGuardThresholdBytes starts a truncate checkpoint when the write-ahead
// log file reaches it, in bytes. It holds the same value as
// walJournalSizeLimitBytes: the guard bounds the peak, the pragma bounds the
// file after a restart. Tests replace it through the test hook.
//
//nolint:gochecknoglobals // The guard threshold stays tunable in tests without a new setting; the hook restores it.
var walGuardThresholdBytes = int64(walJournalSizeLimitBytes)

// walGuardMaxBackoff caps the wait before the next guard tick after a tick
// that fails with a slow busy attempt, in production. The guard doubles the
// wait on each such tick up to this cap and resets it to the base interval
// after a successful truncate or when the log drops below the threshold.
const walGuardMaxBackoff = 5 * time.Second

// walGuardBackoffCap holds the backoff cap the guard enforces: it holds
// walGuardMaxBackoff in production; tests replace it through the test hook,
// so backoff tests stay fast.
//
//nolint:gochecknoglobals // The guard backoff stays tunable in tests without a new setting; the hook restores it.
var walGuardBackoffCap = walGuardMaxBackoff

// walTickInfo describes one past-threshold guard tick for the test hook: the
// database path of the store, so a test reads only the ticks of its own store
// while other stores tick in parallel, when the truncate finished, how long
// the tick took from its stat to the finish, whether it succeeded, whether the
// tick saw a slow busy attempt, and the slow and total attempt counts, and the
// wait the guard slept before the tick, so tests check the backoff without
// measuring wall-clock gaps.
type walTickInfo struct {
	path         string
	at           time.Time
	waited       time.Duration
	elapsed      time.Duration
	succeeded    bool
	slowBusy     bool
	slowAttempts int
	attempts     int
}

// walGuardTicksMu guards walGuardTicks and walGuardCollect below: the guard
// appends past-threshold ticks, and the test hook swaps, clears and reads
// them. Holds stay brief and never nest with other locks.
//
//nolint:gochecknoglobals // The guard and the hook share one tick buffer; every hold stays brief.
var walGuardTicksMu sync.Mutex

// walGuardTicks holds one entry per past-threshold guard tick while a test
// collects them. Production never collects, so the buffer stays empty outside
// tests.
//
//nolint:gochecknoglobals // See walGuardTicksMu: the buffer lives beside its lock.
var walGuardTicks []walTickInfo

// walGuardCollect reports whether the test hook collects guard ticks. Only
// the hook writes it, under walGuardTicksMu; the guard reads it on the same
// lock before it appends.
//
//nolint:gochecknoglobals // See walGuardTicksMu: the flag lives beside its lock.
var walGuardCollect = false

// recordWalTick appends info to the test hook buffer when a test collects
// guard ticks. Production never collects, so this stays a brief locked check.
func recordWalTick(info walTickInfo) {
	walGuardTicksMu.Lock()
	defer walGuardTicksMu.Unlock()

	if !walGuardCollect {
		return
	}

	const maxTicks = 100000

	if len(walGuardTicks) < maxTicks {
		walGuardTicks = append(walGuardTicks, info)
	}
}

// walGuardWarnPeriod is the time a truncate checkpoint must keep failing on
// every tick before the guard writes one warning: 30 s of continuous busy
// results means the log cannot restart and keeps growing.
const walGuardWarnPeriod = 30 * time.Second

// walGuardWarnAfter holds the failing time before the guard warns, in
// nanoseconds as a duration. It holds walGuardWarnPeriod in production;
// tests replace it through the test hook.
//
//nolint:gochecknoglobals // The guard warn period stays tunable in tests without a new setting; the hook restores it.
var walGuardWarnAfter = walGuardWarnPeriod

// walGuardMu guards the interval, the threshold, the warn period and the
// backoff cap above: the test hook holds it exclusive for each swap, and the
// guard holds it shared for each read. Every hold stays brief and never nests
// with the store lock, so the guard never parks behind a test.
//
//nolint:gochecknoglobals // The hook and the guard share one lock; every hold stays brief.
var walGuardMu sync.RWMutex

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements namespace.Store, stack.Store and the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) on SQLite.
type Store struct {
	conn    *sql.DB
	queries *db.Queries
	// gate serves the ports of gate.Store over this store.
	gate *gatestore.Store
	path string
	// mu guards conn and queries across the online restore swap: readers
	// hold it shared, and the swap holds it exclusive while it renames the
	// database file. Lock order: stagingMu, then mu, then enterGate. No path
	// takes them in another order.
	mu sync.RWMutex
	// sweepMu is the sweep lock. One server process uses a SQLite file
	// (processGuard), so a lock in this store is a lock for every sweep.
	sweepMu sync.Mutex
	// enterGate orders an enter of maintenance mode against the checked
	// writes of this store. A checked write holds it shared for its
	// transaction (holdWrite). An enter holds it exclusive, so the checked
	// writes in flight end and the new ones wait. The enter then competes for
	// the database write lock only with the writes that do not use the gate:
	// the control path (durable claims, extends and heartbeats, the audit
	// chain, revocation journaling) and the WAL checkpoint of the guard. Those
	// stay ungated, because SetState itself is a control write and would
	// wait for its own hold. Without the gate the enter polls the write lock
	// against writers that never pause, and it can wait out the whole busy
	// timeout.
	enterGate *writeGate
	// enterWait bounds the wait of an enter for enterGate: maxEnterWait.
	// Tests make it short.
	enterWait time.Duration
	// stagingMu guards stagingPath: one staged restore at a time.
	stagingMu   sync.Mutex
	stagingPath string
	// keepAccessHook, when set, runs after the live access read and before
	// the swap of a keep-access restore, while the write lock is held.
	// Tests set it to prove a concurrent write stays blocked across that
	// window. Nil in production.
	keepAccessHook func(ctx context.Context)
	// beforeLockDelete, when set, runs in the removal of a manual lock between
	// the read of the lock and the delete, inside the transaction. Tests change
	// the lock there to reach the changed-lock check. Nil in production.
	beforeLockDelete func(ctx context.Context, q *db.Queries) error
	// betweenPageReads, when set, runs in QueryStacks and QueryUnits after the
	// statement of the page and before the statement of its tags or labels,
	// inside the read transaction. Tests write there to prove that both
	// statements read one snapshot. Nil in production.
	betweenPageReads func(ctx context.Context)
	// guardCancel stops the write-ahead log guard; guardDone closes when the
	// guard ends. Close waits for the guard before it closes the connection,
	// so no checkpoint runs on a closed handle.
	guardCancel context.CancelFunc
	guardDone   chan struct{}
	// logger records guard warnings. It defaults to a discard handler; pass
	// WithLogger to record the busy warning.
	logger *slog.Logger
	// lastTruncateSuccess holds the last time a guard checkpoint succeeded or
	// the log stayed below the threshold. Only the guard goroutine reads and
	// writes it, so it needs no lock.
	lastTruncateSuccess time.Time
	// busyWarned reports that the guard already warned for the current busy
	// period. A successful truncate clears it, so the next failing period
	// warns once more. Only the guard goroutine reads and writes it.
	busyWarned bool
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
	// processGuard holds the lock on the guard file next to the database,
	// so a second store cannot open the same file. Close releases it.
	processGuard *processGuard

	// stepTxBound is the longest time that a step transaction of a durable
	// operation stays open before it rolls back.
	stepTxBound time.Duration
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// ErrInUse reports that another store holds the database file open. One
// SQLite file serves one server process only.
var ErrInUse = errors.New("another process holds the SQLite database file")

// Option tunes Open. Options keep backward compatibility: callers that pass
// none get the defaults.
type Option func(*Store)

// WithLogger sets the logger that records the write-ahead log guard warning
// and the warning for a stored preference that fails its check. The default is
// a discard logger, so the store stays silent unless the caller passes a
// logger.
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

// WithoutMigrate makes Open check the schema and apply no migration. Open
// then refuses a database with a pending migration with
// schema.ErrMigrationsPending. A command that only writes rows uses it, so
// the server stays the one process that changes the schema.
func WithoutMigrate() Option {
	return func(s *Store) { s.noMigrate = true }
}

// WithInstallationKind sets the installation kind of the command that opens
// the database. When the database already holds a kind, Open compares it with
// kind before it applies a migration, and returns an error that wraps
// installation.ErrKindMismatch without a change when the two differ. A
// database that holds no kind yet passes the check: the claim decides.
func WithInstallationKind(kind installation.Kind) Option {
	return func(s *Store) { s.kind = kind }
}

// Open opens (creating if needed) the database file at path and migrates it.
// It locks the file first: a second Open of the same file, in this process
// or in another one, fails with ErrInUse until Close of the first store. The
// guard resolves symbolic links before it names the guard file, so the same
// file opened through another spelling shares the one guard. A hard link to
// the database file is not detected.
//
// Open leaves running backup rows alone: their operations continue on the
// next claim after a crash, and offline commands open stores that no server
// owns.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	resolved, err := resolveDatabasePath(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	var probe Store
	for _, opt := range opts {
		opt(&probe)
	}

	if probe.noMigrate {
		if _, statErr := os.Stat(resolved); statErr != nil {
			return nil, fmt.Errorf("open database file %s: %w", resolved, statErr)
		}
	}

	guard, err := acquireGuard(resolved)
	if err != nil {
		return nil, err
	}

	var extra url.Values
	if probe.noMigrate {
		extra = url.Values{dsnModeKey: {"rw"}}
	}

	conn, err := openDatabase(ctx, resolved, extra)
	if err != nil {
		return nil, errors.Join(err, guard.release())
	}

	s := newStore(conn, resolved, guard)

	for _, opt := range opts {
		opt(s)
	}
	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(err, conn.Close(), guard.release())
	}

	guardCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.guardCancel = cancel
	s.guardDone = make(chan struct{})

	go s.guardLoop(guardCtx)

	return s, nil
}

// newStore returns the store on conn, before the options and the migration.
func newStore(conn *sql.DB, path string, guard *processGuard) *Store {
	s := &Store{
		conn:                conn,
		queries:             db.New(conn),
		path:                path,
		mu:                  sync.RWMutex{},
		sweepMu:             sync.Mutex{},
		enterGate:           newWriteGate(),
		enterWait:           maxEnterWait,
		stagingMu:           sync.Mutex{},
		stagingPath:         "",
		guardCancel:         nil,
		guardDone:           nil,
		logger:              slog.New(slog.DiscardHandler),
		lastTruncateSuccess: time.Now(),
		busyWarned:          false,
		keepAccessHook:      nil,
		beforeLockDelete:    nil,
		betweenPageReads:    nil,
		binaryVersion:       "",
		kind:                "",
		noMigrate:           false,
		processGuard:        guard,
		stepTxBound:         DefaultStepTransactionBound,
		gate:                nil,
	}
	s.gate = gatestore.New(gateRunner{s: s}, gatestore.WithKindReader(s))

	return s
}

// maxSymlinkChain caps the links resolveDatabasePath follows at one path:
// a chain with more links fails with an error.
const maxSymlinkChain = 40

// errTooManyLinks reports a symbolic link chain with more links than
// maxSymlinkChain allows.
var errTooManyLinks = errors.New("too many links")

// resolveDatabasePath returns the canonical path of the database file at abs,
// with symbolic links resolved, so two spellings of one file share one guard
// file. When the file exists, it returns EvalSymlinks of abs. When the file
// does not exist, it follows the chain of links at abs with Lstat and
// Readlink, resolving each relative target against the directory of its link,
// up to 40 links, then resolves the directory of the final target and joins
// the base name of the final target, so a database created through a dangling
// link still maps to the same guard. A plain missing file with no link
// resolves its directory and joins the base name. It returns an error when
// the chain holds more than 40 links. It never names the database file itself
// for the guard: closing our descriptor would release the POSIX locks SQLite
// holds on that file.
func resolveDatabasePath(abs string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}

	final, err := followLinkChain(abs)
	if err != nil {
		return "", err
	}

	if dir, err := filepath.EvalSymlinks(filepath.Dir(final)); err == nil {
		return filepath.Join(dir, filepath.Base(final)), nil
	}

	return final, nil
}

// followLinkChain walks the chain of symbolic links at abs and returns the
// final target. A relative target resolves against the directory of its link.
// A path that is no link, or that does not exist, ends the chain. A chain
// with more than maxSymlinkChain links returns errTooManyLinks.
func followLinkChain(abs string) (string, error) {
	current := abs

	for followed := 0; ; followed++ {
		info, err := os.Lstat(current)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("stat database path %s: %w", current, err)
			}

			return current, nil
		}

		if info.Mode()&os.ModeSymlink == 0 {
			return current, nil
		}

		if followed >= maxSymlinkChain {
			return "", fmt.Errorf("%w at %s", errTooManyLinks, abs)
		}

		target, err := os.Readlink(current)
		if err != nil {
			return "", fmt.Errorf("read database link %s: %w", current, err)
		}

		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}

		current = target
	}
}

// openDatabase opens the database file at abs with the store pragmas and
// the extra URL parameters. The parameter mode=rw opens a file that exists
// and creates none.
func openDatabase(ctx context.Context, abs string, extra url.Values) (*sql.DB, error) {
	query := url.Values{}
	maps.Copy(query, extra)

	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(wal)")
	query.Add("_pragma", "synchronous(normal)")
	query.Add("_pragma", "foreign_keys(on)")
	query.Add("_pragma", fmt.Sprintf("wal_autocheckpoint(%d)", walAutoCheckpointFrames))
	query.Add("_pragma", fmt.Sprintf("journal_size_limit(%d)", walJournalSizeLimitBytes))

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := openGuarded(dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := conn.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("connect to sqlite database: %w", err), conn.Close())
	}

	return conn, nil
}

// Close closes the database. It stops the write-ahead log guard and waits
// for it first, so no checkpoint runs on a closed handle. It removes a
// staged restore file first, so no scratch file survives the handle. It
// releases the lock on the database file last, so another store can open
// the file only after this one closed it.
func (s *Store) Close() error {
	if s.guardCancel != nil {
		s.guardCancel()
	}

	if s.guardDone != nil {
		<-s.guardDone
	}

	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stagingPath != "" {
		removeSnapshot(s.stagingPath)
		s.stagingPath = ""
	}

	if err := s.conn.Close(); err != nil {
		return errors.Join(fmt.Errorf("close sqlite database: %w", err), s.processGuard.release())
	}

	return s.processGuard.release()
}

// Ping implements namespace.Store. It maps any driver failure to
// ErrUnavailable without driver text.
func (s *Store) Ping(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.conn.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", namespace.ErrUnavailable)
	}

	return nil
}

// CreateNamespace implements namespace.Store.
func (s *Store) CreateNamespace(ctx context.Context, ns namespace.Namespace) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		return insertNamespaceRow(ctx, q, codec.ToNamespaceRow(ns))
	})
}

// GetNamespace implements namespace.Store.
func (s *Store) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetNamespace(ctx, name.String())
	if errors.Is(err, sql.ErrNoRows) {
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
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return createStackRows(ctx, q, st)
	})
}

// CreateStackPlaced implements stack.PlacedStore. The stack, its tags, its
// inventory entry, its membership of the collection and the audit entry of the
// placement share one fenced transaction.
func (s *Store) CreateStackPlaced(
	ctx context.Context, st stack.Stack, p stack.Placement, fence namespace.Fence,
) error {
	// Defense in depth: the service and the handler check the placement too.
	// This check stays so that a new caller of the store cannot make a stack
	// that no collection holds.
	if !p.Complete(st.Ref.Namespace) {
		return collection.ErrNoCollection
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	e := p.Entry

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if err := createStackRows(ctx, q, st); err != nil {
			return err
		}

		return setUnitCollection(ctx, q, placementMove(st, p), e)
	})
}

// placementMove returns the move that puts the new stack st in the collection
// of p: the stack has no collection yet.
func placementMove(st stack.Stack, p stack.Placement) collection.Move {
	return collection.Move{
		Unit: collection.UnitRef{
			Namespace: st.Ref.Namespace, Tool: "pulumi", Name: st.Ref.Project.String() + "/" + st.Ref.Stack.String(),
		},
		From: "", To: p.Collection, ToID: p.CollectionID, EndsLock: false,
	}
}

// createStackRows inserts the stack, its tags and its inventory entry in the
// transaction of q.
func createStackRows(ctx context.Context, q *db.Queries, st stack.Stack) error {
	row := codec.ToStackRow(st)

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

	return publishStackInventory(
		ctx,
		q,
		st.Ref,
		unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeCreated},
	)
}

// GetStack implements stack.Store.
func (s *Store) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// stackNameChangeKey is the audit change key of a stack name.
const stackNameChangeKey = "name"

// RenameStack implements stack.Store. It renames the stack without moving its
// history, checkpoint or events: child rows key on the stable stack ID. The
// inventory keeps one entry for the same stack ID, under the new name, in the
// same transaction. It appends the audit entry e in that transaction, with the
// stable stack ID and the old and the new name. It returns ErrNotFound if no
// live stack has oldRef, and ErrUpdateConflict while a mutating update of the
// stack runs.
func (s *Store) RenameStack(
	ctx context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if newName == oldRef.Stack {
		return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
			return confirmStackPresent(ctx, q, oldRef)
		})
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stackID, err := refuseRename(ctx, q, oldRef, newName)
		if err != nil {
			return err
		}

		oldName := oldRef.Stack.String()
		e.ObjectID = stackID
		e.Changes = []audit.Change{{
			Key: stackNameChangeKey, Old: &oldName, New: newName.String(), Layer: "", TakesEffect: "",
		}}

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

		return publishStackInventory(
			ctx,
			q,
			newRef,
			unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeRenamed},
		)
	})
}

// refuseRename returns ErrNotFound when no live stack has oldRef,
// ErrUpdateConflict while a mutating update of the stack runs, and
// ErrAlreadyExists when newName names a stack of the same project. A running
// update addresses the stack by its name. A rename under it would leave the
// update with a name that no stack has: its checkpoint saves would fail and
// the stack would stay locked by the update. On success it returns the stable
// ID of the stack.
func refuseRename(ctx context.Context, q *db.Queries, oldRef stack.Ref, newName stack.Name) (string, error) {
	stackID, err := stackIDFor(ctx, q, oldRef)
	if err != nil {
		return "", err
	}

	if runErr := refuseRunningStack(ctx, q, stackID); runErr != nil {
		return "", runErr
	}

	if takenErr := rejectTakenName(ctx, q, oldRef, newName); takenErr != nil {
		return "", takenErr
	}

	return stackID, nil
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
// The transaction holds the write lock from the start, so a concurrent start
// or finish of an update either runs before the checks or waits until the
// delete commits.
func (s *Store) DeleteStack(
	ctx context.Context,
	ref stack.Ref,
	fence namespace.Fence,
	guard stack.DeleteGuard,
	d inventory.Deletion,
) error {
	e := d.Entry

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, ref)
		if err != nil {
			return err
		}

		if err := refuseStackDeleteUnderLock(ctx, q, ref); err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, stackID); err != nil {
			return err
		}

		if err := refuseResourcedStack(ctx, q, stackID, guard); err != nil {
			return err
		}

		e.ObjectID = stackID

		return softDeleteStack(ctx, q, ref, stackID, d)
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

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, ref)
		if err != nil {
			return err
		}

		if err := refuseStackDeleteUnderLock(ctx, q, ref); err != nil {
			return err
		}

		if err := refuseRunningStack(ctx, q, stackID); err != nil {
			return err
		}

		e.ObjectID = stackID

		return softDeleteStack(ctx, q, ref, stackID, d)
	})
}

// refuseRunningStack returns the error of stack.RunningUpdateConflict, which
// matches ErrUpdateConflict and names the running update when its ID is
// valid, while a mutating update of the stack with stackID runs.
func refuseRunningStack(ctx context.Context, q *db.Queries, stackID string) error {
	active, err := q.RunningMutatingUpdate(ctx, stackID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read the running update: %w", err)
	}

	return stack.RunningUpdateConflict(active)
}

// refuseResourcedStack returns ErrHasResources when the latest checkpoint of
// the stack with stackID manages resources. It answers from guard, which the
// service counted outside the transaction: a guard naming another checkpoint
// returns ErrConcurrentModification so the service recounts once. It reads
// only the latest row, never a blob.
func refuseResourcedStack(
	ctx context.Context, q *db.Queries, stackID string, guard stack.DeleteGuard,
) error {
	row, err := q.GetLatestCheckpoint(ctx, stackID)
	if errors.Is(err, sql.ErrNoRows) {
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
		DeletedAtMs: sql.NullInt64{Int64: d.Entry.At.UnixMilli(), Valid: true},
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
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// inTx runs fn in a write transaction. It first reads the installation state
// in the transaction, so a write fails with maintenance.ErrInMaintenance as
// soon as the state is maintenance. The transaction is IMMEDIATE, so the
// state cannot change before it commits, and an enter waits for a write in
// flight. It fails closed: a read error fails the write with
// maintenance.ErrUnavailable. The writes of a restore and of the control
// tables use inControlTx instead.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the shared gate; the inner one asks for it again, and it
// queues behind an enter that waits for the outer one. That wait ends only
// when the context ends, and then the inner call returns the context error.
// Take the state check once, in the outermost transaction.
func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	release, err := s.holdWrite(ctx)
	if err != nil {
		return err
	}
	defer release()

	return s.runTx(ctx, fn, s.checkWritable)
}

// holdWrite takes the shared hold of the enter gate for a checked write and
// returns the function that releases it. Every path that runs checkWritable
// calls it before it begins its transaction, so that an enter of maintenance
// mode can order itself against the write. It returns the error of ctx when
// ctx ends while the write waits behind an enter, and then holds nothing.
func (s *Store) holdWrite(ctx context.Context) (func(), error) {
	return s.enterGate.rlock(ctx)
}

// inControlTx runs fn in a transaction with no installation state check. The
// writes of a restore and of the control tables (the installation state,
// backups, restores, durable operations, schedules, heartbeats and the audit
// chain) must run while the installation is in maintenance mode. The control
// path is for the writes that keep the installation able to enter, leave and
// restore from maintenance mode, and that record what happens. Each caller is
// a write that maintenance mode must not refuse. It does not check the minimum
// binary version either, so only a write that an old binary needs to stop
// cleanly uses it, or a write that records what happened. A write that changes
// domain data or runs work uses inMinimumTx.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the write connection; the inner one asks for it again and
// waits behind an enter that waits for the outer one. That gate wait ends
// when the context of the inner call ends, and the inner call returns the
// context error. SQLite lock contention can fail the inner transaction after
// the busy timeout. Take the state check once, in the outermost transaction.
func (s *Store) inControlTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.runTx(ctx, fn, nil)
}

// inMinimumTx runs fn in a control transaction that refuses a binary below the
// minimum binary version of the schema. It reads the minimum in the same
// transaction as fn, before fn runs, and it reads no installation state, so
// maintenance mode does not refuse it. A control write that changes domain
// data or runs work uses it, so a worker of an old binary, which a rolling
// deploy leaves running, cannot claim or run an operation. A control write
// that an old binary needs to stop cleanly uses inControlTx.
//
// The same rule about nested transactions as for inControlTx applies.
func (s *Store) inMinimumTx(ctx context.Context, fn func(q *db.Queries) error) error {
	return s.runTx(ctx, fn, s.checkMinimum)
}

// checkMinimum reads the minimum binary versions of the schema in the
// transaction of q and returns a *schema.MinimumError when the binary of the
// store is below the highest one. It reads the installation state in the same
// statement and ignores its value, but it fails closed with
// maintenance.ErrUnavailable when the read fails.
func (s *Store) checkMinimum(ctx context.Context, q *db.Queries) error {
	row, err := q.GetWriteState(ctx)
	if err != nil {
		return fmt.Errorf("read schema minimum sqlite database: %w", maintenance.ErrUnavailable)
	}

	return checkWriteMinimum(row.Minimums, s.binaryVersion)
}

// runTx runs before, when set, and then fn in one transaction.
func (s *Store) runTx(
	ctx context.Context, fn func(q *db.Queries) error, before func(context.Context, *db.Queries) error,
) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	q := s.queries.WithTx(tx)

	if before != nil {
		if beforeErr := before(ctx, q); beforeErr != nil {
			return errors.Join(beforeErr, rollback(tx))
		}
	}

	if err := fn(q); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// checkWritable reads the installation state and the minimum binary versions
// of the schema in the transaction of q, in one statement. It returns
// maintenance.ErrInMaintenance in maintenance mode, a *schema.MinimumError
// when the binary of the store is below the highest minimum, and
// maintenance.ErrUnavailable when the state cannot be read, the row is
// missing or the stored value is unknown: a write never passes on a read error.
func (s *Store) checkWritable(ctx context.Context, q *db.Queries) error {
	row, err := q.GetWriteState(ctx)
	if storeerr.Is(err) {
		// A database that does not answer is temporary: the client tries again.
		return fmt.Errorf("read installation state sqlite database: %w", err)
	}

	if err != nil {
		return fmt.Errorf("read installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	state, err := maintenance.ParseState(row.State)
	if err != nil {
		return fmt.Errorf("read installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	if state == maintenance.StateMaintenance {
		return maintenance.ErrInMaintenance
	}

	return checkWriteMinimum(row.Minimums, s.binaryVersion)
}

// checkWriteMinimum fails a write whose binary is below the highest minimum
// binary version that the schema records. It returns the *schema.MinimumError
// as it is, so the API names both versions. A minimum that does not parse
// fails closed with maintenance.ErrUnavailable.
func checkWriteMinimum(minimums, binaryVersion string) error {
	err := schema.CheckMinimums(strings.Split(minimums, ","), binaryVersion)
	if err == nil {
		return nil
	}

	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		return err
	}

	return fmt.Errorf("check schema minimum sqlite database: %w", maintenance.ErrUnavailable)
}

// inReadTx runs fn in a read-only transaction. It begins as DEFERRED, not
// IMMEDIATE, so it takes no write lock, and every read of fn sees one
// snapshot of the database.
func (s *Store) inReadTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit read transaction: %w", err)
	}

	return nil
}

// inFencedTx runs fn in a write transaction that first checks the
// installation state (inTx) and then fence. The write lock is held from the
// start, so the failover version cannot change before commit.
//
// Never start a checked transaction (inTx, inFencedTx or any helper built on
// them) from inside fn, and never call a Store method that does. The outer
// transaction holds the shared gate; the inner one asks for it again, and it
// queues behind an enter that waits for the outer one. That wait ends only
// when the context ends, and then the inner call returns the context error.
// Take the state check once, in the outermost transaction.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.fencedTx(ctx, fence, fn, s.inTx)
}

// inControlFencedTx runs fn as inFencedTx does, with no installation state
// check. The audit chain markers of a chain change use it: the change holds
// maintenance mode while it runs.
func (s *Store) inControlFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.fencedTx(ctx, fence, fn, s.inControlTx)
}

// inMinimumFencedTx runs fn as inControlFencedTx does, and refuses a binary
// below the minimum binary version of the schema, as inMinimumTx does.
func (s *Store) inMinimumFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.fencedTx(ctx, fence, fn, s.inMinimumTx)
}

// fencedTx checks fence in the transaction that begin starts, and then runs fn.
func (s *Store) fencedTx(
	ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error,
	begin func(context.Context, func(q *db.Queries) error) error,
) error {
	return begin(ctx, func(q *db.Queries) error {
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

	return s.migrateAll(ctx, all)
}

// migrateAll checks the schema against the migrations all and applies the
// pending ones.
func (s *Store) migrateAll(ctx context.Context, all []migrate.Migration) error {
	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if !s.noMigrate {
		if _, execErr := s.conn.ExecContext(ctx, createTable); execErr != nil {
			return fmt.Errorf("create schema_migrations: %w", execErr)
		}
	}

	recorded, recordedErr := s.recordedMigrations(ctx)
	if recordedErr != nil {
		return recordedErr
	}

	if baselineErr := schema.CheckBaseline(recordedVersions(recorded)); baselineErr != nil {
		return fmt.Errorf("check schema versions: %w", baselineErr)
	}

	plan, checkErr := migrate.Check(recorded, all, s.binaryVersion)

	if checkErr != nil {
		return fmt.Errorf("check schema: %w", checkErr)
	}

	if verifyErr := verifyRecorded(ctx, s.conn, all); verifyErr != nil {
		return fmt.Errorf("check schema: %w", verifyErr)
	}

	if kindErr := s.checkKind(ctx); kindErr != nil {
		return kindErr
	}

	if s.noMigrate && len(plan.Pending) > 0 {
		return fmt.Errorf("%w: %d", schema.ErrMigrationsPending, len(plan.Pending))
	}

	return s.applyPending(ctx, plan.Pending)
}

// applyPending applies the pending migrations in order. It first refuses a
// migration that sets a minimum binary version while an instance row is below
// it, so the check runs before any migration changes the schema.
func (s *Store) applyPending(ctx context.Context, pending []migrate.Migration) error {
	if err := s.checkInstances(ctx, pending); err != nil {
		return err
	}

	for _, m := range pending {
		if err := s.apply(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

// checkInstances refuses a pending migration that sets a minimum binary
// version while an instance version row is below it, and names the rows.
// Before the migration that adds the table, no instance wrote a row, so the
// check passes. Nothing renews a row, so the row of an instance that crashed
// blocks the migration until an administrator deletes it.
func (s *Store) checkInstances(ctx context.Context, pending []migrate.Migration) error {
	if !slices.ContainsFunc(pending, func(m migrate.Migration) bool { return m.MinBinaryVersion != "" }) {
		return nil
	}

	var tables int64

	err := s.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'instance_versions'`,
	).Scan(&tables)
	if err != nil {
		return fmt.Errorf("check instance versions table: %w", err)
	}

	if tables == 0 {
		return nil
	}

	rows, err := s.conn.QueryContext(ctx, `SELECT id, binary_version FROM instance_versions ORDER BY started_at_ms, id`)
	if err != nil {
		return fmt.Errorf("read instance versions: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var instances []schema.InstanceRow

	for rows.Next() {
		var inst schema.InstanceRow

		if scanErr := rows.Scan(&inst.ID, &inst.BinaryVersion); scanErr != nil {
			return fmt.Errorf("read instance versions: %w", scanErr)
		}

		instances = append(instances, inst)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read instance versions: %w", rowsErr)
	}

	return migrate.CheckInstances(pending, instances)
}

// sqlQueryer runs one query on a connection or in a transaction.
type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dsnModeKey is the key of the open mode of a database file in its URL.
const dsnModeKey = "mode"

// versionSumColumn is the column of schema_migrations that holds the SHA-256
// of each migration.
const versionSumColumn = "sha256"

// hasVersionColumn reports whether schema_migrations has the column. The
// baseline adds the min_binary_version and the sha256 columns, so a version
// table from before it lacks them.
func hasVersionColumn(ctx context.Context, q sqlQueryer, column string) (bool, error) {
	var count int64

	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('schema_migrations') WHERE name = ?`, column,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check schema_migrations columns: %w", err)
	}

	return count > 0, nil
}

// appliedQuery returns the query that reads the rows of schema_migrations
// ordered by version: the version, the name, the minimum binary version and
// the SHA-256. A column that the table lacks reads as the empty value, so the
// schema check refuses a recorded migration with no SHA-256.
func appliedQuery(ctx context.Context, q sqlQueryer) (string, error) {
	hasMinimum, err := hasVersionColumn(ctx, q, "min_binary_version")
	if err != nil {
		return "", err
	}

	hasSum, err := hasVersionColumn(ctx, q, versionSumColumn)
	if err != nil {
		return "", err
	}

	minimum, digest := "''", "''"
	if hasMinimum {
		minimum = "min_binary_version"
	}

	if hasSum {
		digest = versionSumColumn
	}

	return "SELECT version, name, " + minimum + ", " + digest + " FROM schema_migrations ORDER BY version", nil
}

// verifyRecorded refuses the database when it records a version that the
// binary embeds under another name or another SHA-256. It reads the rows
// itself, so a fault in the read of the migration check cannot let a
// changed schema through.
//
// Defense in depth: migrate.Check compares the same rows, and the baseline
// check refuses a database from before the baseline; this check stays so
// that a new caller of Check, or a change to it, cannot skip the comparison.
func verifyRecorded(ctx context.Context, q sqlQueryer, embedded []migrate.Migration) error {
	hasSum, err := hasVersionColumn(ctx, q, versionSumColumn)
	if err != nil {
		return err
	}

	for _, m := range embedded {
		var count int64

		countErr := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).
			Scan(&count)
		if countErr != nil {
			return fmt.Errorf("read migration %s: %w", m.Name, countErr)
		}

		if count == 0 {
			continue
		}

		if !hasSum {
			return fmt.Errorf("%w: the version table records no SHA-256 for migration %s",
				schema.ErrSchemaDiverged, m.Name)
		}

		var name, digest string

		readErr := q.QueryRowContext(ctx, `SELECT name, sha256 FROM schema_migrations WHERE version = ?`, m.Version).
			Scan(&name, &digest)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", m.Name, readErr)
		}

		// Defense in depth: migrate.Check refuses a value that is not a
		// SHA-256 too; this check stays so that an embedded migration with an
		// empty sum cannot let an empty recorded sum through.
		if !schema.ValidSHA256(digest) {
			return fmt.Errorf("%w: migration %s records the SHA-256 %q, which is not 64 lowercase hex digits",
				schema.ErrSchemaDiverged, m.Name, digest)
		}

		if name != m.Name || digest != m.SHA256 {
			return fmt.Errorf("%w: the database records migration %s with SHA-256 %q, and this binary has %s "+
				"with SHA-256 %s: another build of the schema made the database",
				schema.ErrSchemaDiverged, name, digest, m.Name, m.SHA256)
		}
	}

	return nil
}

// recordedMigrations returns the rows of schema_migrations, ordered by
// version.
func (s *Store) recordedMigrations(ctx context.Context) ([]schema.AppliedMigration, error) {
	query, err := appliedQuery(ctx, s.conn)
	if err != nil {
		return nil, err
	}

	rows, err := s.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var out []schema.AppliedMigration

	for rows.Next() {
		var row schema.AppliedMigration
		if err := rows.Scan(&row.Version, &row.Name, &row.MinBinaryVersion, &row.SHA256); err != nil {
			return nil, fmt.Errorf("read schema versions: %w", err)
		}

		out = append(out, row)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema versions: %w", iterErr)
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

	if err := recordMigration(ctx, tx, m); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Name, err)
	}

	return nil
}

// recordMigration records m in schema_migrations in tx, after its SQL ran:
// its version, its name, its minimum binary version and the SHA-256 of its
// SQL. The baseline adds the min_binary_version and the sha256 columns, so a
// version table without them is one from before the baseline. The checks
// refuse such a database before any migration runs, and the record fails too.
func recordMigration(ctx context.Context, tx *sql.Tx, m migrate.Migration) error {
	for _, column := range []string{"min_binary_version", versionSumColumn} {
		exists, err := hasVersionColumn(ctx, tx, column)
		if err != nil {
			return err
		}

		if !exists {
			return fmt.Errorf("record migration %s: %w: schema_migrations has no %s column",
				m.Name, schema.ErrSchemaDiverged, column)
		}
	}

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version, sha256) VALUES (?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name, m.MinBinaryVersion, m.SHA256); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}

	return nil
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("roll back transaction: %w", err)
	}

	return nil
}

// guardLoop runs the write-ahead log guard until ctx ends. Every tick it
// stats the log file and forces a truncate checkpoint past the threshold.
// Concurrent requests keep short read transactions open all the time, so the
// passive automatic checkpoint never restarts the log and it only grows;
// the forced checkpoint reclaims it. One tick tries at most 2 slow attempts,
// so it stalls writers for about 105 ms at most (2 times 50 ms plus a 5 ms
// retry delay), plus the time of a successful checkpoint copy, then gives up
// until the next tick. A tick that fails with a slow busy attempt (a long
// reader, such as a backup) doubles the wait before the next tick up to the
// backoff cap; a tick that fails with only fast busy attempts keeps the base
// interval. A successful truncate or a log below the threshold resets the
// wait to the base interval. The loop never panics and never stops early: a
// failed tick only logs at debug level. The interval and the cap are read
// again for every tick, so the test hook takes effect without reopening the
// store.
func (s *Store) guardLoop(ctx context.Context) {
	defer close(s.guardDone)

	walGuardMu.RLock()
	wait := walGuardInterval
	walGuardMu.RUnlock()

	for {
		walGuardMu.RLock()
		base := walGuardInterval
		backoffCap := walGuardBackoffCap
		walGuardMu.RUnlock()

		if wait < base {
			wait = base
		}

		if backoffCap > 0 && wait > backoffCap {
			wait = backoffCap
		}

		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}

		wait = nextGuardWait(wait, s.guardTick(ctx, wait))
	}
}

// nextGuardWait returns the wait before the next guard tick from the last
// outcome: a successful truncate or a log below the threshold resets the wait
// to the base interval, a tick that fails with a slow busy attempt doubles it
// up to the backoff cap, and a tick that fails with only fast busy attempts
// keeps the base interval. The base and the cap are read again on every call,
// so the test hook takes effect without reopening the store.
func nextGuardWait(wait time.Duration, outcome guardOutcome) time.Duration {
	walGuardMu.RLock()
	base := walGuardInterval
	backoffCap := walGuardBackoffCap
	walGuardMu.RUnlock()

	switch {
	case outcome.belowThreshold || outcome.succeeded:
		return base
	case outcome.slowBusy:
		doubled := wait + wait
		if backoffCap > 0 && doubled > backoffCap {
			return backoffCap
		}

		return doubled
	default:
		return base
	}
}

// guardOutcome reports one guard tick to the loop: whether the log stayed
// below the threshold, whether a past-threshold truncate succeeded, and
// whether a failed tick saw a slow busy attempt.
type guardOutcome struct {
	belowThreshold bool
	succeeded      bool
	slowBusy       bool
}

// guardTick stats the write-ahead log file and checkpoints it past the
// threshold. A missing file means no log yet; any other stat failure only
// logs at debug level. A log below the threshold counts as healthy: it
// refreshes the last success (which resets both the warn period and the
// backoff), so a later busy period warns only after its own full warn period
// of failures. A successful truncate also refreshes the last success and
// resets the warn state, so the next failing period warns once more. waited
// is the wait the guard slept before this tick; only the test hook reads it.
func (s *Store) guardTick(ctx context.Context, waited time.Duration) guardOutcome {
	info, err := os.Stat(s.path + "-wal")
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.DebugContext(ctx, "stat write-ahead log", slog.String("error", err.Error()))
		}

		return guardOutcome{belowThreshold: false, succeeded: false, slowBusy: false}
	}

	walGuardMu.RLock()
	threshold := walGuardThresholdBytes
	warnAfter := walGuardWarnAfter
	walGuardMu.RUnlock()

	now := time.Now()
	if info.Size() < threshold {
		s.lastTruncateSuccess = now
		s.busyWarned = false

		return guardOutcome{belowThreshold: true, succeeded: false, slowBusy: false}
	}

	result := s.guardCheckpoint(ctx)

	recordWalTick(walTickInfo{
		path:         s.path,
		at:           time.Now(),
		waited:       waited,
		elapsed:      time.Since(now),
		succeeded:    result.succeeded,
		slowBusy:     result.slowBusy,
		slowAttempts: result.slowAttempts,
		attempts:     result.attempts,
	})

	if result.succeeded {
		s.lastTruncateSuccess = now
		s.busyWarned = false

		return guardOutcome{belowThreshold: false, succeeded: true, slowBusy: false}
	}

	if !s.busyWarned && now.Sub(s.lastTruncateSuccess) >= warnAfter {
		s.busyWarned = true
		s.logger.WarnContext(
			ctx,
			"write-ahead log checkpoint busy",
			slog.Int64("wal_size", info.Size()),
			slog.Duration("since_last_success", now.Sub(s.lastTruncateSuccess)),
		)
	}

	return guardOutcome{belowThreshold: false, succeeded: false, slowBusy: result.slowBusy}
}

// truncateResult reports one runTruncate call: whether the checkpoint
// succeeded, whether the tick saw a slow busy attempt, and the slow and total
// attempt counts.
type truncateResult struct {
	succeeded    bool
	slowBusy     bool
	slowAttempts int
	attempts     int
}

// guardCheckpoint forces a truncate checkpoint on one dedicated connection
// and reports the truncate result. The guard holds the store lock shared, so
// the online restore swap waits out the checkpoint instead of closing the
// handle under it. The checkpoint runs with a short busy timeout, because a
// truncate checkpoint blocks new writers while it waits for readers; the
// guard restores the store timeout before it returns the connection to the
// pool. The work context stays detached from shutdown, so Close never aborts
// a checkpoint midway. A connection failure reports no slow attempt: only a
// busy checkpoint that waited for readers backs the loop off.
func (s *Store) guardCheckpoint(ctx context.Context) truncateResult {
	work := context.WithoutCancel(ctx)

	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, err := s.conn.Conn(work)
	if err != nil {
		s.logger.DebugContext(work, "take guard connection", slog.String("error", err.Error()))

		return truncateResult{succeeded: false, slowBusy: false, slowAttempts: 0, attempts: 0}
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			s.logger.DebugContext(work, "return guard connection", slog.String("error", closeErr.Error()))
		}
	}()

	if _, err := conn.ExecContext(work, fmt.Sprintf("PRAGMA busy_timeout(%d)", walGuardBusyTimeoutMs)); err != nil {
		s.logger.DebugContext(work, "set guard busy timeout", slog.String("error", err.Error()))

		return truncateResult{succeeded: false, slowBusy: false, slowAttempts: 0, attempts: 0}
	}

	defer func() {
		_, restoreErr := conn.ExecContext(work, fmt.Sprintf("PRAGMA busy_timeout(%d)", walGuardDefaultTimeoutMs))
		if restoreErr != nil {
			s.logger.DebugContext(work, "restore guard busy timeout", slog.String("error", restoreErr.Error()))
		}
	}()

	return s.runTruncate(ctx, work, conn)
}

// runTruncate runs PRAGMA wal_checkpoint(TRUNCATE) until it reports no busy
// reader and reports the truncate result. Each attempt is timed: an attempt
// that takes 10 ms or more is slow and may stall writers, while a fast busy
// attempt only met the checkpoint lock of an automatic checkpoint and stalled
// no writer. The guard tries at most 2 slow busy attempts per tick, while
// fast busy attempts may repeat up to 20 attempts per tick in total, with
// 5 ms between them, then gives up until the next tick: it never blocks
// writers past the short busy timeout of each attempt and never returns an
// error to any caller. The worst writer stall of one tick stays at about
// 105 ms (2 times 50 ms plus a 5 ms retry delay), plus the time of a
// successful checkpoint copy. The log keeps growing until a later tick
// succeeds, and journal_size_limit then truncates the file at the next
// write-ahead log restart. When the guard's TRUNCATE stays busy after all
// attempts, the WAL keeps growing until a later tick succeeds. Every failure
// only logs at debug level.
func (s *Store) runTruncate(shutdown, work context.Context, conn *sql.Conn) truncateResult {
	var result truncateResult

	for attempt := 1; ; attempt++ {
		var busy, logFrames, checkpointed int64

		start := time.Now()

		err := conn.QueryRowContext(work, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed)
		elapsed := time.Since(start)

		if err != nil {
			s.logger.DebugContext(work, "checkpoint write-ahead log", slog.String("error", err.Error()))

			result.attempts = attempt

			return result
		}

		if busy == 0 {
			result.succeeded = true
			result.attempts = attempt

			return result
		}

		result.attempts = attempt

		if elapsed >= walGuardSlowAttempt {
			result.slowAttempts++
			result.slowBusy = true
		}

		if result.slowAttempts >= walGuardMaxSlowAttempts || attempt >= walGuardMaxAttempts {
			s.logger.DebugContext(
				work,
				"write-ahead log checkpoint stays busy",
				slog.Int64("attempts", int64(attempt)),
				slog.Int64("slow_attempts", int64(result.slowAttempts)),
			)

			return result
		}

		timer := time.NewTimer(walGuardRetryDelay)

		select {
		case <-shutdown.Done():
			timer.Stop()

			return result
		case <-timer.C:
		}
	}
}
