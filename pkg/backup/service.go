package backup

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
)

// FormatVersion is the manifest format number of the portable format. Check
// and restore refuse a manifest with another number. It changes only with a
// new format version.
const FormatVersion = PortableManifestFormat

// S3ObjectLimit is the largest object an S3-compatible target holds (ADR 31):
// 5 GiB. A Config whose target is S3-compatible sets MaxSQLBytes to it, so a
// SQL part above the limit fails with a clear error before it uploads.
const S3ObjectLimit int64 = 5 << 30

// Sentinel errors returned by the backup service and Check.
var (
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
	// ErrAlreadyRunning reports a second backup started while one runs. Only
	// one backup runs at a time.
	ErrAlreadyRunning = errors.New("backup already running")
	// ErrFailed reports that Run ended a backup as failed. The text after it
	// is the error text of the registry row.
	ErrFailed = errors.New("backup failed")
	// ErrTooLarge reports a SQL part over the target object limit.
	ErrTooLarge = errors.New("backup SQL part too large")
)

// Manifest is the last object a backup writes:
// backups/<id>/<attempt>/manifest.json. It
// names every object the backup holds, so Check and restore can verify the
// target before they use it. SQLFiles holds one descriptor for each table file
// of the SQL part, in the order of Tables (ADR 42, decision 4). SQLSize is the
// sum of their sizes, and SQLSHA256 is the SHA-256 of their JSON, both as the
// pins of a restore compare them. Blobs holds one entry per blob the SQL data
// names, ordered by key. SQLSHA256 and each blob MD5 are lowercase hex. The
// JSON keys are stable: Check and restore parse them, so a new field needs a
// new optional key, never a rename.
type Manifest struct {
	CreatedAt            time.Time      `json:"createdAt"`
	ID                   ID             `json:"id"`
	Dialect              Dialect        `json:"dialect"`
	PrimaryInstallations []string       `json:"primaryInstallations"`
	WrapperIDs           []string       `json:"wrapperIds"`
	Blobs                []ManifestBlob `json:"blobs"`
	Version              string         `json:"version"`
	SQLSHA256            string         `json:"sqlSha256"`
	Format               int            `json:"format"`
	SchemaVersion        int            `json:"schemaVersion"`
	SQLSize              int64          `json:"sqlSize"`
	// Attempt names the directory backups/<id>/<attempt>/ that holds this
	// manifest and the table files. It is the claim fencing token of the run
	// that wrote them. A reader takes the attempt from live.json and refuses a
	// manifest that names another attempt.
	Attempt string `json:"attempt"`
	// RowCounts holds the row count of each table in Tables. Restore compares
	// the loaded counts with it after the SQL load.
	RowCounts map[string]int64 `json:"rowCounts"`
	// SQLFormat names the format of the SQL part: PortableFormat (ADR 42,
	// decision 4).
	SQLFormat string `json:"sqlFormat"`
	// SQLFiles holds one descriptor for each table file of the SQL part.
	SQLFiles []PortableFile `json:"sqlFiles"`
	// InstallationKind is the kind of the installation that made the backup,
	// read from its own database. The key is required. The manifest is not
	// authenticated, so the value is a claim: a restore on a production
	// installation also decides from the staged data.
	InstallationKind installation.Kind `json:"installationKind"`
	// Unverified holds the keys of the blobs that rows of the SQL part name
	// and that the backup does not hold, because the rows have no size and MD5
	// of the stored bytes. The key is optional: a backup with no such blob
	// omits it.
	Unverified []string `json:"unverified,omitempty"`
	// Damaged holds the live blobs that a backup before a restore found
	// missing or damaged, ordered by key, with the reference of the live row
	// of each. The backup holds no copy of them. A restore of this backup
	// leaves those keys as they are, so it brings back the state from before
	// the restore, and its answer names them. The key is optional: a backup
	// with no such blob omits it. Only a backup before a restore records
	// damage; every other backup fails at the first damaged blob.
	Damaged []DamagedBlob `json:"damaged,omitempty"`
	// RegistryFiles holds the registry file section of each namespace, keyed
	// by the stable namespace ID: one entry per unique release file with its
	// SHA-256 content key, its size, and the release and version that name
	// it. A restore checks each named file against its content key before it
	// makes any of them visible. The key is written by every backup of this
	// change: null means a backup from before registry file copy, and the
	// restore then warns that the files are absent.
	RegistryFiles map[string]RegistryFileSection `json:"registryFiles"`
}

// UnmarshalJSON reads a manifest. A value of installationKind that is not a
// JSON string fails with ErrManifestKindInvalid, so a wrong JSON type gets the
// same error as an unknown value. The value is not checked here: readManifest
// checks it after the format number.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	type plain Manifest

	wire := struct {
		*plain

		Kind json.RawMessage `json:"installationKind"`
	}{plain: (*plain)(m), Kind: nil}

	if err := json.Unmarshal(data, &wire); err != nil {
		return err //nolint:wrapcheck // The caller maps every decode failure to ErrCorrupt.
	}

	m.InstallationKind = ""

	if len(wire.Kind) == 0 || string(wire.Kind) == "null" {
		return nil
	}

	var kind string
	if err := json.Unmarshal(wire.Kind, &kind); err != nil {
		return fmt.Errorf("installationKind is not a string: %w", ErrManifestKindInvalid)
	}

	m.InstallationKind = installation.Kind(kind)

	return nil
}

// ManifestBlob names one copied blob with its size and MD5.
type ManifestBlob struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
	// Sealed reports that the stored bytes are sealed with the data key of
	// the workspace scope (ADR 41 decision 10). Size and MD5 then describe
	// the sealed bytes. The key is optional, so older manifests without it
	// read unsealed.
	Sealed bool `json:"sealed,omitempty"`
}

// Config holds the dependencies of a Service.
type Config struct {
	Exporter PortableExporter
	Registry Registry
	// Kinds reads the installation kind of the database that the exporter
	// reads. The manifest records it. A caller cannot pass a kind in.
	Kinds KindReader
	// Blobs is the live content store the backup copies from.
	Blobs blob.Store
	// StateOpener opens sealed OpenTofu state blobs for the copy check: the
	// backup checks the plain bytes against the SQL data and stores the
	// sealed bytes. Nil copies every blob as before, for installations
	// without sealed state.
	StateOpener StateOpener
	// Target is the backup target that holds blobs/<key>,
	// backups/<id>/<attempt>/sql/<table>.jsonl.gz and
	// backups/<id>/<attempt>/manifest.json.
	Target blob.Store
	// TargetName names the target for operators, for example
	// "dir:/var/backups". The registry row keeps it.
	TargetName string
	Now        func() time.Time
	// Version is the server version the manifest records.
	Version string
	// MaxSQLBytes bounds the SQL part; zero means no limit. Targets with an
	// object limit, such as S3-compatible storage, set S3ObjectLimit.
	MaxSQLBytes int64
	// Operations keeps the durable operation of each backup. The registry
	// starts the operation in the transaction of the backup row, so both
	// must name one database.
	Operations durable.Store
	// Owner names this instance in the claims on backup operations, for
	// status only. An empty Owner means "backup".
	Owner string
	// Lease is the time from a claim or a renewal to its expiry. Zero means
	// the default of the durable runner. The runner renews the claim while a
	// step runs, so a slow backup keeps its claim.
	Lease time.Duration
	// Logger receives the messages of the runner. Nil discards them.
	Logger *slog.Logger
}

// Service runs backups of a whole installation into a blob.Store target.
type Service struct {
	exporter   PortableExporter
	kinds      KindReader
	registry   Registry
	source     blob.Store
	opener     StateOpener
	target     blob.Store
	targetName string
	now        func() time.Time
	version    string
	maxSQL     int64
	// maxUnverified is the largest number of unverified keys that a backup
	// this service writes may hold. Zero means MaxUnverifiedKeys; a test sets
	// a smaller bound, so it does not build that many keys.
	maxUnverified int
	runner        *durable.Runner
	operations    durable.Store
	lease         time.Duration
	logger        *slog.Logger
}

// StateOpener opens sealed OpenTofu state blobs of the live store for the
// backup copy check. The backup checks the plain bytes against the size and
// MD5 the SQL data names and stores the sealed bytes, so plain state never
// reaches the backup target.
type StateOpener interface {
	// OpenStateBlob returns the plain content of the sealed blob under key.
	// The caller closes it. Plain bytes that do not match the size and the
	// MD5 of the row of the blob wrap ErrLiveBlobDamaged, at the open or at a
	// read. Sealed bytes that do not open, for example after a key or a key
	// provider fails, must not wrap it: that cause is not clear, so the
	// backup stops. A missing blob wraps blob.ErrNotFound. Any other error
	// stops the backup. No error names a key, a scope or content.
	OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error)
}

