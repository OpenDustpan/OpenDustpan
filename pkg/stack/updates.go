package stack

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// DefaultMaxCheckpointBytes bounds one checkpoint unless configured otherwise.
	DefaultMaxCheckpointBytes = 32 << 20

	maxEventsPerBatch = 1000
	// transitionAttempts bounds how often a transition is re-applied after a
	// concurrent change, such as the CLI renewing its lease mid-completion.
	transitionAttempts = 3
	// closeTimeout bounds the server's own cleanup after its caller gave up.
	closeTimeout    = 30 * time.Second
	maxEventBytes   = 1 << 20
	defaultPageSize = 10
	maxPageSize     = 100
)

// UpdateStore persists updates. Writes carry the namespace fence and fail with
// namespace.ErrFenced if it is stale.
type UpdateStore interface {
	// CreateUpdate persists a new, not-started u with its CreatedBy and
	// RestoredFrom as given. It returns ErrNotFound if the stack does not exist.
	CreateUpdate(ctx context.Context, u Update, fence namespace.Fence) error
	// GetUpdate returns the update with its CreatedBy, RestoredFrom and
	// CheckpointBytes, or ErrUpdateNotFound. CheckpointBytes is the size in
	// bytes of the checkpoint the update saved, or 0 when it saved none.
	GetUpdate(ctx context.Context, ref Ref, id UpdateID) (Update, error)
	// StartUpdate persists the started u and returns it with Version assigned:
	// one more than the highest version of any update of the stack if
	// u.Mutates(), else the stack's current version. A mutating u fails with
	// ErrUpdateConflict while another mutating update of the stack is running.
	// Any u fails with ErrConcurrentModification unless the store holds
	// u.Revision-1. It keeps the CreatedBy and RestoredFrom of u.
	StartUpdate(ctx context.Context, u Update, fence namespace.Fence) (Update, error)
	// SaveUpdate persists a transition of u if the store holds u.Revision-1,
	// else returns ErrConcurrentModification. When a started mutating update
	// ends, the stack's Version becomes u.Version in the same transaction.
	// It keeps the CreatedBy and RestoredFrom of u.
	SaveUpdate(ctx context.Context, u Update, fence namespace.Fence) error
	// ActiveUpdate returns the stack's running update that Mutates, or ErrUpdateNotFound.
	ActiveUpdate(ctx context.Context, ref Ref) (Update, error)
	// ListUpdates returns the stack's started updates that Mutates, newest
	// first. A stack without updates, or one that does not exist, yields an
	// empty list. Each entry carries its CreatedBy, its RestoredFrom (0 when
	// the update is not a restore) and its CheckpointBytes: the size in bytes
	// of the checkpoint the update saved, or 0 when it saved none. The size
	// travels on the update itself, read with one join, so history needs no
	// second query and no new port method.
	ListUpdates(ctx context.Context, ref Ref, page Page) ([]Update, error)
}

// CheckpointStore persists checkpoint metadata, one row for each mutating
// update. Each write of the running update overwrites the row of that update.
// The content lives in the blob store under the row's blob key; rows from
// before blobs keep the content in the SQL column instead. The store never
// touches the blob store: callers write the content before they call Put and
// read it back after they read a row with a key.
type CheckpointStore interface {
	// PutCheckpoint saves cp as the state of update cp.Update, in the same
	// transaction as checking that update cp.Update is still running under
	// lease cp.Lease; otherwise it returns ErrWrongUpdateState, so a write
	// authorized before a cancel cannot land after it. It returns
	// ErrWrongUpdateState when the update is a preview or a dry run, which
	// never saves state. For cp.Sequence > 0 it is idempotent: if the
	// update's last checkpoint has the same sequence and digest it returns
	// nil without writing, a different digest returns ErrCheckpointConflict,
	// and a lower sequence returns ErrStaleCheckpoint. A row with a blob key
	// keeps the key with the size, the digest and the sequence, and no
	// content; a row without a key keeps the content as given.
	PutCheckpoint(ctx context.Context, cp Checkpoint, fence namespace.Fence) error
	// LatestCheckpoint returns the checkpoint of the stack's highest version
	// that has one, or ErrNoState.
	LatestCheckpoint(ctx context.Context, ref Ref) (Checkpoint, error)
	// CheckpointByVersion returns the checkpoint of the stack's update with
	// the given version, or ErrNoState when no such update exists or it has
	// no checkpoint. It never falls back to an older version.
	CheckpointByVersion(ctx context.Context, ref Ref, version int64) (Checkpoint, error)
	// CheckpointByUpdate returns the checkpoint of the stack's update with
	// the given ID, or ErrNoState when the update saved none. It returns no
	// sentinel errors for a missing stack or update: both read as no state.
	CheckpointByUpdate(ctx context.Context, ref Ref, id UpdateID) (Checkpoint, error)
	// ReferencedCheckpointKeys returns the blob keys every checkpoint of the
	// stack names, without the empty ones. A stack without checkpoints, or
	// one that does not exist, yields an empty list. It returns no sentinel
	// errors. A sweeper compares this set against the stored objects to find
	// blobs no checkpoint references.
	ReferencedCheckpointKeys(ctx context.Context, ref Ref) ([]blob.Key, error)
}

