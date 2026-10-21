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
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
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
// The content lives in the blob store under the row's blob key. The store
// never touches the blob store: callers write the content before they call
// Put and read it back after they read a row.
type CheckpointStore interface {
	// PutCheckpoint saves cp as the state of update cp.Update, in the same
	// transaction as checking that update cp.Update is still running under
	// lease cp.Lease; otherwise it returns ErrWrongUpdateState, so a write
	// authorized before a cancel cannot land after it. It returns
	// ErrWrongUpdateState when the update is a preview or a dry run, which
	// never saves state. For cp.Sequence > 0 it is idempotent: if the
	// update's last checkpoint has the same sequence and digest it returns
	// nil without writing, a different digest returns ErrCheckpointConflict,
	// and a lower sequence returns ErrStaleCheckpoint. The row keeps the
	// blob key with the size, the digest and the sequence, and no content.
	// A checkpoint without a blob key returns ErrCheckpointNoKey.
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
	// Tokens checks the access token that started a running update, on each
	// lease request (ADR 53). Nil refuses every update that an access token
	// started, so the check fails closed.
	Tokens TokenChecker
	// Stacks resolves stack references to their stable IDs for checkpoint
	// blob keys.
	Stacks CheckpointStacks
	// Blobs holds checkpoint content under
	// "namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>",
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
	}

	access, err := newBlobAccess(cfg.Blobs, cfg.Refusals)
	if err != nil {
		return nil, err
	}

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

// sweepGracePeriod is how long after its write a checkpoint blob stays before
// the unreferenced sweep deletes it, so a save that still commits is never
// collected: every new blob is fresh, and the row names it only after the
// bytes land.
const sweepGracePeriod = time.Hour

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
	// blobs is the scoped blob access of the namespace of this save.
	blobs *scopedBlobs
}

