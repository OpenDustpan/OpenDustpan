package backup

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/secret"
)

// RestoreLive is the live store ports that a server-side restore needs: the
// check ports of CheckRestore (ADR 38 decision 3), the one-step swap of
// ApplyStaging (decision 6), and the fencing of BumpFailoverVersions
// (decision 7). Every store adapter serves them from one database connection.
type RestoreLive interface {
	CheckStore
	Applier
	Fencer
}

// RestoreMaintenance enters and leaves maintenance mode around the apply
// (ADR 38 decision 4). The server wires the domain service; tests stub the
// two calls.
type RestoreMaintenance interface {
	// Enter moves the installation to maintenance mode. While an update or
	// a run is active it reports maintenance.ErrActiveWork naming each one,
	// unless force cancels them first.
	Enter(ctx context.Context, force bool) (maintenance.EnterResult, error)
	// Leave moves the installation back to normal operation.
	Leave(ctx context.Context) error
}

// RestoreRequest asks for one restore: the backup to apply, whether to check
// only, whether to cancel active work, who asked, and whether to keep the
// current access tables. KeepAccessTables is true when the caller does not
// hold server:restore-access: the restore replaces the other data and keeps
// the current rows of the access tables.
type RestoreRequest struct {
	BackupID         ID
	PreBackupID      ID
	CheckOnly        bool
	Force            bool
	RequestedBy      string
	KeepAccessTables bool
}

// RestoreConfig holds the dependencies of a RestoreService.
type RestoreConfig struct {
	// Live is the live SQL store: checks stage in it, the apply swaps it,
	// and the fence bumps it.
	Live RestoreLive
	// Registry tracks the restores table.
	Registry RestoreRegistry
	// Backups takes the backup of the live data before the apply.
	Backups *Service
	// Maintenance enters and leaves maintenance mode around the apply.
	Maintenance RestoreMaintenance
	// Blobs is the live content store the restore copies into.
	Blobs blob.Store
	// Target is the backup target that holds blobs/<key>,
	// backups/<id>/sql/<table>.jsonl.gz and backups/<id>/manifest.json.
	Target blob.Store
	// Key is the key-encryption key the checks prove against the backup.
	Key secret.Wrapper
	// Region is the region of this installation for the region warning. A
	// blank region disables the warning.
	Region string
	// AfterApply runs after the swap with the rows preserved, while the
	// server still holds maintenance mode, so a restore of data that
	// predates a later change converges to it: the server seals rows that
	// the backup holds unsealed. A failure fails the restore. Nil skips it.
	AfterApply func(ctx context.Context) error
	// Admit runs when a restore starts, before it records the restore. An
	// error refuses the restore: the server refuses a restore while a
	// change of the audit chain waits for an approval. Nil admits every
	// restore.
	Admit  func(ctx context.Context) error
	Now    func() time.Time
	Logger *slog.Logger
}

// RestoreService runs online restores of a whole installation (ADR 38): it
// checks the backup, enters maintenance mode, backs up the live data, copies
// the blobs, swaps the staged SQL data in one step, increments the failover
// version of every namespace, and leaves maintenance mode, also after a
// failure. A check-only request runs the same checks and applies nothing.
type RestoreService struct {
	live        RestoreLive
	registry    RestoreRegistry
	backups     *Service
	maintenance RestoreMaintenance
	blobs       blob.Store
	target      blob.Store
	key         secret.Wrapper
	region      string
	afterApply  func(ctx context.Context) error
	admit       func(ctx context.Context) error
	now         func() time.Time
	logger      *slog.Logger
	mu          sync.Mutex
	running     bool
	// inflight holds the latest known row of each restore this process
	// started and has not finished yet. The swap in applyPinned replaces
	// the live data with staged data that predates the restore row, so
	// the registry loses the row between the swap and recreateAfterSwap.
	// Get serves the held row across that window instead of answering
	// ErrRestoreNotFound.
	inflightMu sync.RWMutex
	inflight   map[RestoreID]RestoreRecord
}