// NewService returns a Service. Exporter, Registry, Blobs and Target are
// required. A nil Now means time.Now; an empty Version means "dev".
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Exporter == nil:
		return nil, fmt.Errorf("%w: exporter", ErrMissingDependency)
	case cfg.Kinds == nil:
		return nil, fmt.Errorf("%w: installation kind reader", ErrMissingDependency)
	case cfg.Registry == nil:
		return nil, fmt.Errorf("%w: registry", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	case cfg.Target == nil:
		return nil, fmt.Errorf("%w: target", ErrMissingDependency)
	case cfg.Operations == nil:
		return nil, fmt.Errorf("%w: durable operations", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	version := cfg.Version
	if version == "" {
		version = "dev"
	}

	s := &Service{
		exporter:      cfg.Exporter,
		kinds:         cfg.Kinds,
		registry:      cfg.Registry,
		source:        cfg.Blobs,
		opener:        cfg.StateOpener,
		target:        cfg.Target,
		targetName:    cfg.TargetName,
		now:           now,
		version:       version,
		maxSQL:        cfg.MaxSQLBytes,
		maxUnverified: 0,
		runner:        nil,
		operations:    cfg.Operations,
		lease:         cmp.Or(cfg.Lease, durable.DefaultLease),
		logger:        cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler)),
	}

	runner, err := s.newRunner(cfg)
	if err != nil {
		return nil, err
	}

	s.runner = runner

	return s, nil
}

// Run starts backup id and blocks until it ends. It captures the SQL data in
// one read transaction, writes it to
// backups/<id>/<attempt>/sql/<table>.jsonl.gz,
// copies each blob the SQL data names to blobs/<blob key>, and writes
// backups/<id>/<attempt>/manifest.json last. A blob the target already holds is not
// copied again. It returns ErrAlreadyRunning while another backup runs. A
// failed backup marks its registry row failed with the error text and leaves
// no manifest. It returns the finished registry row, or ErrFailed with the
// error text of the row.
//
// Run claims the operation of the backup itself, as a worker does, and
// polls the row while another instance holds it. A caller that needs no
// answer starts a backup with Start instead.
func (s *Service) Run(ctx context.Context, id ID) (Record, error) {
	if err := s.Start(ctx, id); err != nil {
		return Record{}, err
	}

	return s.Await(ctx, id)
}

// Await blocks until backup id ends, and returns the finished row, or
// ErrFailed with the error text of the row. It claims the operation of the
// backup itself when no instance holds it, as Run does, and polls the row
// while another instance holds it. It returns ErrNotFound when no row has
// the ID.
func (s *Service) Await(ctx context.Context, id ID) (Record, error) {
	for {
		row, ended, err := s.readEnd(ctx, id)
		if ended || err != nil {
			return row, err
		}

		// A caller that stopped claims nothing: a claim would only end in a
		// release.
		if err := ctx.Err(); err != nil {
			return Record{}, fmt.Errorf("wait for backup %s: %w", id, err)
		}

		runErr := s.runner.RunOnce(ctx)
		if ctx.Err() != nil {
			return Record{}, fmt.Errorf("wait for backup %s: %w", id, ctx.Err())
		}

		if runErr != nil {
			// No work: another instance holds the operation. Read the row
			// again after a pause.
			sleep(ctx, runPoll)
		}
	}
}

// runPoll is the pause of Run between two reads of the row while another
// instance holds the operation.
const runPoll = 100 * time.Millisecond

// sleep waits d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// Start stores id as a running row and starts the durable operation that
// runs the backup, in one transaction, and returns at once, so an HTTP
// handler answers 202 before the backup ends. It does not run the backup: a
// worker of any instance claims the operation. A second Start while one
// backup runs answers ErrAlreadyRunning, and a GET sees the row at once. A
// canceled request never stops the backup. The result lands in the registry
// row, where Get and ReadManifest show it: callers poll until the row leaves
// running. When the instance that runs the backup stops, another instance
// takes the operation over after the lease ends.
func (s *Service) Start(ctx context.Context, id ID) error {
	return s.begin(ctx, id, stopAtDamage, "")
}

// StartBeforeRestore starts backup id as Start does, for the backup of the
// live data before a restore. This backup does not stop at a live blob that
// is missing or that does not match its own live row: it records the blob in
// the Damaged list of its manifest and goes on, so that the restore can name
// every such blob before it changes live data, and so that this backup can
// still undo the restore. Every other read error still stops the backup.
//
// The operation input names restore, so the record of the operation tells
// which restore started the backup. See StartedForRestore.
func (s *Service) StartBeforeRestore(ctx context.Context, id ID, restore RestoreID) error {
	if _, err := ParseRestoreID(restore.String()); err != nil {
		return fmt.Errorf("begin backup %s: %w", id, err)
	}

	return s.begin(ctx, id, recordDamage, restore)
}

// RunBeforeRestore starts backup id for restore as StartBeforeRestore does,
// and runs it on the goroutine of the caller. The start claims the operation
// for the runner of this service in its own transaction, so no worker can
// take it while the caller runs it: the backup before a restore always runs
// on the restore that starts it. When the caller stops before the end, the
// claim ends or expires, and a worker or a later Await continues the backup.
// The result lands in the registry row, where Await reads it. It returns
// ErrAlreadyExists when the ID is taken, and then runs nothing.
func (s *Service) RunBeforeRestore(ctx context.Context, id ID, restore RestoreID) error {
	if _, err := ParseRestoreID(restore.String()); err != nil {
		return fmt.Errorf("begin backup %s: %w", id, err)
	}

	// The local deadline of the claim counts from before the start, so it
	// ends before the expiry that the store sets.
	claimedAt := time.Now()

	create, start, err := s.request(ctx, id, recordDamage, restore)
	if err != nil {
		return err
	}

	op, err := s.registry.StartBackupClaimed(ctx, create, start, s.runner.StartClaim())
	if err != nil {
		return fmt.Errorf("create backup %s: %w", id, err)
	}

	// The outcome of the operation is in the store. A claim that the runner
	// lost, or a stop of ctx, leaves the operation to the next claimer, and
	// Await reads the row.
	if runErr := s.runner.RunClaimed(ctx, op, claimedAt); runErr != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "the backup before a restore did not run to its end on the restore",
			slog.String("backup", id.String()), slog.String("error", durable.ErrorClass(runErr)))
	}

	return nil
}

// Work claims and runs backup operations until ctx ends, and marks failed
// the backup rows whose operation ended without a finish write. A worker
// process calls it. When ctx ends during a backup, the runner releases the
// claim, so another instance, or this one after a restart, continues the
// backup. Work returns after the loops stopped.
func (s *Service) Work(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Go(func() { s.runner.Run(ctx) })
	wg.Go(func() { s.failEnded(ctx) })

	wg.Wait()
}

// operationInput is the input of a backup operation.
type operationInput struct {
	ID        ID        `json:"id"`
	StartedAt time.Time `json:"startedAt"`
	// RecordDamaged marks the backup before a restore: it records a missing
	// or damaged live blob in the manifest and goes on.
	RecordDamaged bool `json:"recordDamaged,omitempty"`
	// Restore names the restore whose pre-backup step started this backup.
	// Only a backup before a restore names one.
	Restore RestoreID `json:"restore,omitempty"`
}

// readInput decodes and checks the input of a backup operation. An input
// that fails is permanent: no retry changes it.
func readInput(op durable.Operation) (operationInput, error) {
	var in operationInput
	if err := json.Unmarshal(op.Input, &in); err != nil {
		return operationInput{}, durable.Permanent(fmt.Errorf("decode backup input: %w", err))
	}

	if _, err := ParseID(in.ID.String()); err != nil {
		return operationInput{}, durable.Permanent(err)
	}

	return in, nil
}

// MaxSQLBytes returns the SQL part limit the service enforces. Zero means
// no limit; an S3-compatible target sets S3ObjectLimit.
func (s *Service) MaxSQLBytes() int64 {
	return s.maxSQL
}

// List returns every backup row, newest first, from the registry.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	records, err := s.registry.ListBackups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}

	return records, nil
}

// Get returns the backup row id, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id ID) (Record, error) {
	record, err := s.registry.GetBackup(ctx, id)
	if err != nil {
		return Record{}, fmt.Errorf("get backup %s: %w", id, err)
	}

	return record, nil
}

// RequireSucceeded returns ErrStillRunning for a backup that runs and
// ErrEndedFailed for a backup that failed. It returns ErrNotFound for an ID
// that has no row, and nil for a backup that succeeded. A restore and a check
// use it before they read the target, so that a partial backup is not
// reported as corrupt.
func (s *Service) RequireSucceeded(ctx context.Context, id ID) error {
	record, err := s.registry.GetBackup(ctx, id)
	if err != nil {
		return fmt.Errorf("get backup %s: %w", id, err)
	}

	return record.RequireSucceeded()
}

// RequireSucceeded returns the refusal for a backup that did not succeed, as
// Service.RequireSucceeded does, from the record that the caller already
// holds. A caller that read the record uses it, so that the check needs no
// second read.
func (r Record) RequireSucceeded() error {
	return stateRefusal(r.ID, r.State)
}

// stateRefusal returns the refusal for a backup in state, or nil for a backup
// that succeeded. An unknown state fails closed.
func stateRefusal(id ID, state State) error {
	switch state {
	case StateSucceeded:
		return nil
	case StateRunning:
		return fmt.Errorf("backup %s: %w", id, ErrStillRunning)
	case StateFailed:
		return fmt.Errorf("backup %s: %w", id, ErrEndedFailed)
	}

	// Fail closed: an unknown state is not a backup that succeeded.
	return fmt.Errorf("backup %s: %w", id, ErrEndedFailed)
}

