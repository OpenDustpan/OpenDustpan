package stack

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	// DefaultMaxCheckpointBytes bounds one checkpoint unless configured otherwise.
	// It stays the bound of a built checkpoint. Measured by
	// BenchmarkJournalBuildChange10000: built checkpoint 2,220,181 bytes for 10,000 resources.
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
	// RestoredFrom as given, and calls key.NewKey in the same transaction with
	// the reference that key.RefFor builds from the stack ID that the
	// transaction resolves, so the record of the item key of u commits with the
	// update row. It returns ErrNotFound if the stack does not exist,
	// ErrInvalidUpdateKey when key.RefFor refuses key, and the error of
	// key.NewKey. On every error it stores nothing.
	CreateUpdate(ctx context.Context, u Update, key UpdateKey, fence namespace.Fence) error
	// GetUpdate returns the update with its CreatedBy, RestoredFrom and
	// CheckpointBytes, or ErrUpdateNotFound. CheckpointBytes is the size in
	// bytes of the checkpoint the update saved, or 0 when it saved none.
	GetUpdate(ctx context.Context, ref Ref, id UpdateID) (Update, error)
	// StartUpdate persists the started u and returns it with Version assigned:
	// one more than the highest version of any update of the stack if
	// u.Mutates(), else the stack's current version. A mutating u fails with
	// ErrUpdateConflict while another mutating update of the stack is running:
	// a *UpdateConflictError that names the running update.
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
// The content lives in the blob store under the row's blob key. The store
// never touches the blob store: callers write the content before they call
// Put and read it back after they read a row.
type CheckpointStore interface {
	// PutCheckpoint saves cp as the state of update cp.Update, in the same
	// transaction as checking that update cp.Update is still running under
	// lease cp.Lease; otherwise it returns ErrWrongUpdateState, so a write
	// authorized before a cancel cannot land after it. It returns
	// ErrWrongUpdateState when the update is a preview or a dry run, which
	// never saves state. For cp.Sequence > 0 the same transaction decides
	// which write wins: if the update's last checkpoint has the same sequence
	// and digest it returns known = true without writing, and the row keeps
	// the blob key of the first write, whatever key cp names. A different
	// digest returns ErrCheckpointConflict, and a lower sequence returns
	// ErrStaleCheckpoint. A write without a sequence number is never known:
	// it replaces the row. The row keeps the blob key with the size, the
	// digest and the sequence, and no content. A checkpoint without a blob
	// key returns ErrCheckpointNoKey. A checkpoint whose AttemptStartedAt
	// fails blob.CheckAttempt against the clock of the database, in the same
	// transaction, returns blob.ErrAttemptExpired and writes nothing, also for
	// a replay. When the answer is known or an error that no row followed, no
	// row names the blob of cp, and the caller may delete that blob, which
	// holds the key of its own attempt.
	PutCheckpoint(ctx context.Context, cp Checkpoint, fence namespace.Fence) (known bool, err error)
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
	// ReferencedBlobKeys returns every blob key that a row of the stack
	// names, without duplicates: the blob of each checkpoint and the blob of
	// each journal batch of every update that still holds journal rows. The
	// stack is named by its ID, the key that a sweep lists the blobs by, so
	// a rename between the listing and this read changes nothing. A live
	// stack without such rows yields an empty list. A stack that does not
	// exist, or that is soft-deleted, returns ErrNotFound: an empty keep set
	// would let a sweep delete every blob of the stack. A sweeper compares
	// this set against the stored objects to find blobs that no row
	// references.
	ReferencedBlobKeys(ctx context.Context, stackID ID) ([]blob.Key, error)
}

// EventStore persists engine events.
type EventStore interface {
	// AppendEvents stores the batch's events, in the same transaction as
	// checking that the update is still running under batch.Lease; otherwise
	// it returns ErrWrongUpdateState. An event whose sequence is already stored
	// for the update is skipped, so retries are harmless.
	AppendEvents(ctx context.Context, batch EventBatch, fence namespace.Fence) error
	// ListEvents returns up to limit events with Sequence greater than after,
	// in order, with each payload as AppendEvents stored it. It returns
	// ErrUpdateNotFound if the update does not exist.
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
	// Tokens checks the access token that started a running update, on each
	// lease request (ADR 53). Nil refuses every update that an access token
	// started, so the check fails closed.
	Tokens TokenChecker
	// Stacks resolves stack references to their stable IDs for checkpoint
	// blob keys.
	Stacks CheckpointStacks
	// Blobs holds checkpoint content under
	// "namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>",
	// gzipped. The service reads and writes it through a scoped view of the
	// namespace of each call.
	Blobs blob.Store
	// Refusals records each refusal of a blob key outside the scope of the
	// namespace of a call.
	Refusals scopeaudit.Recorder
	Now      func() time.Time
	// Logger receives sweep failures; nil discards them.
	Logger *slog.Logger
	// MaxCheckpointBytes bounds one checkpoint; zero means DefaultMaxCheckpointBytes.
	MaxCheckpointBytes int
	// Journal writes the journal batches of an update that started with a
	// journal version above 0. Nil refuses every batch, so the server answers
	// the same as it does for an update without journaling.
	Journal *JournalEntries
	// Replayer builds the checkpoint of a journaled update from its base
	// checkpoint and its stored batches. Nil fails every journaled build
	// with ErrMissingDependency, so an update without a replayer never
	// ends with a built checkpoint.
	Replayer JournalReplayer
	// ManualLocks refuses the start of a mutating update on a stack that a
	// manual lock covers. Nil turns the check off.
	ManualLocks ManualLocks
	// Sealer creates the item key of each update and seals the payload of
	// each event of the update with it. Required: no event is stored as the
	// CLI sent it.
	Sealer secret.ItemSealer
	// BlobGuard tells whether a backup or a restore runs. Every delete of a
	// blob that a backup snapshot or restored data can name asks it after the
	// commit that removed the last row that names the blob, and runs only
	// when nothing runs. Nil is a guard that never lets a delete run: the
	// blobs stay for a later sweep.
	BlobGuard blobguard.Watch
}

// ManualLocks is the read side of the manual locks of the shared core. The
// start of a mutating update asks it before the stack takes its update lease.
type ManualLocks interface {
	// CoveringFor returns the manual lock that covers the unit: a lock on the
	// unit, on the collection that holds it, or on its namespace. jobID is the
	// job whose job token made the request, or empty. The start of an update
	// by a job whose phase started before the lock passes. It returns
	// manuallock.ErrNotLocked when no manual lock covers the unit for the
	// request.
	CoveringFor(ctx context.Context, unit collection.UnitRef, jobID string) (manuallock.Lock, error)
	// NamespaceLock returns the manual lock of the namespace, or
	// manuallock.ErrNotLocked. The restore of a deleted unit asks it: a deleted
	// unit has no inventory entry, so CoveringFor cannot find the lock of its
	// namespace.
	NamespaceLock(ctx context.Context, ns namespace.Name) (manuallock.Lock, error)
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
	blobs          blobAccess
	tokens         TokenChecker
	logger         *slog.Logger
	now            func() time.Time
	maxCheckpoints int
	journal        *JournalEntries
	replayer       JournalReplayer
	manual         ManualLocks
	guard          blobguard.Watch
	sealer         secret.ItemSealer
	skipMu         sync.Mutex
	skipLogged     string
	stateAt        StateAtReader
	names          inventory.HistoryReader
}