// EventStore persists engine events.
type EventStore interface {
	// AppendEvents stores the batch's events, in the same transaction as
	// checking that the update is still running under batch.Lease; otherwise
	// it returns ErrWrongUpdateState. An event whose sequence is already stored
	// for the update is skipped, so retries are harmless.
	AppendEvents(ctx context.Context, batch EventBatch, fence namespace.Fence) error
	// ListEvents returns up to limit events with Sequence greater than after,
	// in order. It returns ErrUpdateNotFound if the update does not exist.
	ListEvents(ctx context.Context, ref Ref, id UpdateID, after int64, limit int) ([]Event, error)
}

// CheckpointStacks resolves a stack reference to its stack. Updates reads
// the stable ID for checkpoint blob keys through it.
type CheckpointStacks interface {
	// GetStack returns the stack identified by ref, or ErrNotFound.
	GetStack(ctx context.Context, ref Ref) (Stack, error)
}

// UpdatesConfig holds the dependencies of Updates.
type UpdatesConfig struct {
	Updates     UpdateStore
	Checkpoints CheckpointStore
	Events      EventStore
	Namespaces  *namespace.Service
	// Stacks resolves stack references to their stable IDs for checkpoint
	// blob keys.
	Stacks CheckpointStacks
	// Blobs holds checkpoint content under
	// "pulumi/<stack ID>/<update ID>/<sequence>", gzipped.
	Blobs blob.Store
	Now   func() time.Time
	// Logger receives sweep failures; nil discards them.
	Logger *slog.Logger
	// MaxCheckpointBytes bounds one checkpoint; zero means DefaultMaxCheckpointBytes.
	MaxCheckpointBytes int
}

// Updates implements the update cycle of the Pulumi CLI: create and start an
// update, hold its lease, save checkpoints and events, and complete or cancel
// it; plus export, import and history. Lease tokens are checked here, never in
// the stores, and only their digests are persisted.
type Updates struct {
	updates        UpdateStore
	checkpoints    CheckpointStore
	events         EventStore
	namespaces     *namespace.Service
	stacks         CheckpointStacks
	blobs          blob.Store
	logger         *slog.Logger
	now            func() time.Time
	maxCheckpoints int
}

// NewUpdates returns Updates. Every store, Stacks, Blobs and Namespaces are required.
func NewUpdates(cfg UpdatesConfig) (*Updates, error) {
	switch {
	case cfg.Updates == nil:
		return nil, fmt.Errorf("%w: update store", ErrMissingDependency)
	case cfg.Checkpoints == nil:
		return nil, fmt.Errorf("%w: checkpoint store", ErrMissingDependency)
	case cfg.Events == nil:
		return nil, fmt.Errorf("%w: event store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Stacks == nil:
		return nil, fmt.Errorf("%w: stack resolver", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	}

	s := &Updates{
		updates:        cfg.Updates,
		checkpoints:    cfg.Checkpoints,
		events:         cfg.Events,
		namespaces:     cfg.Namespaces,
		stacks:         cfg.Stacks,
		blobs:          cfg.Blobs,
		logger:         cfg.Logger,
		now:            cfg.Now,
		maxCheckpoints: cfg.MaxCheckpointBytes,
	}

	if s.now == nil {
		s.now = time.Now
	}

	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}

	if s.maxCheckpoints <= 0 {
		s.maxCheckpoints = DefaultMaxCheckpointBytes
	}

	return s, nil
}

// Started is a running update and the lease token its client must present on
// every later write. The token is not stored and cannot be recovered.
type Started struct {
	Token  token.Token
	Update Update
}

// Create records a new update. It does not lock the stack. It records who
// asked in CreatedBy and, for a restore, which version it copies in
// RestoredFrom, both as given in plan.
func (s *Updates) Create(ctx context.Context, ref Ref, plan Plan) (Update, error) {
	fence, err := s.fence(ctx, ref)
	if err != nil {
		return Update{}, err
	}

	u := Update{
		CreatedAt: s.clock(),
		StartedAt: time.Time{},
		EndedAt:   time.Time{},
		Lease:     Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:        UpdateID(ident.NewID("")),
		Kind:      plan.Kind,
		Status:    StatusNotStarted,
		Ref:       ref,
		DryRun:    plan.DryRun,
		Version:   0,
		Revision:  1,
		// Sizes come from the checkpoint rows on read.
		CreatedBy:       plan.CreatedBy,
		RestoredFrom:    plan.RestoredFrom,
		CheckpointBytes: 0,
	}

	if err := s.updates.CreateUpdate(ctx, u, fence); err != nil {
		return Update{}, fmt.Errorf("create %s update of %s: %w", plan.Kind, ref, err)
	}

	return u, nil
}

// Start starts the update and hands out its lease. A mutating update fails
// with ErrUpdateConflict while another one runs on the stack. It sweeps the
// stack's orphan checkpoint blobs, best effort: a cleanup failure never fails
// the start, and the blobs stay for the next sweep.
func (s *Updates) Start(ctx context.Context, ref Ref, id UpdateID) (Started, error) {
	fence, err := s.fence(ctx, ref)
	if err != nil {
		return Started{}, err
	}

	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Started{}, err
	}

	tok, err := token.New()
	if err != nil {
		return Started{}, err
	}

	now := s.clock()

	u, err = u.start(now, Lease{ExpiresAt: now.Add(DefaultLease), Digest: tok.Digest()})
	if err != nil {
		return Started{}, err
	}

	u, err = s.updates.StartUpdate(ctx, u, fence)
	if err != nil {
		return Started{}, fmt.Errorf("start update %s of %s: %w", id, ref, err)
	}

	s.sweepStackOnStart(ctx, ref)

	return Started{Token: tok, Update: u}, nil
}