// Check checks that the configured target holds every object the manifest
// of backup id names with the recorded size and checksum. It returns nil when
// the backup is complete, else the first missing, short or changed object by
// key wrapping ErrCorrupt. The installation API answers it, so the command
// reads no target.
func (s *Service) Check(ctx context.Context, id ID) error {
	if err := Check(ctx, s.target, id); err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	live, err := readLivePointer(ctx, s.target, id)
	if err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	// The check route has the backups row at hand, so it also requires that
	// the pointer names the attempt that the row records. A pointer that
	// names a stale attempt reads corrupt even when that prefix is whole.
	// Defense in depth: Check read the same pointer above; this check stays
	// so that a row of another attempt never reads complete.
	row, err := s.registry.GetBackup(ctx, id)
	if err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	if live.attempt != row.Attempt {
		return fmt.Errorf(
			"check backup %s: the live pointer names attempt %q, the backups row records %q: %w",
			id, live.attempt, row.Attempt, ErrCorrupt,
		)
	}

	manifest, err := readManifestAt(ctx, s.target, live, maxManifestBytes)
	if err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	if err := checkDamagedOrigin(ctx, s, manifest); err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	return nil
}

// ReadManifest returns the manifest of backup id from the target. A backup
// without a readable manifest reports ErrCorrupt: only a complete backup
// counts.
func (s *Service) ReadManifest(ctx context.Context, id ID) (Manifest, error) {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// PreserveAfterRestore re-records rec after a restore swap replaced the live
// data with a staged snapshot that predates it (ADR 38 decision 9). The
// backup objects already sit in the target; only the registry row went away
// with the old live data. When the row already exists it does nothing, so a
// registry that the swap does not replace still passes.
func (s *Service) PreserveAfterRestore(ctx context.Context, rec Record) error {
	if _, err := s.registry.GetBackup(ctx, rec.ID); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read backup %s after the swap: %w", rec.ID, err)
	}

	if err := s.registry.CreateBackup(ctx, rec); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return nil
		}

		return fmt.Errorf("record backup %s after the swap: %w", rec.ID, err)
	}

	return nil
}

// StartedForRestore returns the restore that started backup id with
// StartBeforeRestore or RunBeforeRestore, as the input of its durable operation names it. It
// returns an empty ID when no operation of backup id exists, when the input
// names no restore, or when the input does not decode or names an ID that is
// not valid: the caller then treats the backup as one that it did not start.
func (s *Service) StartedForRestore(ctx context.Context, id ID) (RestoreID, error) {
	ops, err := s.operations.List(ctx, durable.Filter{Kind: OperationKind, States: nil, Limit: 0})
	if err != nil {
		return "", fmt.Errorf("list the operations of backup %s: %w", id, err)
	}

	key := OperationKey(id)

	for i := range ops {
		if ops[i].Key == key {
			return restoreOfInput(ops[i].Input, id), nil
		}
	}

	return "", nil
}

// readEnd reads the row of backup id for Await. ended is true, with the
// result of Await, when the backup ended or the row cannot be read. It is
// false while the backup runs.
func (s *Service) readEnd(ctx context.Context, id ID) (Record, bool, error) {
	// An operation that ended with no finish write (a step that panicked,
	// too many holders) leaves its row running. When no worker runs,
	// nothing else marks it failed, so the wait does it before it reads the
	// row.
	if _, err := s.registry.FailEndedBackups(ctx, s.now().UTC()); err != nil {
		if ctx.Err() != nil {
			return Record{}, true, fmt.Errorf("wait for backup %s: %w", id, ctx.Err())
		}

		return Record{}, true, fmt.Errorf("fail ended backups while backup %s runs: %w", id, err)
	}

	row, err := s.registry.GetBackup(ctx, id)
	if err != nil {
		return Record{}, true, fmt.Errorf("read backup %s: %w", id, err)
	}

	switch row.State {
	case StateSucceeded:
		return row, true, nil
	case StateFailed:
		return Record{}, true, fmt.Errorf("%w: %s", ErrFailed, row.Error)
	case StateRunning:
		var running Record

		return running, false, nil
	default:
		return Record{}, true, fmt.Errorf("backup %s has the state %q: %w", id, row.State, ErrCorrupt)
	}
}

// restoreOfInput returns the restore that the operation input raw of backup id
// names, or an empty ID when raw does not decode, names another backup, or
// names no valid restore ID.
func restoreOfInput(raw []byte, id ID) RestoreID {
	var in operationInput
	if err := json.Unmarshal(raw, &in); err != nil || in.ID != id {
		return ""
	}

	// Defense in depth: StartBeforeRestore checks the ID before it stores
	// it. This check stays so that a row from another version cannot name a
	// restore that is not valid.
	if _, err := ParseRestoreID(in.Restore.String()); err != nil {
		return ""
	}

	return in.Restore
}

// begin stores id as a running row and starts its durable operation. See
// Start and StartBeforeRestore.
func (s *Service) begin(ctx context.Context, id ID, policy damagePolicy, restore RestoreID) error {
	create, start, err := s.request(ctx, id, policy, restore)
	if err != nil {
		return err
	}

	if err := s.registry.StartBackup(ctx, create, start); err != nil {
		return fmt.Errorf("create backup %s: %w", id, err)
	}

	return nil
}

// request returns the running row and the operation start of backup id,
// with policy and the restore that starts it, if any.
func (s *Service) request(
	ctx context.Context, id ID, policy damagePolicy, restore RestoreID,
) (Record, durable.StartRequest, error) {
	var (
		none      Record
		noneStart durable.StartRequest
	)

	if err := ctx.Err(); err != nil {
		return none, noneStart, fmt.Errorf("begin backup %s: %w", id, err)
	}

	id, err := ParseID(id.String())
	if err != nil {
		return none, noneStart, fmt.Errorf("begin backup %s: %w", id, err)
	}

	startedAt := s.now().UTC()

	input, err := json.Marshal(operationInput{
		ID: id, StartedAt: startedAt, RecordDamaged: policy == recordDamage, Restore: restore,
	})
	if err != nil {
		return none, noneStart, fmt.Errorf("encode input of backup %s: %w", id, err)
	}

	start, err := s.runner.StartRequest(OperationKind, OperationKey(id), input)
	if err != nil {
		return none, noneStart, fmt.Errorf("begin backup %s: %w", id, err)
	}

	create := Record{
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    s.targetName,
		Error:     "",
		State:     StateRunning,
		Attempt:   "",
	}

	return create, start, nil
}

// newRunner builds the runner that claims and runs the backup operations.
func (s *Service) newRunner(cfg Config) (*durable.Runner, error) {
	runner, err := durable.NewRunner(durable.RunnerConfig{
		Store: cfg.Operations,
		Owner: cmp.Or(cfg.Owner, "backup"),
		Kinds: []durable.KindSpec{{
			Kind:       OperationKind,
			Version:    OperationVersion,
			MinVersion: OperationVersion,
			Steps: []durable.Step{
				{Name: StepWrite, Run: s.stepWrite},
				{Name: StepFinish, Run: s.stepFinish},
			},
		}},
		Lease:                cfg.Lease,
		RenewEvery:           0,
		PollEvery:            0,
		MaxAttempts:          0,
		ExpiryLimit:          0,
		DeadlineReleaseLimit: 0,
		Backoff:              durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff:         durable.Backoff{Base: 0, Max: 0},
		WriteTimeout:         0,
		Deferred:             nil,
		StopOnRenewalError:   false,
		Logger:               cfg.Logger,
		Rand:                 nil,
	})
	if err != nil {
		return nil, fmt.Errorf("backup runner: %w", err)
	}

	return runner, nil
}

// failEnded marks failed the rows of backups whose operation ended failed
// or canceled, once in each lease period, until ctx ends. The finish step
// does that itself for a backup error. This loop covers an operation that the
// runner ended with no step: too many holders stopped, a step that panicked,
// or a cancel. Without it such a row would stay running and refuse the next
// backup.
func (s *Service) failEnded(ctx context.Context) {
	ticker := time.NewTicker(s.lease)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A failure here is not final: the next period tries again, and
			// the row stays running until then.
			n, err := s.registry.FailEndedBackups(ctx, s.now().UTC())
			if err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "backup rows of ended operations were not marked failed",
					slog.String("error", durable.ErrorClass(err)))
			} else if n > 0 {
				s.logger.WarnContext(ctx, "backups ended without a finish write were marked failed",
					slog.Int("backups", n))
			}
		}
	}
}

// stepWrite writes the SQL part, the blobs and the manifest to the target.
// The table files and the manifest go to backups/<id>/<attempt>/, where the
// attempt is the fencing token of this claim, so a run after a takeover writes
// a new directory and a stale run touches only its own. Each put and each
// delete reads the claim right before the target call. A run that wrote its
// files records its attempt on the backups row in the same fenced transaction
// that records this step, so the database decides which attempt counts. A
// stop of the runner (the context ends, the claim moves or a cancel wins)
// returns with no write to the row: the next claim runs the step again. Any
// other error marks the row failed with the fence of this claim and ends the
// operation.
func (s *Service) stepWrite(ctx context.Context, sc durable.StepContext) ([]byte, error) {
	in, err := readInput(sc.Operation)
	if err != nil {
		return nil, err
	}

	prefix, err := newAttemptPrefix(in.ID, AttemptOf(sc.Fence.Token))
	if err != nil {
		return nil, durable.Permanent(err)
	}

	fenced := s.fencedBy(sc.Fence)

	backupErr := fenced.clearPartial(ctx, prefix)
	if backupErr == nil {
		backupErr = fenced.backup(ctx, backupRun{
			id: in.ID, startedAt: in.StartedAt, prefix: prefix, policy: policyOf(in),
		})
	}

	if backupErr == nil {
		// The row attempt commits in the same fenced transaction as the
		// record of this step, so the database decides which attempt counts.
		backupErr = s.recordWriteAttempt(ctx, sc.Fence, in.ID, prefix.attempt)
		if errors.Is(backupErr, durable.ErrStepDone) {
			// The step record, with its attempt, was already committed.
			backupErr = nil
		}
	}

	if backupErr == nil {
		return nil, nil
	}

	if ctx.Err() != nil || errors.Is(backupErr, durable.ErrClaimLost) || errors.Is(backupErr, durable.ErrCanceled) {
		return nil, backupErr
	}

	return nil, s.failRow(ctx, sc.Fence, in.ID, backupErr)
}