// NewRestoreService returns a RestoreService. Live, Registry, Backups,
// Maintenance, Blobs and Target are required. A nil Now means time.Now; a nil
// Logger discards the restore log line.
func NewRestoreService(cfg RestoreConfig) (*RestoreService, error) {
	switch {
	case cfg.Live == nil:
		return nil, fmt.Errorf("%w: live store", ErrMissingDependency)
	case cfg.Registry == nil:
		return nil, fmt.Errorf("%w: restore registry", ErrMissingDependency)
	case cfg.Backups == nil:
		return nil, fmt.Errorf("%w: backup service", ErrMissingDependency)
	case cfg.Maintenance == nil:
		return nil, fmt.Errorf("%w: maintenance", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	case cfg.Target == nil:
		return nil, fmt.Errorf("%w: target", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &RestoreService{
		live:        cfg.Live,
		registry:    cfg.Registry,
		backups:     cfg.Backups,
		maintenance: cfg.Maintenance,
		blobs:       cfg.Blobs,
		target:      cfg.Target,
		key:         cfg.Key,
		region:      cfg.Region,
		afterApply:  cfg.AfterApply,
		admit:       cfg.Admit,
		now:         now,
		logger:      logger,
		mu:          sync.Mutex{},
		running:     false,
		inflightMu:  sync.RWMutex{},
		inflight:    map[RestoreID]RestoreRecord{},
	}, nil
}

// Start begins restore id in the background and returns at once, so an HTTP
// handler answers 202 before the restore ends. It claims the single restore
// slot and stores id as a checking row before it returns, so a second Start
// while one runs answers ErrRestoreRunning and a GET sees the row at once.
// The background work detaches from ctx: a canceled request never stops the
// restore. The result lands in the registry row, where Get shows it: callers
// poll until the row leaves checking and applying.
func (s *RestoreService) Start(
	ctx context.Context, id RestoreID, req RestoreRequest,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("begin restore %s: %w", id, err)
	}

	if err := s.claim(); err != nil {
		return fmt.Errorf("begin restore %s: %w", id, err)
	}

	releaseOnErr := true

	defer func() {
		if releaseOnErr {
			s.release()
		}
	}()

	if runningErr := s.runningElsewhere(ctx, id); runningErr != nil {
		return fmt.Errorf("begin restore %s: %w", id, runningErr)
	}

	if s.admit != nil {
		if err := s.admit(ctx); err != nil {
			return fmt.Errorf("begin restore %s: %w", id, err)
		}
	}

	create := RestoreRecord{
		StartedAt:   s.now().UTC(),
		EndedAt:     time.Time{},
		ID:          id,
		BackupID:    req.BackupID,
		PreBackupID: "",
		RequestedBy: req.RequestedBy,
		Error:       "",
		State:       RestoreChecking,
	}

	if createErr := s.registry.CreateRestore(ctx, create); createErr != nil {
		return fmt.Errorf("create restore %s: %w", id, createErr)
	}

	s.trackRestore(create)

	if winnerErr := s.yieldToEarlierRunner(ctx, create); winnerErr != nil {
		_, _ = s.registry.FinishRestore( //nolint:errcheck // Loser row records its own refusal.
			ctx, id, RestoreFailed, sanitizeRestoreError(winnerErr), s.now().UTC(),
		)
		s.untrackRestore(id)

		return fmt.Errorf("begin restore %s: %w", id, winnerErr)
	}

	releaseOnErr = false

	go s.finish(context.WithoutCancel(ctx), id, req)

	return nil
}

// Get returns the restore row id, or ErrRestoreNotFound. While the swap in
// applyPinned hides the registry row between the swap and recreateAfterSwap,
// Get serves the row this process holds for the running restore, so polling
// never sees a transient 404.
func (s *RestoreService) Get(ctx context.Context, id RestoreID) (RestoreRecord, error) {
	record, err := s.registry.GetRestore(ctx, id)
	if err == nil {
		return record, nil
	}

	if held, ok := s.inflightRecord(id); ok && errors.Is(err, ErrRestoreNotFound) {
		return held, nil
	}

	return RestoreRecord{}, fmt.Errorf("get restore %s: %w", id, err)
}

// ReadManifest returns the manifest of backup id from the target. A backup
// without a readable manifest reports ErrCorrupt: only a complete backup
// counts.
func (s *RestoreService) ReadManifest(ctx context.Context, id ID) (Manifest, error) {
	manifest, err := s.backups.ReadManifest(ctx, id)
	if err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// List returns every restore row, newest first, from the registry.
func (s *RestoreService) List(ctx context.Context) ([]RestoreRecord, error) {
	records, err := s.registry.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}

	return records, nil
}

// Running reports whether a restore runs: one that this service started
// and that did not end, or a row of the registry that is still checking or
// applying, from this instance or from another one.
func (s *RestoreService) Running(ctx context.Context) (bool, error) {
	s.mu.Lock()
	local := s.running
	s.mu.Unlock()

	if local {
		return true, nil
	}

	err := s.runningElsewhere(ctx, "")
	if errors.Is(err, ErrRestoreRunning) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("list restores: %w", err)
	}

	return false, nil
}

// claim takes the single restore slot, or reports ErrRestoreRunning.
func (s *RestoreService) claim() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return ErrRestoreRunning
	}

	s.running = true

	return nil
}

// release frees the single restore slot.
func (s *RestoreService) release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.running = false
}

// trackRestore remembers the latest known row of a running restore, so Get
// can serve it while the swap hides the registry row.
func (s *RestoreService) trackRestore(record RestoreRecord) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()

	if s.inflight == nil {
		s.inflight = map[RestoreID]RestoreRecord{}
	}

	s.inflight[record.ID] = record
}

// untrackRestore forgets a restore that reached its end state in the
// registry, so later reads come from the stored row.
func (s *RestoreService) untrackRestore(id RestoreID) {
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()

	delete(s.inflight, id)
}

