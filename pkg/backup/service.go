package backup

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
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
	// ErrTooLarge reports a SQL part over the target object limit.
	ErrTooLarge = errors.New("backup SQL part too large")
)

// Manifest is the last object a backup writes: backups/<id>/manifest.json. It
// names every object the backup holds, so Check and restore can verify the
// target before they use it. SQLFiles holds one descriptor for each table file
// of the SQL part, in the order of Tables (ADR 42, decision 4). SQLSize is the
// sum of their sizes, and SQLSHA256 is the SHA-256 of their JSON, both as the
// pins of a restore compare them. Blobs holds one entry per blob the SQL data
// names, ordered by key. SQLSHA256 and each blob MD5 are lowercase hex. The
// JSON keys are stable: Check and restore parse them, so a new field needs a
// new optional key, never a rename.
type Manifest struct {
	CreatedAt     time.Time      `json:"createdAt"`
	ID            ID             `json:"id"`
	Dialect       Dialect        `json:"dialect"`
	HomeRegions   []string       `json:"homeRegions"`
	WrapperIDs    []string       `json:"wrapperIds"`
	Blobs         []ManifestBlob `json:"blobs"`
	Version       string         `json:"version"`
	SQLSHA256     string         `json:"sqlSha256"`
	Format        int            `json:"format"`
	SchemaVersion int            `json:"schemaVersion"`
	SQLSize       int64          `json:"sqlSize"`
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
	// backups/<id>/sql/<table>.jsonl.gz and backups/<id>/manifest.json.
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
	mu         sync.Mutex
	running    bool
}

// StateOpener opens sealed OpenTofu state blobs of the live store for the
// backup copy check. The backup checks the plain bytes against the size and
// MD5 the SQL data names and stores the sealed bytes, so plain state never
// reaches the backup target.
type StateOpener interface {
	// OpenStateBlob returns the plain content of the sealed blob under key.
	// The caller closes it. A failure wraps ErrCorrupt or reports a missing
	// blob, never a key, a scope or content.
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
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	version := cfg.Version
	if version == "" {
		version = "dev"
	}

	return &Service{
		exporter:   cfg.Exporter,
		kinds:      cfg.Kinds,
		registry:   cfg.Registry,
		source:     cfg.Blobs,
		opener:     cfg.StateOpener,
		target:     cfg.Target,
		targetName: cfg.TargetName,
		now:        now,
		version:    version,
		maxSQL:     cfg.MaxSQLBytes,
		mu:         sync.Mutex{},
		running:    false,
	}, nil
}

// Run captures the SQL data in one read transaction, writes it to
// backups/<id>/sql/<table>.jsonl.gz, copies each blob the SQL data names to blobs/<blob key>,
// and writes backups/<id>/manifest.json last. A blob the target already holds
// is not copied again. It returns ErrAlreadyRunning while another backup
// runs. A failed backup marks its registry row failed with the error text and
// leaves no manifest. It returns the finished registry row. Run blocks until
// the backup ends; Start begins one in the background instead.
func (s *Service) Run(ctx context.Context, id ID) (Record, error) {
	started, err := s.begin(ctx, id)
	if err != nil {
		return Record{}, err
	}

	defer s.release()

	if backupErr := s.backup(ctx, id, started.startedAt, started.manifestKey); backupErr != nil {
		return Record{}, s.markFailed(ctx, id, backupErr)
	}

	done, err := s.registry.FinishBackup(ctx, id, StateSucceeded, "", s.now().UTC())
	if err != nil {
		return Record{}, fmt.Errorf("finish backup %s: %w", id, err)
	}

	return done, nil
}