// TokenChecker answers whether an access token still works. It is the port of
// the lease check. accesstoken.Service implements it.
type TokenChecker interface {
	// CheckActive returns nil when the token with the ID id is active and its
	// principal exists. Otherwise it wraps identity.ErrUnauthenticated.
	CheckActive(ctx context.Context, id string) error
}

// NewUpdates returns Updates. Every store, Stacks, Blobs, Namespaces and
// Sealer are required.
func NewUpdates(cfg UpdatesConfig) (*Updates, error) {
	if err := checkUpdatesConfig(&cfg); err != nil {
		return nil, err
	}

	access, err := newBlobAccess(cfg.Blobs, cfg.Refusals)
	if err != nil {
		return nil, err
	}

	return buildUpdates(&cfg, access), nil
}

// checkUpdatesConfig returns ErrMissingDependency for a required dependency
// that cfg lacks.
func checkUpdatesConfig(cfg *UpdatesConfig) error {
	switch {
	case cfg.Updates == nil:
		return fmt.Errorf("%w: update store", ErrMissingDependency)
	case cfg.Checkpoints == nil:
		return fmt.Errorf("%w: checkpoint store", ErrMissingDependency)
	case cfg.Events == nil:
		return fmt.Errorf("%w: event store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.Stacks == nil:
		return fmt.Errorf("%w: stack resolver", ErrMissingDependency)
	case cfg.Sealer == nil:
		return fmt.Errorf("%w: item sealer", ErrMissingDependency)
	}

	return nil
}

// buildUpdates builds the service of a checked cfg over the blob access.
func buildUpdates(cfg *UpdatesConfig, access blobAccess) *Updates {
	s := &Updates{
		updates:        cfg.Updates,
		checkpoints:    cfg.Checkpoints,
		events:         cfg.Events,
		namespaces:     cfg.Namespaces,
		stacks:         cfg.Stacks,
		blobs:          access,
		tokens:         cfg.Tokens,
		logger:         cfg.Logger,
		now:            cfg.Now,
		maxCheckpoints: cfg.MaxCheckpointBytes,
		journal:        cfg.Journal,
		replayer:       cfg.Replayer,
		manual:         cfg.ManualLocks,
		guard:          cfg.BlobGuard,
		sealer:         cfg.Sealer,
		skipMu:         sync.Mutex{},
		skipLogged:     "",
		stateAt:        nil,
		names:          nil,
	}

	// An update store that also finds the state at a time enables ExportAt.
	if reader, ok := cfg.Updates.(StateAtReader); ok {
		s.stateAt = reader
	}

	// An update store that also keeps the inventory change log lets ExportAt
	// name the stack at the time.
	if reader, ok := cfg.Updates.(inventory.HistoryReader); ok {
		s.names = reader
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

	return s
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
		TokenID:        "",
		JournalVersion: 0,
		CreatedAt:      s.clock(),
		StartedAt:      time.Time{},
		EndedAt:        time.Time{},
		Lease:          Lease{ExpiresAt: time.Time{}, Digest: token.Digest{}},
		ID:             UpdateID(ident.NewID("")),
		Kind:           plan.Kind,
		Status:         StatusNotStarted,
		Ref:            ref,
		DryRun:         plan.DryRun,
		Version:        0,
		Revision:       1,
		// Sizes come from the checkpoint rows on read.
		CreatedBy: plan.CreatedBy,
		// The job whose token starts the update comes later, with the start.
		JobID: "",
		// GitHead comes from the body of the create: no later request of the
		// update carries it.
		GitHead:         plan.GitHead,
		RestoredFrom:    plan.RestoredFrom,
		CheckpointBytes: 0,
	}

	key := UpdateKey{NewKey: s.sealer.NewItemKey}
	if err := s.updates.CreateUpdate(ctx, u, key, fence); err != nil {
		return Update{}, fmt.Errorf("create %s update of %s: %w", plan.Kind, ref, err)
	}

	return u, nil
}

// Start starts the update and hands out its lease. A mutating update fails
// with ErrUpdateConflict while another one runs on the stack. It sweeps the
// stack's orphan checkpoint blobs, best effort: a cleanup failure never fails
// the start, and the blobs stay for the next sweep.
// Start moves an update to running under a new lease. The update keeps journal
// version 0, which means checkpoints.
func (s *Updates) Start(ctx context.Context, ref Ref, id UpdateID) (Started, error) {
	return s.StartWithJournalVersion(ctx, ref, id, 0)
}

// StartWithJournalVersion starts the update like Start, and keeps the journal
// version that the server negotiated for it (ADR 43, decision 1.3). The store
// writes the version in the same fenced transaction as the start.
func (s *Updates) StartWithJournalVersion(
	ctx context.Context, ref Ref, id UpdateID, journalVersion int64,
) (Started, error) {
	return s.StartBy(ctx, ref, id, StartedBy{TokenID: "", JobID: ""}, journalVersion)
}

// StartedBy names who starts an update: the access token, and the job whose job
// token made the request.
type StartedBy struct {
	// TokenID is the ID of the access token. The update keeps it, so each later
	// lease request can check the token (ADR 53). It is empty when the update
	// starts for no access token.
	TokenID string
	// JobID is the job whose job token made the request, or empty. A mutating
	// update that a job starts from a phase that began before a manual lock
	// passes that lock.
	JobID string
}

// StartBy starts the update like StartWithJournalVersion, for the caller by.
func (s *Updates) StartBy(
	ctx context.Context, ref Ref, id UpdateID, by StartedBy, journalVersion int64,
) (Started, error) {
	ns, err := s.writable(ctx, ref)
	if err != nil {
		return Started{}, err
	}

	fence := ns.Fence()

	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Started{}, err
	}

	tok, err := token.New()
	if err != nil {
		return Started{}, err
	}

	now := s.clock()

	// The manual lock check and the start of the update below run in separate
	// transactions. A manual lock that commits between them is missed by this
	// one request, the same as a job phase that started before the lock. The
	// update that started keeps running, and the next start meets the lock.
	if u.Mutates() {
		if lockErr := s.checkManualLock(ctx, ref, by.JobID); lockErr != nil {
			return Started{}, lockErr
		}
	}

	u, err = u.start(now, Lease{ExpiresAt: now.Add(DefaultLease), Digest: tok.Digest()})
	if err != nil {
		return Started{}, err
	}

	u.JournalVersion = journalVersion
	u.TokenID = by.TokenID
	u.JobID = by.JobID

	u, err = s.updates.StartUpdate(ctx, u, fence)
	if err != nil {
		return Started{}, fmt.Errorf("start update %s of %s: %w", id, ref, err)
	}

	s.sweepStackOnStart(ctx, ns.ID, ref)

	return Started{Token: tok, Update: u}, nil
}