// Renew extends the update's lease to d from now.
func (s *Updates) Renew(ctx context.Context, ref Ref, id UpdateID, tok token.Token, d time.Duration) (Update, error) {
	return s.transition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.renew(tok, now, d)
	})
}

// Complete ends the update with the status its client reports.
func (s *Updates) Complete(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, status UpdateStatus,
) (Update, error) {
	return s.transition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.complete(tok, now, status)
	})
}

// Cancel ends the update without its lease, as `pulumi cancel` does. It is the
// only way to release a stack whose client vanished.
func (s *Updates) Cancel(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	return s.transition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.cancel(now)
	})
}

// sweepGracePeriod is how long after its write a checkpoint blob stays before
// the unreferenced sweep deletes it, so a save that still commits is never
// collected: every new blob is fresh, and the row names it only after the
// bytes land.
const sweepGracePeriod = time.Hour

// checkpointDeleteRetries bounds the blob deletes of one cleanup: a transient
// object store failure retries instead of orphaning the blob.
const checkpointDeleteRetries = 3

// checkpointSave groups one checkpoint save after the lease and idempotency
// checks: the row parts with the blob key to write.
type checkpointSave struct {
	ref     Ref
	id      UpdateID
	stackID ID
	fence   namespace.Fence
	lease   token.Digest
	in      CheckpointInput
	digest  [sha256.Size]byte
	prev    *Checkpoint
	key     blob.Key
}

// SaveCheckpoint stores the stack's state as of now in the update. Previews
// do not save state.
//
// The content goes to the blob store first, gzipped, under
// pulumi/<stack ID>/<update ID>/<sequence>; then the fenced SQL row names it.
// A row never names a missing blob: a crash between the blob write and the
// commit leaves only an unreferenced blob, which the sweep below collects.
// After the commit, the blob of the previous save of the same update is
// deleted. The deployment bytes travel exactly as received: the server never
// decodes and encodes them again. The save path holds one decompressed copy
// (the input) with one compressed copy (the gzip buffer).
func (s *Updates) SaveCheckpoint(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, in CheckpointInput,
) error {
	if err := s.validate(in); err != nil {
		return err
	}

	u, fence, err := s.authorized(ctx, ref, id, tok)
	if err != nil {
		return err
	}

	if !u.Mutates() {
		return fmt.Errorf("%w: a preview does not save state", ErrWrongUpdateState)
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
	}

	digest := sha256.Sum256(in.Deployment)

	prev, err := s.previousCheckpoint(ctx, ref, id)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
	}

	known, err := checkpointKnown(prev, in.Sequence, digest)
	if err != nil {
		return err
	}

	if known {
		// A retried save of the same content: nothing to write. The sweep
		// still runs, so a retry also collects orphans of the update.
		s.sweepUpdateBlobs(ctx, ref, st.ID, id)

		return nil
	}

	key, err := checkpointBlobKey(st.ID, id, in.Sequence, prev)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
	}

	return s.storeNewCheckpoint(ctx, checkpointSave{
		ref: ref, id: id, stackID: st.ID, fence: fence, lease: u.Lease.Digest,
		in: in, digest: digest, prev: prev, key: key,
	})
}