// Start begins backup id in the background and returns at once, so an HTTP
// handler answers 202 before the backup ends. It claims the single backup
// slot and stores id as a running row before it returns, so a second Start
// while one runs answers ErrAlreadyRunning and a GET sees the row at once.
// The background work detaches from ctx: a canceled request never stops the
// backup. The result lands in the registry row, where Get and ReadManifest
// show it: callers poll until the row leaves running.
func (s *Service) Start(ctx context.Context, id ID) error {
	started, err := s.begin(ctx, id)
	if err != nil {
		return err
	}

	go s.finish(context.WithoutCancel(ctx), id, started)

	return nil
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

// Check checks that the configured target holds every object the manifest
// of backup id names with the recorded size. It returns nil when the backup
// is complete, else the first missing or short object by key wrapping
// ErrCorrupt. The installation API answers it, so the command reads no
// target.
func (s *Service) Check(ctx context.Context, id ID) error {
	if err := Check(ctx, s.target, id); err != nil {
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

// startedBackup holds the keys and start time of a claimed backup row. Run
// uses them at once; Start hands them to its background work.
type startedBackup struct {
	manifestKey blob.Key
	startedAt   time.Time
}

// begin claims the single backup slot and stores id as a running row,
// returning the keys and start time the backup writes. The caller holds the
// slot: Run releases it when it returns, Start when its background work ends.
// A begin error holds no slot, so the caller releases nothing.
func (s *Service) begin(ctx context.Context, id ID) (startedBackup, error) {
	if err := ctx.Err(); err != nil {
		return startedBackup{}, fmt.Errorf("begin backup %s: %w", id, err)
	}

	if err := s.claim(); err != nil {
		return startedBackup{}, fmt.Errorf("begin backup %s: %w", id, err)
	}

	manifestKey, err := blob.ParseKey("backups/" + id.String() + "/manifest.json")
	if err != nil {
		s.release()

		return startedBackup{}, fmt.Errorf("begin backup %s: %w", id, err)
	}

	startedAt := s.now().UTC()

	create := Record{
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		ID:        id,
		Target:    s.targetName,
		Error:     "",
		State:     StateRunning,
	}

	if createErr := s.registry.CreateBackup(ctx, create); createErr != nil {
		s.release()

		return startedBackup{}, fmt.Errorf("create backup %s: %w", id, createErr)
	}

	return startedBackup{manifestKey: manifestKey, startedAt: startedAt}, nil
}

// finish completes a backup that Start began: it exports the SQL part, copies
// the named blobs, writes the manifest last, and records the result in the
// registry row. It releases the slot when the work ends. Errors land in the
// row, where Get shows them to the operator.
func (s *Service) finish(ctx context.Context, id ID, started startedBackup) {
	backupErr := s.backup(ctx, id, started.startedAt, started.manifestKey)

	// The slot goes free before the row records the result, so a client
	// that reads the final state can start the next backup at once. The
	// slot only guards the work above; the row write touches no object of
	// this backup.
	s.release()

	if backupErr != nil {
		// Background result: the registry row records the outcome, and a
		// finish failure leaves the row running for the next server start
		// to mark failed.
		_ = s.markFailed(ctx, id, backupErr) //nolint:errcheck // The row is the record.

		return
	}

	// Background result, as above.
	end := s.now().UTC()
	_, _ = s.registry.FinishBackup(ctx, id, StateSucceeded, "", end) //nolint:errcheck // The row is the record.
}

// markFailed marks backup id failed with the error text. It returns the
// backup error, joined with the finish error when marking fails too.
func (s *Service) markFailed(ctx context.Context, id ID, backupErr error) error {
	_, finishErr := s.registry.FinishBackup(ctx, id, StateFailed, backupErr.Error(), s.now().UTC())
	if finishErr != nil {
		return errors.Join(backupErr, fmt.Errorf("mark backup %s failed: %w", id, finishErr))
	}

	return backupErr
}

// claim takes the single backup slot, or reports ErrAlreadyRunning.
func (s *Service) claim() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return ErrAlreadyRunning
	}

	s.running = true

	return nil
}

// release frees the single backup slot.
func (s *Service) release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.running = false
}