// Renew extends the update's lease to d from now.
func (s *Updates) Renew(ctx context.Context, ref Ref, id UpdateID, tok token.Token, d time.Duration) (Update, error) {
	return s.leaseTransition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.renew(tok, now, d)
	})
}

// Complete ends the update with the status its client reports. A journaled
// update builds and stores its checkpoint first, so the update ends with
// state like any other update. A failed build keeps the journal and ends
// nothing.
func (s *Updates) Complete(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, status UpdateStatus,
) (Update, error) {
	return s.completeJournaled(ctx, ref, id, tok, status)
}

// Cancel ends the update without its lease, as `pulumi cancel` does. It is the
// only way to release a stack whose client vanished. A journaled update
// builds and stores its checkpoint first, with unfinished operations as
// pending operations. A failed build keeps the journal and ends nothing.
func (s *Updates) Cancel(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	return s.cancelJournaled(ctx, ref, id)
}

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
	// origin is the writer of the state version that the save publishes: the
	// one of the update.
	origin identity.StateOrigin
	// blobs is the scoped blob access of the namespace of this save.
	blobs *scopedBlobs
}

// SaveCheckpoint stores the stack's state as of now in the update. Previews
// do not save state.
//
// The content goes to the blob store first, gzipped, under
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>; then the fenced SQL row names it.
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

	u, ns, err := s.authorizedIn(ctx, ref, id, tok)
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

	bl, err := s.blobs.scope(ns.ID, st.ID)
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
		s.sweepUpdateBlobs(ctx, bl, ref, st.ID, id)

		return nil
	}

	key, err := checkpointBlobKey(bl, st.ID, id, in.Sequence)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
	}

	return s.storeNewCheckpoint(ctx, checkpointSave{
		ref: ref, id: id, stackID: st.ID, fence: ns.Fence(), lease: u.Lease.Digest,
		in: in, digest: digest, prev: prev, key: key, origin: u.Origin(), blobs: bl,
	})
}

// DeleteStackBlobs removes every checkpoint blob of the purged stack stackID
// of the namespace with the ID ns, with a retry for each blob (ADR 47).
// Journal batches of the stack are under the same stack directory, so they go
// too. The caller takes ns from the namespace row of the purge, never from a
// request field. The purge runs it after the rows are gone, so no save can
// still commit a row naming these blobs. It returns an error when a blob may
// remain, so the caller keeps the blob purge record for the sweep. No log
// line and no error names a blob key.
func (s *Updates) DeleteStackBlobs(ctx context.Context, ns namespace.ID, stackID ID) error {
	bl, err := s.blobs.scope(ns, stackID)
	if err != nil {
		return fmt.Errorf("delete checkpoint blobs of stack %s: %w", stackID, err)
	}

	failed := 0

	// The walk deletes each blob as its page arrives, so the purge never holds
	// the keys of the whole stack. A key sorts after the cursor of the walk, so
	// a delete does not move the walk.
	var guardErr error

	err = blob.Walk(ctx, bl, bl.stackPrefix(stackID), func(obj blob.Object) error {
		// The guard comes after the commit of the purge and before each
		// delete: a restore that starts during the walk keeps the blobs that
		// are left. A backup or a restore that runs, or a failed read, stops
		// the walk. The error keeps the blob purge record, and the retention
		// sweep tries again.
		if holdErr := blobguard.Hold(ctx, s.guard); holdErr != nil {
			guardErr = holdErr

			return holdErr
		}

		if blobErr := s.deleteBlobRetried(ctx, bl, obj.Key); blobErr != nil {
			failed++
		}

		return nil
	})
	if guardErr != nil {
		return fmt.Errorf("delete checkpoint blobs of stack %s: %w", stackID, guardErr)
	}

	if err != nil {
		return fmt.Errorf("list checkpoint blobs of stack %s: %w", stackID, err)
	}

	if failed > 0 {
		return fmt.Errorf("%w: %d checkpoint blobs of stack %s", ErrBlobsRemain, failed, stackID)
	}

	return nil
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

	item, err := s.updateItem(ctx, ref, id)
	if err != nil {
		return fmt.Errorf("append events to update %s: %w", id, err)
	}

	// The payload of an event can hold a secret value in plain text, for
	// example a diagnostic message that the program printed. So each payload
	// is sealed before the row write.
	sealed, err := sealEvents(ctx, s.sealer, item, id, events)
	if err != nil {
		return fmt.Errorf("append events to update %s: %w", id, err)
	}

	batch := EventBatch{Events: sealed, Ref: ref, Update: id, Lease: u.Lease.Digest}
	if err := s.events.AppendEvents(ctx, batch, fence); err != nil {
		return fmt.Errorf("append events to update %s: %w", id, err)
	}

	return nil
}

// ErrNotJournaled refuses a journal batch for an update that saves checkpoints
// (journal version 0). It is an ErrWrongUpdateState, so the route answers 409
// as it does for the other update states, and its message is the one the
// Pulumi CLI shows to the operator.
var ErrNotJournaled error = notJournaledError{}

type notJournaledError struct{}

// Error returns the message that the route sends to the caller.
func (notJournaledError) Error() string {
	return "This update does not use journaling. Send checkpoints."
}

// Is reports that the refusal is an update state refusal.
func (notJournaledError) Is(target error) bool {
	return target == ErrWrongUpdateState
}

