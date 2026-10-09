package stack

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/namespace"
)

const (
	// DefaultMaxCheckpointBytes bounds one checkpoint unless configured otherwise.
	DefaultMaxCheckpointBytes = 256 << 20

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
	// CreateUpdate persists a new, not-started u. It returns ErrNotFound if
	// the stack does not exist.
	CreateUpdate(ctx context.Context, u Update, fence namespace.Fence) error
	// GetUpdate returns the update, or ErrUpdateNotFound.
	GetUpdate(ctx context.Context, ref Ref, id UpdateID) (Update, error)
	// StartUpdate persists the started u and returns it with Version assigned:
	// one more than the highest version of any update of the stack if
	// u.Mutates(), else the stack's current version. A mutating u fails with
	// ErrUpdateConflict while another mutating update of the stack is running.
	// Any u fails with ErrConcurrentModification unless the store holds
	// u.Revision-1.
	StartUpdate(ctx context.Context, u Update, fence namespace.Fence) (Update, error)
	// SaveUpdate persists a transition of u if the store holds u.Revision-1,
	// else returns ErrConcurrentModification. When a started mutating update
	// ends, the stack's Version becomes u.Version in the same transaction.
	SaveUpdate(ctx context.Context, u Update, fence namespace.Fence) error
	// ActiveUpdate returns the stack's running update that Mutates, or ErrUpdateNotFound.
	ActiveUpdate(ctx context.Context, ref Ref) (Update, error)
	// ListUpdates returns the stack's started updates that Mutates, newest
	// first. A stack without updates, or one that does not exist, yields an
	// empty list.
	ListUpdates(ctx context.Context, ref Ref, page Page) ([]Update, error)
}

// CheckpointStore persists checkpoints. Adapters typically keep the metadata
// in SQL and the deployment in the blob port.
type CheckpointStore interface {
	// PutCheckpoint saves cp as the stack's latest state, in the same
	// transaction as checking that update cp.Update is still running under
	// lease cp.Lease; otherwise it returns ErrWrongUpdateState, so a write
	// authorized before a cancel cannot land after it. For cp.Sequence > 0 it
	// is idempotent: if the update's last checkpoint has the same sequence and
	// digest it returns nil without writing, a different digest returns
	// ErrCheckpointConflict, and a lower sequence returns ErrStaleCheckpoint.
	PutCheckpoint(ctx context.Context, cp Checkpoint, fence namespace.Fence) error
	// LatestCheckpoint returns the stack's latest state, or ErrNoState.
	LatestCheckpoint(ctx context.Context, ref Ref) (Checkpoint, error)
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

// UpdatesConfig holds the dependencies of Updates.
type UpdatesConfig struct {
	Updates     UpdateStore
	Checkpoints CheckpointStore
	Events      EventStore
	Namespaces  *namespace.Service
	Now         func() time.Time
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
	now            func() time.Time
	maxCheckpoints int
}

// NewUpdates returns Updates. Every store and Namespaces are required.
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
	}

	s := &Updates{
		updates:        cfg.Updates,
		checkpoints:    cfg.Checkpoints,
		events:         cfg.Events,
		namespaces:     cfg.Namespaces,
		now:            cfg.Now,
		maxCheckpoints: cfg.MaxCheckpointBytes,
	}

	if s.now == nil {
		s.now = time.Now
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

// Create records a new update. It does not lock the stack.
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
	}

	if err := s.updates.CreateUpdate(ctx, u, fence); err != nil {
		return Update{}, fmt.Errorf("create %s update of %s: %w", plan.Kind, ref, err)
	}

	return u, nil
}

// Start starts the update and hands out its lease. A mutating update fails
// with ErrUpdateConflict while another one runs on the stack.
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

// SaveCheckpoint stores the stack's state as of now in the update. Previews
// do not save state.
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

	cp := Checkpoint{
		SavedAt:       s.clock(),
		Features:      slices.Clone(in.Features),
		Deployment:    in.Deployment,
		Update:        id,
		Ref:           ref,
		Lease:         u.Lease.Digest,
		SchemaVersion: in.SchemaVersion,
		Sequence:      in.Sequence,
		Digest:        sha256.Sum256(in.Deployment),
		Invalid:       in.Invalid,
	}

	if err := s.checkpoints.PutCheckpoint(ctx, cp, fence); err != nil {
		return fmt.Errorf("save checkpoint of update %s: %w", id, err)
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

// Import replaces the stack's state with a deployment the user supplies, as
// `pulumi stack import` does. It runs as an update of kind "import" so it
// shows in history and respects the stack lock.
//
// Invalid input is rejected before any update exists. Once the import update
// starts, only this call holds its lease, so it is always ended, even when ctx
// is canceled: otherwise the stack would stay locked until `pulumi cancel`.
func (s *Updates) Import(ctx context.Context, ref Ref, in CheckpointInput) (Update, error) {
	if err := s.validate(in); err != nil {
		return Update{}, err
	}

	u, err := s.Create(ctx, ref, Plan{Kind: KindImport, DryRun: false})
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

	return cp, nil
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