// recordWriteAttempt stores attempt as the attempt of backup id and records
// the write step in one fenced transaction. A holder that lost the claim
// records nothing.
func (s *Service) recordWriteAttempt(
	ctx context.Context, fence durable.StepFence, id ID, attempt string,
) error {
	if err := s.registry.RecordBackupAttempt(ctx, fence, id, attempt); err != nil {
		return fmt.Errorf("record the attempt of backup %s: %w", id, err)
	}

	return nil
}

// clearPartial removes the objects under p that an earlier run with the same
// attempt left, the manifest first. It never lists or deletes outside p, so a
// run that lost its claim cannot touch the directory of another attempt. The
// manifest goes first, so a half-cleared backup never looks complete. The
// copies under blobs/ stay: the copy keeps the first copy of a blob and checks
// it against the live bytes.
func (s *Service) clearPartial(ctx context.Context, p attemptPrefix) error {
	id := p.id

	manifestKey, err := p.manifestKey()
	if err != nil {
		return err
	}

	left, err := listObjects(ctx, s.target, p.dir())
	if err != nil {
		return fmt.Errorf("list objects of backup %s: %w", id, err)
	}

	keys := make([]blob.Key, 0, len(left))

	for _, obj := range left {
		if obj.Key != manifestKey {
			keys = append(keys, obj.Key)
		}
	}

	if slices.ContainsFunc(left, func(obj blob.Object) bool { return obj.Key == manifestKey }) {
		keys = slices.Insert(keys, 0, manifestKey)
	}

	for _, key := range keys {
		// Defense in depth: List filters by prefix, but a target whose List
		// matches the prefix as a plain string prefix also returns the keys
		// of another attempt or another backup (for example
		// "backups/<id>/<attempt>0/..."). This check stays so that a List
		// that is too wide never deletes the files of another attempt. It
		// does not rely on the List layer or on the claim check of the target.
		if !p.owns(key) {
			return fmt.Errorf("refuse to delete object %s outside %s of backup %s: %w", key, p.dir(), id, ErrCorrupt)
		}

		if err := s.target.Delete(ctx, key); err != nil {
			return fmt.Errorf("delete object %s of backup %s: %w", key, id, err)
		}
	}

	return nil
}

// failRow marks backup id failed under fence and returns the error that ends
// operation: the backup error as a permanent error, or the error of the write
// when the claim is lost or the operation is canceled, which the runner reads
// as a stop. A failed row keeps the attempt that it holds: the finish commit
// compares the attempt only for a row that reads succeeded, so this call
// passes none.
func (s *Service) failRow(ctx context.Context, fence durable.StepFence, id ID, backupErr error) error {
	_, finishErr := s.registry.FinishBackup(ctx, fence, id, StateFailed, backupErr.Error(), s.now().UTC(), "")

	switch {
	case finishErr == nil:
		return durable.Permanent(backupErr)
	case errors.Is(finishErr, durable.ErrClaimLost), errors.Is(finishErr, durable.ErrCanceled):
		return finishErr
	default:
		return durable.Permanent(errors.Join(backupErr, fmt.Errorf("mark backup %s failed: %w", id, finishErr)))
	}
}

// stepFinish checks the attempt directory that the backups row records with
// the same Check that the check route runs, writes the live pointer, commits
// the row and the step record in one fenced transaction, then deletes the
// other attempt directories of the backup.
//
// The step takes the attempt from the backups row, never from the token of
// its own claim: the durable runner skips a recorded step, so a holder that
// took over after the write step wrote no files under its own token. The
// live pointer goes to the target before the commit: the commit records this
// step, so the runner gives no second chance, and a holder that stops between
// the pointer and the commit leaves a pointer that the next holder finds
// equal and commits. The pointer holds the row attempt in one fixed byte
// form, and the writer reads it first, so two holders give the same pointer
// on a store that refuses the second put and on a store that replaces the
// object.
//
// The deletes that this change covers: the clear of the write step, the
// pointer put and the cleanup of this step all read the claim right before
// the call, so a holder that lost its claim reaches no object, and a delete
// of a stale holder lands only under the directory of its own attempt, never
// under the directory of the attempt that the row records. The cleanup runs
// after the commit and only when the pointer names the row attempt; it keeps
// that directory and the pointer.
func (s *Service) stepFinish(ctx context.Context, sc durable.StepContext) ([]byte, error) {
	in, err := readInput(sc.Operation)
	if err != nil {
		return nil, err
	}

	// The backups row decides the live attempt, not the claim of this holder.
	row, err := s.registry.GetBackup(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("read backup %s for the finish step: %w", in.ID, err)
	}

	// Defense in depth: the store refuses a row that is not running in the
	// same transaction as the step record. This check stays so that a caller
	// that skips the store never finishes a backup that already ended.
	if row.State != StateRunning {
		return nil, durable.Permanent(fmt.Errorf("finish backup %s: %w", in.ID, ErrNotRunning))
	}

	// Defense in depth: the store allowlist and the compare of the finish commit
	// refuse an invalid attempt too. This check stays so that the step never
	// builds a key from an empty or unparsable attempt of the row.
	prefix, err := newAttemptPrefix(in.ID, row.Attempt)
	if err != nil {
		return nil, s.failRow(ctx, sc.Fence, in.ID, fmt.Errorf("backup %s: %w", in.ID, err))
	}

	if checkErr := s.checkFinishAttempt(ctx, sc.Fence, in.ID, prefix); checkErr != nil {
		return nil, checkErr
	}

	fenced := s.fencedBy(sc.Fence)

	if pointerErr := s.writeFinishPointer(ctx, fenced, sc.Fence, in.ID, prefix); pointerErr != nil {
		return nil, pointerErr
	}

	if finishErr := fenced.commitFinishAttempt(ctx, sc.Fence, in.ID, prefix); finishErr != nil {
		return nil, finishErr
	}

	if cleanErr := fenced.cleanupOrphans(ctx, prefix); cleanErr != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "cleanup of old attempt directories of a backup stopped",
			slog.String("backup", in.ID.String()), slog.String("error", durable.ErrorClass(cleanErr)))
	}

	return nil, nil
}

// checkFinishAttempt checks the attempt directory that the backups row
// records with the same Check that the check route runs. A canceled context
// ends the step with the check error; any other check failure marks the row
// failed.
func (s *Service) checkFinishAttempt(
	ctx context.Context, fence durable.StepFence, id ID, prefix attemptPrefix,
) error {
	if checkErr := checkAttempt(ctx, s.target, prefix); checkErr != nil {
		if ctx.Err() != nil {
			return checkErr
		}

		return s.failRow(ctx, fence, id, checkErr)
	}

	return nil
}

// writeFinishPointer writes the live pointer that names the row attempt. A
// lost claim or a cancel ends the step with the pointer error; a pointer that
// names another attempt or does not parse marks the row failed, so the
// backup never reads succeeded and the operator runs a new backup. The
// writer never replaces the pointer, and the cleanup deletes nothing then.
// fence is the fence of the step: it goes back to failRow when the pointer
// check fails.
func (s *Service) writeFinishPointer(
	ctx context.Context, fenced *Service, fence durable.StepFence, id ID, prefix attemptPrefix,
) error {
	if pointerErr := writeLivePointer(ctx, fenced.target, prefix); pointerErr != nil {
		switch {
		case ctx.Err() != nil,
			errors.Is(pointerErr, durable.ErrClaimLost),
			errors.Is(pointerErr, durable.ErrCanceled):
			return pointerErr
		case errors.Is(pointerErr, ErrCorrupt):
			s.logger.WarnContext(
				ctx,
				"backup finish stopped on a live pointer that does not name the attempt of the row",
				slog.String("backup", id.String()),
			)

			return fenced.failRow(ctx, fence, id, pointerErr)
		default:
			return pointerErr
		}
	}

	return nil
}

// commitFinishAttempt commits the row and the step record in one fenced
// transaction. A lost claim or a cancel ends the step; any other finish
// error is permanent.
func (s *Service) commitFinishAttempt(ctx context.Context, fence durable.StepFence, id ID, prefix attemptPrefix) error {
	_, finishErr := s.registry.FinishBackup(ctx, fence, id, StateSucceeded, "", s.now().UTC(), prefix.attempt)

	switch {
	case finishErr == nil:
		return nil
	case errors.Is(finishErr, durable.ErrClaimLost), errors.Is(finishErr, durable.ErrCanceled):
		return finishErr
	default:
		return durable.Permanent(fmt.Errorf("finish backup %s: %w", id, finishErr))
	}
}

// damagePolicy says what a backup does at a live blob that is missing or
// that does not match its own live row.
type damagePolicy int