// inflightRecord returns the held row of a running restore, if this process
// started it and has not finished it yet.
func (s *RestoreService) inflightRecord(id RestoreID) (RestoreRecord, bool) {
	s.inflightMu.RLock()
	defer s.inflightMu.RUnlock()

	record, ok := s.inflight[id]

	return record, ok
}

// runningElsewhere reports ErrRestoreRunning when the registry already
// holds a checking or applying row. The in-memory claim stops two starts on
// one process; this check stops a start on one process while another process
// runs one, because both share the same database.
func (s *RestoreService) runningElsewhere(ctx context.Context, _ RestoreID) error {
	records, err := s.registry.ListRestores(ctx)
	if err != nil {
		return err
	}

	for i := range records {
		if records[i].State == RestoreChecking || records[i].State == RestoreApplying {
			return ErrRestoreRunning
		}
	}

	return nil
}

// yieldToEarlierRunner reports ErrRestoreRunning when a concurrent Start won
// the slot: after CreateRestore, two starters can both hold a checking row,
// so the later starter (by start time, then ID) yields to the earlier one.
// The loser marks its own row failed; the winner keeps running.
func (s *RestoreService) yieldToEarlierRunner(ctx context.Context, own RestoreRecord) error {
	records, err := s.registry.ListRestores(ctx)
	if err != nil {
		return err
	}

	for i := range records {
		record := records[i]
		if record.ID == own.ID {
			continue
		}

		if record.State != RestoreChecking && record.State != RestoreApplying {
			continue
		}

		if record.StartedAt.Before(own.StartedAt) {
			return ErrRestoreRunning
		}

		if record.StartedAt.Equal(own.StartedAt) && record.ID < own.ID {
			return ErrRestoreRunning
		}
	}

	return nil
}

// finish runs a restore that Start began: checks, maintenance mode, backup
// of the live data, blob copy, the one-step swap, fencing, and the leave.
// The slot goes free before the row reaches its end state, while the row
// still reads checking or applying: a client that polls GET to the end state
// and starts the next restore at once never meets the stale in-memory flag,
// and a start that arrives before the row finishes is still refused through
// the registry. Errors land in the row as sanitized text, where Get shows them
// to the operator; the sanitized detail goes to the server log. It leaves
// maintenance mode when the restore ends, also after a failure.
func (s *RestoreService) finish(ctx context.Context, id RestoreID, req RestoreRequest) {
	restoreErr := s.restore(ctx, id, req)
	end := s.now().UTC()

	// The slot frees first, so a client that read the end state starts the
	// next restore at once; the registry still refuses an early start.
	s.release()

	if restoreErr != nil {
		// Background result: the registry row records the outcome, and a
		// finish failure leaves the row running for the next server start
		// to mark failed. When the swap hid the row and the recreate never
		// ran, FinishRestore reports not found: create the failed row so
		// the restore stays visible.
		text := sanitizeRestoreError(restoreErr)
		if _, finishErr := s.registry.FinishRestore(ctx, id, RestoreFailed, text, end); finishErr != nil &&
			errors.Is(finishErr, ErrRestoreNotFound) {
			s.createFinishedRow(
				ctx,
				restoreOutcome{id: id, req: req, state: RestoreFailed, errText: text, full: nil},
				end,
			)
		}

		s.untrackRestore(id)
		s.logRestore(ctx, restoreOutcome{id: id, req: req, state: RestoreFailed, errText: text, full: restoreErr})

		return
	}

	// Background result, as above.
	if _, finishErr := s.registry.FinishRestore(ctx, id, RestoreSucceeded, "", end); finishErr != nil &&
		errors.Is(finishErr, ErrRestoreNotFound) {
		s.createFinishedRow(ctx, restoreOutcome{id: id, req: req, state: RestoreSucceeded, errText: "", full: nil}, end)
	}

	s.untrackRestore(id)
	s.logRestore(ctx, restoreOutcome{id: id, req: req, state: RestoreSucceeded, errText: "", full: nil})
}

// createFinishedRow stores the end state of out.id when the swap hid the row
// and the recreate never ran, so FinishRestore had no row to finish. The
// held row, when present, keeps the start time, the backup, the pre-backup
// and the requester. Best effort: the row is the record, and a failed create
// leaves the held row for the next server start to mark failed.
func (s *RestoreService) createFinishedRow(ctx context.Context, out restoreOutcome, end time.Time) {
	record := RestoreRecord{
		StartedAt:   end,
		EndedAt:     end,
		ID:          out.id,
		BackupID:    out.req.BackupID,
		PreBackupID: out.req.PreBackupID,
		RequestedBy: out.req.RequestedBy,
		Error:       out.errText,
		State:       out.state,
	}

	if held, ok := s.inflightRecord(out.id); ok {
		record.StartedAt = held.StartedAt
		record.BackupID = held.BackupID
		record.PreBackupID = held.PreBackupID
		record.RequestedBy = held.RequestedBy
	}

	_ = s.registry.CreateRestore(ctx, record) //nolint:errcheck // The row is the record.
}