// DeleteStackBlobs removes every checkpoint blob of the stack. The caller
// runs it after DeleteStack or ForceDeleteStack removed the rows, so no save
// can still commit a row naming these blobs: a concurrent save fails its
// fenced write once the stack is gone. It is best effort and logs no key.
func (s *Updates) DeleteStackBlobs(ctx context.Context, ref Ref, stackID ID) {
	listed, err := s.blobs.List(ctx, "pulumi/"+stackID.String()+"/")
	if err != nil {
		s.logger.WarnContext(ctx, "delete checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	failed := 0

	for _, obj := range listed {
		if blobErr := s.deleteBlobRetried(ctx, obj.Key); blobErr != nil {
			failed++
		}
	}

	if failed > 0 {
		s.logger.WarnContext(
			ctx,
			"delete checkpoint blobs",
			slog.String("stack", ref.String()),
			slog.Int("failed", failed),
		)
	}
}

// AppendEvents stores a batch of engine events for the update.
func (s *Updates) AppendEvents(ctx context.Context, ref Ref, id UpdateID, tok token.Token, events []Event) error {
	if len(events) > maxEventsPerBatch {
		return fmt.Errorf("%w: %d events in one batch, limit %d", ErrTooLarge, len(events), maxEventsPerBatch)
	}

	for _, e := range events {
		switch {
		case e.Sequence < 0:
			return fmt.Errorf("%w: event sequence %d", ErrInvalidUpdate, e.Sequence)
		case len(e.Payload) == 0, len(e.Payload) > maxEventBytes:
			return fmt.Errorf("%w: event of %d bytes, want 1 to %d", ErrInvalidUpdate, len(e.Payload), maxEventBytes)
		}
	}

	u, fence, err := s.authorized(ctx, ref, id, tok)
	if err != nil {
		return err
	}

	batch := EventBatch{Events: events, Ref: ref, Update: id, Lease: u.Lease.Digest}
	if err := s.events.AppendEvents(ctx, batch, fence); err != nil {
		return fmt.Errorf("append events to update %s: %w", id, err)
	}

	return nil
}

// Import replaces the stack's state with a deployment the user supplies, as
// `pulumi stack import` does. It runs as an update of kind "import" so it
// shows in history and respects the stack lock. It records subject as the
// creator.
//
// Invalid input is rejected before any update exists. Once the import update
// starts, only this call holds its lease, so it is always ended, even when ctx
// is canceled: otherwise the stack would stay locked until `pulumi cancel`.
func (s *Updates) Import(ctx context.Context, ref Ref, subject string, in CheckpointInput) (Update, error) {
	if err := s.validate(in); err != nil {
		return Update{}, err
	}

	u, err := s.Create(ctx, ref, Plan{Kind: KindImport, DryRun: false, CreatedBy: subject, RestoredFrom: 0})
	if err != nil {
		return Update{}, err
	}

	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()

	started, err := s.Start(ctx, ref, u.ID)
	if err != nil {
		// Do not leave a not-started update behind for every conflicting attempt.
		_, cancelErr := s.Cancel(closing, ref, u.ID)

		return Update{}, errors.Join(err, cancelErr)
	}

	status := StatusSucceeded

	saveErr := s.SaveCheckpoint(ctx, ref, u.ID, started.Token, in)
	if saveErr != nil {
		status = StatusFailed
	}

	done, err := s.Complete(closing, ref, u.ID, started.Token, status)

	return done, errors.Join(saveErr, err)
}

// Restore copies the checkpoint of version into a new update of kind
// "restore" and completes it. It follows the rules of Import: the restore
// update shows in history with RestoredFrom set, respects the stack lock,
// and always ends, even when ctx is canceled. A version without a checkpoint
// fails with ErrNoState before any update exists. A restore changes no
// infrastructure; it writes old state as a new version. The deployment stays
// opaque: the service never reads secret content.
func (s *Updates) Restore(ctx context.Context, ref Ref, version int64, subject string) (Update, error) {
	in, err := s.restoreInput(ctx, ref, version)
	if err != nil {
		return Update{}, err
	}

	u, err := s.Create(ctx, ref, Plan{Kind: KindRestore, DryRun: false, CreatedBy: subject, RestoredFrom: version})
	if err != nil {
		return Update{}, err
	}

	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()

	started, err := s.Start(ctx, ref, u.ID)
	if err != nil {
		// Do not leave a not-started update behind for every conflicting attempt.
		_, cancelErr := s.Cancel(closing, ref, u.ID)

		return Update{}, errors.Join(err, cancelErr)
	}

	status := StatusSucceeded

	saveErr := s.SaveCheckpoint(ctx, ref, u.ID, started.Token, in)
	if saveErr != nil {
		status = StatusFailed
	}

	done, err := s.Complete(closing, ref, u.ID, started.Token, status)

	return done, errors.Join(saveErr, err)
}

// Export returns the stack's latest checkpoint, or ErrNoState.
func (s *Updates) Export(ctx context.Context, ref Ref) (Checkpoint, error) {
	cp, err := s.checkpoints.LatestCheckpoint(ctx, ref)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s: %w", ref, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, cp)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s: %w", ref, err)
	}

	return resolved, nil
}

// ExportVersion returns the checkpoint of the stack's update with the given
// version, or ErrNoState when no such update exists or it has no checkpoint.
// It never falls back to an older version.
func (s *Updates) ExportVersion(ctx context.Context, ref Ref, version int64) (Checkpoint, error) {
	cp, err := s.checkpoints.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s version %d: %w", ref, version, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, cp)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s version %d: %w", ref, version, err)
	}

	return resolved, nil
}

// MaxCheckpointBytes returns the largest checkpoint SaveCheckpoint accepts.
func (s *Updates) MaxCheckpointBytes() int {
	return s.maxCheckpoints
}

// Get returns one update.
func (s *Updates) Get(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	u, err := s.updates.GetUpdate(ctx, ref, id)
	if err != nil {
		return Update{}, fmt.Errorf("get update %s of %s: %w", id, ref, err)
	}

	return u, nil
}

// Active returns the stack's running mutating update, or ErrUpdateNotFound.
func (s *Updates) Active(ctx context.Context, ref Ref) (Update, error) {
	u, err := s.updates.ActiveUpdate(ctx, ref)
	if err != nil {
		return Update{}, fmt.Errorf("active update of %s: %w", ref, err)
	}

	return u, nil
}

// History returns the stack's updates, newest first. Previews are not history.
func (s *Updates) History(ctx context.Context, ref Ref, page Page) ([]Update, error) {
	if page.Limit <= 0 {
		page.Limit = defaultPageSize
	}

	page.Limit = min(page.Limit, maxPageSize)
	page.Offset = max(page.Offset, 0)

	list, err := s.updates.ListUpdates(ctx, ref, page)
	if err != nil {
		return nil, fmt.Errorf("history of %s: %w", ref, err)
	}

	return list, nil
}