const (
	// stopAtDamage stops the backup: every backup but the one before a
	// restore.
	stopAtDamage damagePolicy = iota
	// recordDamage records the blob in the manifest and goes on: the backup
	// before a restore.
	recordDamage
)

// policyOf returns the damage policy of a backup operation input.
func policyOf(in operationInput) damagePolicy {
	if in.RecordDamaged {
		return recordDamage
	}

	return stopAtDamage
}

// backupRun holds the input of one run of a backup.
type backupRun struct {
	startedAt time.Time
	id        ID
	prefix    attemptPrefix
	policy    damagePolicy
}

// backup exports the SQL part, copies the named blobs, and writes the
// manifest last, so a backup without a manifest never counts as complete.
func (s *Service) backup(ctx context.Context, run backupRun) error {
	id := run.id

	// Read the kind before the first object lands in the target, so a database
	// that claims no kind leaves no file and no manifest.
	kind, err := s.readKind(ctx)
	if err != nil {
		return err
	}

	exported, err := s.exportPortable(ctx, run.prefix)
	if err != nil {
		return err
	}

	if n := len(exported.sum.Unverified); n > 0 {
		s.logger.WarnContext(ctx, "backup does not copy blobs that the data names without a check value",
			slog.String("backup", id.String()), slog.Int("blobs", n), slog.String("first", exported.sum.Unverified[0]))
	}

	// The refusal comes before the first copy and before the manifest, so a
	// refused backup leaves no manifest that a restore would accept as
	// complete.
	if boundErr := refuseUnverified(exported.sum.Unverified, s.unverifiedLimit()); boundErr != nil {
		s.logger.WarnContext(ctx, "backup refused: the data names too many blobs without a check value",
			slog.String("backup", id.String()), slog.Int("blobs", len(exported.sum.Unverified)),
			slog.Int("limit", s.unverifiedLimit()))

		return boundErr
	}

	// A registry file above a limit that a restore applies stops the backup
	// here too, before the first copy and before the manifest. Defense in
	// depth: the store collector checks the same limits, and this check does
	// not rely on it, so an exporter that does not check cannot write a
	// manifest that no restore accepts.
	if limitErr := CheckRegistryFileLimits(exported.sum.RegistryFiles); limitErr != nil {
		s.logger.WarnContext(ctx, "backup refused: a registry file is above the limit of a restore",
			slog.String("backup", id.String()))

		return limitErr
	}

	// The export refuses a key that a restore would refuse, so a poisoned row
	// cannot make this backup and every later one unrestorable.
	if keyErr := checkExportKeys(id, exported.sum.Blobs); keyErr != nil {
		return keyErr
	}

	ordered := orderBlobs(exported.sum.Blobs)

	stored, damaged, err := s.copyBlobs(ctx, ordered, run.policy, exported.sum.RegistryFiles)
	if err != nil {
		return err
	}

	// The registry file check reads each named file after the copy and stops
	// the backup when its bytes do not match the SHA-256 content key, so the
	// manifest never names a file that the target copy does not hold.
	if shaErr := s.verifyRegistryFiles(ctx, ordered, exported.sum.RegistryFiles); shaErr != nil {
		return shaErr
	}

	if len(damaged) > 0 {
		s.logger.WarnContext(ctx, "backup before a restore found live blobs that are missing or damaged",
			slog.String("backup", id.String()), slog.Int("blobs", len(damaged)),
			slog.String("first", truncateKey(damaged[0].Key)))
	}

	return s.writeManifest(ctx, manifestInput{
		startedAt: run.startedAt,
		kind:      kind,
		id:        id,
		sum:       exported.sum,
		ordered:   stored,
		files:     exported.files,
		damaged:   damaged,
		size:      exported.size,
		sha:       exported.sha,
		prefix:    run.prefix,
	})
}

// copyBlobs copies each blob of ordered with copyBlob, in order, and returns
// the manifest entries. With recordDamage, a live blob that is missing or
// that does not match its own live row does not stop the backup: it goes to
// the damaged list, in key order, and the backup holds no copy of it. Every
// other error stops the backup. The error of a registry file names the
// release and the version that name the file.
func (s *Service) copyBlobs(
	ctx context.Context, ordered []BlobRef, policy damagePolicy, registryFiles map[string][]RegistryFileEntry,
) ([]ManifestBlob, []DamagedBlob, error) {
	entries := registryFileEntries(registryFiles)

	stored := make([]ManifestBlob, 0, len(ordered))

	var damaged []DamagedBlob

	for _, ref := range ordered {
		copied, copyErr := s.copyBlob(ctx, ref)
		if copyErr == nil {
			stored = append(stored, copied)

			continue
		}

		damage, ok := errors.AsType[*liveDamageError](copyErr)
		if policy != recordDamage || !ok || damage == nil {
			return nil, nil, registryFileCopyError(entries, ref.Key, copyErr)
		}

		// Defense in depth: the copy builds the entry from a key that the
		// export checked. This check stays so that the manifest never names
		// an entry that a restore refuses.
		if err := validDamagedEntry(damage.entry); err != nil {
			return nil, nil, errors.Join(copyErr, err)
		}

		damaged = append(damaged, damage.entry)
	}

	return stored, damaged, nil
}

// readKind reads the installation kind of the database from its own store.
// Any failure, and any value that is not a known kind, is
// installation.ErrUnavailable: the export fails and writes no backup.
func (s *Service) readKind(ctx context.Context) (installation.Kind, error) {
	kind, err := s.kinds.ReadKind(ctx)
	if err != nil {
		return "", fmt.Errorf("read the installation kind for the backup: %w", err)
	}

	if !kind.Valid() {
		return "", fmt.Errorf("read the installation kind for the backup: %w", installation.ErrUnavailable)
	}

	return kind, nil
}

// orderBlobs orders the references by key in place and returns them, so
// copies and the manifest follow a stable order whatever order the export
// returns. It makes no copy of the list, which can hold a million references:
// the caller does not use refs in its old order after the call.
func orderBlobs(refs []BlobRef) []BlobRef {
	slices.SortFunc(refs, func(a, b BlobRef) int { return strings.Compare(a.Key, b.Key) })

	return refs
}

// registryFileEntries returns the manifest entry of each blob key that the
// registry file entries of the namespaces name, so an error of the copy or
// of the check of a registry file names the release and the version that
// name the file.
func registryFileEntries(registryFiles map[string][]RegistryFileEntry) map[string]RegistryFileEntry {
	out := make(map[string]RegistryFileEntry)

	for nsID, list := range registryFiles {
		for _, e := range list {
			key, err := RegistryFileBlobKey(nsID, e.SHA256)
			if err != nil {
				continue
			}

			if _, dup := out[key]; !dup {
				out[key] = e
			}
		}
	}

	return out
}

// registryFileCopyError returns cause with the release and the version that
// name the registry file of key, or cause unchanged for any other key.
func registryFileCopyError(entries map[string]RegistryFileEntry, key string, cause error) error {
	e, ok := entries[key]
	if !ok {
		return cause
	}

	return fmt.Errorf("registry file of release %q version %q: %w", e.Release, e.Version, cause)
}

// verifyRegistryFiles streams each registry file of the backup from the live
// store and stops the backup when the bytes do not match the SHA-256 content
// key that the file key names. The error names the release, the version and
// the key, so the operator finds the release that names the file. The check
// runs after the copy, so a backup never writes a manifest that names a file
// whose bytes it did not check.
func (s *Service) verifyRegistryFiles(
	ctx context.Context, refs []BlobRef, registryFiles map[string][]RegistryFileEntry,
) error {
	entries := registryFileEntries(registryFiles)

	for _, ref := range refs {
		hash, ok := registryFileSHAOfKey(ref.Key)
		if !ok {
			continue
		}

		if err := s.checkRegistryFileSHA(ctx, ref, hash); err != nil {
			return registryFileCopyError(entries, ref.Key, err)
		}
	}

	return nil
}

// checkRegistryFileSHA streams the live blob of ref and reports an error that
// wraps ErrCorrupt when the bytes are not the SHA-256 that the key names.
// Any other read error returns as it is, so the step retries.
func (s *Service) checkRegistryFileSHA(ctx context.Context, ref BlobRef, hash string) error {
	key, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("backup blob key %q: %w", ref.Key, err)
	}

	reader, err := s.source.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read blob %q: %w", ref.Key, err)
	}

	digest := sha256.New()

	_, copyErr := io.Copy(digest, reader)
	closeErr := reader.Close()

	if copyErr != nil {
		return fmt.Errorf("read blob %q: %w", ref.Key, copyErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close blob %q: %w", ref.Key, closeErr)
	}

	if hex.EncodeToString(digest.Sum(nil)) != hash {
		return fmt.Errorf("%w: registry file %q does not match its SHA-256 content key", ErrCorrupt, ref.Key)
	}

	return nil
}

// registryFileSHAOfKey returns the SHA-256 content key that a registry file
// blob key names, and false for any other key: the key is
// namespaces/<namespace ID>/registry/sha256/<hash>.
func registryFileSHAOfKey(key string) (string, bool) {
	segments := strings.Split(key, "/")

	if len(segments) != 5 || segments[0] != "namespaces" || segments[2] != "registry" ||
		segments[3] != "sha256" || !ValidRegistryFileHash(segments[4]) {
		return "", false
	}

	return segments[4], true
}