// restoreOutcome carries one finished restore to the log line.
type restoreOutcome struct {
	id      RestoreID
	req     RestoreRequest
	state   RestoreState
	errText string
	full    error
}

// sanitizeRestoreError maps a restore failure to the stable text stored in
// the row and returned by GET and List. Stored text never holds driver detail,
// a capability path, a token, a scope string, or a key fingerprint: only the
// sentinel decides the message. The server log keeps the full error.
func sanitizeRestoreError(err error) string {
	if text, ok := secretRestoreMessage(err); ok {
		return text
	}

	switch {
	case errors.Is(err, ErrRestoreRunning):
		return ErrRestoreRunning.Error()
	case errors.Is(err, ErrRestoreAccessMismatch):
		return ErrRestoreAccessMismatch.Error()
	case errors.Is(err, ErrCorrupt):
		return ErrCorrupt.Error()
	case errors.Is(err, ErrSchemaBeforeBaseline):
		return ErrSchemaBeforeBaseline.Error()
	case errors.Is(err, ErrSchemaMismatch):
		return ErrSchemaMismatch.Error()
	case errors.Is(err, maintenance.ErrActiveWork):
		return activeWorkMessage(err)
	case errors.Is(err, ErrNotFound):
		return ErrNotFound.Error()
	case errors.Is(err, ErrRestoreNotFound):
		return ErrRestoreNotFound.Error()
	default:
		return "restore failed"
	}
}

// activeWorkMessage answers the stored text for a refused maintenance
// enter: the sentinel with the unit names it carries, without any wrapping
// prefix. Wrapping can add driver text (for example a store failure), but
// the unit names after the sentinel are safe to show and tell the operator
// what to finish or cancel first.
func activeWorkMessage(err error) string {
	const sentinel = "cannot enter maintenance mode while work is active"

	msg := err.Error()

	if i := strings.Index(msg, sentinel); i >= 0 {
		return msg[i:]
	}

	return maintenance.ErrActiveWork.Error()
}

// secretRestoreMessage maps key-encryption-key failures to stable text that
// names the DUSTPAN_KEY_ENCRYPTION_KEY setting but no key material. It
// reports whether err is such a failure.
func secretRestoreMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, secret.ErrKeyNotConfigured):
		return "restore backup needs a key for its wrappers: " +
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key", true
	case errors.Is(err, secret.ErrWrapperMismatch):
		return "restore backup key does not match its wrappers: " +
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key", true
	case errors.Is(err, secret.ErrDecrypt):
		return "restore backup key does not open the backup: " +
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key", true
	default:
		return "", false
	}
}

// logRestore writes one log line for a finished restore (issue 140): the
// restore ID, the backup ID, the backup taken before it, who asked, whether
// the access tables were replaced or kept, and the end state. It holds no
// secret and no driver text. The sanitized detail of the failure goes to a
// second error line, so the operator can tell which table, blob or check
// failed from the log alone.
func (s *RestoreService) logRestore(ctx context.Context, out restoreOutcome) {
	record, getErr := s.registry.GetRestore(ctx, out.id)
	pre := out.req.PreBackupID.String()

	if getErr == nil && record.PreBackupID != "" {
		pre = record.PreBackupID.String()
	}

	accessMode := "replaced"
	if out.req.KeepAccessTables {
		accessMode = "kept"
	}

	attrs := []slog.Attr{
		slog.String("restore_id", out.id.String()),
		slog.String("backup_id", out.req.BackupID.String()),
		slog.String("requested_by", out.req.RequestedBy),
		slog.String("access_tables", accessMode),
		slog.String("state", string(out.state)),
	}

	if pre != "" {
		attrs = append(attrs, slog.String("pre_backup_id", pre))
	}

	if out.errText != "" {
		attrs = append(attrs, slog.String("error", out.errText))
	}

	s.logger.LogAttrs(ctx, slog.LevelInfo, "restore", attrs...)

	if out.full != nil {
		s.logger.ErrorContext(
			ctx,
			"restore failed",
			slog.String("restore_id", out.id.String()),
			slog.String("error", sanitizeLogError(out.full)),
		)
	}
}

// logObjectPath matches a quoted backup-target object path, for example
// "backups/bkp-1/manifest.json" or "blobs/pulumi/stk-1/upd-1/0": storage layout, not
// data. Blob content keys without the target prefix stay, so the log still
// names the blob that failed.
var logObjectPath = regexp.MustCompile(`"[^"]*(?:backups|blobs)/[^"]*"`)

// logToken matches a token value after the word token. Our own messages
// never print one, but a wrapped foreign error may carry it.
var logToken = regexp.MustCompile(`(?i)\btoken\s+("[^"]*"|\S+)`)