// AppendJournal stores one journal batch of the update (ADR 43, decision 6).
// The lease must be held. A version 0 update refuses the batch with
// ErrNotJournaled and stores nothing. The writer checks the batch against the
// update's journal version and refuses an invalid batch with ErrJournalBatch.
// A batch over MaxJournalBatchBytes is refused with ErrTooLarge, unless it
// holds one entry and fits MaxCheckpointBytes, because the CLI cannot split
// one entry.
func (s *Updates) AppendJournal(ctx context.Context, ref Ref, id UpdateID, tok token.Token, content []byte) error {
	u, ns, err := s.authorizedIn(ctx, ref, id, tok)
	if err != nil {
		return err
	}

	if u.JournalVersion == 0 {
		return ErrNotJournaled
	}

	if !u.Mutates() {
		return fmt.Errorf("%w: a preview does not save state", ErrWrongUpdateState)
	}

	// A body over both bounds is refused before any read of the stack. The
	// journal writer refuses a body over the batch bound that does not hold
	// exactly one entry when it reads the batch.
	if len(content) > MaxJournalBatchBytes && len(content) > s.maxCheckpoints {
		return fmt.Errorf(
			"append journal to update %s: %w",
			id,
			&batchLimitError{size: len(content), limit: MaxJournalBatchBytes},
		)
	}

	if s.journal == nil {
		return fmt.Errorf("journal writer of update %s: %w", id, ErrMissingDependency)
	}

	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return fmt.Errorf("append journal to update %s: %w", id, err)
	}

	batch := JournalBatch{
		Ref:       ref,
		Namespace: ns.ID,
		StackID:   st.ID,
		Update:    id,
		Lease:     u.Lease.Digest,
		Content:   content,
		Version:   int(u.JournalVersion),
		StartedAt: s.clock(),
	}

	if err := s.journal.Append(ctx, batch, ns.Fence()); err != nil {
		return fmt.Errorf("append journal to update %s: %w", id, err)
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

	u, err := s.Create(
		ctx,
		ref,
		Plan{Kind: KindImport, DryRun: false, CreatedBy: subject, RestoredFrom: 0, GitHead: ""},
	)
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

	u, err := s.Create(
		ctx,
		ref,
		Plan{Kind: KindRestore, DryRun: false, CreatedBy: subject, RestoredFrom: version, GitHead: ""},
	)
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

// Export returns the stack's latest checkpoint, or ErrNoState. While a
// journaled update runs, it returns the checkpoint built from its stored
// batches, without storing it.
// A sequenced save keeps the versioned bytes the CLI sent, so the export serves the
// inner deployment they wrap, while the digest still names the stored row.
// A checkpoint whose blob cannot be read fails with ErrCheckpointUnavailable
// and one whose bytes do not match its record with ErrCheckpointMismatch;
// both still return the stored row identity (Update and Digest) with no
// deployment, so the caller can name the checkpoint it could not read.
func (s *Updates) Export(ctx context.Context, ref Ref) (Checkpoint, error) {
	if built, ok, err := s.journaledState(ctx, ref); err != nil {
		return Checkpoint{}, err
	} else if ok {
		return built, nil
	}

	cp, err := s.checkpoints.LatestCheckpoint(ctx, ref)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s: %w", ref, err)
	}

	bl, err := s.blobs.scope(cp.NamespaceID, cp.StackID)
	if err != nil {
		return cp, fmt.Errorf("export %s: %w", ref, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, bl, cp)
	if err != nil {
		return cp, fmt.Errorf("export %s: %w", ref, err)
	}

	return innerDeployment(resolved), nil
}

// LatestStateVersion returns the version of the update that saved the stack's
// latest checkpoint, or ErrNoState. It reads the checkpoint row and the update
// row and never the blob of the checkpoint, so it costs the same for a small
// and for a large deployment. A journaled update that still runs counts only
// after it saved a checkpoint: the batches of its journal are not replayed.
func (s *Updates) LatestStateVersion(ctx context.Context, ref Ref) (int64, error) {
	cp, err := s.checkpoints.LatestCheckpoint(ctx, ref)
	if err != nil {
		return 0, fmt.Errorf("latest state of %s: %w", ref, err)
	}

	u, err := s.Get(ctx, ref, cp.Update)
	if err != nil {
		return 0, err
	}

	return u.Version, nil
}

// ExportVersion returns the checkpoint of the stack's update with the given
// version, or ErrNoState when no such update exists or it has no checkpoint.
// It never falls back to an older version. While a journaled update runs, a
// read of its version returns the checkpoint built from its stored batches,
// without storing it.
// A sequenced save keeps the
// versioned bytes the CLI sent, so the export serves the inner deployment
// they wrap.
func (s *Updates) ExportVersion(ctx context.Context, ref Ref, version int64) (Checkpoint, error) {
	if built, ok, err := s.journaledVersion(ctx, ref, version); err != nil {
		return Checkpoint{}, err
	} else if ok {
		return built, nil
	}

	cp, err := s.checkpoints.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s version %d: %w", ref, version, err)
	}

	bl, err := s.blobs.scope(cp.NamespaceID, cp.StackID)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s version %d: %w", ref, version, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, bl, cp)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("export %s version %d: %w", ref, version, err)
	}

	return innerDeployment(resolved), nil
}

// verbatimEnvelope is the versioned shape the CLI sends on verbatim saves:
// the inner deployment with its schema version. Sequenced saves keep these
// bytes exactly, because later deltas apply to that text and hash it.
type verbatimEnvelope struct {
	Deployment json.RawMessage `json:"deployment"`
}

// innerDeployment returns the checkpoint to serve for cp: the inner
// deployment of a sequenced save, or cp unchanged. A sequenced save keeps
// the versioned bytes, so reads unwrap them without decoding and encoding
// them again: the inner bytes stay exactly as the CLI sent them. Anything
// else, including a save that carries no envelope, serves as stored.
func innerDeployment(cp Checkpoint) Checkpoint {
	cp.Deployment = innerDeploymentBytes(cp.Deployment, cp.Sequence)

	return cp
}

// innerDeploymentBytes returns the inner deployment of a sequenced save:
// the deployment a versioned envelope wraps. Anything else, including a
// save that carries no envelope, serves as stored. The delete guard counts
// through this same path, so a sequenced save counts its resources, not
// the envelope, which holds no resources array.
func innerDeploymentBytes(deployment []byte, seq int64) []byte {
	if seq <= 0 || len(deployment) == 0 {
		return deployment
	}

	var env verbatimEnvelope
	if err := json.Unmarshal(deployment, &env); err != nil || len(env.Deployment) == 0 {
		return deployment
	}

	return []byte(env.Deployment)
}

// MaxCheckpointBytes returns the largest checkpoint SaveCheckpoint accepts.
func (s *Updates) MaxCheckpointBytes() int {
	return s.maxCheckpoints
}

// MaxJournalBatchBytes returns the largest journal batch AppendJournal accepts.
func (s *Updates) MaxJournalBatchBytes() int {
	return MaxJournalBatchBytes
}

// Get returns one update.
func (s *Updates) Get(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	u, err := s.updates.GetUpdate(ctx, ref, id)
	if err != nil {
		return Update{}, fmt.Errorf("get update %s of %s: %w", id, ref, err)
	}

	return u, nil
}