// Events returns up to limit events of the update after the given sequence.
func (s *Updates) Events(ctx context.Context, ref Ref, id UpdateID, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > maxEventsPerBatch {
		limit = maxEventsPerBatch
	}

	events, err := s.events.ListEvents(ctx, ref, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("events of update %s: %w", id, err)
	}

	return events, nil
}

// storeNewCheckpoint writes the blob of save, then its row. A blob that is
// already there belongs to a first attempt whose commit never landed: the
// retry adopts it when it holds the same content.
func (s *Updates) storeNewCheckpoint(ctx context.Context, save checkpointSave) error {
	size, sum, err := s.putCheckpointBlob(ctx, save.key, save.in.Deployment)
	if err != nil {
		if errors.Is(err, blob.ErrExists) {
			return s.adoptCheckpointBlob(ctx, save)
		}

		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save checkpoint of update %s: %w", save.id, cerr)
		}

		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrCheckpointUnavailable)
	}

	if err := s.commitCheckpointRow(ctx, save, size, sum); err != nil {
		// The blob stays without a row that names it; the sweep below
		// collects it once it is older than the grace period.
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, err)
	}

	s.afterCheckpointSave(ctx, save)

	return nil
}

// adoptCheckpointBlob answers a blob write that met an existing key: the
// first attempt wrote it but never committed its row. A blob with the same
// content commits the row and succeeds, so a crash never blocks the retry.
// Anything else is a concurrent save of the same update, which fails without
// touching SQL, so no row can name a blob with other content. No error text
// names the key.
func (s *Updates) adoptCheckpointBlob(ctx context.Context, save checkpointSave) error {
	raw, err := s.blobs.Get(ctx, save.key)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save checkpoint of update %s: %w", save.id, cerr)
		}

		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrCheckpointUnavailable)
	}

	gave, err := readStoredBlob(raw, s.storedLimit())
	if err != nil {
		if errors.Is(err, ErrCheckpointMismatch) {
			return s.checkpointCollision(ctx, save)
		}

		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save checkpoint of update %s: %w", save.id, cerr)
		}

		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrCheckpointUnavailable)
	}

	deployment, err := gunzipBytes(gave, s.maxCheckpoints)
	if err != nil {
		return s.checkpointCollision(ctx, save)
	}

	if sha256.Sum256(deployment) != save.digest {
		return s.checkpointCollision(ctx, save)
	}

	size, sum := storedAttrs(gave)

	if err := s.commitCheckpointRow(ctx, save, size, sum); err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, err)
	}

	s.afterCheckpointSave(ctx, save)

	return nil
}

// checkpointCollision answers a blob that holds other content than save: a
// row that already matches the sequence and digest means the first attempt
// committed and this is its retry, which succeeds without writing. A save
// without a sequence number mints the next key of its update, leaving the
// orphan for the sweep. Anything else is a concurrent save of the same
// update, which fails without touching SQL.
func (s *Updates) checkpointCollision(ctx context.Context, save checkpointSave) error {
	current, err := s.previousCheckpoint(ctx, save.ref, save.id)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, err)
	}

	known, err := checkpointKnown(current, save.in.Sequence, save.digest)
	if err != nil {
		return err
	}

	if known {
		return nil
	}

	if save.in.Sequence == 0 {
		return s.storeMintedKey(ctx, save)
	}

	return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrConcurrentModification)
}

// storeMintedKey writes save under the next key of its update, for a save
// without a sequence number whose derived key holds other content. The
// orphan keeps its key for the sweep.
func (s *Updates) storeMintedKey(ctx context.Context, save checkpointSave) error {
	prefix := "pulumi/" + save.stackID.String() + "/" + save.id.String() + "/"

	next, err := nextBlobKey(prefix, save.key)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrConcurrentModification)
	}

	size, sum, err := s.putCheckpointBlob(ctx, next, save.in.Deployment)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("save checkpoint of update %s: %w", save.id, cerr)
		}

		if errors.Is(err, blob.ErrExists) {
			return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrConcurrentModification)
		}

		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrCheckpointUnavailable)
	}

	save.key = next

	if err := s.commitCheckpointRow(ctx, save, size, sum); err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, err)
	}

	s.afterCheckpointSave(ctx, save)

	return nil
}

// commitCheckpointRow writes the fenced SQL row of save naming its blob.
func (s *Updates) commitCheckpointRow(
	ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte,
) error {
	cp := Checkpoint{
		SavedAt:       s.clock(),
		Features:      slices.Clone(save.in.Features),
		Deployment:    save.in.Deployment,
		Update:        save.id,
		Ref:           save.ref,
		Lease:         save.lease,
		SchemaVersion: save.in.SchemaVersion,
		Sequence:      save.in.Sequence,
		Digest:        save.digest,
		Invalid:       save.in.Invalid,
		BlobKey:       save.key,
		BlobSize:      size,
		BlobMD5:       sum,
	}

	return s.checkpoints.PutCheckpoint(ctx, cp, save.fence)
}