// sanitizeLogError renders err for the server log: our own wrapping context
// with object paths and tokens redacted, down to the first foreign leaf,
// which never reaches the log. Known sentinels stay: they name the cause
// without driver detail. The row keeps the shorter stable text from
// sanitizeRestoreError; the log detail names which table, blob or check
// failed, so the operator can tell from the log alone.
func sanitizeLogError(err error) string {
	if err == nil {
		return ""
	}

	var parts []string

	for _, frame := range flattenLogFrames(err) {
		if frame.leaf && !isLogSentinel(frame.value) {
			continue
		}

		text := redactLogFrame(frame.text)
		if text == "" {
			continue
		}

		if len(parts) > 0 && parts[len(parts)-1] == text {
			continue
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, ": ")
}

// logFrame is one error of the flattened cause chain: its own text, whether
// it wraps nothing further, and the value for the sentinel check.
type logFrame struct {
	value error
	text  string
	leaf  bool
}

// flattenLogFrames flattens err into its wrapping messages, outermost first.
// Each frame holds only its own text: for `fmt.Errorf("outer: %w", inner)`
// the frame is "outer". Joined errors contribute each branch in order, since
// a join carries no text of its own.
func flattenLogFrames(err error) []logFrame {
	var out []logFrame

	var walk func(e error)
	walk = func(e error) {
		if e == nil {
			return
		}

		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				walk(inner)
			}

			return
		}

		if single, ok := e.(interface{ Unwrap() error }); ok {
			if inner := single.Unwrap(); inner != nil {
				out = append(out, logFrame{
					value: e,
					text:  ownFrameText(e.Error(), inner.Error()),
					leaf:  false,
				})

				walk(inner)

				return
			}
		}

		out = append(out, logFrame{value: e, text: e.Error(), leaf: true})
	}

	walk(err)

	return out
}

// ownFrameText returns the text full adds around inner: the wrapping context
// without the cause it wraps. Every wrap in this package ends with the cause,
// so a trailing cut keeps the context.
func ownFrameText(full, inner string) string {
	if inner == "" {
		return full
	}

	if rest, ok := strings.CutSuffix(full, inner); ok {
		return strings.TrimRight(rest, ": ")
	}

	return full
}

// redactLogFrame redacts capability paths and tokens from one frame of
// restore context. Unit references, table names, backup IDs and blob content
// keys stay: the operator needs them to tell what failed.
func redactLogFrame(text string) string {
	text = logObjectPath.ReplaceAllString(text, `"[backup object]"`)
	text = logToken.ReplaceAllString(text, "token [redacted]")

	return strings.TrimSpace(text)
}

// isLogSentinel reports whether err is a stable domain or context value that
// the server log may name. Anything else at the end of a cause chain may
// hold driver text, so the log drops it.
func isLogSentinel(err error) bool {
	switch {
	case errors.Is(err, ErrCorrupt),
		errors.Is(err, ErrSchemaMismatch),
		errors.Is(err, ErrSchemaBeforeBaseline),
		errors.Is(err, ErrNotFound),
		errors.Is(err, ErrAlreadyExists),
		errors.Is(err, ErrNotRunning),
		errors.Is(err, ErrAlreadyRunning),
		errors.Is(err, ErrTooLarge),
		errors.Is(err, ErrMissingDependency),
		errors.Is(err, ErrRestoreNotFound),
		errors.Is(err, ErrRestoreExists),
		errors.Is(err, ErrRestoreNotRunning),
		errors.Is(err, ErrRestoreRunning),
		errors.Is(err, ErrRestoreAccessMismatch),
		errors.Is(err, maintenance.ErrActiveWork),
		errors.Is(err, maintenance.ErrUnavailable),
		errors.Is(err, maintenance.ErrInvalidState),
		errors.Is(err, maintenance.ErrStateChanged),
		errors.Is(err, secret.ErrDecrypt),
		errors.Is(err, secret.ErrInvalidScope),
		errors.Is(err, secret.ErrKeyNotFound),
		errors.Is(err, secret.ErrTooLarge),
		errors.Is(err, secret.ErrMissingDependency),
		errors.Is(err, secret.ErrKeyNotConfigured),
		errors.Is(err, secret.ErrWrapperMismatch),
		errors.Is(err, secret.ErrInvalidKey),
		errors.Is(err, blob.ErrNotFound),
		errors.Is(err, blob.ErrInvalidKey),
		errors.Is(err, blob.ErrExists),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return true
	default:
		return false
	}
}