// CheckLease reports whether tok is the lease token of the update. It reads
// the update with Get and matches tok against the stored digest, as the
// complete retry does. It ignores the expiry and the status: the digest stays
// after the update ends, so the check only proves that the caller holds the
// token of this update. It writes nothing. An unknown update returns the same
// error that Get returns.
func (s *Updates) CheckLease(ctx context.Context, ref Ref, id UpdateID, tok token.Token) error {
	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return err
	}

	if !u.Lease.Digest.Matches(tok) {
		return fmt.Errorf("update %s of %s: %w", id, ref, ErrLeaseInvalid)
	}

	return nil
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
// It returns ErrNotFound when the stack does not exist.
func (s *Updates) History(ctx context.Context, ref Ref, page Page) ([]Update, error) {
	if page.Limit <= 0 {
		page.Limit = defaultPageSize
	}

	page.Limit = min(page.Limit, maxPageSize)
	page.Offset = max(page.Offset, 0)

	// The store answers an empty list for a stack that does not exist, so the
	// service reads the stack first: a caller must not read "no updates" for
	// "no such stack".
	//
	// Defense in depth: the route reads the stack before it calls History, so
	// it answers with the error of the stack route. This check stays so that a
	// new caller cannot read an empty history for a stack that is missing.
	if _, err := s.stacks.GetStack(ctx, ref); err != nil {
		return nil, fmt.Errorf("history of %s: %w", ref, err)
	}

	list, err := s.updates.ListUpdates(ctx, ref, page)
	if err != nil {
		return nil, fmt.Errorf("history of %s: %w", ref, err)
	}

	if len(list) == 0 {
		// The stack can be deleted between the two reads above. The store
		// answers an empty list for a stack that does not exist, so an empty
		// list reads the stack again.
		//
		// Defense in depth: the route reads the stack before it calls History,
		// so it answers with the error of the stack route. This check stays
		// for the same reason as the read above: a new caller cannot read an
		// empty history for a stack that is missing.
		if _, stackErr := s.stacks.GetStack(ctx, ref); stackErr != nil {
			return nil, fmt.Errorf("history of %s: %w", ref, stackErr)
		}
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

	if len(events) == 0 {
		return events, nil
	}

	item, err := s.updateItem(ctx, ref, id)
	if err != nil {
		return nil, fmt.Errorf("events of update %s: %w", id, err)
	}

	if err := openEvents(ctx, s.sealer, item, id, events); err != nil {
		return nil, fmt.Errorf("events of update %s: %w", id, err)
	}

	return events, nil
}

// updateItem returns the reference of the item key of the update id of the
// stack ref.
func (s *Updates) updateItem(ctx context.Context, ref Ref, id UpdateID) (secret.ItemRef, error) {
	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return secret.ItemRef{}, err
	}

	return UpdateItem(ref.Namespace, st.ID, id)
}

// leaseGet reads one update for a lease request. A token-started update must
// still pass the access token check (ADR 53). The check uses the token ID of
// this read, so a lease request costs one read of the update.
func (s *Updates) leaseGet(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Update{}, err
	}

	if u.TokenID == "" {
		return u, nil
	}

	if s.tokens == nil {
		return Update{}, fmt.Errorf("%w: update %s", ErrAccessTokenUnchecked, id)
	}

	if err := s.tokens.CheckActive(ctx, u.TokenID); err != nil {
		return Update{}, fmt.Errorf("access token of update %s: %w", id, err)
	}

	return u, nil
}

// storeNewCheckpoint writes the blob of save under the key of its attempt,
// then its row. The row decides: a save that repeats the sequence and digest
// of the row, or that conflicts with it, deletes its own blob.
func (s *Updates) storeNewCheckpoint(ctx context.Context, save checkpointSave) error {
	return s.writeCheckpoint(ctx, checkpointWrite{
		op: "save checkpoint", ref: save.ref, id: save.id, blobs: save.blobs, stackID: save.stackID,
		key: save.key, deployment: save.in.Deployment,
		commit: func(ctx context.Context, size int64, sum [md5.Size]byte, started time.Time) (bool, error) {
			return s.commitCheckpoint(ctx, save, size, sum, started)
		},
	})
}

// commitCheckpoint commits the row of save, then cleans up after the save.
// known is true when the row already holds the sequence and digest of save:
// the first write keeps its blob, nothing else changes and no cleanup runs. A
// failed commit leaves the blob without a row that names it; the sweep
// collects it once it is older than the grace period.
func (s *Updates) commitCheckpoint(
	ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte, started time.Time,
) (bool, error) {
	known, err := s.commitCheckpointRow(ctx, save, size, sum, started)
	if err != nil {
		return false, fmt.Errorf("save checkpoint of update %s: %w", save.id, err)
	}

	if known {
		return true, nil
	}

	s.afterCheckpointSave(ctx, save)

	return false, nil
}

// commitCheckpointRow writes the fenced SQL row of save naming its blob, with
// the start of the attempt that wrote the blob.
func (s *Updates) commitCheckpointRow(
	ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte, started time.Time,
) (bool, error) {
	cp := Checkpoint{
		NamespaceID: "", StackID: "",
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
		Origin:        save.origin,

		AttemptStartedAt: started,
	}

	return s.checkpoints.PutCheckpoint(ctx, cp, save.fence)
}

// afterCheckpointSave deletes the blob of the previous save of the same
// update and sweeps its orphan blobs. Both are best effort: a cleanup
// failure never fails the save, and the rows stay for the next sweep. No log
// line names a blob key.
func (s *Updates) afterCheckpointSave(ctx context.Context, save checkpointSave) {
	// The guard comes after the commit of the new row. A running backup or
	// restore keeps the previous blob. It is then an orphan of the update, and
	// the next sweep deletes it when nothing runs.
	if save.prev != nil && save.prev.BlobKey != save.key && !s.deletesHeld(ctx, "checkpoint replace", save.ref) {
		// The key comes from a row. It must name a save of this update in this
		// stack, so the access is narrowed to the directory of the update.
		own := save.blobs.narrow(save.blobs.updatePrefix(save.stackID, save.id))

		if err := s.deleteBlobRetried(ctx, own, save.prev.BlobKey); err != nil {
			s.logger.WarnContext(
				ctx,
				"delete superseded checkpoint blob",
				slog.String("stack", save.ref.String()),
				slog.String("update", save.id.String()),
			)
		}
	}

	s.sweepUpdateBlobs(ctx, save.blobs, save.ref, save.stackID, save.id)
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
// size and MD5 of the stored bytes for backups. It compresses at the fastest
// level: a checkpoint save is latency sensitive, and the default level costs
// about one second for a 24 MiB deployment, over 15 seconds under the race
// detector, while the fastest level costs under one second in both. Stored
// bytes stay larger, but readers decompress any level, so the change needs
// no migration. It holds one decompressed copy (deployment) with one
// compressed copy (the gzip buffer). No error text names the key: callers
// match ErrExists and redact the rest.
func (s *Updates) putCheckpointBlob(
	ctx context.Context, bl *scopedBlobs, key blob.Key, deployment []byte,
) (int64, [md5.Size]byte, error) {
	var zipped bytes.Buffer

	w, err := gzip.NewWriterLevel(&zipped, gzip.BestSpeed)
	if err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("compress checkpoint: %w", err)
	}

	if _, err := w.Write(deployment); err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("compress checkpoint: %w", err)
	}

	if err := w.Close(); err != nil {
		return 0, [md5.Size]byte{}, fmt.Errorf("compress checkpoint: %w", err)
	}

	gave := zipped.Bytes()

	size, sum := storedAttrs(gave)

	if err := bl.Put(ctx, key, bytes.NewReader(gave)); err != nil {
		if errors.Is(err, blob.ErrExists) {
			// The retry compares these against the stored bytes without
			// decompressing anything; other errors carry no attributes.
			return size, sum, fmt.Errorf("store checkpoint: %w", err)
		}

		return 0, [md5.Size]byte{}, fmt.Errorf("store checkpoint: %w", err)
	}

	return size, sum, nil
}