// sqlExport holds the summary of a portable export with the descriptors of its
// table files, and the size and SHA-256 that the manifest keeps for the SQL part.
type sqlExport struct {
	sum   Summary
	files []PortableFile
	size  int64
	sha   [sha256.Size]byte
}

// exportPortable writes one object for each table file of the SQL part, under
// backups/<id>/<attempt>/sql, and returns the summary with the descriptors. Each file
// streams into the target while the export writes it, so the SQL part never sits
// in memory whole. A file that fails before it completes is aborted in the
// target, so no partial table file stays there as a good one.
//
// The service finishes every file it opened before it returns, also when the
// exporter returns without a Close or an Abort of a file. An open file keeps
// its target Put waiting on the pipe, so it must never outlive the export.
func (s *Service) exportPortable(ctx context.Context, prefix attemptPrefix) (sqlExport, error) {
	var (
		mu     sync.Mutex
		opened []*targetWriter
	)

	sink := func(path string) (io.WriteCloser, error) {
		key, err := prefix.key(path)
		if err != nil {
			return nil, fmt.Errorf("name table file %q: %w", path, err)
		}

		w := newTargetWriter(ctx, s.target, key, s.maxSQL)

		mu.Lock()
		opened = append(opened, w)
		mu.Unlock()

		return w, nil
	}

	sum, files, err := s.exporter.ExportPortable(ctx, sink)

	mu.Lock()
	left := abortOpen(opened, err)
	mu.Unlock()

	if err != nil {
		return sqlExport{}, fmt.Errorf("export SQL data: %w", err)
	}

	if left > 0 {
		return sqlExport{}, fmt.Errorf("export SQL data: %w: %d table files", errTableFileOpen, left)
	}

	size, sha, err := sqlAggregate(files)
	if err != nil {
		return sqlExport{}, err
	}

	return sqlExport{sum: sum, files: files, size: size, sha: sha}, nil
}

// sqlAggregate returns the total size of the table files and the SHA-256 of
// their descriptors, as JSON. The manifest keeps both, so a restore pins the SQL
// part.
func sqlAggregate(files []PortableFile) (int64, [sha256.Size]byte, error) {
	var sha [sha256.Size]byte

	var size int64

	for _, file := range files {
		size += file.Size
	}

	raw, err := json.Marshal(files)
	if err != nil {
		return 0, sha, fmt.Errorf("encode SQL part descriptors: %w", err)
	}

	sha = sha256.Sum256(raw)

	return size, sha, nil
}

// errTableFileOpen reports an export that returned with a table file that it
// did not close or abort. The service aborts the file, so the target keeps
// nothing for it.
var errTableFileOpen = errors.New("exporter left a table file open")

// abortOpen aborts each writer that is not finished and returns their count.
// cause is the error of the export, or nil when the export reported success.
func abortOpen(writers []*targetWriter, cause error) int {
	if cause == nil {
		cause = errTableFileOpen
	}

	left := 0

	for _, w := range writers {
		if w.abortOpen(cause) {
			left++
		}
	}

	return left
}

// targetWriter streams one table file into the target. Close finishes the
// object and reports the target's error. Abort ends the object with err, so the
// target stores nothing for the file. Only the first Close or Abort acts. A
// later Close returns the error of the first Close, or nil after an Abort.
type targetWriter struct {
	pipe     *io.PipeWriter
	done     chan error
	size     int64
	limit    int64
	once     sync.Once
	closeErr error
}

// targetWriter is the abort sink of WritePortable, so a failed table file never
// completes in the target.
var _ abortSink = (*targetWriter)(nil)

// newTargetWriter starts a Put of key on target that reads from a pipe. A limit
// above zero bounds the bytes of the object, as an S3-compatible target bounds
// one object (ADR 31).
func newTargetWriter(ctx context.Context, target blob.Store, key blob.Key, limit int64) *targetWriter {
	reader, writer := io.Pipe()
	done := make(chan error, 1)

	go func() {
		err := target.Put(ctx, key, reader)
		if err != nil {
			_ = reader.CloseWithError(err) // PipeReader.CloseWithError always returns nil.
		}

		done <- err
	}()

	return &targetWriter{pipe: writer, done: done, size: 0, limit: limit, once: sync.Once{}, closeErr: nil}
}

// Write streams p into the object. It fails above the limit, so an oversized
// object stops the export with ErrTooLarge instead of filling the target.
func (w *targetWriter) Write(p []byte) (int, error) {
	if w.limit > 0 && w.size+int64(len(p)) > w.limit {
		return 0, fmt.Errorf("%w: table file of %d bytes is over the target limit of %d bytes",
			ErrTooLarge, w.size+int64(len(p)), w.limit)
	}

	n, err := w.pipe.Write(p)
	w.size += int64(n)

	return n, err //nolint:wrapcheck // The pipe reports the target's error unchanged.
}

// Close finishes the object and returns the target's error.
func (w *targetWriter) Close() error {
	w.once.Do(func() {
		if err := w.pipe.Close(); err != nil {
			w.closeErr = fmt.Errorf("finish table file: %w", err)

			return
		}

		if err := <-w.done; err != nil {
			w.closeErr = fmt.Errorf("write table file: %w", err)
		}
	})

	return w.closeErr
}

// Abort ends the object with err, so the target stores nothing for it. It
// waits until the target Put returns. It does nothing after a Close or an
// Abort.
func (w *targetWriter) Abort(err error) {
	w.abortOpen(err)
}

// abortOpen aborts the object as Abort does, and reports whether it acted:
// false means that an earlier Close or Abort already finished the object.
func (w *targetWriter) abortOpen(err error) bool {
	acted := false

	w.once.Do(func() {
		acted = true
		_ = w.pipe.CloseWithError(err) // PipeWriter.CloseWithError always returns nil.
		putErr := <-w.done
		_ = putErr // The abort error ends the Put, so the Put reports it.
	})

	return acted
}

// copyBufferSize sizes the counting pass buffer, so the passes that hash
// without a target write hold one chunk at a time, never the state size.
// The passes that write into the target stream through its Put instead.
const copyBufferSize = 32 * 1024

// copyBlob copies one blob the SQL data names from the live store to the
// target and returns the manifest entry of the stored bytes. Every pass
// streams the blob without holding it whole, so peak memory per blob stays
// bounded by the read chunk size, not by the state size. A plain blob
// streams once, straight into the target, while MD5 and size accumulate; on
// a mismatch the copy deletes the bad object and reports ErrCorrupt, so no
// bad copy stays in the target as a good one. A sealed state blob opens
// first: the check hashes the plain stream and the copy streams the sealed
// bytes, so plain state never reaches the backup target. A blob the target
// already holds keeps its first copy, after the live bytes still pass the
// check.
func (s *Service) copyBlob(ctx context.Context, ref BlobRef) (ManifestBlob, error) {
	keys, keysErr := copyKeys(ref.Key)
	if keysErr != nil {
		return ManifestBlob{}, keysErr
	}

	if ref.Sealed {
		return s.copySealedBlob(ctx, keys.dest, keys.source, ref)
	}

	return s.copyPlainBlob(ctx, keys.dest, keys.source, ref)
}

// copyKeysResult holds the live key and the target key of one blob
// reference.
type copyKeysResult struct {
	source blob.Key
	dest   blob.Key
}

// copyKeys parses the live key and the target key of one blob reference.
func copyKeys(key string) (copyKeysResult, error) {
	source, err := blob.ParseKey(key)
	if err != nil {
		return copyKeysResult{}, fmt.Errorf("backup blob key %q: %w", key, err)
	}

	dest, err := blob.ParseKey("blobs/" + key)
	if err != nil {
		return copyKeysResult{}, fmt.Errorf("backup target key of %q: %w", key, err)
	}

	return copyKeysResult{source: source, dest: dest}, nil
}

// copyPlainBlob streams the plain bytes of one blob reference from the live
// store into the target while MD5 and size accumulate, then checks them
// against the size and MD5 in the SQL data. On a mismatch it deletes the
// copied object and reports ErrCorrupt.
func (s *Service) copyPlainBlob(
	ctx context.Context, dest, source blob.Key, ref BlobRef,
) (ManifestBlob, error) {
	copied, err := s.streamCopy(ctx, dest, source, ref)
	if err != nil {
		if sourceMissing(err) {
			return ManifestBlob{}, missingDamage(ref, err)
		}

		return ManifestBlob{}, err
	}

	if copied.size != ref.Size || !bytes.Equal(copied.sum, ref.MD5[:]) {
		corrupt := corruptBlobError(ref.Key, copied.size, copied.sum, ref)
		damage := mismatchDamage(ref, plainDamageReason(copied.size, ref), corrupt)

		if copied.kept {
			return ManifestBlob{}, damage
		}

		// A bad copy that stays in the target is not damage of the live
		// blob: the backup stops.
		if delErr := s.target.Delete(ctx, dest); delErr != nil {
			return ManifestBlob{}, errors.Join(
				corrupt,
				fmt.Errorf("delete bad copy of blob %q: %w", ref.Key, delErr),
			)
		}

		return ManifestBlob{}, damage
	}

	return ManifestBlob{
		Key:    ref.Key,
		Size:   ref.Size,
		MD5:    hex.EncodeToString(ref.MD5[:]),
		Sealed: false,
	}, nil
}