// restore runs the restore order of ADR 38 decisions 3 to 9: checks,
// maintenance mode (canceling active work with force), backup of the live
// data, blob copy, the one-step swap, fencing, and the leave. A failed check
// or a refused enter stops the restore and leaves the live data as it was,
// with no backup taken. A check-only request runs the same checks and applies
// nothing.
func (s *RestoreService) restore(ctx context.Context, id RestoreID, req RestoreRequest) error {
	pinned, pinErr := s.pinManifest(ctx, req.BackupID)
	if pinErr != nil {
		return pinErr
	}

	checkInput := CheckInput{
		Target: s.target,
		Live:   s.live,
		Key:    s.key,
		ID:     req.BackupID,
		Region: s.region,
	}

	if _, _, checkErr := CheckRestore(ctx, checkInput); checkErr != nil {
		return checkErr
	}

	if req.CheckOnly {
		return nil
	}

	if _, enterErr := s.maintenance.Enter(ctx, req.Force); enterErr != nil {
		return fmt.Errorf("enter maintenance mode for restore %s: %w", id, enterErr)
	}

	// The server leaves maintenance mode when the restore ends, also after
	// a failure: the deferred leave runs before the error returns.
	defer func() {
		_ = s.maintenance.Leave(context.WithoutCancel(ctx)) //nolint:errcheck // Best-effort leave.
	}()

	// The pre-backup runs after Enter, so a forced restore backs up the live
	// data with the active work already canceled (ADR 38 decisions 5 and 8):
	// the undo restores the state the restore actually replaced, and a
	// refused restore takes no backup at all.
	preID := req.PreBackupID
	if preID == "" {
		preID = ID(ident.NewID("bkp-"))
	}

	pre, backupErr := s.backups.Run(ctx, preID)
	if backupErr != nil {
		return fmt.Errorf("backup live data before restore %s: %w", id, backupErr)
	}

	if markErr := s.registry.MarkRestoreApplying(ctx, id, pre.ID); markErr != nil {
		return fmt.Errorf("mark restore %s applying: %w", id, markErr)
	}

	// Remember the applying row before the swap hides it: Get serves the
	// held row while the registry loses it.
	if marked, getErr := s.registry.GetRestore(ctx, id); getErr == nil {
		s.trackRestore(marked)
	}

	return s.applyPinned(ctx, id, req, pinned, pre)
}

// applyPinned stages the pinned backup, copies its blobs, swaps the staged
// data in one fenced step, fails the running rows the swap resurrected, and
// preserves the restore row and the pre-backup row across the swap. The
// manifest is pinned again first, so a target mutated after the checks cannot
// be applied. When req keeps the access tables, the swap keeps the current
// rows of the access tables and replaces the other tables.
func (s *RestoreService) applyPinned(
	ctx context.Context, id RestoreID, req RestoreRequest, pinned pinnedManifest, pre Record,
) error {
	backupID := req.BackupID
	if repinErr := s.repinManifest(ctx, backupID, pinned); repinErr != nil {
		return repinErr
	}

	if stageErr := s.stage(ctx, backupID); stageErr != nil {
		return fmt.Errorf("stage backup %s for restore %s: %w", backupID, id, stageErr)
	}

	// The staging area goes away with the swap; a failed apply discards it
	// here, so no staging area survives the restore either way.
	defer func() {
		_ = s.live.DiscardStaging(context.WithoutCancel(ctx)) //nolint:errcheck // Best-effort cleanup.
	}()

	if copyErr := s.copyBlobs(ctx, backupID); copyErr != nil {
		return fmt.Errorf("copy blobs for restore %s: %w", id, copyErr)
	}

	// The swap replaces the live data with the staged backup, which predates
	// the restore row and the pre-backup row: both rows go away with the old
	// live data. The records below preserve them across the swap, so both
	// outlive the apply.
	before, err := s.registry.GetRestore(ctx, id)
	if err != nil {
		return fmt.Errorf("read restore %s before the swap: %w", id, err)
	}

	// Hold the row across the swap: the apply below drops the registry row,
	// and Get serves the held row until recreateAfterSwap stores it again.
	s.trackRestore(before)

	if err := s.applyStaged(ctx, id, req); err != nil {
		return err
	}

	// The swap replaced the live data. A later step that fails still runs
	// the after-apply step, which the restored data needs, and the restore
	// fails with both errors.
	if err := s.afterSwap(ctx, id, before, pre); err != nil {
		return errors.Join(err, s.runAfterApply(ctx, id))
	}

	// The audit entry for the start went away with the old live data: the
	// swap replaced audit_entries from the backup. Write the entry after the
	// swap, in the restored data, so it survives. The access mode lives in
	// the existing object field: no new column. A lost entry does not fail
	// the restore.
	s.appendRestoreAudit(ctx, id, req)

	return s.runAfterApply(ctx, id)
}

// afterSwap fails the running rows that the swap resurrected and preserves
// the restore row and the pre-backup row across the swap.
func (s *RestoreService) afterSwap(ctx context.Context, id RestoreID, before RestoreRecord, pre Record) error {
	// The swapped-in snapshot predates this restore, so it may hold running
	// rows of restores that finished since: the pre-backup of an undo holds
	// the running row of the restore it undoes. Those rows will never finish,
	// and every later start would meet ErrRestoreRunning through them. Fail
	// them before recording this restore again, while its slot is still held
	// and its applying row still refuses concurrent starts, so no new running
	// row can land in between.
	if failErr := s.registry.FailRunningRestores(ctx, s.now().UTC(), restoreSupersededText(id)); failErr != nil {
		return fmt.Errorf("supersede running restores for restore %s: %w", id, failErr)
	}

	if err := s.recreateAfterSwap(ctx, before); err != nil {
		return err
	}

	// The pre-backup objects already sit in the target; only its registry row
	// went away with the old live data. Without it the undo source vanishes
	// from the backups list (ADR 30 decision 9).
	if err := s.backups.PreserveAfterRestore(ctx, pre); err != nil {
		return fmt.Errorf("record pre-backup %s after the swap: %w", pre.ID, err)
	}

	return nil
}

