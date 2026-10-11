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
	"hash"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// FormatVersion is the manifest format number. Check and restore refuse a
// manifest with another number. It changes only with a new ADR.
const FormatVersion = 1

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
// target before they use it. Blobs holds one entry per blob the SQL data
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
}

// ManifestBlob names one copied blob with its size and MD5.
type ManifestBlob struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// Config holds the dependencies of a Service.
type Config struct {
	Exporter Exporter
	Registry Registry
	// Blobs is the live content store the backup copies from.
	Blobs blob.Store
	// Target is the backup target that holds blobs/<key>,
	// backups/<id>/db and backups/<id>/manifest.json.
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
	exporter   Exporter
	registry   Registry
	source     blob.Store
	target     blob.Store
	targetName string
	now        func() time.Time
	version    string
	maxSQL     int64
	mu         sync.Mutex
	running    bool
}

// NewService returns a Service. Exporter, Registry, Blobs and Target are
// required. A nil Now means time.Now; an empty Version means "dev".
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Exporter == nil:
		return nil, fmt.Errorf("%w: exporter", ErrMissingDependency)
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
		registry:   cfg.Registry,
		source:     cfg.Blobs,
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
// backups/<id>/db, copies each blob the SQL data names to blobs/<blob key>,
// and writes backups/<id>/manifest.json last. A blob the target already holds
// is not copied again. It returns ErrAlreadyRunning while another backup
// runs. A failed backup marks its registry row failed with the error text and
// leaves no manifest. It returns the finished registry row.
func (s *Service) Run(ctx context.Context, id ID) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, fmt.Errorf("run backup %s: %w", id, err)
	}

	if err := s.claim(); err != nil {
		return Record{}, fmt.Errorf("run backup %s: %w", id, err)
	}

	defer s.release()

	dbKey, err := blob.ParseKey("backups/" + id.String() + "/db")
	if err != nil {
		return Record{}, fmt.Errorf("run backup %s: %w", id, err)
	}

	manifestKey, err := blob.ParseKey("backups/" + id.String() + "/manifest.json")
	if err != nil {
		return Record{}, fmt.Errorf("run backup %s: %w", id, err)
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
		return Record{}, fmt.Errorf("create backup %s: %w", id, createErr)
	}

	if backupErr := s.backup(ctx, id, startedAt, dbKey, manifestKey); backupErr != nil {
		return Record{}, s.markFailed(ctx, id, backupErr)
	}

	done, err := s.registry.FinishBackup(ctx, id, StateSucceeded, "", s.now().UTC())
	if err != nil {
		return Record{}, fmt.Errorf("finish backup %s: %w", id, err)
	}

	return done, nil
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
	ctx context.Context, id ID, startedAt time.Time, dbKey, manifestKey blob.Key,
) error {
	exported, err := s.exportSQL(ctx, dbKey)
	if err != nil {
		return err
	}

	ordered := orderBlobs(exported.sum.Blobs)

	for _, ref := range ordered {
		if copyErr := s.copyBlob(ctx, ref); copyErr != nil {
			return copyErr
		}
	}

	return s.writeManifest(ctx, manifestInput{
		startedAt: startedAt,
		id:        id,
		sum:       exported.sum,
		ordered:   ordered,
		size:      exported.size,
		sha:       exported.sha,
		key:       manifestKey,
	})
}

// orderBlobs returns the references ordered by key, so copies and the
// manifest follow a stable order whatever order the export returns.
func orderBlobs(refs []BlobRef) []BlobRef {
	ordered := slices.Clone(refs)
	slices.SortFunc(ordered, func(a, b BlobRef) int { return strings.Compare(a.Key, b.Key) })

	return ordered
}

// sqlExport holds the summary of an export with the size and SHA-256 of the
// stored SQL part.
type sqlExport struct {
	sum  Summary
	size int64
	sha  [sha256.Size]byte
}