// afterCheckpointSave deletes the blob of the previous save of the same
// update and sweeps its orphan blobs. Both are best effort: a cleanup
// failure never fails the save, and the rows stay for the next sweep. No log
// line names a blob key.
func (s *Updates) afterCheckpointSave(ctx context.Context, save checkpointSave) {
	if save.prev != nil && save.prev.BlobKey != "" && save.prev.BlobKey != save.key {
		if err := s.deleteBlobRetried(ctx, save.prev.BlobKey); err != nil {
			s.logger.WarnContext(
				ctx,
				"delete superseded checkpoint blob",
				slog.String("stack", save.ref.String()),
				slog.String("update", save.id.String()),
			)
		}
	}

	s.sweepUpdateBlobs(ctx, save.ref, save.stackID, save.id)
}

// previousCheckpoint returns the checkpoint the update saved before, or nil
// when it saved none.
func (s *Updates) previousCheckpoint(ctx context.Context, ref Ref, id UpdateID) (*Checkpoint, error) {
	prev, err := s.checkpoints.CheckpointByUpdate(ctx, ref, id)
	if errors.Is(err, ErrNoState) {
		return nil, nil //nolint:nilnil // Nil without an error means the update saved no checkpoint yet.
	}

	if err != nil {
		return nil, err
	}

	return &prev, nil
}

// putCheckpointBlob gzips deployment and stores it under key, returning the
// size and MD5 of the stored bytes for backups. It holds one decompressed
// copy (deployment) with one compressed copy (the gzip buffer). No error text
// names the key: callers match ErrExists and redact the rest.
func (s *Updates) putCheckpointBlob(
	ctx context.Context, key blob.Key, deployment []byte,
) (int64, [md5.Size]byte, error) {
	var zipped bytes.Buffer

	w := gzip.NewWriter(&zipped)

	if _, err := w.Write(deployment); err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("compress checkpoint: %w", err)
	}

	if err := w.Close(); err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("compress checkpoint: %w", err)
	}

	gave := zipped.Bytes()

	if err := s.blobs.Put(ctx, key, bytes.NewReader(gave)); err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("store checkpoint: %w", err)
	}

	size, sum := storedAttrs(gave)

	return size, sum, nil
}

// storedLimit bounds one stored checkpoint blob: the uncompressed limit with
// slack for gzip overhead.
func (s *Updates) storedLimit() int64 {
	return int64(s.maxCheckpoints) + storedBlobSlack
}

// deleteBlobRetried removes the object under key, retrying a transient
// failure. Deleting a missing key is not an error.
func (s *Updates) deleteBlobRetried(ctx context.Context, key blob.Key) error {
	var err error

	for range checkpointDeleteRetries {
		if err = s.blobs.Delete(ctx, key); err == nil {
			return nil
		}
	}

	return err
}

// sweepUpdateBlobs deletes the checkpoint blobs of one update that no
// checkpoint names and that are older than the grace period: saves whose SQL
// commit never landed, and previous saves whose delete never ran. The listing
// covers one update prefix, so it stays small no matter how many updates the
// stack holds. It is best effort: a cleanup failure never fails the save,
// and the rows stay for the next sweep. No log line names a blob key.
func (s *Updates) sweepUpdateBlobs(ctx context.Context, ref Ref, stackID ID, id UpdateID) {
	listed, err := s.blobs.List(ctx, "pulumi/"+stackID.String()+"/"+id.String()+"/")
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	if len(listed) == 0 {
		return
	}

	now := s.clock()

	candidates := make([]blob.Object, 0, len(listed))

	for _, obj := range listed {
		if now.Sub(obj.ModifiedAt) >= sweepGracePeriod {
			candidates = append(candidates, obj)
		}
	}

	if len(candidates) == 0 {
		return
	}

	s.sweepCandidates(ctx, ref, id, candidates)
}