// runAfterApply runs the after-apply step of the restore id. A backup from before
// the seal change holds unsealed rows: the step runs while maintenance
// mode still holds, so those rows end sealed. A failure fails the restore.
func (s *RestoreService) runAfterApply(ctx context.Context, id RestoreID) error {
	if s.afterApply == nil {
		return nil
	}

	if err := s.afterApply(ctx); err != nil {
		return fmt.Errorf("after the apply of restore %s: %w", id, err)
	}

	return nil
}

// appendRestoreAudit writes the audit entry of restore id after the swap, in
// the restored data, so the entry survives the swap that replaced
// audit_entries from the backup. The object names whether the access tables
// were replaced or kept. The live store keeps the entry; a store without the
// server audit port skips it. Best effort: a lost entry never fails the
// restore.
func (s *RestoreService) appendRestoreAudit(ctx context.Context, id RestoreID, req RestoreRequest) {
	store, ok := s.live.(interface {
		AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
	})
	if !ok {
		return
	}

	mode := "replaced"
	if req.KeepAccessTables {
		mode = "kept"
	}

	entry := audit.Entry{
		At:           s.now().UTC(),
		Namespace:    audit.ServerScope,
		Subject:      req.RequestedBy,
		IDPSubject:   "",
		TokenID:      "",
		Action:       string(identity.ActionServerRestore),
		Object:       "restore " + id.String() + " of backup " + req.BackupID.String() + " (access tables " + mode + ")",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}

	if _, err := store.AppendServer(ctx, entry); err != nil {
		s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", id.String()))
	}
}

// restoreSupersededText explains a running row the swap resurrected: the row
// belongs to a restore whose data this restore replaced, so it can never
// finish. The text holds only the superseding restore ID.
func restoreSupersededText(id RestoreID) string {
	return fmt.Sprintf("restore superseded by restore %s", id)
}

// applyStaged swaps the staged backup live. When req keeps the access
// tables, the swap keeps the current rows of the access tables and replaces
// the other tables.
func (s *RestoreService) applyStaged(ctx context.Context, id RestoreID, req RestoreRequest) error {
	if !req.KeepAccessTables {
		if applyErr := s.live.ApplyStaging(ctx); applyErr != nil {
			return fmt.Errorf("apply staged data for restore %s: %w", id, applyErr)
		}

		return nil
	}

	keeper, ok := s.live.(AccessKeepingApplier)
	if !ok {
		return fmt.Errorf("apply staged data for restore %s: %w", id, ErrMissingDependency)
	}

	if applyErr := keeper.ApplyStagingKeepingAccess(ctx); applyErr != nil {
		return fmt.Errorf("apply staged data for restore %s: %w", id, applyErr)
	}

	return nil
}

// pinnedManifest pins the manifest fields the second stage must still match,
// so a target mutated after the check cannot be applied.
type pinnedManifest struct {
	sqlSize  int64
	sqlSHA   string
	schema   int
	dialect  Dialect
	wrappers []string
}

// pinManifest reads the manifest of id before the checks.
func (s *RestoreService) pinManifest(ctx context.Context, id ID) (pinnedManifest, error) {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return pinnedManifest{}, err
	}

	sha, parseErr := parseSQLSHA(manifest, id)
	if parseErr != nil {
		return pinnedManifest{}, parseErr
	}

	return pinnedManifest{
		sqlSize:  manifest.SQLSize,
		sqlSHA:   string(sha),
		schema:   manifest.SchemaVersion,
		dialect:  manifest.Dialect,
		wrappers: append([]string{}, manifest.WrapperIDs...),
	}, nil
}

// repinManifest rereads the manifest after the pre-backup and refuses to
// apply when it changed since the checks: fail closed on target mutation.
func (s *RestoreService) repinManifest(ctx context.Context, id ID, pinned pinnedManifest) error {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return err
	}

	sha, parseErr := parseSQLSHA(manifest, id)
	if parseErr != nil {
		return parseErr
	}

	if manifest.SQLSize != pinned.sqlSize || string(sha) != pinned.sqlSHA ||
		manifest.SchemaVersion != pinned.schema || manifest.Dialect != pinned.dialect {
		return fmt.Errorf("%w: backup %s changed after the checks", ErrCorrupt, id)
	}

	return nil
}