// copySealedBlob checks the plain bytes of one sealed blob reference against
// the size and MD5 in the SQL data by hashing the opened stream, then
// streams the sealed bytes into the target. The manifest entry names the
// sealed size and MD5. Both passes move in chunks, so neither the plain nor
// the sealed side ever sits in memory whole. A blob the target already holds
// keeps its first copy.
func (s *Service) copySealedBlob(
	ctx context.Context, dest, source blob.Key, ref BlobRef,
) (ManifestBlob, error) {
	if s.opener == nil {
		size, sum, err := s.hashStoredBlob(ctx, source, ref)
		if err != nil {
			return ManifestBlob{}, err
		}

		return ManifestBlob{}, corruptBlobError(ref.Key, size, sum, ref)
	}

	size, sum, err := s.hashPlainCopy(ctx, source, ref)
	if err != nil {
		return ManifestBlob{}, sealedDamage(ref, err)
	}

	if size != ref.Size || !bytes.Equal(sum, ref.MD5[:]) {
		mismatch := fmt.Errorf("%w: %w", ErrLiveBlobDamaged, corruptBlobError(ref.Key, size, sum, ref))

		return ManifestBlob{}, sealedDamage(ref, mismatch)
	}

	copied, err := s.streamCopy(ctx, dest, source, ref)
	if err != nil {
		return ManifestBlob{}, err
	}

	return ManifestBlob{
		Key:    ref.Key,
		Size:   copied.size,
		MD5:    hex.EncodeToString(copied.sum),
		Sealed: true,
	}, nil
}

// sealedDamage classifies the error of the check of one sealed blob. A
// missing live object is missing damage. Plain bytes that opened and do not
// match the size or the MD5 of the row are damaged: the error wraps
// ErrLiveBlobDamaged. Sealed bytes that do not open are damaged with the
// reason unopened: the error wraps ErrLiveBlobUnopened. A restore stops at
// both and writes over neither. Any other error, for example a row that
// cannot be read, returns as it is, so the backup stops.
func sealedDamage(ref BlobRef, cause error) error {
	switch {
	case sourceMissing(cause):
		return missingDamage(ref, cause)
	case errors.Is(cause, ErrLiveBlobDamaged):
		return mismatchDamage(ref, ReasonSealed, cause)
	case errors.Is(cause, ErrLiveBlobUnopened):
		return mismatchDamage(ref, ReasonUnopened, cause)
	default:
		return cause
	}
}

// copyResult holds the size and MD5 of one streamed blob copy. Kept reports
// that the target already held the blob, so the first copy stays.
type copyResult struct {
	sum  []byte
	size int64
	kept bool
}

// streamCopy streams one live blob from source into the target while MD5 and
// size accumulate, and returns them. When the target already holds the blob
// it keeps its first copy and streamCopy reports kept: a refused Put may
// leave the stream undrained, so the kept values come from a second
// streaming pass over the live bytes.
func (s *Service) streamCopy(
	ctx context.Context, dest, source blob.Key, ref BlobRef,
) (copyResult, error) {
	reader, err := s.source.Get(ctx, source)
	if err != nil {
		return copyResult{}, fmt.Errorf("read blob %q: %w", ref.Key, err)
	}

	tracked := &readTracker{reader: reader, err: nil}
	counter := &md5SizeWriter{hash: blob.NewMD5(), size: 0}

	putErr := s.target.Put(ctx, dest, io.TeeReader(tracked, counter))
	closeErr := reader.Close()

	if tracked.err != nil {
		return copyResult{}, fmt.Errorf("read blob %q: %w", ref.Key, tracked.err)
	}

	if closeErr != nil {
		return copyResult{}, fmt.Errorf("close blob %q: %w", ref.Key, closeErr)
	}

	if putErr != nil {
		if errors.Is(putErr, blob.ErrExists) {
			size, sum, hashErr := s.hashStoredBlob(ctx, source, ref)
			if hashErr != nil {
				return copyResult{}, hashErr
			}

			return copyResult{sum: sum, size: size, kept: true}, nil
		}

		return copyResult{}, fmt.Errorf("copy blob %q: %w", ref.Key, putErr)
	}

	return copyResult{sum: counter.hash.Sum(nil), size: counter.size, kept: false}, nil
}

// hashStoredBlob streams one live blob through MD5 and returns its size and
// checksum. It never holds the blob whole.
func (s *Service) hashStoredBlob(
	ctx context.Context, source blob.Key, ref BlobRef,
) (int64, []byte, error) {
	reader, err := s.source.Get(ctx, source)
	if err != nil {
		return 0, nil, fmt.Errorf("read blob %q: %w", ref.Key, err)
	}

	size, sum, countErr := countBlob(reader)
	closeErr := reader.Close()

	if countErr != nil {
		return 0, nil, fmt.Errorf("read blob %q: %w", ref.Key, countErr)
	}

	if closeErr != nil {
		return 0, nil, fmt.Errorf("close blob %q: %w", ref.Key, closeErr)
	}

	return size, sum, nil
}

// hashPlainCopy opens the sealed blob under key and streams its plain bytes
// through MD5, returning the plain size and checksum for the SQL data check.
// An open failure names the blob key only, never a key, a scope or content.
func (s *Service) hashPlainCopy(
	ctx context.Context, key blob.Key, ref BlobRef,
) (int64, []byte, error) {
	reader, err := s.opener.OpenStateBlob(ctx, key)
	if err != nil {
		return 0, nil, fmt.Errorf("open sealed blob %q: %w", ref.Key, err)
	}

	size, sum, countErr := countBlob(reader)
	closeErr := reader.Close()

	if countErr != nil {
		return 0, nil, fmt.Errorf("read sealed blob %q: %w", ref.Key, countErr)
	}

	if closeErr != nil {
		return 0, nil, fmt.Errorf("close sealed blob %q: %w", ref.Key, closeErr)
	}

	return size, sum, nil
}

// countBlob streams r through MD5 in chunks of copyBufferSize and returns
// the size and the checksum. The explicit loop keeps one chunk in flight,
// whatever the reader implements.
func countBlob(r io.Reader) (int64, []byte, error) {
	counter := &md5SizeWriter{hash: blob.NewMD5(), size: 0}
	buf := make([]byte, copyBufferSize)

	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			if _, err := counter.Write(buf[:n]); err != nil {
				return 0, nil, fmt.Errorf("count blob bytes: %w", err)
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				return counter.size, counter.hash.Sum(nil), nil
			}

			return 0, nil, fmt.Errorf("count blob bytes: %w", readErr)
		}
	}
}

// readTracker records the first non-EOF read error of a source stream, so a
// copy that fails inside Put still reports a source failure as a read error
// and a target failure as a copy error.
type readTracker struct {
	reader io.Reader
	err    error
}

func (t *readTracker) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see io.EOF unchanged.
}

// corruptBlobError reports stored bytes of size with MD5 sum that differ
// from the SQL data.
func corruptBlobError(key string, size int64, sum []byte, ref BlobRef) error {
	if size != ref.Size {
		return fmt.Errorf(
			"%w: blob %q holds %d bytes, the SQL data names %d",
			ErrCorrupt,
			key,
			size,
			ref.Size,
		)
	}

	if !bytes.Equal(sum, ref.MD5[:]) {
		return fmt.Errorf("%w: blob %q does not match the MD5 in the SQL data", ErrCorrupt, key)
	}

	return fmt.Errorf("%w: blob %q does not match the SQL data", ErrCorrupt, key)
}

// manifestInput groups the manifest fields writeManifest needs.
type manifestInput struct {
	startedAt time.Time
	kind      installation.Kind
	id        ID
	sum       Summary
	files     []PortableFile
	ordered   []ManifestBlob
	damaged   []DamagedBlob
	size      int64
	sha       [sha256.Size]byte
	prefix    attemptPrefix
}

// writeManifest writes backups/<id>/<attempt>/manifest.json last, after the
// SQL part and every blob, so a backup without a manifest never counts as
// complete.
func (s *Service) writeManifest(ctx context.Context, in manifestInput) error {
	// Defense in depth: backup refuses the bound before the first copy, so no
	// manifest above the bound reaches this write. This check stays so that no
	// caller writes such a manifest. It reads only the summary of this run,
	// not the target or the rows of the database.
	if boundErr := refuseUnverified(in.sum.Unverified, s.unverifiedLimit()); boundErr != nil {
		return boundErr
	}

	manifest := Manifest{
		CreatedAt:            in.startedAt,
		ID:                   in.id,
		Attempt:              in.prefix.attempt,
		Dialect:              in.sum.Dialect,
		PrimaryInstallations: in.sum.PrimaryInstallations,
		WrapperIDs:           in.sum.WrapperIDs,
		Blobs:                []ManifestBlob{},
		Version:              s.version,
		SQLSHA256:            hex.EncodeToString(in.sha[:]),
		Format:               FormatVersion,
		SchemaVersion:        in.sum.SchemaVersion,
		SQLSize:              in.size,
		RowCounts:            in.sum.RowCounts,
		SQLFormat:            PortableFormat,
		SQLFiles:             in.files,

		InstallationKind: in.kind,
		Unverified:       slices.Clone(in.sum.Unverified),
		Damaged:          slices.Clone(in.damaged),
		RegistryFiles:    RegistryFilesOf(in.sum.RegistryFiles),
	}

	key, err := in.prefix.manifestKey()
	if err != nil {
		return err
	}

	// The manifest goes to the target as a stream, so the blob list of a large
	// backup is never one buffer of the size of the manifest.
	if err := s.putManifest(ctx, key, manifest, in.ordered); err != nil {
		return fmt.Errorf("write manifest of backup %s: %w", in.id, err)
	}

	return nil
}