// SaveCheckpoint stores the stack's state as of now in the update. Previews
// do not save state.
//
// The content goes to the blob store first, gzipped, under
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>; then the fenced SQL row names it.
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

	key, err := checkpointBlobKey(bl, st.ID, id, in.Sequence, prev)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
	}

	return s.storeNewCheckpoint(ctx, checkpointSave{
		ref: ref, id: id, stackID: st.ID, fence: ns.Fence(), lease: u.Lease.Digest,
		in: in, digest: digest, prev: prev, key: key, blobs: bl,
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

	listed, err := bl.List(ctx, bl.stackPrefix(stackID))
	if err != nil {
		return fmt.Errorf("list checkpoint blobs of stack %s: %w", stackID, err)
	}

	failed := 0

	for _, obj := range listed {
		if blobErr := s.deleteBlobRetried(ctx, bl, obj.Key); blobErr != nil {
			failed++
		}
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

	batch := EventBatch{Events: events, Ref: ref, Update: id, Lease: u.Lease.Digest}
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

	if batchTooLarge(content, s.maxCheckpoints) {
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

// storeNewCheckpoint writes the blob of save, then its row. A blob that is
// already there belongs to a first attempt whose commit never landed: the
// retry adopts it when it holds the same bytes, and checkpointCollision
// answers any other blob.
func (s *Updates) storeNewCheckpoint(ctx context.Context, save checkpointSave) error {
	return s.writeCheckpoint(ctx, checkpointWrite{
		op: "save checkpoint", id: save.id, blobs: save.blobs, key: save.key, deployment: save.in.Deployment,
		other: func(ctx context.Context, _ []byte, _ int64, _ [md5.Size]byte) error {
			// Anything but a retry of a committed save is a concurrent save
			// of the same update, which fails without touching SQL, so no
			// row can name a blob with other content.
			return s.checkpointCollision(ctx, save)
		},
		commit: func(ctx context.Context, size int64, sum [md5.Size]byte) error {
			return s.commitCheckpoint(ctx, save, size, sum)
		},
	})
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
	prefix := save.blobs.updatePrefix(save.stackID, save.id)

	next, err := nextBlobKey(prefix, save.key)
	if err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", save.id, ErrConcurrentModification)
	}

	size, sum, err := s.putCheckpointBlob(ctx, save.blobs, next, save.in.Deployment)
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

	return s.commitCheckpoint(ctx, save, size, sum)
}

// commitCheckpoint commits the row of save, then cleans up after the save.
// A failed commit leaves the blob without a row that names it; the sweep
// collects it once it is older than the grace period.
func (s *Updates) commitCheckpoint(ctx context.Context, save checkpointSave, size int64, sum [md5.Size]byte) error {
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
	}

	return s.checkpoints.PutCheckpoint(ctx, cp, save.fence)
}

// afterCheckpointSave deletes the blob of the previous save of the same
// update and sweeps its orphan blobs. Both are best effort: a cleanup
// failure never fails the save, and the rows stay for the next sweep. No log
// line names a blob key.
func (s *Updates) afterCheckpointSave(ctx context.Context, save checkpointSave) {
	if save.prev != nil && save.prev.BlobKey != save.key {
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

// sweepUpdateBlobs deletes the checkpoint blobs of one update that no
// checkpoint names and that are older than the grace period: saves whose SQL
// commit never landed, and previous saves whose delete never ran. The listing
// covers one update prefix, so it stays small no matter how many updates the
// stack holds. It is best effort: a cleanup failure never fails the save,
// and the rows stay for the next sweep. No log line names a blob key.
func (s *Updates) sweepUpdateBlobs(ctx context.Context, bl *scopedBlobs, ref Ref, stackID ID, id UpdateID) {
	listed, err := bl.List(ctx, bl.updatePrefix(stackID, id))
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

	s.sweepCandidates(ctx, bl, ref, id, candidates)
}

// sweepCandidates deletes the candidate blobs of one update except the one
// its checkpoint names. A missing checkpoint keeps none: without a row every
// blob of the update is an orphan.
func (s *Updates) sweepCandidates(
	ctx context.Context, bl *scopedBlobs, ref Ref, id UpdateID, candidates []blob.Object,
) {
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

		if blobErr := s.deleteBlobRetried(ctx, bl, obj.Key); blobErr != nil {
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
func (s *Updates) sweepStackBlobs(ctx context.Context, bl *scopedBlobs, ref Ref, stackID ID) {
	listed, err := bl.List(ctx, bl.stackPrefix(stackID))
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

		if blobErr := s.deleteBlobRetried(ctx, bl, obj.Key); blobErr != nil {
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

// resolveCheckpoint fills the deployment of a checkpoint row: it reads and
// decompresses the blob of the row. The bytes match the stored digest, so a
// blob with other content fails instead of serving foreign state. The read
// holds one decompressed copy.
func (s *Updates) resolveCheckpoint(ctx context.Context, bl *scopedBlobs, cp Checkpoint) (Checkpoint, error) {
	// The key comes from a row. It must name a save of the update of the row
	// in the stack of bl, so the read is narrowed to that directory.
	raw, err := bl.narrow(bl.updatePrefix(bl.stack, cp.Update)).Get(ctx, cp.BlobKey)
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
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/<sequence>. A save without a sequence number
// mints the next key of its update after the previous one, so every save
// writes a new key while the row keeps sequence 0. No error text names a key.
func checkpointBlobKey(bl *scopedBlobs, stackID ID, id UpdateID, seq int64, prev *Checkpoint) (blob.Key, error) {
	prefix := bl.updatePrefix(stackID, id)

	if seq > 0 || prev == nil {
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

	l, err := locks.CoveringFor(ctx, collection.UnitRef{
		Namespace: ref.Namespace, Tool: InventoryTool, Name: ref.Project.String() + "/" + ref.Stack.String(),
	}, jobID)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of %s: %w", ref, err)
	}

	return fmt.Errorf("%w: %w", ErrUpdateConflict, &manuallock.LockedError{Lock: l, Noun: "stack", Name: ref.String()})
}