// sweepCandidates deletes the candidate blobs of one update except the one
// its checkpoint names. A missing checkpoint keeps none: without a row every
// blob of the update is an orphan.
func (s *Updates) sweepCandidates(ctx context.Context, ref Ref, id UpdateID, candidates []blob.Object) {
	var current Checkpoint

	stored, err := s.checkpoints.CheckpointByUpdate(ctx, ref, id)
	if err == nil {
		current = stored
	} else if !errors.Is(err, ErrNoState) {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	failed := 0

	for _, obj := range candidates {
		if obj.Key == current.BlobKey {
			continue
		}

		if blobErr := s.deleteBlobRetried(ctx, obj.Key); blobErr != nil {
			failed++
		}
	}

	if failed > 0 {
		s.logger.WarnContext(
			ctx,
			"sweep checkpoint blobs",
			slog.String("stack", ref.String()),
			slog.String("update", id.String()),
			slog.Int("failed", failed),
		)
	}
}

// sweepStackOnStart resolves the stack ID and sweeps its orphan blobs. It is
// best effort: a cleanup failure never fails the start, and the blobs stay
// for the next sweep. No log line names a blob key.
func (s *Updates) sweepStackOnStart(ctx context.Context, ref Ref) {
	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	s.sweepStackBlobs(ctx, ref, st.ID)
}

// sweepStackBlobs deletes the checkpoint blobs of one stack that no
// checkpoint names and that are older than the grace period: orphans of an
// update that never saves again, such as a crash between the blob write and
// the SQL commit, a minted-key loser, or a delete race loser. The next update
// that starts on the stack collects them. Blobs younger than the grace period
// stay, so a save that still commits is never collected: every new blob is
// fresh, and the row names it only after the bytes land. The sweep reads the
// referenced keys only when old candidates remain, so a start with only fresh
// blobs pays for one listing and no checkpoint read. It is best effort: a
// cleanup failure never fails the start, and the blobs stay for the next
// sweep. No log line names a blob key.
func (s *Updates) sweepStackBlobs(ctx context.Context, ref Ref, stackID ID) {
	listed, err := s.blobs.List(ctx, "pulumi/"+stackID.String()+"/")
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	if len(listed) == 0 {
		return
	}

	now := s.clock()

	candidates := make([]blob.Object, 0, len(listed))

	for _, obj := range listed {
		if now.Sub(obj.ModifiedAt) >= sweepGracePeriod {
			candidates = append(candidates, obj)
		}
	}

	if len(candidates) == 0 {
		return
	}

	refs, err := s.checkpoints.ReferencedCheckpointKeys(ctx, ref)
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	named := make(map[blob.Key]struct{}, len(refs))

	for _, key := range refs {
		named[key] = struct{}{}
	}

	failed := 0

	for _, obj := range candidates {
		if _, ok := named[obj.Key]; ok {
			continue
		}

		if blobErr := s.deleteBlobRetried(ctx, obj.Key); blobErr != nil {
			failed++
		}
	}

	if failed > 0 {
		s.logger.WarnContext(
			ctx,
			"sweep checkpoint blobs",
			slog.String("stack", ref.String()),
			slog.Int("failed", failed),
		)
	}
}

// resolveCheckpoint fills the deployment of a checkpoint row: rows from
// before blobs carry it, while rows with a blob key read and decompress it.
// The bytes match the stored digest, so a blob with other content fails
// instead of serving foreign state. The read holds one decompressed copy.
func (s *Updates) resolveCheckpoint(ctx context.Context, cp Checkpoint) (Checkpoint, error) {
	if cp.BlobKey == "" {
		return cp, nil
	}

	raw, err := s.blobs.Get(ctx, cp.BlobKey)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Checkpoint{}, fmt.Errorf("resolve checkpoint: %w", cerr)
		}

		return Checkpoint{}, ErrCheckpointUnavailable
	}

	deployment, err := s.gunzipCheckpoint(raw)
	if err != nil {
		return Checkpoint{}, ErrCheckpointMismatch
	}

	if sha256.Sum256(deployment) != cp.Digest {
		return Checkpoint{}, ErrCheckpointMismatch
	}

	cp.Deployment = deployment

	return cp, nil
}

// gunzipCheckpoint decompresses one checkpoint blob, bounded by the
// checkpoint size limit, so a corrupt blob cannot exhaust memory.
func (s *Updates) gunzipCheckpoint(raw io.ReadCloser) ([]byte, error) {
	gave, err := readStoredBlob(raw, s.storedLimit())
	if err != nil {
		return nil, err
	}

	return gunzipBytes(gave, s.maxCheckpoints)
}

// validate checks a checkpoint's input without touching any store.
func (s *Updates) validate(in CheckpointInput) error {
	switch {
	case in.SchemaVersion < 1:
		return fmt.Errorf("%w: deployment schema version %d", ErrInvalidUpdate, in.SchemaVersion)
	case in.Sequence < 0:
		return fmt.Errorf("%w: checkpoint sequence %d", ErrInvalidUpdate, in.Sequence)
	case len(in.Deployment) > s.maxCheckpoints:
		return fmt.Errorf("%w: checkpoint of %d bytes, limit %d", ErrTooLarge, len(in.Deployment), s.maxCheckpoints)
	default:
		return nil
	}
}

// restoreInput returns the checkpoint input that restores version of ref: a
// copy of that version's checkpoint. It returns ErrNoState when the version
// has no checkpoint, before any update exists.
func (s *Updates) restoreInput(ctx context.Context, ref Ref, version int64) (CheckpointInput, error) {
	src, err := s.checkpoints.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return CheckpointInput{}, fmt.Errorf("restore %s version %d: %w", ref, version, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, src)
	if err != nil {
		return CheckpointInput{}, fmt.Errorf("restore %s version %d: %w", ref, version, err)
	}

	in := CheckpointInput{
		Features:      resolved.Features,
		Deployment:    resolved.Deployment,
		SchemaVersion: resolved.SchemaVersion,
		Sequence:      0,
		Invalid:       resolved.Invalid,
	}

	return in, s.validate(in)
}

// transition applies change to the stored update and saves the result. The
// CLI renews its lease from a background loop, so a renewal can win the
// compare-and-swap while a completion is in flight. change is a pure function
// of the stored update, so it is re-applied to a fresh read: a completion that
// raced a cancel then fails for the right reason.
func (s *Updates) transition(
	ctx context.Context, ref Ref, id UpdateID, change func(Update, time.Time) (Update, error),
) (Update, error) {
	fence, err := s.fence(ctx, ref)
	if err != nil {
		return Update{}, err
	}

	for attempt := 1; ; attempt++ {
		u, err := s.applyOnce(ctx, ref, id, change, fence)
		if !errors.Is(err, ErrConcurrentModification) || attempt == transitionAttempts {
			return u, err
		}
	}
}