// putManifest writes the manifest to key as a stream. The bytes are those of
// json.Marshal of the manifest with blobs as its blob list: writeManifestJSON
// encodes one blob entry at a time.
func (s *Service) putManifest(ctx context.Context, key blob.Key, manifest Manifest, blobs []ManifestBlob) error {
	reader, writer := io.Pipe()

	// The channel holds the encoder result, so the goroutine never waits for a
	// receiver.
	errc := make(chan error, 1)

	go func() {
		encErr := writeManifestJSON(writer, manifest, blobs)

		// CloseWithError(nil) is a normal end of the stream.
		_ = writer.CloseWithError(encErr)

		errc <- encErr
	}()

	putErr := s.target.Put(ctx, key, reader)

	// PipeReader.Close always returns nil. It stops the encoder when Put
	// returns before it read the whole stream, so the goroutine never waits
	// for a reader that left.
	closeErr := reader.Close()

	encErr := <-errc

	if putErr != nil {
		return putErr
	}

	// A target that returns nil after a partial read must not make a partial
	// manifest valid: an encoder error, or io.ErrClosedPipe after an early
	// return of Put, fails the write.
	if encErr != nil {
		return fmt.Errorf("encode manifest: %w", encErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close manifest stream: %w", closeErr)
	}

	return nil
}

// manifestWriteBuffer is the size of the buffer of the manifest stream.
const manifestWriteBuffer = 64 << 10

// errManifestShape reports an encoded manifest in which the blob list is not
// where writeManifestJSON expects it. It cannot happen with the Manifest type;
// the check stops a manifest that would be written with a wrong shape.
var errManifestShape = errors.New("the encoded manifest has no empty blob list")

// blobsOpen is the text that json.Marshal writes for the start of a manifest
// whose blob list is empty. writeManifestJSON splits the encoded manifest
// after it.
const blobsOpen = `"blobs":[`

// writeManifestJSON writes the JSON of manifest to w with blobs as its blob
// list, byte for byte as json.Marshal writes it, but with one blob entry in
// memory at a time. The manifest holds no blob list when the call starts: the
// text before the list and the text after it come from one encoding of the
// manifest with an empty list.
func writeManifestJSON(w io.Writer, manifest Manifest, blobs []ManifestBlob) error {
	manifest.Blobs = []ManifestBlob{}

	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	// The first match is the blob list: the keys before it are plain, and the
	// strings in them have escaped quotes.
	at := bytes.Index(data, []byte(blobsOpen+"]"))
	if at < 0 {
		return errManifestShape
	}

	head, tail := data[:at+len(blobsOpen)], data[at+len(blobsOpen):]

	// The buffer joins the small writes of the entries, so a pipe or a network
	// writer sees few large writes.
	out := bufio.NewWriterSize(w, manifestWriteBuffer)

	if _, err := out.Write(head); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	for i := range blobs {
		entry, err := json.Marshal(blobs[i])
		if err != nil {
			return fmt.Errorf("encode manifest blob %d: %w", i, err)
		}

		if i > 0 {
			if err := out.WriteByte(','); err != nil {
				return fmt.Errorf("write manifest: %w", err)
			}
		}

		if _, err := out.Write(entry); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}
	}

	if _, err := out.Write(tail); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if err := out.Flush(); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

// Check reads the manifest of backup id from target and checks that the
// target holds every object it names with the recorded size and checksum: the
// SHA-256 of a table file and the MD5 of a blob. The blob port has no Stat
// and no List, so it Gets each object, counts the bytes and hashes them. It
// returns nil when the backup is complete, else the first object by key that
// is missing, has another size or has another checksum.
func Check(ctx context.Context, target blob.Store, id ID) error {
	if target == nil {
		return fmt.Errorf("check backup %s: %w", id, ErrMissingDependency)
	}

	live, err := readLivePointer(ctx, target, id)
	if err != nil {
		return err
	}

	return checkAttempt(ctx, target, live)
}

// checkAttempt checks the manifest and the objects of the attempt directory p
// as Check does. The finish step calls it before the live pointer exists, so
// it takes the prefix and reads no pointer.
func checkAttempt(ctx context.Context, target blob.Store, p attemptPrefix) error {
	id := p.id

	manifest, err := readManifestAt(ctx, target, p, maxManifestBytes)
	if err != nil {
		return err
	}

	for _, file := range manifest.SQLFiles {
		key, err := p.key(file.Path)
		if err != nil {
			return fmt.Errorf("check backup %s: %w", id, err)
		}

		if err := checkObject(ctx, target, key, tableFileWant(file)); err != nil {
			return err
		}
	}

	for _, ref := range manifest.Blobs {
		key, err := blob.ParseKey("blobs/" + ref.Key)
		if err != nil {
			return fmt.Errorf("check backup %s: %w", id, err)
		}

		if err := checkObject(ctx, target, key, blobWant(ref)); err != nil {
			return err
		}
	}

	return nil
}

// maxManifestBytes bounds the manifest that readManifest reads from a target.
// The write path sets no limit on the number of blob entries, so the bound is
// a fixed size, not a count. A larger object is refused before it is parsed, so
// a hostile or broken target cannot grow the read without bound.
const maxManifestBytes = 64 << 20

// readManifest reads the live pointer of backup id, then the manifest in the
// attempt directory that it names. A backup without a readable pointer or a
// readable manifest of the current format never counts as complete.
func readManifest(ctx context.Context, target blob.Store, id ID) (Manifest, error) {
	live, err := readLivePointer(ctx, target, id)
	if err != nil {
		return Manifest{}, err
	}

	return readManifestAt(ctx, target, live, maxManifestBytes)
}

// readManifestAt reads the manifest of the attempt directory p with the size
// bound limit, so a test can reach the bound with a small limit instead of
// maxManifestBytes. The manifest must name the attempt of p, else it is
// corrupt.
func readManifestAt(ctx context.Context, target blob.Store, p attemptPrefix, limit int64) (Manifest, error) {
	id := p.id

	key, err := p.manifestKey()
	if err != nil {
		return Manifest{}, fmt.Errorf("check backup %s: %w", id, err)
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return Manifest{}, fmt.Errorf("backup %s has no manifest at %q: %w", id, key, ErrCorrupt)
		}

		return Manifest{}, fmt.Errorf("read manifest of backup %s: %w", id, err)
	}

	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()

	if readErr != nil {
		return Manifest{}, fmt.Errorf("read manifest of backup %s: %w", id, readErr)
	}

	if closeErr != nil {
		return Manifest{}, fmt.Errorf("close manifest of backup %s: %w", id, closeErr)
	}

	if int64(len(data)) > limit {
		return Manifest{}, fmt.Errorf("manifest of backup %s is larger than %d bytes: %w", id, limit, ErrCorrupt)
	}

	manifest, err := parseManifest(id, data)
	if err != nil {
		return Manifest{}, err
	}

	if manifest.Attempt != p.attempt {
		return Manifest{}, fmt.Errorf("backup %s holds a manifest of attempt %q in the directory of attempt %q: %w",
			id, manifest.Attempt, p.attempt, ErrCorrupt)
	}

	return manifest, nil
}

// parseManifest decodes the bytes of the manifest of backup id and checks the
// format, the ID, the kind and the file descriptors.
func parseManifest(id ID, data []byte) (Manifest, error) {
	var manifest Manifest

	if err := json.Unmarshal(data, &manifest); err != nil {
		if errors.Is(err, ErrManifestKindInvalid) {
			return Manifest{}, fmt.Errorf("backup %s: %w", id, err)
		}

		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w", id, ErrCorrupt)
	}

	switch {
	case manifest.Format != FormatVersion || manifest.SQLFormat != PortableFormat:
		return Manifest{}, fmt.Errorf(
			"backup %s uses manifest format %d with SQL format %q, this server reads format %d with %q: %w",
			id,
			manifest.Format,
			manifest.SQLFormat,
			FormatVersion,
			PortableFormat,
			ErrCorrupt,
		)
	case manifest.ID != id:
		return Manifest{}, fmt.Errorf("backup %s holds a manifest of %s: %w", id, manifest.ID, ErrCorrupt)
	}

	// The kind is a claim, and an unknown value is not valid. There is no
	// conversion and no default.
	if _, err := installation.ParseKind(string(manifest.InstallationKind)); err != nil {
		return Manifest{}, fmt.Errorf("backup %s: %w", id, ErrManifestKindInvalid)
	}

	// The descriptors name the objects that Check, restore and the key peek
	// read. Check their names here, before any object is opened. The rows of
	// each table file are counted again when the data is staged, in both
	// dialects.
	if _, err := indexPortableDescriptors(manifest.SQLFiles); err != nil {
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w", id, err)
	}

	// Defense in depth: staging counts the rows that it loads and compares
	// them with the row counts. This check stays so that backup check, which
	// loads nothing, refuses a manifest whose totals do not match its table
	// files, and so that a restore refuses it before it opens an object.
	if err := checkManifestPins(manifest); err != nil {
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w", id, err)
	}

	if err := validDamaged(manifest.Damaged); err != nil {
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w: %w", id, err, ErrCorrupt)
	}

	if err := damagedOnce(manifest); err != nil {
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w: %w", id, err, ErrCorrupt)
	}

	return manifest, nil
}