// storedLimit bounds one stored checkpoint blob: the uncompressed limit with
// slack for gzip overhead.
func (s *Updates) storedLimit() int64 {
	return int64(s.maxCheckpoints) + storedBlobSlack
}

// deleteBlobRetried removes the object under key, retrying a transient
// failure after a short backoff (blob.DeleteRetried). Deleting a missing key
// is not an error.
func (s *Updates) deleteBlobRetried(ctx context.Context, bl *scopedBlobs, key blob.Key) error {
	if err := blob.DeleteRetried(ctx, bl, key); err != nil {
		return fmt.Errorf("delete blob: %w", err)
	}

	return nil
}

// sweepDeleter names the sweep for a log line: the update sweep when id names
// an update, else the stack sweep.
func sweepDeleter(id UpdateID) string {
	if id != "" {
		return "update sweep"
	}

	return "stack sweep"
}

// deletesHeld reports whether a blob delete of deleter must not run now. It is
// true when a backup or a restore runs, and when the guard fails or is not
// set: every case but "nothing runs" fails closed. The blobs stay, and a later
// sweep deletes them. The first skip for each running backup or restore logs
// at INFO and the next ones at DEBUG, because a checkpoint save asks on each
// save. No log line names a blob key.
func (s *Updates) deletesHeld(ctx context.Context, deleter string, ref Ref) bool {
	err := blobguard.Hold(ctx, s.guard)
	if err == nil {
		return false
	}

	wait, isWait := errors.AsType[*blobguard.WaitError](err)
	if !isWait {
		s.logger.WarnContext(ctx, "blob delete skipped: the guard failed",
			slog.String("deleter", deleter), slog.String("stack", ref.String()),
			slog.String("error_kind", inventory.ErrorKind(err)))

		return true
	}

	level := slog.LevelDebug

	s.skipMu.Lock()

	if s.skipLogged != wait.Work.String() {
		s.skipLogged = wait.Work.String()
		level = slog.LevelInfo
	}

	s.skipMu.Unlock()

	s.logger.Log(ctx, level, "blob delete skipped: a backup or restore runs",
		slog.String("deleter", deleter), slog.String("stack", ref.String()),
		slog.String("running", wait.Work.String()))

	return true
}

// sweepUpdateBlobs deletes the blobs of one update that no row names and that
// are older than the grace period: saves whose SQL commit never landed,
// previous saves whose delete never ran, and journal batches of a closed
// journal. The listing covers one update prefix, so it stays small no matter
// how many updates the stack holds. It is best effort: a cleanup failure
// never fails the save, and the rows stay for the next sweep. No log line
// names a blob key.
func (s *Updates) sweepUpdateBlobs(ctx context.Context, bl *scopedBlobs, ref Ref, stackID ID, id UpdateID) {
	listed, err := bl.listAll(ctx, bl.updatePrefix(stackID, id))
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	s.sweepOrphans(ctx, bl, sweepTarget{ref: ref, stackID: stackID, update: id}, listed)
}

// sweepTarget names what a sweep covers: the stack by its reference (for log
// lines) and by its ID (for the keep set), and one update, or none for the
// whole stack.
type sweepTarget struct {
	ref     Ref
	stackID ID
	update  UpdateID
}

// sweepNow returns the time that a sweep takes as now: the clock of the
// database, which the checkpoint store reads. It returns blob.ErrClockSkew
// when the store cannot read it, or when the clock of the instance differs
// from it by more than blob.AttemptSkew.
func (s *Updates) sweepNow(ctx context.Context) (time.Time, error) {
	// A store with no clock leaves clock nil, and SweepNow refuses nil.
	var clock blob.DatabaseClock
	if c, ok := s.checkpoints.(blob.DatabaseClock); ok {
		clock = c
	}

	now, err := blob.SweepNow(ctx, clock, s.clock())
	if err != nil {
		return time.Time{}, fmt.Errorf("sweep clock: %w", err)
	}

	return now, nil
}

// graceCandidates returns the objects of listed that are older than the grace
// period at now. An object with no modification time is never a candidate.
func graceCandidates(listed []blob.Object, now time.Time) []blob.Object {
	candidates := make([]blob.Object, 0, len(listed))

	for _, obj := range listed {
		if blob.OldEnough(obj.ModifiedAt, now) {
			candidates = append(candidates, obj)
		}
	}

	return candidates
}

// unnamed returns the objects of candidates that no row names, read by the
// stack ID of the target. It returns false, and logs, when the read fails.
func (s *Updates) unnamed(ctx context.Context, target sweepTarget, candidates []blob.Object) ([]blob.Object, bool) {
	refs, err := s.checkpoints.ReferencedBlobKeys(ctx, target.stackID)
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", target.ref.String()))

		return nil, false
	}

	named := make(map[blob.Key]struct{}, len(refs))

	for _, key := range refs {
		named[key] = struct{}{}
	}

	doomed := make([]blob.Object, 0, len(candidates))

	for _, obj := range candidates {
		if _, ok := named[obj.Key]; !ok {
			doomed = append(doomed, obj)
		}
	}

	return doomed, true
}