// backup exports the SQL part, copies the named blobs, and writes the
// manifest last, so a backup without a manifest never counts as complete.
func (s *Service) backup(
	ctx context.Context, id ID, startedAt time.Time, manifestKey blob.Key,
) error {
	// Read the kind before the first object lands in the target, so a database
	// that claims no kind leaves no file and no manifest.
	kind, err := s.readKind(ctx)
	if err != nil {
		return err
	}

	exported, err := s.exportPortable(ctx, id)
	if err != nil {
		return err
	}

	ordered := orderBlobs(exported.sum.Blobs)

	stored := make([]ManifestBlob, 0, len(ordered))

	for _, ref := range ordered {
		copied, copyErr := s.copyBlob(ctx, ref)
		if copyErr != nil {
			return copyErr
		}

		stored = append(stored, copied)
	}

	return s.writeManifest(ctx, manifestInput{
		startedAt: startedAt,
		kind:      kind,
		id:        id,
		sum:       exported.sum,
		ordered:   stored,
		files:     exported.files,
		size:      exported.size,
		sha:       exported.sha,
		key:       manifestKey,
	})
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

// orderBlobs returns the references ordered by key, so copies and the
// manifest follow a stable order whatever order the export returns.
func orderBlobs(refs []BlobRef) []BlobRef {
	ordered := slices.Clone(refs)
	slices.SortFunc(ordered, func(a, b BlobRef) int { return strings.Compare(a.Key, b.Key) })

	return ordered
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
// backups/<id>/sql, and returns the summary with the descriptors. Each file
// streams into the target while the export writes it, so the SQL part never sits
// in memory whole. A file that fails before it completes is aborted in the
// target, so no partial table file stays there as a good one.
//
// The service finishes every file it opened before it returns, also when the
// exporter returns without a Close or an Abort of a file. An open file keeps
// its target Put waiting on the pipe, so it must never outlive the export.
func (s *Service) exportPortable(ctx context.Context, id ID) (sqlExport, error) {
	var (
		mu     sync.Mutex
		opened []*targetWriter
	)

	sink := func(path string) (io.WriteCloser, error) {
		key, err := blob.ParseKey("backups/" + id.String() + "/" + path)
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
		<-w.done
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
		return ManifestBlob{}, err
	}

	if copied.size != ref.Size || !bytes.Equal(copied.sum, ref.MD5[:]) {
		corrupt := corruptBlobError(ref.Key, copied.size, copied.sum, ref)

		if copied.kept {
			return ManifestBlob{}, corrupt
		}

		if delErr := s.target.Delete(ctx, dest); delErr != nil {
			return ManifestBlob{}, errors.Join(
				corrupt,
				fmt.Errorf("delete bad copy of blob %q: %w", ref.Key, delErr),
			)
		}

		return ManifestBlob{}, corrupt
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
		return ManifestBlob{}, err
	}

	if size != ref.Size || !bytes.Equal(sum, ref.MD5[:]) {
		return ManifestBlob{}, corruptBlobError(ref.Key, size, sum, ref)
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
	counter := &md5SizeWriter{hash: md5.New(), size: 0} //nolint:gosec // The manifest records MD5, for integrity.

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
	counter := &md5SizeWriter{hash: md5.New(), size: 0} //nolint:gosec // The manifest records MD5, for integrity.
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
	size      int64
	sha       [sha256.Size]byte
	key       blob.Key
}

// writeManifest writes backups/<id>/manifest.json last, after the SQL part
// and every blob, so a backup without a manifest never counts as complete.
func (s *Service) writeManifest(ctx context.Context, in manifestInput) error {
	manifest := Manifest{
		CreatedAt:     in.startedAt,
		ID:            in.id,
		Dialect:       in.sum.Dialect,
		HomeRegions:   in.sum.HomeRegions,
		WrapperIDs:    in.sum.WrapperIDs,
		Blobs:         make([]ManifestBlob, 0, len(in.ordered)),
		Version:       s.version,
		SQLSHA256:     hex.EncodeToString(in.sha[:]),
		Format:        FormatVersion,
		SchemaVersion: in.sum.SchemaVersion,
		SQLSize:       in.size,
		RowCounts:     in.sum.RowCounts,
		SQLFormat:     PortableFormat,
		SQLFiles:      in.files,

		InstallationKind: in.kind,
	}

	manifest.Blobs = append(manifest.Blobs, in.ordered...)

	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode manifest of backup %s: %w", in.id, err)
	}

	if err := s.target.Put(ctx, in.key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write manifest of backup %s: %w", in.id, err)
	}

	return nil
}

// Check reads the manifest of backup id from target and checks that the
// target holds every object it names with the recorded size. The blob port
// has no Stat and no List, so it Gets each object and counts the bytes. It
// returns nil when the backup is complete, else the first missing or
// short object by key.
func Check(ctx context.Context, target blob.Store, id ID) error {
	if target == nil {
		return fmt.Errorf("check backup %s: %w", id, ErrMissingDependency)
	}

	manifest, err := readManifest(ctx, target, id)
	if err != nil {
		return err
	}

	for _, file := range manifest.SQLFiles {
		key, err := blob.ParseKey("backups/" + id.String() + "/" + file.Path)
		if err != nil {
			return fmt.Errorf("check backup %s: %w", id, err)
		}

		if err := checkObject(ctx, target, key, file.Size); err != nil {
			return err
		}
	}

	for _, ref := range manifest.Blobs {
		key, err := blob.ParseKey("blobs/" + ref.Key)
		if err != nil {
			return fmt.Errorf("check backup %s: %w", id, err)
		}

		if err := checkObject(ctx, target, key, ref.Size); err != nil {
			return err
		}
	}

	return nil
}

// readManifest reads and parses backups/<id>/manifest.json. A backup without
// a readable manifest of the current format never counts as complete.
func readManifest(ctx context.Context, target blob.Store, id ID) (Manifest, error) {
	key, err := blob.ParseKey("backups/" + id.String() + "/manifest.json")
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

	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()

	if readErr != nil {
		return Manifest{}, fmt.Errorf("read manifest of backup %s: %w", id, readErr)
	}

	if closeErr != nil {
		return Manifest{}, fmt.Errorf("close manifest of backup %s: %w", id, closeErr)
	}

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
	// read. Check their names here, before any object is opened. The row counts
	// are checked when the data is staged, in both dialects.
	if _, err := indexPortableDescriptors(manifest.SQLFiles); err != nil {
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w", id, err)
	}

	return manifest, nil
}

// checkObject checks that the target holds key with wantSize bytes. It Gets
// the object and counts the bytes, because the blob port has no Stat.
func checkObject(ctx context.Context, target blob.Store, key blob.Key, wantSize int64) error {
	reader, err := target.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("backup is incomplete: object %q is missing: %w", key, ErrCorrupt)
		}

		return fmt.Errorf("read backup object %q: %w", key, err)
	}

	size, readErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()

	if readErr != nil {
		return fmt.Errorf("read backup object %q: %w", key, readErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", key, closeErr)
	}

	if size != wantSize {
		return fmt.Errorf(
			"backup is incomplete: object %q holds %d bytes, the manifest names %d: %w",
			key,
			size,
			wantSize,
			ErrCorrupt,
		)
	}

	return nil
}