// recreateAfterSwap stores the restore row again after the swap replaced the
// live data with the staged backup, which predates the row. The new row keeps
// the ID, the start time, the backup, the pre-backup and the requester, back
// in applying, so FinishRestore can move it to its end state. The backup
// cannot hold the same ID: IDs are unique per restore.
func (s *RestoreService) recreateAfterSwap(ctx context.Context, before RestoreRecord) error {
	recreated := RestoreRecord{
		StartedAt:   before.StartedAt,
		EndedAt:     time.Time{},
		ID:          before.ID,
		BackupID:    before.BackupID,
		PreBackupID: before.PreBackupID,
		RequestedBy: before.RequestedBy,
		Error:       "",
		State:       RestoreApplying,
	}

	if err := s.registry.CreateRestore(ctx, recreated); err != nil {
		return fmt.Errorf("record restore %s after the swap: %w", before.ID, err)
	}

	return nil
}

// stage loads the SQL part of backup id into the staging area a second time,
// after the checks passed and the live backup is taken. The checks staged
// and discarded it; the apply needs it staged again. It verifies the SQL
// part against the manifest first, and staging checks the data again (row
// counts, foreign keys, blob references), so the swap only moves checked
// data.
func (s *RestoreService) stage(ctx context.Context, id ID) error {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return err
	}

	if _, shaErr := parseSQLSHA(manifest, id); shaErr != nil {
		return shaErr
	}

	if verifyErr := verifySQLPart(ctx, s.target, id, manifest.SQLFiles); verifyErr != nil {
		return verifyErr
	}

	wantBlobs, blobsErr := parseManifestBlobs(manifest, id)
	if blobsErr != nil {
		return blobsErr
	}

	plan := stagePlan{want: restoreWant(manifest, wantBlobs), files: manifest.SQLFiles}

	if _, stageErr := stageBackup(ctx, s.target, s.live, id, plan); stageErr != nil {
		return stageErr
	}

	return nil
}

// copyBlobs copies every blob the manifest of backup id names from the
// target into the live blob store. Blobs are write-once, so this adds keys
// and changes none: a key the live store already holds is verified, not
// copied again. Live blobs that the restored data does not name become
// orphans for the sweep (issue 203).
func (s *RestoreService) copyBlobs(ctx context.Context, id ID) error {
	manifest, err := readManifest(ctx, s.target, id)
	if err != nil {
		return err
	}

	for _, ref := range manifest.Blobs {
		if err := s.copyBlob(ctx, ref); err != nil {
			return err
		}
	}

	return nil
}

// copyBlob copies one blob from the target into the live store. A key the
// live store already holds is verified against the manifest instead: on a
// size or checksum mismatch it reports ErrCorrupt.
func (s *RestoreService) copyBlob(ctx context.Context, ref ManifestBlob) error {
	if err := copyRestoreBlob(ctx, s.target, s.blobs, ref); err != nil {
		if !errors.Is(err, blob.ErrExists) {
			return err
		}

		return s.verifyLiveBlob(ctx, ref)
	}

	return nil
}

// verifyLiveBlob checks that the live store already holds ref with the size
// and checksum of the manifest. It writes nothing.
func (s *RestoreService) verifyLiveBlob(ctx context.Context, ref ManifestBlob) error {
	wantMD5, err := hex.DecodeString(ref.MD5)
	if err != nil || len(wantMD5) != md5.Size {
		return fmt.Errorf("%w: blob %q MD5 %q", ErrCorrupt, ref.Key, ref.MD5)
	}

	key, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	reader, err := s.blobs.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read live blob %q: %w", ref.Key, err)
	}

	counter := &md5SizeWriter{hash: md5.New(), size: 0} //nolint:gosec // The manifest records MD5, for integrity.

	_, copyErr := io.Copy(io.Discard, io.TeeReader(reader, counter))
	closeErr := reader.Close()

	if copyErr != nil {
		return fmt.Errorf("read live blob %q: %w", ref.Key, copyErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close live blob %q: %w", ref.Key, closeErr)
	}

	if counter.size != ref.Size || !bytes.Equal(counter.hash.Sum(nil), wantMD5) {
		return fmt.Errorf("%w: live blob %q holds %d bytes, the manifest names %d",
			ErrCorrupt, ref.Key, counter.size, ref.Size)
	}

	return nil
}

// MintRestoreID mints one restore ID: rst- for the restore row. The service
// mints the pre-backup ID itself after the checks pass, so a failed check
// never names a backup that was never taken.
func MintRestoreID() RestoreID {
	return RestoreID(ident.NewID("rst-"))
}

// MintRestoreIDs mints the restore ID and its pre-backup ID: rst- for the
// restore row, bkp- for the backup of the live data taken before the apply.
// It stays for callers that mint both up front; routes mint only the restore
// ID through MintRestoreID so a failed check never names a backup that was
// never taken.
func MintRestoreIDs() (RestoreID, ID) {
	return MintRestoreID(), ID(ident.NewID("bkp-"))
}