// sweepOrphans deletes the objects of listed that are older than the grace
// period and that no row names. The keep set is ReferencedBlobKeys: a
// checkpoint blob and a journal batch blob of a running journaled update both
// stay, whatever prefix holds them. The sweep reads the keys only when old
// candidates remain, so a start with only fresh blobs pays for one listing
// and no row read. A read failure deletes nothing, also when the stack row is
// gone. The keep set is read by the stack ID that the listing used, so a
// rename between the two reads cannot empty it. A running backup or restore
// keeps every blob: the guard is read after the listing and after the keys,
// and before each delete. The update ID is empty for a sweep over the
// whole stack.
func (s *Updates) sweepOrphans(
	ctx context.Context, bl *scopedBlobs, target sweepTarget, listed []blob.Object,
) {
	ref, id := target.ref, target.update

	// The clock of the database costs a round trip, so the sweep reads it only
	// when a blob could be old. When the clocks agree to AttemptSkew, the
	// database is at most AttemptSkew ahead of this instance, so a blob that is
	// not old by the instance clock plus AttemptSkew is not old by the clock of
	// the database. When the clocks do not agree, sweepNow refuses the run and
	// nothing is deleted, so skipping it changes only the log line.
	if len(graceCandidates(listed, s.clock().Add(blob.AttemptSkew))) == 0 {
		return
	}

	now, err := s.sweepNow(ctx)
	if err != nil {
		// The grace period holds only when the clocks agree, so a run with a
		// clock that it cannot trust deletes nothing.
		s.logger.WarnContext(ctx, "blob delete skipped: the clocks of the instance and the database do not agree",
			slog.String("deleter", sweepDeleter(id)), slog.String("stack", ref.String()),
			slog.String("error", err.Error()))

		return
	}

	candidates := graceCandidates(listed, now)
	if len(candidates) == 0 {
		return
	}

	doomed, ok := s.unnamed(ctx, target, candidates)
	if !ok || len(doomed) == 0 {
		return
	}

	failed := 0

	for _, obj := range doomed {
		// The guard comes after the listing and after the read of the named
		// keys. A blob that is unnamed here lost its last name in a commit
		// before that read, so a backup that the guard does not see cannot name
		// it. The guard is read again before each delete, so a restore that
		// starts during the loop keeps the blobs that are left. A running
		// backup or restore keeps them for the next sweep.
		if s.deletesHeld(ctx, sweepDeleter(id), ref) {
			return
		}

		if blobErr := s.deleteBlobRetried(ctx, bl, obj.Key); blobErr != nil {
			failed++
		}
	}

	if failed > 0 {
		attrs := []any{slog.String("stack", ref.String()), slog.Int("failed", failed)}
		if id != "" {
			attrs = append(attrs, slog.String("update", id.String()))
		}

		s.logger.WarnContext(ctx, "sweep checkpoint blobs", attrs...)
	}
}

// sweepStackOnStart resolves the stack ID and sweeps its orphan blobs. It is
// best effort: a cleanup failure never fails the start, and the blobs stay
// for the next sweep. No log line names a blob key.
func (s *Updates) sweepStackOnStart(ctx context.Context, ns namespace.ID, ref Ref) {
	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	bl, err := s.blobs.scope(ns, st.ID)
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))

		return
	}

	s.sweepStackBlobs(ctx, bl, ref, st.ID)
}

// sweepStackBlobs deletes the blobs of one stack that no row names and that
// are older than the grace period: orphans of an update that never saves
// again, such as a crash between the blob write and the SQL commit, the blob
// of a save that the row refused, or a delete race loser. The next update that starts on the
// stack collects them. Blobs younger than the grace period stay, so a save
// that still commits is never collected: every new blob is fresh, and the row
// names it only after the bytes land. A blob that a row names stays at any
// age, also a journal batch of a journaled update that runs for more than the
// grace period while a preview starts on the stack. It is best effort: a
// cleanup failure never fails the start, and the blobs stay for the next
// sweep. No log line names a blob key.
func (s *Updates) sweepStackBlobs(ctx context.Context, bl *scopedBlobs, ref Ref, stackID ID) {
	// The sweep goes page by page: each page is listed, then the keep set is
	// read for the page, then the orphans of the page go. So the sweep never
	// holds the blobs of the whole stack.
	target := sweepTarget{ref: ref, stackID: stackID, update: ""}

	err := blob.WalkPages(ctx, bl, bl.stackPrefix(stackID), func(page []blob.Object) error {
		s.sweepOrphans(ctx, bl, target, page)

		return nil
	})
	if err != nil {
		s.logger.WarnContext(ctx, "sweep checkpoint blobs", slog.String("stack", ref.String()))
	}
}

// resolveCheckpoint fills the deployment of a checkpoint row: it reads and
// decompresses the blob of the row. The bytes match the stored digest, so a
// blob with other content fails instead of serving foreign state. The read
// holds one decompressed copy.
func (s *Updates) resolveCheckpoint(ctx context.Context, bl *scopedBlobs, cp Checkpoint) (Checkpoint, error) {
	// The key comes from a row. It must name a save of the update of the row
	// in the stack of bl, so the read is narrowed to that directory.
	prefix := bl.updatePrefix(bl.stack, cp.Update)

	raw, err := bl.narrow(prefix).Get(ctx, cp.BlobKey)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Checkpoint{}, fmt.Errorf("resolve checkpoint: %w", cerr)
		}

		// A row that names a blob outside the scope of its namespace shows a
		// fault or an attack. The caller sees the refusal itself, and the
		// view wrote the security entry.
		if errors.Is(err, blob.ErrOutOfScope) {
			return Checkpoint{}, blob.ErrOutOfScope
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

	// The number in the key is the sequence of the row. A row whose key names
	// another save is a corrupt record, and its blob is not served. The scoped
	// read comes first, so a key outside the scope gets its own refusal.
	//
	// Defense in depth: the row store checks the key of a write, and the write
	// path builds it from the sequence. This check stays so that a row that
	// reached the store by other means cannot serve the blob of another save.
	name, named := parseCheckpointName(strings.TrimPrefix(cp.BlobKey.String(), prefix))
	if !named || strconv.FormatUint(name.number, 10) != strconv.FormatInt(cp.Sequence, 10) {
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
		// The size is within the limit, so the scan of the shape is bounded.
		return checkDeploymentShape(in.Deployment, in.Sequence)
	}
}

// restoreInput returns the checkpoint input that restores version of ref: a
// copy of that version's checkpoint. It returns ErrNoState when the version
// has no checkpoint, before any update exists. A sequenced save keeps the
// versioned bytes the CLI sent, so the copy holds the inner deployment they
// wrap, as a new unsequenced save.
func (s *Updates) restoreInput(ctx context.Context, ref Ref, version int64) (CheckpointInput, error) {
	src, err := s.checkpoints.CheckpointByVersion(ctx, ref, version)
	if err != nil {
		return CheckpointInput{}, fmt.Errorf("restore %s version %d: %w", ref, version, err)
	}

	bl, err := s.blobs.scope(src.NamespaceID, src.StackID)
	if err != nil {
		return CheckpointInput{}, fmt.Errorf("restore %s version %d: %w", ref, version, err)
	}

	resolved, err := s.resolveCheckpoint(ctx, bl, src)
	if err != nil {
		return CheckpointInput{}, fmt.Errorf("restore %s version %d: %w", ref, version, err)
	}

	inner := innerDeployment(resolved)

	in := CheckpointInput{
		Features:      inner.Features,
		Deployment:    inner.Deployment,
		SchemaVersion: inner.SchemaVersion,
		Sequence:      0,
		Invalid:       inner.Invalid,
	}

	return in, s.validate(in)
}

// transition applies change to the stored update and saves the result. The
// CLI renews its lease from a background loop, so a renewal can win the
// compare-and-swap while a completion is in flight. change is a pure function
// of the stored update, so it is re-applied to a fresh read: a completion that
// raced a cancel then fails for the right reason.
// transitionOp is one change of an update: the read that loads it, and the
// change that moves it on.
type transitionOp struct {
	read   func(context.Context, Ref, UpdateID) (Update, error)
	change func(Update, time.Time) (Update, error)
}