// exportSQL runs the export in one read transaction while the target reads
// the bytes, so the SQL part never sits in memory whole. It returns the
// summary with the size and SHA-256 of the stored bytes.
func (s *Service) exportSQL(ctx context.Context, key blob.Key) (sqlExport, error) {
	reader, writer := io.Pipe()

	type outcome struct {
		sum  Summary
		size int64
		sha  [sha256.Size]byte
		err  error
	}

	finished := make(chan outcome, 1)

	go func() {
		counter := &sizeHashWriter{hash: sha256.New(), limit: s.maxSQL, size: 0}
		sum, exportErr := s.exporter.Export(ctx, io.MultiWriter(writer, counter))

		var sha [sha256.Size]byte

		copy(sha[:], counter.hash.Sum(nil))
		closeErr := writer.CloseWithError(exportErr)
		finished <- outcome{sum: sum, size: counter.size, sha: sha, err: errors.Join(exportErr, closeErr)}
	}()

	putErr := s.target.Put(ctx, key, reader)
	if putErr != nil {
		// The target failed without draining the reader. Close it so the
		// blocked export stops on its next Write instead of hanging the run.
		reader.CloseWithError(putErr) //nolint:gosec // PipeReader.CloseWithError always returns nil.
	}

	res := <-finished

	if res.err != nil {
		return sqlExport{}, errors.Join(fmt.Errorf("export SQL data: %w", res.err), putErr)
	}

	if putErr != nil {
		return sqlExport{}, fmt.Errorf("write backup database: %w", putErr)
	}

	return sqlExport{sum: res.sum, size: res.size, sha: res.sha}, nil
}

// sizeHashWriter counts and hashes the SQL bytes while they stream to the
// target. It fails above limit, so an oversized SQL part stops the export
// with ErrTooLarge instead of filling the target.
type sizeHashWriter struct {
	hash  hash.Hash
	limit int64
	size  int64
}

func (w *sizeHashWriter) Write(p []byte) (int, error) {
	if w.limit > 0 && w.size+int64(len(p)) > w.limit {
		return 0, fmt.Errorf(
			"%w: SQL part of %d bytes is over the target limit of %d bytes",
			ErrTooLarge,
			w.size+int64(len(p)),
			w.limit,
		)
	}

	if _, err := w.hash.Write(p); err != nil {
		return 0, fmt.Errorf("hash SQL part: %w", err)
	}

	w.size += int64(len(p))

	return len(p), nil
}

// copyBlob copies one blob the SQL data names from the live store to the
// target. It checks the bytes against the size and MD5 in the SQL data before
// it writes, so a mismatch leaves the target unchanged. A blob the target
// already holds keeps its first copy.
func (s *Service) copyBlob(ctx context.Context, ref BlobRef) error {
	source, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("backup blob key %q: %w", ref.Key, err)
	}

	dest, err := blob.ParseKey("blobs/" + ref.Key)
	if err != nil {
		return fmt.Errorf("backup target key of %q: %w", ref.Key, err)
	}

	reader, err := s.source.Get(ctx, source)
	if err != nil {
		return fmt.Errorf("read blob %q: %w", ref.Key, err)
	}

	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()

	if readErr != nil {
		return fmt.Errorf("read blob %q: %w", ref.Key, readErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close blob %q: %w", ref.Key, closeErr)
	}

	switch {
	case int64(len(data)) != ref.Size:
		return fmt.Errorf(
			"%w: blob %q holds %d bytes, the SQL data names %d",
			ErrCorrupt,
			ref.Key,
			len(data),
			ref.Size,
		)
	case md5.Sum(data) != ref.MD5: //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.
		return fmt.Errorf("%w: blob %q does not match the MD5 in the SQL data", ErrCorrupt, ref.Key)
	}

	if err := s.target.Put(ctx, dest, bytes.NewReader(data)); err != nil {
		if errors.Is(err, blob.ErrExists) {
			return nil
		}

		return fmt.Errorf("copy blob %q: %w", ref.Key, err)
	}

	return nil
}

// manifestInput groups the manifest fields writeManifest needs.
type manifestInput struct {
	startedAt time.Time
	id        ID
	sum       Summary
	ordered   []BlobRef
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
	}

	for _, ref := range in.ordered {
		manifest.Blobs = append(manifest.Blobs, ManifestBlob{
			Key:  ref.Key,
			Size: ref.Size,
			MD5:  hex.EncodeToString(ref.MD5[:]),
		})
	}

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

	dbKey, err := blob.ParseKey("backups/" + id.String() + "/db")
	if err != nil {
		return fmt.Errorf("check backup %s: %w", id, err)
	}

	if err := checkObject(ctx, target, dbKey, manifest.SQLSize); err != nil {
		return err
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
		return Manifest{}, fmt.Errorf("backup %s has a corrupt manifest: %w", id, ErrCorrupt)
	}

	switch {
	case manifest.Format != FormatVersion:
		return Manifest{}, fmt.Errorf(
			"backup %s uses manifest format %d, this server reads %d: %w",
			id,
			manifest.Format,
			FormatVersion,
			ErrCorrupt,
		)
	case manifest.ID != id:
		return Manifest{}, fmt.Errorf("backup %s holds a manifest of %s: %w", id, manifest.ID, ErrCorrupt)
	default:
		return manifest, nil
	}
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