func (s *Updates) applyOnce(
	ctx context.Context, ref Ref, id UpdateID, change func(Update, time.Time) (Update, error), fence namespace.Fence,
) (Update, error) {
	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Update{}, err
	}

	before := u.Revision

	u, err = change(u, s.clock())
	if err != nil {
		return Update{}, fmt.Errorf("update %s of %s: %w", id, ref, err)
	}

	if u.Revision == before {
		// A retried complete or cancel: nothing changed, nothing to write.
		return u, nil
	}

	if err := s.updates.SaveUpdate(ctx, u, fence); err != nil {
		return Update{}, fmt.Errorf("save update %s of %s: %w", id, ref, err)
	}

	return u, nil
}

// authorized loads a running update and checks tok against its lease.
func (s *Updates) authorized(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token,
) (Update, namespace.Fence, error) {
	fence, err := s.fence(ctx, ref)
	if err != nil {
		return Update{}, namespace.Fence{}, err
	}

	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Update{}, namespace.Fence{}, err
	}

	if err := u.authorize(tok, s.clock()); err != nil {
		return Update{}, namespace.Fence{}, fmt.Errorf("update %s of %s: %w", id, ref, err)
	}

	return u, fence, nil
}

// fence returns the fence for writes to ref's namespace, which must be homed here.
func (s *Updates) fence(ctx context.Context, ref Ref) (namespace.Fence, error) {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("write to %s: %w", ref, err)
	}

	return ns.Fence(), nil
}

func (s *Updates) clock() time.Time {
	return s.now().UTC()
}

// checkpointKnown reports whether a save with sequence and digest repeats the
// previous checkpoint or conflicts with it. It returns true for a retried
// save of the same content, which writes nothing. The store rechecks in its
// own transaction; this only avoids a wasted blob write.
func checkpointKnown(prev *Checkpoint, seq int64, digest [sha256.Size]byte) (bool, error) {
	if prev == nil {
		return false, nil
	}

	if seq > 0 {
		switch {
		case seq < prev.Sequence:
			return false, ErrStaleCheckpoint
		case seq == prev.Sequence && digest != prev.Digest:
			return false, ErrCheckpointConflict
		case seq == prev.Sequence:
			return true, nil
		default:
			return false, nil
		}
	}

	return digest == prev.Digest, nil
}

// checkpointBlobKey returns the blob key of one save:
// pulumi/<stack ID>/<update ID>/<sequence>. A save without a sequence number
// mints the next key of its update after the previous one, so every save
// writes a new key while the row keeps sequence 0. No error text names a key.
func checkpointBlobKey(stackID ID, id UpdateID, seq int64, prev *Checkpoint) (blob.Key, error) {
	prefix := "pulumi/" + stackID.String() + "/" + id.String() + "/"

	if seq > 0 || prev == nil || prev.BlobKey == "" {
		key, err := blob.ParseKey(prefix + strconv.FormatInt(seq, 10))
		if err != nil {
			return "", ErrCheckpointMismatch
		}

		return key, nil
	}

	return nextBlobKey(prefix, prev.BlobKey)
}

// nextBlobKey mints the key after raw in prefix: the same prefix with the
// trailing sequence number plus one. A key outside prefix, or one without a
// trailing number, is a corrupt record, which fails with ErrCheckpointMismatch
// and never names the key.
func nextBlobKey(prefix string, raw blob.Key) (blob.Key, error) {
	rest, ok := strings.CutPrefix(raw.String(), prefix)
	if !ok {
		return "", ErrCheckpointMismatch
	}

	n, err := strconv.Atoi(rest)
	if err != nil {
		return "", fmt.Errorf("checkpoint key: %w", errors.Join(ErrCheckpointMismatch, err))
	}

	key, err := blob.ParseKey(prefix + strconv.Itoa(n+1))
	if err != nil {
		return "", ErrCheckpointMismatch
	}

	return key, nil
}

// storedAttrs returns the size and MD5 of stored gzip bytes, for backups.
func storedAttrs(gave []byte) (int64, [md5.Size]byte) {
	//nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	return int64(len(gave)), md5.Sum(gave)
}

// storedBlobSlack bounds how much larger the stored gzip bytes may be than
// the uncompressed size limit: the server writes compressed bytes, so they
// stay near the deployment size.
const storedBlobSlack = 1 << 20

// readStoredBlob reads the stored gzip bytes of raw, bounded so a foreign
// object under the key cannot exhaust memory. It closes raw. A body over the
// limit is a corrupt record, which fails with ErrCheckpointMismatch.
func readStoredBlob(raw io.ReadCloser, limit int64) ([]byte, error) {
	gave, readErr := io.ReadAll(io.LimitReader(raw, limit+1))
	closeErr := raw.Close()

	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close checkpoint: %w", closeErr)
	}

	if int64(len(gave)) > limit {
		return nil, ErrCheckpointMismatch
	}

	return gave, nil
}

// gunzipBytes decompresses stored gzip bytes, bounded by limit, so a corrupt
// blob cannot exhaust memory.
func gunzipBytes(gave []byte, limit int) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(gave))
	if err != nil {
		return nil, fmt.Errorf("decompress checkpoint: %w", err)
	}

	deployment, readErr := io.ReadAll(io.LimitReader(gz, int64(limit)+1))
	closeErr := gz.Close()

	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close checkpoint: %w", closeErr)
	}

	if len(deployment) > limit {
		return nil, fmt.Errorf("%w: checkpoint of %d bytes, limit %d", ErrTooLarge, len(deployment), limit)
	}

	return deployment, nil
}