func (s *Updates) transition(
	ctx context.Context, ref Ref, id UpdateID, change func(Update, time.Time) (Update, error),
) (Update, error) {
	return s.transitionFrom(ctx, ref, id, transitionOp{read: s.Get, change: change})
}

// leaseTransition is transition for a lease request: the update it changes
// must still pass the access token check of leaseGet.
func (s *Updates) leaseTransition(
	ctx context.Context, ref Ref, id UpdateID, change func(Update, time.Time) (Update, error),
) (Update, error) {
	return s.transitionFrom(ctx, ref, id, transitionOp{read: s.leaseGet, change: change})
}

func (s *Updates) transitionFrom(ctx context.Context, ref Ref, id UpdateID, op transitionOp) (Update, error) {
	fence, err := s.fence(ctx, ref)
	if err != nil {
		return Update{}, err
	}

	for attempt := 1; ; attempt++ {
		u, err := s.applyOnce(ctx, ref, id, op, fence)
		if !errors.Is(err, ErrConcurrentModification) || attempt == transitionAttempts {
			return u, err
		}
	}
}

func (s *Updates) applyOnce(
	ctx context.Context, ref Ref, id UpdateID, op transitionOp, fence namespace.Fence,
) (Update, error) {
	u, err := op.read(ctx, ref, id)
	if err != nil {
		return Update{}, err
	}

	before := u.Revision

	u, err = op.change(u, s.clock())
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
	u, ns, err := s.authorizedIn(ctx, ref, id, tok)
	if err != nil {
		return Update{}, namespace.Fence{}, err
	}

	return u, ns.Fence(), nil
}

// authorizedIn is authorized, and it also returns the namespace row that it
// resolved. A call that touches blobs takes the namespace ID from that row for
// the scoped blob view.
func (s *Updates) authorizedIn(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token,
) (Update, namespace.Namespace, error) {
	ns, err := s.writable(ctx, ref)
	if err != nil {
		return Update{}, namespace.Namespace{}, err
	}

	u, err := s.leaseGet(ctx, ref, id)
	if err != nil {
		return Update{}, namespace.Namespace{}, err
	}

	if err := u.authorize(tok, s.clock()); err != nil {
		return Update{}, namespace.Namespace{}, fmt.Errorf("update %s of %s: %w", id, ref, err)
	}

	return u, ns, nil
}

// writable returns the namespace row of ref, which must be homed here.
func (s *Updates) writable(ctx context.Context, ref Ref) (namespace.Namespace, error) {
	ns, err := s.namespaces.WritableHere(ctx, ref.Namespace)
	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("write to %s: %w", ref, err)
	}

	return ns, nil
}

// fence returns the fence for writes to ref's namespace, which must be homed here.
func (s *Updates) fence(ctx context.Context, ref Ref) (namespace.Fence, error) {
	ns, err := s.writable(ctx, ref)
	if err != nil {
		return namespace.Fence{}, err
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

// checkpointBlobKey returns the blob key of one save in the scope of bl:
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>-<attempt ID>.
// The attempt ID is new for each write attempt, so no two writes of the
// same update share a key, also not two saves without a sequence number. No
// error text names a key.
func checkpointBlobKey(bl *scopedBlobs, stackID ID, id UpdateID, seq int64) (blob.Key, error) {
	key, err := blob.ParseKey(bl.updatePrefix(stackID, id) + strconv.FormatInt(seq, 10) + "-" + newAttemptID())
	if err != nil {
		return "", ErrCheckpointMismatch
	}

	return key, nil
}

// newAttemptID returns the attempt ID of one blob write: 20 random lowercase
// letters and digits that the writer makes before the write.
func newAttemptID() string {
	return ident.NewID("")
}

// storedAttrs returns the size and MD5 of stored gzip bytes, for backups.
func storedAttrs(gave []byte) (int64, [md5.Size]byte) {
	digest := blob.DigestBytes(gave)

	return digest.Size, digest.MD5
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

// completeJournaled ends the update with the status its client reports,
// building the checkpoint of a journaled update first. A missing update
// fails here, like the transition would. The lease token and the requested
// status are checked before the build, with the same checks the completion
// uses, so a refused completion keeps the journal and stores no checkpoint.
func (s *Updates) completeJournaled(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, status UpdateStatus,
) (Update, error) {
	if _, err := s.fence(ctx, ref); err != nil {
		return Update{}, err
	}

	u, err := s.leaseGet(ctx, ref, id)
	if err != nil {
		return Update{}, err
	}

	if _, err := u.complete(tok, s.clock(), status); err != nil {
		return Update{}, fmt.Errorf("update %s of %s: %w", id, ref, err)
	}

	if status == StatusSucceeded {
		if err := s.checkJournalComplete(ctx, ref, id, u); err != nil {
			return Update{}, err
		}
	}

	if err := s.buildBeforeEnd(ctx, ref, id, u); err != nil {
		return Update{}, err
	}

	return s.leaseTransition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.complete(tok, now, status)
	})
}

// cancelJournaled ends the update without its lease, building the checkpoint
// of a journaled update first. A missing update fails here, like the
// transition would.
func (s *Updates) cancelJournaled(ctx context.Context, ref Ref, id UpdateID) (Update, error) {
	u, err := s.Get(ctx, ref, id)
	if err != nil {
		return Update{}, err
	}

	if err := s.buildBeforeEnd(ctx, ref, id, u); err != nil {
		return Update{}, err
	}

	return s.transition(ctx, ref, id, func(u Update, now time.Time) (Update, error) {
		return u.cancel(now)
	})
}

// checkManualLock refuses the start of a mutating update on the stack ref while
// a manual lock covers it. The error matches ErrUpdateConflict and
// manuallock.ErrLocked, and names the holder and the reason of the manual lock.
func (s *Updates) checkManualLock(ctx context.Context, ref Ref, jobID string) error {
	return refuseManualLock(ctx, s.manual, ref, jobID)
}

// refuseManualLock returns an error that matches ErrUpdateConflict and
// manuallock.ErrLocked while a manual lock covers the stack ref, and nil when
// locks is nil or no lock covers the stack for the request. A lock that cannot
// be read refuses the request: the check fails closed.
func refuseManualLock(ctx context.Context, locks ManualLocks, ref Ref, jobID string) error {
	if locks == nil {
		return nil
	}

	unit := collection.UnitRef{
		Namespace: ref.Namespace, Tool: InventoryTool, Name: ref.Project.String() + "/" + ref.Stack.String(),
	}

	l, err := locks.CoveringFor(ctx, unit, jobID)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of %s: %w", ref, err)
	}

	return fmt.Errorf("%w: %w", ErrUpdateConflict, &manuallock.LockedError{
		Lock: l, Unit: unit, Noun: "stack", Name: ref.String(),
	})
}
