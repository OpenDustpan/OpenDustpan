package workspace

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
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
	// DefaultMaxStateBytes bounds one state version unless configured otherwise.
	// It is the policy default of ADR 45.
	DefaultMaxStateBytes = 32 << 20
	// UploadWindow is how long a state upload capability stays valid.
	UploadWindow = 10 * time.Minute
	// DefaultMaxPendingVersions bounds the pending state versions of one
	// workspace. A lock holder that declares without uploading cannot fill
	// the store past this count.
	DefaultMaxPendingVersions = 10
	// SweepGracePeriod is how long after its capability expires a pending
	// state version stays before a sweep deletes it with its attempted
	// content. The grace covers an upload that started before expiry and
	// still streams.
	SweepGracePeriod = time.Hour
)

// Store persists workspaces. Writes carry the namespace fence and fail with
// namespace.ErrFenced if it is stale.
//
//nolint:interfacebloat // One adapter contract for one table set: every workspace write shares its rows and its fence.
type Store interface {
	// CreateWorkspace persists a new w. It returns ErrAlreadyExists,
	// namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale. A store that keeps manual locks
	// returns an error that matches ErrLocked and manuallock.ErrLocked, and
	// wraps a *manuallock.LockedError, while a manual lock covers the namespace
	// of w. It reads the lock in the transaction of the create. LockPassFrom
	// of ctx gives the start of the phase of the job of the request: a lock
	// that started after a pass that is not zero does not refuse the create.
	CreateWorkspace(ctx context.Context, w Workspace, fence namespace.Fence) error
	// GetWorkspace returns the workspace called ref, or ErrNotFound.
	GetWorkspace(ctx context.Context, ref Ref) (Workspace, error)
	// GetWorkspaceByID returns the workspace with id, or ErrNotFound.
	GetWorkspaceByID(ctx context.Context, id ID) (Workspace, error)
	// ListWorkspaces returns the namespace's workspaces, ordered by name; an
	// empty list if it has none or does not exist.
	ListWorkspaces(ctx context.Context, ns namespace.Name) ([]Workspace, error)
	// QueryWorkspaces returns one page of the live workspaces that match q,
	// in the order of ListWorkspaces, with the count of every match, from
	// one snapshot. PageWorkspaces defines the result for the list of
	// ListWorkspaces. A namespace that does not exist reads as an empty page.
	QueryWorkspaces(ctx context.Context, q ListQuery) (ListPage, error)
	// SaveWorkspace persists w if the store holds w.Revision-1, else returns
	// ErrConcurrentModification. It returns ErrNotFound if the workspace does
	// not exist, namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	SaveWorkspace(ctx context.Context, w Workspace, fence namespace.Fence) error
	// ReleaseUnfenced saves a released unit like SaveWorkspace, and refuses with
	// ErrOverrideRefused unless the forced-write switches of the namespace and of
	// the unit are on, in the same transaction. It is the unfenced release of a
	// client-encrypted state under the forced-write override (ADR 44, decision 4).
	// It appends the audit entry e in the same transaction; if the append fails,
	// nothing changes. It returns audit.ErrInvalidEntry if e fails validation or
	// names another namespace.
	ReleaseUnfenced(ctx context.Context, w Workspace, fence namespace.Fence, e audit.Entry) error
	// SaveReleased saves a released unit like SaveWorkspace and appends the
	// audit entry e in the same transaction; if the append fails, nothing
	// changes. It returns audit.ErrInvalidEntry if e fails validation or names
	// another namespace.
	SaveReleased(ctx context.Context, w Workspace, fence namespace.Fence, e audit.Entry) error
	// DeleteWorkspace soft-deletes the workspace called ref at the time e.At,
	// in one fenced transaction (ADR 47), and appends the audit entry e to the
	// chain of the namespace in the same transaction. If the append fails,
	// nothing changes (ADR 60, fail closed). The workspace leaves every lookup by
	// name or ID and every list, and its name is free at once. Its state
	// versions and its wrapped data key stay until the final delete. It
	// removes the inventory entry in the same transaction. It returns
	// ErrLocked while the workspace is locked, ErrPendingState while it has a
	// pending state version, ErrNotFound if no live workspace has ref, namespace.ErrNotFound if the namespace does not
	// exist, namespace.ErrFenced if fence is stale, or audit.ErrInvalidEntry
	// if e fails validation or names another namespace. While a manual lock
	// covers the workspace, its collection or its namespace, the error matches
	// ErrLocked and manuallock.ErrLocked.
	DeleteWorkspace(ctx context.Context, ref Ref, fence namespace.Fence, d inventory.Deletion) error
	DeletedStore
}

// DeletedStore is the part of Store that acts on a soft-deleted workspace by
// its ID (ADR 47): the restore and the final delete.
type DeletedStore interface {
	// RestoreWorkspace brings back the deleted workspace id of the
	// namespace of fence before its retention period ends (ADR 47), under
	// newName, or under its name before the delete when newName is empty. It
	// moves the inventory entry back and appends the audit entry e in the
	// same fenced transaction (ADR 60, fail closed). It returns the reference
	// of the restored workspace, ErrNotFound if the namespace has no deleted
	// workspace id, ErrAlreadyExists if a live workspace holds the name,
	// namespace.ErrFenced if fence is stale, or audit.ErrInvalidEntry if e
	// fails validation.
	RestoreWorkspace(ctx context.Context, id ID, newName Name, fence namespace.Fence, e audit.Entry) (Ref, error)
	// PurgeWorkspace removes the deleted workspace id of the namespace of
	// fence for good (ADR 47): its state versions, data key and deleted
	// inventory entry, in one fenced transaction with the audit entry e. It
	// ends the grants on the workspace and records the blob purge
	// (inventory.BlobPurge) in that transaction. The caller removes its
	// blobs after the commit; the sweep finishes the record. It returns
	// ErrNotFound if the namespace has no deleted workspace id. With a
	// non-zero dueBy, it also returns ErrNotFound and changes nothing when
	// the retention period of the workspace ends after dueBy, a check in the
	// same transaction. The retention sweep sets dueBy, and a purge by an
	// admin leaves it zero.
	PurgeWorkspace(ctx context.Context, id ID, fence namespace.Fence, dueBy time.Time, e audit.Entry) error
}

// StateStore persists state version metadata.
//
//nolint:interfacebloat // One adapter contract for one table set: every state version operation shares its rows.
type StateStore interface {
	// CreateStateVersion persists a new, pending sv, refusing with
	// ErrTooManyPending when the workspace already holds maxPending
	// pending versions. The count and the insert run in one fenced
	// transaction, so concurrent declares over several services never
	// pass the cap together. A non-positive maxPending refuses every
	// version. It returns ErrNotFound if sv.Workspace does not exist,
	// namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	CreateStateVersion(
		ctx context.Context, sv StateVersion, fence namespace.Fence, maxPending int,
	) error
	// GetStateVersion returns the state version, or ErrStateVersionNotFound.
	GetStateVersion(ctx context.Context, id StateVersionID) (StateVersion, error)
	// FinalizeStateVersion saves the finalized sv (with its Content key),
	// stamps the version's finalize time and the workspace's UpdatedAt, and
	// makes sv the workspace's current state. The stamped time is
	// sv.FinalizedAt, or one millisecond after the latest finalize time of
	// the workspace when sv.FinalizedAt is not later than it, so the current
	// state always has the latest finalize time. The sv of the caller keeps
	// the requested time; read the stored version for the stamped time. It
	// checks in the same transaction that:
	//   - the stored version is still pending, else ErrUploadDenied;
	//   - the stored version still names sv.Content as its latest attempt,
	//     else ErrUploadDenied: a superseded attempt never finalizes over a
	//     newer one, so the current state never points at a deleted blob;
	//   - the workspace is held by lock sv.LockID, else ErrNotLocked or
	//     ErrLockedByOther;
	//   - unless sv.Force, the current state (if any) has sv.Lineage, else
	//     ErrLineageMismatch, and a lower serial, else ErrStaleSerial;
	//   - when sv.RestoredFrom is set, the current state (if any) does not
	//     restore sv.RestoredFrom for sv.Holder, else ErrAlreadyRestored: two
	//     concurrent restores of one version write one version, also across
	//     services.
	// Those errors, ErrNotFound, ErrStateVersionNotFound,
	// namespace.ErrNotFound and namespace.ErrFenced mean nothing was
	// written. Any other error leaves the outcome unknown: the version may
	// be current. Callers record the attempt key with RecordUploadAttempt
	// before finalizing, so the row names sv.Content when the write runs.
	FinalizeStateVersion(ctx context.Context, sv StateVersion, fence namespace.Fence) error
	// FinalizeStateVersionExpecting works as FinalizeStateVersion, and also
	// checks in the same transaction, after it locks the workspace row, that
	// the current state version of the workspace is expect (the empty ID when
	// the workspace has no state), else ErrConcurrentModification. A write by
	// the same holder under the same lock cannot slip in between a read of the
	// current version and this finalize. The expected version adds a check and
	// removes none. ErrConcurrentModification means nothing was written.
	FinalizeStateVersionExpecting(
		ctx context.Context, sv StateVersion, fence namespace.Fence, expect StateVersionID,
	) error
	// FenceAndUnlock releases the lock of w and makes sv the workspace's current
	// state, in one fenced transaction. sv is a finalized fence version of w:
	// it names the content, the new lineage, its serial and FenceBy. The
	// caller stores sv as a pending version with CreateStateVersion before it
	// writes the content, so a sweep finds the key; FenceAndUnlock finalizes
	// that row, with the finalize time that FinalizeStateVersion stamps. The
	// stored version must still be pending and name sv.Content,
	// else ErrUploadDenied. The stored workspace must be locked and hold
	// revision w.Revision-1, else
	// ErrNotLocked or ErrConcurrentModification. The current version must be
	// expect (the empty ID when the workspace has no state), else
	// ErrConcurrentModification, and sv.Serial must be one more than its serial,
	// else ErrStaleSerial. It returns ErrNotFound if the workspace does not
	// exist, namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale. It appends the audit entry e in the
	// same transaction, and returns audit.ErrInvalidEntry if e fails validation
	// or names another namespace. An error writes nothing.
	FenceAndUnlock(
		ctx context.Context, w Workspace, sv StateVersion, expect StateVersionID, fence namespace.Fence,
		e audit.Entry,
	) error
	// CurrentStateVersion returns the workspace's current state, or ErrNoState.
	CurrentStateVersion(ctx context.Context, workspace ID) (StateVersion, error)
	// ListStateVersions returns the finalized versions of workspace, newest
	// first by finalize time, with the total count of finalized versions.
	// Ties on finalize time break by version ID, so every adapter orders
	// identically. Page selects the window: a non-positive limit reads as
	// DefaultStatePageSize, a limit above MaxStatePageSize is capped, and a
	// negative offset reads as zero. A workspace without finalized versions,
	// or one that does not exist, yields an empty list and a zero total. It
	// returns no sentinel errors.
	ListStateVersions(ctx context.Context, workspace ID, page Page) ([]StateVersion, int, error)
	// CountPendingStateVersions returns how many pending versions workspace
	// holds. A workspace that does not exist holds none. It returns no
	// sentinel errors.
	CountPendingStateVersions(ctx context.Context, workspace ID) (int, error)
	// ReferencedContentKeys returns the blob keys every version of
	// workspace names, pending and finalized, without the empty ones. The
	// workspace is named by its ID, the key that a sweep lists the blobs by.
	// A workspace without versions yields an empty list. A soft-deleted
	// workspace still returns its keys, because its versions stay until the
	// final delete. A workspace that does not exist returns ErrNotFound: an
	// empty keep set would let a sweep delete every blob of the workspace. A
	// sweeper compares this set against the stored objects to find blobs no
	// version references.
	ReferencedContentKeys(ctx context.Context, workspace ID) ([]blob.Key, error)
	// RecordUploadAttempt notes the blob key of one upload attempt on a
	// pending version, so a later sweep finds the content even when the
	// finalize outcome stays unknown. It returns the key the attempt
	// supersedes, so the caller deletes the earlier blob the row stopped
	// naming: an empty key when the version names no content yet. It returns
	// ErrStateVersionNotFound when id does not exist, ErrUploadDenied when
	// it is not pending, namespace.ErrNotFound when the namespace does not
	// exist, or namespace.ErrFenced when fence is stale.
	RecordUploadAttempt(
		ctx context.Context, id StateVersionID, key blob.Key, fence namespace.Fence,
	) (blob.Key, error)
	// SaveSensitiveMarks stores the secret paths of the json-state rendering
	// of id, by resource instance address. It holds no value, only paths. It
	// works on a pending and on a finalized version: the rendering may land
	// after the state upload finalizes. It returns ErrStateVersionNotFound
	// when id does not exist, namespace.ErrNotFound when the namespace does
	// not exist, or namespace.ErrFenced when fence is stale.
	SaveSensitiveMarks(
		ctx context.Context, id StateVersionID, marks SensitiveMarks, fence namespace.Fence,
	) error
	// DeleteExpiredPendingStateVersions deletes the pending versions of
	// workspace whose capability expired before before, oldest expiry
	// first with ties by version ID, and returns them with their attempted
	// content keys, so the caller deletes the blobs. The delete matches
	// only versions that are still pending: a version that finalized after
	// the listing stays, with its content untouched, and stays out of the
	// result. A workspace that does not exist yields an empty list. It
	// returns namespace.ErrNotFound when the namespace does not exist, or
	// namespace.ErrFenced when fence is stale.
	DeleteExpiredPendingStateVersions(
		ctx context.Context,
		workspace ID,
		before time.Time,
		fence namespace.Fence,
	) ([]StateVersion, error)
	// DiscardStateVersion deletes the pending version id, with its row only. The
	// caller deletes the blob of the attempt first. A version that is not pending
	// matches no row and stays: a finalized version is never discarded. It returns
	// namespace.ErrNotFound when the namespace does not exist, or namespace.ErrFenced
	// when fence is stale. A refused streamed write uses it when the finalize
	// outcome is known, so its placeholder does not fill the pending cap.
	DiscardStateVersion(ctx context.Context, id StateVersionID, fence namespace.Fence) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Workspaces Store
	States     StateStore
	// Blobs holds state content under
	// "namespaces/<namespace id>/tofu/<workspace id>/<state version id>/<attempt>".
	// The service reads and writes it only through a view of one namespace.
	Blobs      blob.Store
	Namespaces *namespace.Service
	// OutOfScope records each refusal of a blob key outside the namespace of
	// a call. It is required: a refusal must reach the audit log.
	OutOfScope scopeaudit.Recorder
	// Sealer seals state blobs and outputs documents with the data key of
	// the workspace scope. It is required: every stored version is sealed.
	Sealer Sealer
	// Logger receives sweep failures; nil discards them.
	Logger *slog.Logger
	// BlobGuard tells whether a backup or a restore runs. The delete of the
	// state blobs of a purged workspace asks it after the commit of the purge
	// and runs only when nothing runs. Nil is a guard that never lets a
	// delete run: the blobs stay for the retention sweep.
	BlobGuard blobguard.Watch
	Now       func() time.Time
	// MaxStateBytes bounds one state version; zero means DefaultMaxStateBytes.
	MaxStateBytes int64
	// MaxPendingVersions bounds the pending state versions of one
	// workspace; zero means DefaultMaxPendingVersions.
	MaxPendingVersions int
	// Retention is how long a deleted workspace stays before its final
	// delete (ADR 47, decision 2). Zero means inventory.DeletedRetention.
	Retention time.Duration
	// ManualLocks refuses a lock request on a workspace that a manual lock
	// covers. Nil turns the check off.
	ManualLocks ManualLocks
}

// ManualLocks is the read side of the manual locks of the shared core. A lock
// request of OpenTofu asks it before the workspace takes its own lock.
type ManualLocks interface {
	// CoveringFor returns the manual lock that covers the unit: a lock on the
	// unit, on the collection that holds it, or on its namespace. jobID is the
	// job whose job token made the request, or empty. The lock request of a
	// job whose phase started before the lock passes. It returns
	// manuallock.ErrNotLocked when no manual lock covers the unit for the
	// request.
	CoveringFor(ctx context.Context, unit manuallock.Unit, jobID string) (manuallock.Lock, error)
	// NamespaceLock returns the manual lock of the namespace, or
	// manuallock.ErrNotLocked. The restore of a deleted unit asks it: a deleted
	// unit has no inventory entry, so CoveringFor cannot find the lock of its
	// namespace.
	NamespaceLock(ctx context.Context, ns namespace.Name) (manuallock.Lock, error)
	// NamespaceLockFor is CoveringFor for a unit that does not exist yet: only
	// the lock of its namespace covers it. A job whose phase started before
	// the lock passes it.
	NamespaceLockFor(ctx context.Context, unit manuallock.Unit, jobID string) (manuallock.Lock, error)
	// PhaseStart returns when the current phase of the job jobID on the unit
	// started, or the zero time, which passes no lock.
	PhaseStart(ctx context.Context, unit manuallock.Unit, jobID string) time.Time
}

// lockNounWorkspace names a workspace in the error of a manual lock.
const lockNounWorkspace = "workspace"

// lockStripes is the number of striped mutexes serializing declares and
// uploads. Two IDs that share a stripe only wait for each other; the bound
// is fixed, so the table grows with neither workspaces nor versions.
const lockStripes = 64

// Service implements the workspace use cases behind OpenTofu's cloud block:
// workspaces, locking, and state versions uploaded through single-use
// capabilities.
type Service struct {
	workspaces Store
	states     StateStore
	blobs      *stateBlobs
	namespaces *namespace.Service
	sealer     Sealer
	logger     *slog.Logger
	guard      blobguard.Watch
	now        func() time.Time
	maxState   int64
	maxPending int
	retention  time.Duration
	manual     ManualLocks
	stateAt    StateAtReader
	names      inventory.HistoryReader
	stripes    [lockStripes]sync.Mutex
}

// attachOptionalReaders sets the optional reads of the state store. A store
// that also finds the state at a time enables ReadStateAt. A store that also
// keeps the inventory change log lets ReadStateAt name the workspace at the
// time.
func attachOptionalReaders(s *Service, states StateStore) {
	if reader, ok := states.(StateAtReader); ok {
		s.stateAt = reader
	}

	if reader, ok := states.(inventory.HistoryReader); ok {
		s.names = reader
	}
}

// NewService returns a Service. Every port, Namespaces and the Sealer are
// required.
func NewService(cfg Config) (*Service, error) {
	switch {
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: workspace store", ErrMissingDependency)
	case cfg.States == nil:
		return nil, fmt.Errorf("%w: state store", ErrMissingDependency)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: blob store", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace service", ErrMissingDependency)
	case cfg.OutOfScope == nil:
		return nil, fmt.Errorf("%w: out-of-scope recorder", ErrMissingDependency)
	case cfg.Sealer == nil:
		return nil, fmt.Errorf("%w: workspace service", ErrMissingSealer)
	}

	s := &Service{
		workspaces: cfg.Workspaces,
		states:     cfg.States,
		blobs:      &stateBlobs{store: cfg.Blobs, refusals: cfg.OutOfScope},
		namespaces: cfg.Namespaces,
		sealer:     cfg.Sealer,
		logger:     cfg.Logger,
		guard:      cfg.BlobGuard,
		now:        cfg.Now,
		maxState:   cfg.MaxStateBytes,
		maxPending: cfg.MaxPendingVersions,
		retention:  cfg.Retention,
		manual:     cfg.ManualLocks,
		stateAt:    nil,
		names:      nil,
		stripes:    [lockStripes]sync.Mutex{},
	}

	attachOptionalReaders(s, cfg.States)

	if s.now == nil {
		s.now = time.Now
	}

	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}

	if s.maxState <= 0 {
		s.maxState = DefaultMaxStateBytes
	}

	if s.maxPending <= 0 {
		s.maxPending = DefaultMaxPendingVersions
	}

	if s.retention <= 0 {
		s.retention = inventory.DeletedRetention
	}

	return s, nil
}

// ErrPlacementUnsupported reports that the store cannot make a workspace and
// put it in a collection in one transaction, so the service makes no workspace.
var ErrPlacementUnsupported = errors.New("the store cannot put a new workspace in a collection")

// Create creates an unlocked workspace without state and puts it in the
// collection of p in one fenced transaction. It returns an error that matches
// collection.ErrNoCollection, and makes no workspace, when p names no
// collection, no checked collection ID, or a collection of another namespace.
// It returns ErrPlacementUnsupported when the store is not a PlacedStore.
func (s *Service) Create(ctx context.Context, ref Ref, p Placement) (Workspace, error) {
	// Defense in depth: the handler chooses the collection by a placement rule
	// and the store checks the collection in the transaction. This check stays
	// so that a new caller cannot make a workspace that no collection holds.
	if !p.Complete(ref.Namespace) {
		return Workspace{}, errNoCollection(ref)
	}

	placed, ok := s.workspaces.(PlacedStore)
	if !ok {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", ref, ErrPlacementUnsupported)
	}

	n, err := s.writable(ctx, ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	fence := n.Fence()

	now := s.clock()
	w := newWorkspaceOf(ref)
	w.CreatedAt, w.UpdatedAt = now, now
	w.ID = ID(ident.NewID("ws-"))
	w.NamespaceID = n.ID

	// Defense in depth: the store refuses the create too while the namespace
	// has a manual lock, in the transaction of the create. This check stays so
	// that a store adapter that does not read the manual locks cannot create a
	// workspace in a locked namespace. The phase of the job of the request, if
	// it has one, decides whether the lock applies to it, as it does for the
	// lock request that follows the create.
	if err := s.refuseNewUnitManualLock(ctx, ref, n.ID, JobFrom(ctx)); err != nil {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", ref, err)
	}

	// The lock pass of the store always comes from this service, never from
	// the caller: a pass that the caller put in ctx is replaced. With no manual
	// lock service, the pass is the zero time, which passes no lock.
	pass := time.Time{}
	if s.manual != nil {
		pass = s.manual.PhaseStart(ctx, newLockUnitOf(ref, n.ID), JobFrom(ctx))
	}

	ctx = WithLockPass(ctx, pass)

	p.Entry.At = now

	if err := placed.CreateWorkspacePlaced(ctx, w, p, fence); err != nil {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", ref, err)
	}

	return w, nil
}

// newWorkspaceOf returns the workspace that Create would make for ref, with no
// ID and no time: the value that a check holds for a workspace that does not
// exist yet.
func newWorkspaceOf(ref Ref) Workspace {
	return Workspace{
		CreatedAt:   time.Time{},
		UpdatedAt:   time.Time{},
		Lock:        Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: NoRelease(),
		ID:          "",
		Ref:         ref,
		NamespaceID: "",
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// Delete soft-deletes the workspace called ref (ADR 47): the workspace leaves
// every lookup by name or ID and every list, and its name is free at once,
// but its state versions and data key stay until the final delete. It first
// sweeps the expired pending versions of the workspace with their attempted
// content, as a declare does, so only a pending version that has not expired
// blocks the delete. The sweep runs before the lock check, the pending check
// and the not-found check, so a refused delete still removes expired pending
// rows. It returns ErrLocked while the workspace is locked, ErrPendingState
// while it has a pending state version, and ErrNotFound if no live workspace
// has ref. The write is fenced on the namespace's failover version
// and only allowed in its primary installation. The delete and the audit entry e,
// stamped with the time of the delete, commit together or not at all (ADR 60,
// fail closed).
func (s *Service) Delete(ctx context.Context, ref Ref, e audit.Entry) error {
	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return err
	}

	// A manual lock stops the delete of the workspace. The workspace must exist
	// to be covered: a missing one falls through to the not-found answer of the
	// store. Any other read error refuses the delete (fail closed).
	w, getErr := s.Get(ctx, ref)
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return fmt.Errorf("delete workspace %s: %w", ref, getErr)
	}

	if getErr == nil {
		if lockErr := s.refuseManualLock(ctx, w, ""); lockErr != nil {
			return fmt.Errorf("delete workspace %s: %w", ref, lockErr)
		}

		if sweepErr := s.sweepExpiredRows(ctx, w); sweepErr != nil {
			s.logger.WarnContext(
				ctx,
				"sweep expired state",
				slog.String("workspace", w.ID.String()),
				slog.String("error", sweepErr.Error()),
			)
		}
	}

	e.At = s.clock()
	err = s.workspaces.DeleteWorkspace(
		ctx,
		ref,
		fence,
		inventory.Deletion{Entry: e, PurgeAfter: e.At.Add(s.retention)},
	)
	if err != nil {
		return fmt.Errorf("delete workspace %s: %w", ref, err)
	}

	return nil
}

// RestoreDeleted brings back the deleted workspace id of ns under newName, or under
// its name before the delete when newName is empty (ADR 47). The store
// appends the audit entry e, stamped with the time of the restore, in the
// same fenced transaction (ADR 60, fail closed). It returns the reference of
// the restored workspace, ErrNotFound if ns has no deleted workspace id, and
// ErrAlreadyExists if a live workspace holds the name.
func (s *Service) RestoreDeleted(
	ctx context.Context, ns namespace.Name, id ID, newName Name, e audit.Entry,
) (Ref, error) {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Ref{}, err
	}

	// Defense in depth: the store refuses a restore under a manual lock in the
	// transaction of the restore, after the workspace is live again. This check
	// stays so that the refusal does not rely on the store: a deleted workspace
	// has no inventory entry, so it reads the lock of the namespace, and the
	// store check covers the lock of the workspace and of its collection.
	if lockErr := s.refuseRestoreUnderNamespaceLock(ctx, ns, id); lockErr != nil {
		return Ref{}, fmt.Errorf("restore workspace %s: %w", id, lockErr)
	}

	e.At = s.clock()

	ref, err := s.workspaces.RestoreWorkspace(ctx, id, newName, fence, e)
	if err != nil {
		return Ref{}, fmt.Errorf("restore workspace %s: %w", id, err)
	}

	return ref, nil
}

// PurgeDeleted removes the deleted workspace id of ns for good, before its
// retention period ends (ADR 47). The store removes its rows, records the
// blob purge and appends the audit entry e in one fenced transaction. The
// caller then removes the state blobs with DeletePurgedBlobs, in the scope of
// the namespace ID that this call returns. It returns ErrNotFound if ns has
// no deleted workspace id.
func (s *Service) PurgeDeleted(ctx context.Context, ns namespace.Name, id ID, e audit.Entry) (namespace.ID, error) {
	return s.purge(ctx, ns, id, time.Time{}, e)
}

// PurgeExpired removes the deleted workspace id of ns for good, as
// PurgeDeleted does, only when its retention period ended at or before
// dueBy. The store checks the end of the retention in the purge
// transaction. It returns ErrNotFound if ns has no deleted workspace id
// whose retention period ended by then: a restore and a new delete after
// the caller read the unit gave it a new retention period. It returns the ID
// of the namespace row that it resolved, for the delete of the blobs.
func (s *Service) PurgeExpired(
	ctx context.Context, ns namespace.Name, id ID, dueBy time.Time, e audit.Entry,
) (namespace.ID, error) {
	return s.purge(ctx, ns, id, dueBy, e)
}

// DeletePurgedBlobs removes every state blob of the purged workspace id of the
// namespace with the ID ns, with a retry for each blob (ADR 47). The caller
// takes ns from the namespace row of the purge or from the blob purge record,
// never from a request field. The purge runs it after the rows are gone, so no
// upload can still commit a row that names these blobs. It returns an error
// when a blob may remain, so the caller keeps the blob purge record for the
// sweep. A running backup or restore keeps every blob and returns an error that matches
// blobguard.ErrWait. No log line and no error names a blob key.
func (s *Service) DeletePurgedBlobs(ctx context.Context, ns namespace.ID, id ID) error {
	failed := 0

	// The walk deletes each blob as its page arrives, so the purge never holds
	// the keys of the whole workspace. A key sorts after the cursor of the
	// walk, so a delete does not move the walk.
	var guardErr error

	err := s.blobs.walk(ctx, ns, id, func(page []blob.Object) error {
		for _, obj := range page {
			// The guard comes after the commit of the purge and before each
			// delete: a restore that starts during the walk keeps the blobs
			// that are left. A backup or a restore that runs, or a failed
			// read, stops the walk. The error keeps the blob purge record,
			// and the retention sweep tries again.
			if holdErr := blobguard.Hold(ctx, s.guard); holdErr != nil {
				guardErr = holdErr

				return holdErr
			}

			if deleteErr := s.blobs.deleteRetried(ctx, ns, id, obj.Key); deleteErr != nil {
				failed++
			}
		}

		return nil
	})
	if guardErr != nil {
		return fmt.Errorf("delete state blobs of workspace %s: %w", id, guardErr)
	}

	if err != nil {
		return fmt.Errorf("list state blobs of workspace %s: %w", id, err)
	}

	if failed > 0 {
		return fmt.Errorf("%w: %d state blobs of workspace %s", ErrBlobsRemain, failed, id)
	}

	return nil
}

// Get returns the workspace called ref.
func (s *Service) Get(ctx context.Context, ref Ref) (Workspace, error) {
	w, err := s.workspaces.GetWorkspace(ctx, ref)
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace %s: %w", ref, err)
	}

	return w, nil
}

// GetByID returns the workspace with id.
func (s *Service) GetByID(ctx context.Context, id ID) (Workspace, error) {
	w, err := s.workspaces.GetWorkspaceByID(ctx, id)
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace %s: %w", id, err)
	}

	return w, nil
}

// List returns the namespace's workspaces.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]Workspace, error) {
	list, err := s.workspaces.ListWorkspaces(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list workspaces in %q: %w", ns, err)
	}

	return list, nil
}

// Query returns one page of the namespace's workspaces that match q, with the
// count of every match.
func (s *Service) Query(ctx context.Context, q ListQuery) (ListPage, error) {
	page, err := s.workspaces.QueryWorkspaces(ctx, q)
	if err != nil {
		return ListPage{}, fmt.Errorf("query workspaces in %q: %w", q.Namespace, err)
	}

	return page, nil
}

// Lock locks the workspace for holder, the subject of the calling principal.
func (s *Service) Lock(ctx context.Context, id ID, holder, reason string) (Workspace, error) {
	return s.LockForJob(ctx, id, holder, reason, "")
}

// LockForJob is Lock for a request that carries the job token of the job
// jobID, or no job token when jobID is empty. A job whose phase started before
// a manual lock passes that lock.
func (s *Service) LockForJob(ctx context.Context, id ID, holder, reason, jobID string) (Workspace, error) {
	if holder == "" {
		return Workspace{}, fmt.Errorf("%w: lock without a holder", ErrNotLocked)
	}

	// The manual lock check and the tool lock below run in separate
	// transactions. A manual lock that commits between them is missed by this
	// one request, the same as a job phase that started before the lock. The
	// next request meets the lock.
	if err := s.checkManualLock(ctx, id, jobID); err != nil {
		return Workspace{}, err
	}

	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		return w.lock(now, holder, reason)
	})
}

// LockWithID locks the workspace for holder under lockID, an ID that the
// caller chose. The HTTP backend sends its lock ID in the LOCK request and
// names it again in each write (ADR 49, decision 6). An ID that is empty, too
// long or has characters outside the lock ID set fails with ErrInvalidLockID.
func (s *Service) LockWithID(ctx context.Context, id ID, holder, reason, lockID string) (Workspace, error) {
	return s.LockWithIDForJob(ctx, id, LockRequest{Holder: holder, Reason: reason, LockID: lockID, JobID: ""})
}

// LockRequest is a lock request of the OpenTofu HTTP backend: the holder, the
// reason and the lock ID that the client chose, and the job whose job token made
// the request. JobID is empty for a request with another credential.
type LockRequest struct {
	Holder string
	Reason string
	LockID string
	JobID  string
}

// LockWithIDForJob is LockWithID for a request that can carry the job token of
// a job. A job whose phase started before a manual lock passes that lock.
func (s *Service) LockWithIDForJob(ctx context.Context, id ID, req LockRequest) (Workspace, error) {
	holder, reason, lockID, jobID := req.Holder, req.Reason, req.LockID, req.JobID

	if err := checkLockRequest(req); err != nil {
		return Workspace{}, err
	}

	// A LOCK that repeats the lock that the workspace holds succeeds without a
	// change: OpenTofu sends a LOCK again when the answer of a committed LOCK
	// is lost. The check comes before the manual lock check, because a manual
	// lock never stops the holder of the tool lock. The check in the change
	// below stays for a lock that another request takes after this read.
	repeated, isRepeat, err := s.repeatedLock(ctx, id, holder, lockID)
	if err != nil || isRepeat {
		return repeated, err
	}

	// The manual lock check and the tool lock below run in separate
	// transactions. A manual lock that commits between them is missed by this
	// one request, the same as a job phase that started before the lock. The
	// next request meets the lock.
	if err := s.checkManualLock(ctx, id, jobID); err != nil {
		return Workspace{}, err
	}

	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		if w.RepeatsLock(holder, lockID) {
			return w, errRepeated
		}

		return w.lockAs(now, holder, reason, lockID)
	})
}

// errRepeated is the result of an apply of change for a request that repeats
// a change that the workspace already holds. change saves nothing for it.
var errRepeated = errors.New("request repeats a committed change")

// RepeatsRelease reports whether q repeats the last release of w, at the
// time of the clock of the service (see Workspace.RepeatsRelease). A handler
// asks it before it refuses an unlock of a free workspace.
func (s *Service) RepeatsRelease(w Workspace, q ReleaseRepeat) bool {
	return w.RepeatsRelease(s.clock(), q)
}

// checkLockRequest returns the error of a lock request that names no holder or
// a lock ID that the domain does not accept.
func checkLockRequest(req LockRequest) error {
	if req.Holder == "" {
		return fmt.Errorf("%w: lock without a holder", ErrNotLocked)
	}

	if !validLockID(req.LockID) {
		return fmt.Errorf("%w: %d characters", ErrInvalidLockID, len(req.LockID))
	}

	return nil
}

// AdmitFirstLock reports whether a lock request on the workspace called ref
// would pass the checks that come before the lock, when the workspace does not
// exist yet: the holder, the lock ID, the fence and the manual lock. It
// returns the error that LockWithIDForJob would return, and nil when the lock
// could go on. It writes nothing, so a caller creates the workspace only after
// it returns nil: a refused first request leaves no workspace.
func (s *Service) AdmitFirstLock(ctx context.Context, ref Ref, req LockRequest) error {
	n, err := s.writable(ctx, ref.Namespace)
	if err != nil {
		return err
	}

	if err := checkLockRequest(req); err != nil {
		return err
	}

	// The manual lock query reads the row of the unit, and a unit that does not
	// exist has none. The only lock that covers a new unit is the lock of its
	// namespace, so the check reads that lock, with the rule of the job.
	return s.refuseNewUnitManualLock(ctx, ref, n.ID, req.JobID)
}

// maxLockIDLength bounds a lock ID the caller chooses.
const maxLockIDLength = 128

// validLockID reports whether id is a lock ID the domain accepts: 1 to 128
// characters of letters, digits, '-', '_' and '.'.
func validLockID(id string) bool {
	if id == "" || len(id) > maxLockIDLength {
		return false
	}

	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}

	return true
}

// Unlock releases holder's lock.
func (s *Service) Unlock(ctx context.Context, id ID, holder string) (Workspace, error) {
	return s.UnlockLock(ctx, id, "", holder)
}

// UnlockLock releases holder's lock when the lock is lockID. An empty lockID
// releases any lock holder holds. The check and the release read and save the
// same revision, so a lock that another caller took after this one read the
// unit is refused with ErrLockMismatch, and it is not released (ADR 49,
// decision 6).
//
// On a workspace that is not locked, a request that repeats the last normal
// release of holder succeeds without a change (see Workspace.RepeatsRelease):
// OpenTofu sends an unlock again when the answer of a committed unlock is
// lost. The check runs before the check of the lock ID. Each other unlock of
// a free workspace is refused as before.
func (s *Service) UnlockLock(ctx context.Context, id ID, lockID, holder string) (Workspace, error) {
	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		repeat := ReleaseRepeat{LockID: lockID, Holder: "", Caller: holder, Kind: ReleaseNormal}
		if w.RepeatsRelease(now, repeat) {
			return w, errRepeated
		}

		if lockErr := checkLockID(w, Lock{LockedAt: time.Time{}, ID: lockID, Holder: "", Reason: ""}); lockErr != nil {
			return Workspace{}, lockErr
		}

		return w.unlock(now, holder)
	})
}

// checkLockID refuses a lock other than the expected one. Only the ID and, when
// it is set, the holder of expect are compared. An empty ID accepts any lock. A
// lock ID the client chose can repeat after a force-unlock, so the holder is part
// of the check, as in the finalize (ADR 49, decision 6).
func checkLockID(w Workspace, expect Lock) error {
	if expect.ID == "" {
		return nil
	}

	if w.Lock.ID != expect.ID || (expect.Holder != "" && w.Lock.Holder != expect.Holder) {
		return lockRefused(w.Lock, fmt.Errorf("%w: unit %s", ErrLockMismatch, w.Ref))
	}

	return nil
}

// ForceUnlock releases the lock whoever holds it, as `tofu force-unlock` does.
// A workspace with a state gets a fence version with it: a new lineage, the
// next serial and by as the principal that forced the unlock (ADR 44, decision
// 1). A workspace without a state only releases its lock. by is the subject of
// the principal that asks.
//
// The bool result reports an unfenced release: the caller's forced-write
// override let the release go through for a client-encrypted state, which
// has no fence (ADR 44, decision 4). Without the override that state is
// refused with ErrClientEncrypted and the lock stays held. record runs before
// an unfenced release and writes its audit entry; a failure stops the
// release. An unfenced release with a nil record is refused with
// ErrNoRecorder before it changes anything.
//
// Each release appends one audit entry of the kind change, in the transaction
// of the release (see ForceUnlockLock).
//
//nolint:revive // ForceUnlockLock takes these arguments and the expected lock; the entry joins them.
func (s *Service) ForceUnlock(
	ctx context.Context, id ID, by string, allow namespace.Overrides, record func() error, entry audit.Entry,
) (Workspace, bool, error) {
	return s.ForceUnlockLock(
		ctx, id, Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}, by, allow, record, entry,
	)
}

// ForceUnlockLock is ForceUnlock for the lock expect names. An empty expect.ID
// releases whatever lock the unit holds. A different lock, or the same ID under
// another holder, is refused with ErrLockMismatch. On a unit that is not
// locked, a request that repeats the last forced release by the same principal
// succeeds without a change and without an audit entry; any other request
// fails with ErrNotLocked. The check runs before the
// fence and in the revision the release saves, so a lock taken after the caller
// read the unit is not released.
//
// entry holds what the caller knows of the request: the action, the object,
// the token ID, the source address, the user agent, the channel, the request
// ID and the reason. A reason that audit.ValidateReason refuses fails the call
// with audit.ErrInvalidReason before anything changes. The service sets the
// rest: the time, the namespace, the subject by, the kind change, the unit ID,
// the new state version when the release writes one, and the lock that the
// release ends (its holder and its time) in the changes. The store appends the
// entry in the transaction of the release, so a release without its entry
// does not happen.
//
//nolint:revive // The expected lock and the entry are arguments beyond the port's other parameters, named at the call.
func (s *Service) ForceUnlockLock(
	ctx context.Context, id ID, expect Lock, by string, allow namespace.Overrides, record func() error,
	entry audit.Entry,
) (Workspace, bool, error) {
	if err := audit.ValidateReason(entry.Reason); err != nil {
		return Workspace{}, false, fmt.Errorf("force-unlock %s: %w", id, err)
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, false, err
	}

	if !w.Lock.Locked() {
		return s.repeatedForceUnlock(ctx, w, expect, by)
	}

	if lockErr := checkLockID(w, expect); lockErr != nil {
		return Workspace{}, false, lockErr
	}

	cur, err := s.states.CurrentStateVersion(ctx, id)

	switch {
	case errors.Is(err, ErrNoState):
		// A unit without state is a plain release: no override is used.
		released, saveErr := s.releaseWithoutState(ctx, w, expect, by, entry)

		return released, false, saveErr
	case err != nil:
		return Workspace{}, false, fmt.Errorf("current state of %s: %w", w.Ref, err)
	}

	released, err := s.releaseWithFence(ctx, w, cur, by, forceUnlockEntry(entry, w, by, s.clock()))
	if errors.Is(err, ErrClientEncrypted) && allow.Forced {
		// Defense in depth: the adapters pass a callback too; this check stays so
		// that a new caller cannot release without the entry of the override.
		if record == nil {
			return Workspace{}, false, fmt.Errorf("unfenced release of %s: %w", w.Ref, ErrNoRecorder)
		}

		if recordErr := record(); recordErr != nil {
			return Workspace{}, false, fmt.Errorf("record override of %s: %w", w.Ref, recordErr)
		}

		unfenced, changeErr := s.releaseUnfenced(ctx, w, by, forceUnlockEntry(entry, w, by, s.clock()))

		return unfenced, true, changeErr
	}

	return released, false, err
}

// forceUnlockEntry returns the audit entry of a force-unlock of w by the
// principal by, from the entry template that the caller gave. The changes
// hold the lock that the release ends. The version stays empty until
// releaseWithFence sets it: a release that writes no state version has none.
func forceUnlockEntry(template audit.Entry, w Workspace, by string, at time.Time) audit.Entry {
	e := template
	e.At = at
	e.Namespace = w.Ref.Namespace
	e.Subject = by
	e.Version = ""
	e.ObjectID = w.ID.String()
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = []audit.Change{
		releasedLockChange("lock-holder", w.Lock.Holder),
		releasedLockChange("locked-at", w.Lock.LockedAt.UTC().Format(time.RFC3339)),
	}

	return e
}

// releasedLockChange returns the change of one field of a lock that a
// force-unlock ends: the value before, and no value after.
func releasedLockChange(key, old string) audit.Change {
	return audit.Change{Key: key, Old: &old, New: "", Layer: "", TakesEffect: ""}
}

// SetOverrides replaces the override switch of the unit.
func (s *Service) SetOverrides(ctx context.Context, id ID, ov namespace.Overrides) (Workspace, error) {
	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		w.Overrides = ov

		return w.bump(now), nil
	})
}

// PendingState is a state version waiting for its content, and the
// capability that may upload it. The token is shown once.
type PendingState struct {
	Upload  token.Token
	Version StateVersion
}

// BeginState declares a new state version for the workspace. The caller must
// hold the lock, or make an unlocked write that allow.Unlocked permits. The
// content follows through Upload with the returned token. An unlocked write
// fails with ErrLocked, matching manuallock.ErrLocked, while a manual lock
// covers the workspace; a write under the lock of the caller is not refused.
// It first sweeps the expired pending versions of the workspace with their
// attempted content, then refuses with ErrTooManyPending when the workspace
// still holds the maximum pending versions. The sweep is best effort: a
// cleanup failure never fails the declare, and the rows stay for the next
// sweep. Retention follows the declares: a workspace with no new declares
// keeps its expired pendings, at most MaxPendingVersions, until the next
// declare sweeps them. The store enforces the cap where concurrent declares
// serialize, so it holds across services; the stripe below only trims
// contention between declares of one workspace in this service.
//
// auth holds the overrides the caller may use for this declare, and the record
// callback of each override it uses. A forced declare into a unit that has a
// current state version needs auth.Allow.Forced, and a declare without the lock
// needs auth.Allow.Unlocked. A forced declare into a unit with no current state
// overwrites nothing, so it needs no override (ADR 44, decisions 4 and 5).
// Without the override a refused declare names the way out in its error text
// (ADR 44, decision 8).
//
// auth.Record runs before the store write, and a failed record refuses the
// declare, so no override writes without its entry. A
// declare that uses an override with a nil auth.Record is refused with
// ErrNoRecorder before it writes.
func (s *Service) BeginState(
	ctx context.Context, id ID, holder string, in NewState, auth Authority,
) (PendingState, error) {
	sum, err := in.parse()
	if err != nil {
		return PendingState{}, err
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return PendingState{}, err
	}

	current, hasState, err := s.followedState(ctx, w, in)
	if err != nil {
		return PendingState{}, err
	}

	var known *StateVersion
	if hasState {
		known = &current
	}

	unlocked, used, err := declareOverrides(w, holder, in, known, auth.Allow)
	if err != nil {
		return PendingState{}, fmt.Errorf("new state for %s: %w", w.Ref, err)
	}

	// A write that holds no lock is not the write of a tool that holds its own
	// lock, so a manual lock stops it. The check runs before the override is
	// recorded: a refused write leaves no override entry. A write under the lock
	// of the caller is never refused here.
	if unlocked {
		if lockErr := s.refuseManualLock(ctx, w, ""); lockErr != nil {
			return PendingState{}, fmt.Errorf("new state for %s: %w", w.Ref, lockErr)
		}
	}

	if recordErr := recordOverrides(w, used, auth.Record); recordErr != nil {
		return PendingState{}, recordErr
	}

	// Declares of one workspace share a stripe from here to the create,
	// which trims contention in this process. The stripe and the cap check
	// in admitDeclare do not hold the cap across instances. The store
	// enforces the cap where concurrent declares serialize.
	unlock, err := s.admitDeclare(ctx, w)
	if err != nil {
		return PendingState{}, err
	}

	defer unlock()

	return s.createPending(ctx, w, in, sum, pendingDeclare{
		restoredFrom: "", unlocked: unlocked, origin: originFrom(ctx),
	})
}

// Authority is the caller's side of a declare: the overrides the caller may
// use, and the callback that writes the audit entry of each override that the
// declare uses.
type Authority struct {
	// Allow holds the overrides the caller may use.
	Allow namespace.Overrides
	// Record writes the audit entry of one override. A declare that uses an
	// override while Record is nil is refused with ErrNoRecorder.
	Record func(Override) error
}

// Override names an override that a declare uses. The domain reports each use
// to the record callback of BeginState before the declare writes.
type Override int

// Overrides that a declare can use.
const (
	// OverrideForced is a forced declare into a unit that has state.
	OverrideForced Override = iota + 1
	// OverrideUnlocked is a declare into a unit that no principal holds.
	OverrideUnlocked
)

// OverrideRefusedError is the error of an override that the unit, its namespace or
// the caller's permissions do not allow. It matches ErrOverrideRefused and any
// cause that it wraps, so a refused unlocked write still matches ErrNotLocked.
// Adapters read Override to pick the action to record, never the text.
type OverrideRefusedError struct {
	// Override is the override that the refused write needed.
	Override Override
	msg      string
	causes   []error
}

// NewOverrideRefusedError returns an OverrideRefusedError for override with the text msg.
// The result matches ErrOverrideRefused and each of causes.
func NewOverrideRefusedError(override Override, msg string, causes ...error) *OverrideRefusedError {
	return &OverrideRefusedError{Override: override, msg: msg, causes: causes}
}

// Error returns the text of the refusal.
func (e *OverrideRefusedError) Error() string { return e.msg }

// Unwrap returns ErrOverrideRefused and the causes of the refusal.
func (e *OverrideRefusedError) Unwrap() []error {
	return append([]error{ErrOverrideRefused}, e.causes...)
}

// RefusedOverride returns the override of the first OverrideRefusedError in the
// chain of err, and whether the chain holds one.
func RefusedOverride(err error) (Override, bool) {
	if refusal, ok := errors.AsType[*OverrideRefusedError](err); ok {
		return refusal.Override, true
	}

	return 0, false
}

// declareOverrides checks the lock and the overrides of one declare. It returns
// whether the declare is an unlocked write and the overrides it uses. A forced
// declare needs the override only when the unit has state (ADR 44, decision 4).
// current is nil when the unit has no state.
func declareOverrides(
	w Workspace, holder string, in NewState, current *StateVersion, allow namespace.Overrides,
) (bool, []Override, error) {
	var used []Override

	if in.Force && current != nil {
		if !allow.Forced {
			return false, nil, forcedRefusal()
		}

		used = append(used, OverrideForced)
	}

	lockErr := w.heldBy(holder)
	if lockErr == nil {
		return false, used, nil
	}

	if !errors.Is(lockErr, ErrNotLocked) || !allow.Unlocked {
		return false, nil, unlockedRefusal(lockErr)
	}

	return true, append(used, OverrideUnlocked), nil
}

// recordOverrides writes the audit entry of each override that a declare uses,
// and stops at the first entry that fails.
func recordOverrides(w Workspace, used []Override, record func(Override) error) error {
	if record == nil {
		return refuseWithoutRecorder(w, used)
	}

	for _, override := range used {
		if err := record(override); err != nil {
			return fmt.Errorf("record override of %s: %w", w.Ref, err)
		}
	}

	return nil
}

// refuseWithoutRecorder is called for a write that has no callback that
// records its overrides. It refuses the write when the write uses an override.
//
// Defense in depth: the adapters pass a callback too; this check stays so that
// a new caller cannot write an override without its entry.
func refuseWithoutRecorder(w Workspace, used []Override) error {
	if len(used) > 0 {
		return fmt.Errorf("write to %s: %w", w.Ref, ErrNoRecorder)
	}

	return nil
}

// pendingDeclare holds the optional parts of a declare: the version it restores
// and whether it is an unlocked write.
type pendingDeclare struct {
	restoredFrom StateVersionID
	unlocked     bool
	// origin is the writer of the declared version. A restore leaves it
	// empty: the version holds the content of an earlier one, and no write of
	// this request made it.
	origin identity.StateOrigin
}

// forcedRefusal names the action and the switches that allow a forced write.
func forcedRefusal() error {
	return NewOverrideRefusedError(OverrideForced, fmt.Sprintf(
		"%s: a forced write needs the forced-write switch on for the namespace "+
			"(`PUT /api/server/namespaces/{namespace}/override-switches`) and for the workspace "+
			"(`PUT /api/v2/workspaces/{id}/override-switches`), "+
			"and the permission to write forced state (`unit:write-forced`)",
		ErrOverrideRefused,
	))
}

// unlockedRefusal keeps the status of a refused declare on an unlocked unit
// and adds the way out: the switch and the action of an unlocked write.
func unlockedRefusal(lockErr error) error {
	if !errors.Is(lockErr, ErrNotLocked) {
		return lockErr
	}

	return NewOverrideRefusedError(OverrideUnlocked, fmt.Sprintf(
		"%s: a write without the lock needs the unlocked-write switch on for the namespace "+
			"(`PUT /api/server/namespaces/{namespace}/override-switches`) and for the workspace "+
			"(`PUT /api/v2/workspaces/{id}/override-switches`), "+
			"and the permission to write without the lock (`unit:write-unlocked`)",
		lockErr,
	), lockErr)
}

// Upload stores the content of a pending state version and makes it current.
// The content must match the declared MD5 and fit the size limit, and the
// lock it was declared under must still be held when it finalizes; otherwise
// the content is discarded. Concurrent uploads of one version are safe
// across services: each writes its own blob and records its own attempt,
// the finalize commits only when the row still names that attempt, so
// exactly one attempt wins, and each loser deletes only its own blob. The
// stripe below only trims contention between uploads of one version in this
// service. A retry after success (same capability, same content) succeeds
// without storing anything, because go-tfe retries when a response is lost.
// A retry after an unknown outcome records a new attempt, which supersedes
// the earlier one: the row always names the blob the sweep must keep.
func (s *Service) Upload(
	ctx context.Context,
	id StateVersionID,
	tok token.Token,
	content io.Reader,
) (StateVersion, error) {
	unlock := s.lockKey("upload/" + id.String())
	defer unlock()

	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("upload state %s: %w", id, err)
	}

	if checkErr := sv.checkUploadCapability(tok, s.clock()); checkErr != nil {
		return StateVersion{}, checkErr
	}

	if sv.retriedBy(tok) {
		return s.confirmRetry(sv, content)
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return StateVersion{}, err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return StateVersion{}, err
	}

	key, err := s.blobs.attemptKey(w.NamespaceID, w.ID, sv.ID)
	if err != nil {
		return StateVersion{}, err
	}

	// Note the attempt before writing: when the finalize outcome stays
	// unknown, the row still names the content, so a later sweep deletes it.
	if noteErr := s.noteAttempt(ctx, w, id, key, fence); noteErr != nil {
		return StateVersion{}, noteErr
	}

	body, err := s.storeUploadBody(ctx, w, sv, key, content)
	if err != nil {
		return StateVersion{}, err
	}

	sv.Status = StatusFinalized
	sv.FinalizedAt = s.clock()
	sv.Size = body.size
	sv.Content = key
	sv.Outputs = body.outputs.stored

	if err := s.states.FinalizeStateVersion(ctx, sv, fence); err != nil {
		err = fmt.Errorf("finalize state %s: %w", id, err)
		if rejected(err) {
			// Nothing was written, so this attempt's blob is an orphan.
			return StateVersion{}, errors.Join(err, s.deleteOnCleanup(ctx, w, key))
		}

		// The outcome is unknown; the version may point at this blob. Keep it.
		return StateVersion{}, err
	}

	return sv, nil
}

// CheckUpload reports whether the capability tok may upload state version id
// now. It reads no content. It returns the same errors as Upload returns for
// a bad capability: ErrStateVersionNotFound for an unknown version,
// ErrUploadDenied for a wrong or expired capability or a version that is not
// pending. A retry with the capability that finalized the version passes, as
// in Upload. On success it also returns the namespace of the workspace of
// the version, after the capability check passed, so a caller with a bad
// token learns nothing about an allowlist.
func (s *Service) CheckUpload(
	ctx context.Context, id StateVersionID, tok token.Token,
) (namespace.Name, error) {
	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return "", fmt.Errorf("upload state %s: %w", id, err)
	}

	if capErr := sv.checkUploadCapability(tok, s.clock()); capErr != nil {
		return "", capErr
	}

	w, err := s.workspaces.GetWorkspaceByID(ctx, sv.Workspace)
	if err != nil {
		return "", fmt.Errorf("upload state %s: %w", id, err)
	}

	return w.Ref.Namespace, nil
}

// CheckJSONRenderingUpload does the same for the JSON rendering upload (the
// rules of DiscardJSONRendering: wrong capability or expired capability fail;
// a finalized version with the same capability still passes). On success it
// also returns the namespace of the workspace of the version, after the
// capability check passed.
func (s *Service) CheckJSONRenderingUpload(
	ctx context.Context, id StateVersionID, tok token.Token,
) (namespace.Name, error) {
	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return "", fmt.Errorf("upload JSON rendering of %s: %w", id, err)
	}

	if capErr := sv.checkRenderingCapability(tok, s.clock()); capErr != nil {
		return "", capErr
	}

	w, err := s.workspaces.GetWorkspaceByID(ctx, sv.Workspace)
	if err != nil {
		return "", fmt.Errorf("upload JSON rendering of %s: %w", id, err)
	}

	return w.Ref.Namespace, nil
}

// uploadedBody is what a state upload stores: the size of its bytes, and the
// outputs read from them as they streamed.
type uploadedBody struct {
	size    int64
	outputs streamedOutputs
}

// DiscardJSONRendering accepts the JSON rendering the CLI uploads alongside
// a state version: it keeps only the JSON Pointer paths that sensitive_values
// marks, by resource instance address, and discards the rest of the rendering.
// It never keeps a value from it. The capability rules match Upload, except
// that a retry of a finalized version with the same capability succeeds
// whatever the rendering holds: the two PUTs of one upload carry other
// content by design. A rendering without marks keeps nothing: the read then
// masks every attribute value. A rendering over the size limit fails with
// ErrTooLarge, and an expired capability fails whatever the version status.
// Unlike Upload it never finalizes and never consumes the capability, so the
// state upload with the same capability still works afterwards.
func (s *Service) DiscardJSONRendering(
	ctx context.Context,
	id StateVersionID,
	tok token.Token,
	content io.Reader,
) error {
	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return fmt.Errorf("upload JSON rendering of %s: %w", id, err)
	}

	if checkErr := sv.checkRenderingCapability(tok, s.clock()); checkErr != nil {
		return checkErr
	}

	raw, err := io.ReadAll(io.LimitReader(content, s.maxState+1))
	if err != nil {
		return fmt.Errorf("read JSON rendering of %s: %w", id, err)
	}

	if int64(len(raw)) > s.maxState {
		return errTooLargeBody("JSON rendering", int64(len(raw)), s.maxState)
	}

	marks, err := ExtractSensitiveMarks(raw)
	if err != nil {
		// A rendering the server cannot parse keeps nothing: the read then
		// masks every attribute value, so no secret value ever leaks.
		marks = SensitiveMarks{}
	}

	// An empty parse over stored marks keeps the stored ones: a retry with
	// other content must never wipe the marks of the version.
	if len(marks) == 0 {
		return nil
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return err
	}

	if err := s.states.SaveSensitiveMarks(ctx, id, marks, fence); err != nil {
		return fmt.Errorf("keep sensitive marks of %s: %w", id, err)
	}

	return nil
}

// Current returns the workspace's current state version, or ErrNoState.
// A sealed version carries its outputs opened, so callers read plain outputs.
func (s *Service) Current(ctx context.Context, id ID) (StateVersion, error) {
	sv, err := s.states.CurrentStateVersion(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("current state of %s: %w", id, err)
	}

	if err := s.openOutputs(ctx, &sv); err != nil {
		return StateVersion{}, err
	}

	return sv, nil
}

// ReadState returns the finalized state version with id, or
// ErrStateVersionNotFound when it does not exist or is not finalized. A
// pending version is never readable. A sealed version carries its outputs
// opened, so callers read plain outputs.
func (s *Service) ReadState(ctx context.Context, id StateVersionID) (StateVersion, error) {
	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("read state %s: %w", id, err)
	}

	if sv.Status != StatusFinalized {
		return StateVersion{}, fmt.Errorf("%w: state version %s is %s", ErrStateVersionNotFound, id, sv.Status)
	}

	if err := s.openOutputs(ctx, &sv); err != nil {
		return StateVersion{}, err
	}

	return sv, nil
}

// Open returns the content of a finalized state version. The caller closes it.
// The version opens its sealed blob, so the caller reads the same bytes
// the CLI sent. A decryption failure reports the version ID only, never a
// key, a scope or content.
func (s *Service) Open(ctx context.Context, sv StateVersion) (io.ReadCloser, error) {
	if sv.Status != StatusFinalized {
		return nil, fmt.Errorf("%w: state version %s is %s", ErrStateVersionNotFound, sv.ID, sv.Status)
	}

	// The namespace comes from the workspace row, never from the key of the
	// version: a key of another namespace gives blob.ErrOutOfScope.
	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return nil, err
	}

	r, err := s.blobs.get(ctx, w.NamespaceID, w.ID, sv.Content)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, err)
	}

	opened, err := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), string(sv.ID), r)
	if err != nil {
		return nil, errors.Join(sanitizeOpenError(sv.ID, err), r.Close())
	}

	return &closeReader{reader: newVerifiedReader(opened, sv), opened: opened, closer: r, id: sv.ID}, nil
}

// OpenStateBlob returns the plain content of the sealed blob under key, for
// the backup copy check. The key names one state version attempt as
// "namespaces/<namespace id>/tofu/<workspace id>/<state version id>/<attempt>".
// A failure names the operation and, when it is known, the version ID or the
// workspace ID, never a key, a scope or content. The caller closes the result.
//
// The namespace of the read comes from the workspace row of the version, never
// from the key. A key that names another namespace gives blob.ErrOutOfScope,
// and the store gets no call.
func (s *Service) OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	parsed, err := ParseStateBlobKey(key)
	if err != nil {
		return nil, fmt.Errorf("open state blob: %w", err)
	}

	sv, err := s.states.GetStateVersion(ctx, parsed.Version)
	if err != nil {
		return nil, fmt.Errorf("open state blob %s: %w", parsed.Version, err)
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return nil, err
	}

	// The key must name the workspace and the version it resolves to:
	// a key whose workspace segment names another workspace opens
	// nothing, so one workspace never opens another's sealed bytes. The
	// refusal goes to the audit log in the namespace of the row.
	if parsed.Workspace != sv.Workspace || parsed.Version != sv.ID {
		return nil, fmt.Errorf(
			"open state blob %s of workspace %s: %w",
			parsed.Version, parsed.Workspace, s.blobs.refuseMismatch(ctx, w.NamespaceID),
		)
	}

	r, err := s.blobs.get(ctx, w.NamespaceID, w.ID, key)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, err)
	}

	opened, err := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), string(sv.ID), r)
	if err != nil {
		return nil, errors.Join(sanitizeOpenError(sv.ID, err), r.Close())
	}

	return &closeReader{reader: newVerifiedReader(opened, sv), opened: opened, closer: r, id: sv.ID}, nil
}

// History returns the finalized state versions of the workspace, newest
// first, with the total count of finalized versions. Sealed versions carry
// their outputs opened, so callers read plain outputs.
func (s *Service) History(ctx context.Context, id ID, page Page) ([]StateVersion, int, error) {
	list, total, err := s.states.ListStateVersions(ctx, id, page)
	if err != nil {
		return nil, 0, fmt.Errorf("history of %s: %w", id, err)
	}

	for i := range list {
		if err := s.openOutputs(ctx, &list[i]); err != nil {
			return nil, 0, err
		}
	}

	return list, total, nil
}

// ReadResources returns one page of the masked resource list of the
// finalized state version id. It reads the state through Open, the same path
// the download uses, so sealed state opens before the read and the blob
// store never serves callers directly. Each secret value answers as a marker
// with its value path, never as plain text. A state the client encrypted
// fails with ErrClientEncrypted: the outputs list still works for it. Size
// is the page size, or zero for the default page; cursor is "" or the
// NextCursor of an earlier page. Filter is a substring of the address or of
// the resource type, or "" for no filter: it never matches attribute values.
// Paging applies after the filter, so a page holds only matching resources.
func (s *Service) ReadResources(
	ctx context.Context, id StateVersionID, cursor string, size int, filter string,
) (StatePage, error) {
	state, sv, err := s.openStateBytes(ctx, id)
	if err != nil {
		return StatePage{}, err
	}

	if encryptedErr := rejectEncryptedState(state, id); encryptedErr != nil {
		return StatePage{}, encryptedErr
	}

	previous, err := s.predecessorBytes(ctx, sv)
	if err != nil {
		return StatePage{}, err
	}

	page, err := ListStateResources(state, previous, cursor, size, filter)
	if err != nil {
		return StatePage{}, fmt.Errorf("read resources of %s: %w", id, err)
	}

	return page, nil
}

// ReadResource returns one resource of the finalized state version id with
// its instances masked. It reads the state through Open, the same path the
// download uses. The address matches verbatim. A state the client encrypted
// fails with ErrClientEncrypted.
func (s *Service) ReadResource(
	ctx context.Context, id StateVersionID, address string,
) (StateDetail, error) {
	state, sv, err := s.openStateBytes(ctx, id)
	if err != nil {
		return StateDetail{}, err
	}

	if encryptedErr := rejectEncryptedState(state, id); encryptedErr != nil {
		return StateDetail{}, encryptedErr
	}

	detail, err := ReadStateResource(state, sv.SensitiveMarks, address)
	if err != nil {
		return StateDetail{}, fmt.Errorf("read resource of %s: %w", id, err)
	}

	return detail, nil
}

// DiffStates returns the diff of two finalized state versions: to is the
// newer side, from is the older one. Both versions must belong to the same
// workspace; a from version of another workspace fails with
// ErrStateVersionNotFound as if it did not exist, so no workspace leaks. It
// reads both states through Open, the same path the download uses, so sealed
// state opens before the diff and the blob store never serves callers
// directly. It compares the plain values in memory only to compute paths; no
// value leaves the diff. A state either side encrypted fails with
// ErrClientEncrypted. Cursor is "" or the NextCursor of an earlier page;
// size is the page size, or zero for the default page.
func (s *Service) DiffStates(
	ctx context.Context, to, from StateVersionID, cursor string, size int,
) (StateDiff, error) {
	toState, toVersion, err := s.openStateBytes(ctx, to)
	if err != nil {
		return StateDiff{}, err
	}

	fromVersion, err := s.ReadState(ctx, from)
	if err != nil {
		return StateDiff{}, err
	}

	if fromVersion.Workspace != toVersion.Workspace {
		return StateDiff{}, fmt.Errorf("%w: state version %s", ErrStateVersionNotFound, from)
	}

	fromState, _, err := s.openStateBytes(ctx, from)
	if err != nil {
		return StateDiff{}, err
	}

	if encryptedErr := rejectEncryptedState(toState, to); encryptedErr != nil {
		return StateDiff{}, encryptedErr
	}

	if encryptedErr := rejectEncryptedState(fromState, from); encryptedErr != nil {
		return StateDiff{}, encryptedErr
	}

	diff, err := DiffStates(
		StateWithMarks{State: fromState, Marks: fromVersion.SensitiveMarks},
		StateWithMarks{State: toState, Marks: toVersion.SensitiveMarks},
		cursor, size,
	)
	if err != nil {
		return StateDiff{}, fmt.Errorf("diff states %s from %s: %w", to, from, err)
	}

	return diff, nil
}

// Restore makes the finalized state version from current again as a new,
// forced version, so OpenTofu reads the old bytes with the next apply. The
// workspace must be unlocked or locked by holder; a lock that another
// principal holds gives ErrLockedByOther. The service takes the lock when it
// is free and releases it at the end, also on error. A lock the caller
// already holds stays.
//
// The new version keeps the lineage, serial, MD5 and size of the old one,
// sets Force and RestoredFrom, and becomes current through the same pending
// and finalize path as an upload, so the lock and fence checks stay in the
// store. The content is copied to its own blob key with the MD5 checked while
// it streams; a mismatch deletes the new key. The service never parses or
// rewrites state content. A restore repeats the serial of the old version in
// the history, as `tofu state push -force` does. A retried restore answers
// the version the first call made current and writes nothing new, but still
// needs the workspace unlocked or held by holder.
func (s *Service) Restore(ctx context.Context, id ID, from StateVersionID, holder string) (StateVersion, error) {
	return s.restoreChecked(ctx, id, from, holder, nil)
}

// restoreChecked is Restore with an optional expected current version. A nil expect
// skips the check.
func (s *Service) restoreChecked(
	ctx context.Context, id ID, from StateVersionID, holder string, expect *StateVersionID,
) (StateVersion, error) {
	if holder == "" {
		return StateVersion{}, fmt.Errorf("%w: restore without a holder", ErrNotLocked)
	}

	src, err := s.states.GetStateVersion(ctx, from)
	if err != nil {
		return StateVersion{}, fmt.Errorf("restore %s from %s: %w", id, from, err)
	}

	if src.Workspace != id || src.Status != StatusFinalized {
		return StateVersion{}, fmt.Errorf("%w: state version %s", ErrStateVersionNotFound, from)
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("restore %s: %w", id, err)
	}

	// Restores of one workspace share a stripe from the retry check to the
	// finalize. A holder that already holds the lock takes no lock below,
	// so without the stripe two concurrent restores of the same version
	// would both pass the check and both write a restored version. With
	// it, the second waits and then answers the first one's version.
	unlock := s.lockKey("restore/" + id.String())
	defer unlock()

	if retried, found, retryErr := s.restoredCurrent(ctx, id, from, holder); retryErr != nil || found {
		return retried, retryErr
	}

	w, release, err := s.takeRestoreLock(ctx, w, holder)
	if err != nil {
		return StateVersion{}, err
	}

	sv, err := s.copyState(ctx, w, src, expect)
	if errors.Is(err, ErrAlreadyRestored) {
		sv, err = s.answerDuplicateRestore(ctx, id, from, holder, err)
	}

	if !release {
		return sv, err
	}

	// The release that ends a restore belongs to the restore, not to the
	// request that started it: it records no origin, like the version that
	// the restore writes.
	_, unlockErr := s.Unlock(WithJob(ctx, ""), id, holder)

	return sv, errors.Join(err, unlockErr)
}

// releaseWithoutState releases the lock of a unit that has no state, and
// appends the audit entry of the release in the same transaction. The check of
// the expected lock runs again against the revision that the release saves.
func (s *Service) releaseWithoutState(
	ctx context.Context, w Workspace, expect Lock, by string, entry audit.Entry,
) (Workspace, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	current, err := s.GetByID(ctx, w.ID)
	if err != nil {
		return Workspace{}, err
	}

	if lockErr := checkLockID(current, expect); lockErr != nil {
		return Workspace{}, lockErr
	}

	now := s.clock()

	released, err := current.forceUnlock(now, by)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", w.ID, err)
	}

	released.Origin = originFrom(ctx)

	err = s.workspaces.SaveReleased(ctx, released, fence, forceUnlockEntry(entry, current, by, now))
	if err != nil {
		return Workspace{}, fmt.Errorf("save workspace %s: %w", w.ID, err)
	}

	return released, nil
}

// answerDuplicateRestore answers the version that a concurrent restore of from
// made current, after the store refused this restore as a duplicate with
// dupErr. The duplicate wrote nothing. When the current state no longer
// restores from, dupErr stays the answer.
func (s *Service) answerDuplicateRestore(
	ctx context.Context, id ID, from StateVersionID, holder string, dupErr error,
) (StateVersion, error) {
	retried, found, err := s.restoredCurrent(ctx, id, from, holder)
	if err != nil {
		return StateVersion{}, errors.Join(dupErr, err)
	}

	if !found {
		return StateVersion{}, dupErr
	}

	return retried, nil
}

// followedState returns the current state version of the unit, and whether the
// unit has one. A new state must follow the current one.
func (s *Service) followedState(ctx context.Context, w Workspace, in NewState) (StateVersion, bool, error) {
	current, err := s.states.CurrentStateVersion(ctx, w.ID)
	if errors.Is(err, ErrNoState) {
		var none StateVersion

		return none, false, nil
	}

	if err != nil {
		var none StateVersion

		return none, false, fmt.Errorf("current state of %s: %w", w.Ref, err)
	}

	if followErr := in.follows(current); followErr != nil {
		var none StateVersion

		return none, false, fmt.Errorf("new state for %s: %w", w.Ref, followErr)
	}

	return current, true, nil
}

// rejectEncryptedState fails with ErrClientEncrypted when state is a state
// file the client encrypted: the resource routes cannot read it, while the
// outputs list still works. A state that the check cannot classify is refused
// with the error of the check: the state never reads as plaintext.
func rejectEncryptedState(state []byte, id StateVersionID) error {
	encrypted, err := IsClientEncrypted(state)
	if err != nil {
		return fmt.Errorf("read state %s: %w", id, err)
	}

	if encrypted {
		return fmt.Errorf("read state %s: %w", id, ErrClientEncrypted)
	}

	return nil
}

// openStateBytes returns the plain content of the finalized state version
// id with its version. It opens the sealed blob through Open, so callers
// read the same bytes the CLI sent, and it reads at most the state size
// limit.
func (s *Service) openStateBytes(ctx context.Context, id StateVersionID) ([]byte, StateVersion, error) {
	sv, err := s.ReadState(ctx, id)
	if err != nil {
		return nil, StateVersion{}, err
	}

	r, err := s.Open(ctx, sv)
	if err != nil {
		return nil, StateVersion{}, err
	}

	raw, err := io.ReadAll(io.LimitReader(r, s.maxState+1))
	closeErr := r.Close()

	if err != nil {
		return nil, StateVersion{}, fmt.Errorf("read state %s: %w", id, errors.Join(err, closeErr))
	}

	if closeErr != nil {
		return nil, StateVersion{}, fmt.Errorf("read state %s: %w", id, closeErr)
	}

	if int64(len(raw)) > s.maxState {
		return nil, StateVersion{}, errTooLargeBody("state", int64(len(raw)), s.maxState)
	}

	return raw, sv, nil
}

// predecessorBytes returns the plain content of the finalized version before
// sv in its workspace, or nil when sv is the first version. A previous
// version the client encrypted reads as no previous version: every resource
// then counts as new, while the current version still answers.
func (s *Service) predecessorBytes(ctx context.Context, sv StateVersion) ([]byte, error) {
	previous, found, err := s.predecessor(ctx, sv)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, nil
	}

	r, err := s.Open(ctx, previous)
	if err != nil {
		return nil, fmt.Errorf("read previous state of %s: %w", sv.ID, err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(r, s.maxState+1))
	closeErr := r.Close()

	if readErr != nil {
		return nil, fmt.Errorf("read previous state of %s: %w", sv.ID, errors.Join(readErr, closeErr))
	}

	if closeErr != nil {
		return nil, fmt.Errorf("read previous state of %s: %w", sv.ID, closeErr)
	}

	encrypted, encErr := IsClientEncrypted(raw)
	if encErr != nil {
		return nil, fmt.Errorf("read previous state of %s: %w", sv.ID, encErr)
	}

	if encrypted {
		return nil, nil
	}

	return raw, nil
}

// predecessor returns the finalized version before sv in its workspace,
// newest first. Found reports whether sv has a previous version: the first
// version has none. It pages through the history until it finds sv.
func (s *Service) predecessor(
	ctx context.Context, sv StateVersion,
) (StateVersion, bool, error) {
	const pageSize = 100

	var none StateVersion

	for offset := 0; ; offset += pageSize {
		list, total, err := s.History(ctx, sv.Workspace, Page{Limit: pageSize, Offset: offset})
		if err != nil {
			return none, false, fmt.Errorf("history of %s: %w", sv.Workspace, err)
		}

		previous, found, err := s.predecessorIn(ctx, sv, list, offset, total)
		if err != nil || found {
			return previous, found, err
		}

		if offset+len(list) >= total {
			return none, false, nil
		}
	}
}

// predecessorIn looks for sv in one history page and returns the version
// before it: the next entry of the page, or the lead of the next page when
// sv ends it. Found reports whether a previous version exists.
func (s *Service) predecessorIn(
	ctx context.Context, sv StateVersion, list []StateVersion, offset, total int,
) (StateVersion, bool, error) {
	var none StateVersion

	for i := range list {
		if list[i].ID != sv.ID {
			continue
		}

		if i+1 < len(list) {
			return list[i+1], true, nil
		}

		if offset+len(list) >= total {
			return none, false, nil
		}

		next, _, err := s.History(ctx, sv.Workspace, Page{Limit: 1, Offset: offset + len(list)})
		if err != nil {
			return none, false, fmt.Errorf("history of %s: %w", sv.Workspace, err)
		}

		if len(next) == 0 {
			return none, false, nil
		}

		return next[0], true, nil
	}

	return none, false, nil
}

// takeRestoreLock holds w for holder: it takes the lock when w is free and
// reports whether the caller must release it afterwards.
func (s *Service) takeRestoreLock(ctx context.Context, w Workspace, holder string) (Workspace, bool, error) {
	switch {
	case !w.Lock.Locked():
		locked, err := s.Lock(ctx, w.ID, holder, "restore")
		if err != nil {
			return Workspace{}, false, s.mapRestoreLockError(ctx, w.ID, holder, err)
		}

		return locked, true, nil
	case w.Lock.Holder != holder:
		return Workspace{}, false, fmt.Errorf("restore %s: %w", w.ID, w.heldBy(holder))
	default:
		return w, false, nil
	}
}

// restoredCurrent answers the current version when it already restores from,
// without taking the lock. Found reports a retry.
func (s *Service) restoredCurrent(
	ctx context.Context,
	id ID,
	from StateVersionID,
	holder string,
) (StateVersion, bool, error) {
	var none StateVersion

	current, err := s.states.CurrentStateVersion(ctx, id)
	switch {
	case errors.Is(err, ErrNoState):
		return none, false, nil
	case err != nil:
		return none, false, fmt.Errorf("current state of %s: %w", id, err)
	case current.RestoredFrom != from:
		return none, false, nil
	}

	fresh, err := s.GetByID(ctx, id)
	if err != nil {
		return none, false, fmt.Errorf("restore %s: %w", id, err)
	}

	if fresh.Lock.Locked() {
		if lockErr := fresh.heldBy(holder); lockErr != nil {
			return none, false, fmt.Errorf("restore %s: %w", fresh.ID, lockErr)
		}
	}

	// The row holds sealed outputs; open them so the caller renders plain
	// outputs.
	if err := s.openOutputs(ctx, &current); err != nil {
		return none, false, err
	}

	return current, true, nil
}

// mapRestoreLockError names the holder when a take of a free workspace loses
// a race to another principal. ErrLocked and ErrConcurrentModification both
// mean the stored workspace changed under the fetch, so a fresh read decides
// the sentinel: a lock another principal holds gives ErrLockedByOther.
func (s *Service) mapRestoreLockError(ctx context.Context, id ID, holder string, err error) error {
	if !errors.Is(err, ErrLocked) && !errors.Is(err, ErrConcurrentModification) {
		return err
	}

	fresh, freshErr := s.GetByID(ctx, id)
	if freshErr != nil {
		return err
	}

	if lockErr := fresh.heldBy(holder); errors.Is(lockErr, ErrLockedByOther) {
		return fmt.Errorf("restore %s: %w", id, lockErr)
	}

	return err
}

// copyState copies src to a new blob key and makes it current as a forced
// version that records src. The pending row records the attempt before the
// copy, and the blob lands before the finalize, so a committed version
// never names a missing blob.
//
// A non-nil expect names the current version that the caller read. The finalize
// checks it in its own transaction, so a write by the same holder under the
// same lock cannot slip in between: a mismatch gives ErrConcurrentModification
// and writes nothing.
func (s *Service) copyState(
	ctx context.Context, w Workspace, src StateVersion, expect *StateVersionID,
) (StateVersion, error) {
	outputs, err := s.openRestoreOutputs(ctx, w, src)
	if err != nil {
		return StateVersion{}, err
	}

	// The hex MD5 in NewState is display only; createPending stores the
	// validated sum passed alongside it, which is src.MD5 here.
	pending, err := s.createPending(ctx, w, NewState{
		Lineage: src.Lineage,
		MD5:     hex.EncodeToString(src.MD5[:]),
		Outputs: outputs,
		Serial:  src.Serial,
		Force:   true,
	}, src.MD5, pendingDeclare{restoredFrom: src.ID, unlocked: false, origin: identity.StateOrigin{Job: "", Commit: ""}})
	if err != nil {
		return StateVersion{}, err
	}

	// The stored row holds no outputs; finalize it, so the commit keeps
	// what the row holds.
	stored, err := s.states.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		return StateVersion{}, fmt.Errorf("restore %s from %s: %w", w.ID, src.ID, err)
	}

	key, err := s.blobs.attemptKey(w.NamespaceID, w.ID, stored.ID)
	if err != nil {
		return StateVersion{}, err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return StateVersion{}, err
	}

	// A restore copies the content, so it copies the kept marks too: the new
	// version masks like the version it restores.
	if len(src.SensitiveMarks) > 0 {
		marksErr := s.states.SaveSensitiveMarks(ctx, pending.Version.ID, src.SensitiveMarks, fence)
		if marksErr != nil {
			return StateVersion{}, fmt.Errorf("restore %s from %s: %w", w.ID, src.ID, marksErr)
		}
	}

	// Like an upload, the row names the attempt before the bytes land, so a
	// sweep finds the blob even when the finalize outcome stays unknown.
	if noteErr := s.noteAttempt(ctx, w, pending.Version.ID, key, fence); noteErr != nil {
		return StateVersion{}, noteErr
	}

	size, err := s.copyBlob(ctx, w, key, src, string(stored.ID))
	if err != nil {
		return StateVersion{}, err
	}

	sv := stored
	sv.Status = StatusFinalized
	sv.FinalizedAt = s.clock()
	sv.Size = size
	sv.Content = key

	if err := s.finalizeRestore(ctx, sv, fence, expect); err != nil {
		return StateVersion{}, s.refusedRestore(ctx, w, sv, fence, err)
	}

	// The caller renders the new version: carry the plain outputs, while the
	// committed row keeps the sealed document.
	sv.Outputs = outputs

	return sv, nil
}

// finalizeRestore finalizes the restored version sv. With expect set, it also
// checks the current version in the finalize transaction.
func (s *Service) finalizeRestore(
	ctx context.Context, sv StateVersion, fence namespace.Fence, expect *StateVersionID,
) error {
	if expect == nil {
		return s.states.FinalizeStateVersion(ctx, sv, fence)
	}

	return s.states.FinalizeStateVersionExpecting(ctx, sv, fence, *expect)
}

// refusedRestore cleans up after the failed finalize of the restored version
// sv and returns the error. A rejection wrote nothing, so the blob of the
// attempt is an orphan and goes. A duplicate restore also discards its pending
// row, so the row does not fill the pending cap. Any other error leaves the
// outcome unknown: the version may name the blob, so it stays.
func (s *Service) refusedRestore(
	ctx context.Context, w Workspace, sv StateVersion, fence namespace.Fence, finalizeErr error,
) error {
	err := fmt.Errorf("finalize restored state %s: %w", sv.ID, finalizeErr)

	switch {
	case errors.Is(err, ErrAlreadyRestored), errors.Is(err, ErrConcurrentModification):
		return errors.Join(
			err,
			s.blobs.delete(ctx, w.NamespaceID, w.ID, sv.Content),
			s.states.DiscardStateVersion(ctx, sv.ID, fence),
		)
	case rejected(err):
		return errors.Join(err, s.blobs.delete(ctx, w.NamespaceID, w.ID, sv.Content))
	default:
		return err
	}
}

// openRestoreOutputs opens the stored outputs of src, so the new version
// seals the plain document again, never the ciphertext. A version without
// outputs keeps none. A failure reports the version ID only, never a key,
// a scope or content.
func (s *Service) openRestoreOutputs(
	ctx context.Context, w Workspace, src StateVersion,
) ([]byte, error) {
	if len(src.Outputs) == 0 {
		return nil, nil
	}

	plain, err := s.sealer.OpenOutputs(ctx, w.Ref.Namespace, w.ID.String(), string(src.ID), src.Outputs)
	if err != nil {
		return nil, sanitizeOpenError(src.ID, fmt.Errorf("restore %s: %w", src.ID, err))
	}

	return plain, nil
}

// copyBlob copies the content of src under key, checking size and MD5 as it
// streams. The source opens before the check, so the check runs on the
// plain bytes, and the copy seals again, bound to the new state version. On
// any mismatch the object is deleted.
func (s *Service) copyBlob(
	ctx context.Context, w Workspace, key blob.Key, src StateVersion, version string,
) (int64, error) {
	r, err := s.blobs.get(ctx, w.NamespaceID, w.ID, src.Content)
	if err != nil {
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, err)
	}

	opened, oerr := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), string(src.ID), r)
	if oerr != nil {
		return 0, errors.Join(
			sanitizeOpenError(src.ID, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, oerr)),
			r.Close(),
		)
	}

	r = &closeReader{reader: opened, opened: opened, closer: r, id: src.ID}

	size, storeErr := s.store(ctx, storeInput{
		namespace:   w.Ref.Namespace,
		namespaceID: w.NamespaceID,
		workspace:   w.ID,
		version:     version,
		key:         key,
		want:        src.MD5,
		content:     r,
		unchecked:   false,
	})
	closeErr := r.Close()

	switch {
	case storeErr != nil:
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, errors.Join(storeErr, closeErr))
	case closeErr != nil:
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, errors.Join(
			closeErr,
			s.blobs.delete(ctx, w.NamespaceID, w.ID, key),
		))
	default:
		return size, nil
	}
}

// confirmRetry answers a retried upload of a finalized version: the same
// content succeeds without being stored again, other content is denied.
func (s *Service) confirmRetry(sv StateVersion, content io.Reader) (StateVersion, error) {
	digest := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.

	n, err := io.Copy(digest, io.LimitReader(content, s.maxState+1))
	if err != nil {
		return StateVersion{}, fmt.Errorf("read retried upload of %s: %w", sv.ID, err)
	}

	if n != sv.Size || [md5.Size]byte(digest.Sum(nil)) != sv.MD5 {
		return StateVersion{}, fmt.Errorf(
			"%w: state version %s is finalized with other content",
			ErrUploadDenied,
			sv.ID,
		)
	}

	return sv, nil
}

// sweepExpired deletes the expired pending versions of w with their
// attempted content, then deletes the blobs under the workspace prefix
// that no version references. A version expires with its upload capability;
// the sweep keeps it for SweepGracePeriod past expiry, so an upload that
// started before expiry still finishes. A version that never recorded an
// attempt sweeps its row with no blob to delete. A blob whose row delete
// already committed, or whose superseded delete never ran, stays
// unreferenced: the second pass collects it once it is older than the grace
// period, so a cleanup failure or a crash between the row delete and the
// blob delete never leaves an orphan for ever. A cleanup failure fails the
// sweep, so the caller decides: BeginState treats the sweep as best effort
// and declares anyway, leaving the rows for the next sweep.
func (s *Service) sweepExpired(ctx context.Context, w Workspace) error {
	expired, err := s.expirePending(ctx, w)
	if err != nil {
		return err
	}

	cleanupErr := s.deleteExpiredBlobs(ctx, w, expired)

	// A blob delete above may have failed, or the process may have crashed
	// between an earlier row delete and its blob delete: the unreferenced
	// pass retries those blobs once they are past the grace period.
	if unrefErr := s.sweepUnreferenced(ctx, w); unrefErr != nil {
		cleanupErr = errors.Join(cleanupErr, unrefErr)
	}

	if cleanupErr != nil {
		return fmt.Errorf("sweep expired state of %s: %w", w.Ref, cleanupErr)
	}

	return nil
}

// sweepExpiredRows deletes the expired pending versions of w with their
// attempted content, without the unreferenced-blob pass. The workspace
// delete uses it, so the delete never removes a blob that no version
// references: those blobs stay for the retention sweep, which runs under
// the audit chain gate.
func (s *Service) sweepExpiredRows(ctx context.Context, w Workspace) error {
	expired, err := s.expirePending(ctx, w)
	if err != nil {
		return err
	}

	if cleanupErr := s.deleteExpiredBlobs(ctx, w, expired); cleanupErr != nil {
		return fmt.Errorf("sweep expired state of %s: %w", w.Ref, cleanupErr)
	}

	return nil
}

// expirePending deletes the expired pending versions of w and returns them
// with their attempted content keys, so the caller deletes the blobs. A
// version that is already gone counts as done, with no error, so two sweeps
// that overlap on one row do not fail.
func (s *Service) expirePending(ctx context.Context, w Workspace) ([]StateVersion, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return nil, err
	}

	expired, err := s.states.DeleteExpiredPendingStateVersions(ctx, w.ID, s.clock().Add(-SweepGracePeriod), fence)
	if errors.Is(err, ErrStateVersionNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("sweep expired state of %s: %w", w.Ref, err)
	}

	return expired, nil
}

// deleteExpiredBlobs deletes the attempted content of the expired versions.
// A blob that is already gone counts as done, with no error, so two sweeps
// that overlap on one blob do not fail.
func (s *Service) deleteExpiredBlobs(ctx context.Context, w Workspace, expired []StateVersion) error {
	var cleanupErr error

	for i := range expired {
		if expired[i].Content == "" {
			continue
		}

		if blobErr := s.blobs.deleteRetried(ctx, w.NamespaceID, w.ID, expired[i].Content); blobErr != nil {
			if errors.Is(blobErr, blob.ErrNotFound) {
				continue
			}

			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete content of %s: %w", expired[i].ID, blobErr))
		}
	}

	return cleanupErr
}

// sweepUnreferenced deletes the blobs under the workspace prefix that no
// version names and that are older than the grace period: superseded
// attempts whose delete never ran, and swept attempts whose row delete
// committed before the blob delete. Blobs younger than the grace period
// stay, so an upload that still streams is never collected: every attempt
// blob is fresh, and the row names it before the bytes land. Blob
// modification times share the service clock wherever the store runs. The
// sweep filters by age before reading the referenced keys, so a declare
// with only fresh blobs pays for one listing and no history read. The keep
// set is read by the workspace ID that the listing used. A read failure
// deletes nothing, also when the workspace row is gone.
func (s *Service) sweepUnreferenced(ctx context.Context, w Workspace) error {
	var cleanupErr error

	// The sweep goes page by page: it lists a page, keeps the candidates of the
	// page, reads the referenced keys after that listing, and deletes. So it
	// never holds the blobs of the whole workspace, and each read of the
	// referenced keys comes after the listing that it filters.
	err := s.blobs.walk(ctx, w.NamespaceID, w.ID, func(page []blob.Object) error {
		cleanupErr = errors.Join(cleanupErr, s.sweepPage(ctx, w, page))

		return nil
	})
	if err != nil {
		return fmt.Errorf("list state content of %s: %w", w.Ref, err)
	}

	return cleanupErr
}

// sweepPage deletes the blobs of one page of the listing of w that no version
// names and that are older than the grace period. It reads the referenced keys
// only when old candidates remain. A read failure deletes nothing.
func (s *Service) sweepPage(ctx context.Context, w Workspace, page []blob.Object) error {
	now := s.clock()

	candidates := make([]blob.Object, 0, len(page))

	for _, obj := range page {
		if now.Sub(obj.ModifiedAt) >= SweepGracePeriod {
			candidates = append(candidates, obj)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	refs, err := s.states.ReferencedContentKeys(ctx, w.ID)
	if err != nil {
		return fmt.Errorf("list referenced content of %s: %w", w.Ref, err)
	}

	named := make(map[blob.Key]struct{}, len(refs))

	for _, key := range refs {
		named[key] = struct{}{}
	}

	var cleanupErr error

	for _, obj := range candidates {
		if _, ok := named[obj.Key]; ok {
			continue
		}

		if blobErr := s.blobs.deleteRetried(ctx, w.NamespaceID, w.ID, obj.Key); blobErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete unreferenced content of %s: %w", w.Ref, blobErr))
		}
	}

	return cleanupErr
}

// noteAttempt records key as the latest upload attempt of id, so a later
// sweep finds the content even when the finalize outcome stays unknown.
// The record runs in the store, which returns the superseded key atomically:
// the row names only the latest attempt, so only that earlier blob goes,
// after the row stops naming it. A delete failure leaves it for the
// unreferenced-blob sweep. The caller never deletes a key the row still
// names, so a concurrent upload on another service never loses its content:
// the finalize admits only the recorded attempt, and each loser deletes
// only its own blob.
func (s *Service) noteAttempt(
	ctx context.Context, w Workspace, id StateVersionID, key blob.Key, fence namespace.Fence,
) error {
	superseded, err := s.states.RecordUploadAttempt(ctx, id, key, fence)
	if err != nil {
		return fmt.Errorf("upload state %s: %w", id, err)
	}

	if superseded != "" && superseded != key {
		if deleteErr := s.blobs.deleteRetried(ctx, w.NamespaceID, w.ID, superseded); deleteErr != nil {
			return fmt.Errorf("upload state %s: %w", id, deleteErr)
		}
	}

	return nil
}

// stripeSpread widens adjacent keys across the lock stripes.
const stripeSpread = 31

// lockKey serializes the callers that share one key: concurrent declares of
// one workspace, and concurrent uploads of one state version. The stripes
// are fixed, so two keys that share a stripe only wait for each other.
func (s *Service) lockKey(key string) func() {
	var sum uint32

	for i := range len(key) {
		sum = sum*stripeSpread + uint32(key[i])
	}

	stripe := &s.stripes[sum%lockStripes]
	stripe.Lock()

	return stripe.Unlock
}

// rejected reports whether a finalize error proves nothing was written.
func rejected(err error) bool {
	for _, sentinel := range []error{
		ErrUploadDenied,
		ErrAlreadyRestored,
		ErrNotLocked,
		ErrLockedByOther,
		ErrLineageMismatch,
		ErrStaleSerial,
		ErrNotFound,
		ErrStateVersionNotFound,
		namespace.ErrNotFound,
		namespace.ErrFenced,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}

	return false
}

// createPending persists a pending state version for w with a fresh upload capability.
func (s *Service) createPending(
	ctx context.Context,
	w Workspace,
	in NewState,
	sum [md5.Size]byte,
	decl pendingDeclare,
) (PendingState, error) {
	restoredFrom, unlocked := decl.restoredFrom, decl.unlocked

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return PendingState{}, err
	}

	tok, err := token.New()
	if err != nil {
		return PendingState{}, err
	}

	now := s.clock()

	// The client's JSON rendering of the outputs is never stored: the outputs of a
	// version come from its body (ADR 49, decision 8).
	svID := StateVersionID(ident.NewID("sv-"))

	sv := StateVersion{
		CreatedAt:       now,
		FinalizedAt:     time.Time{},
		UploadExpiresAt: now.Add(UploadWindow),
		Upload:          tok.Digest(),
		ID:              svID,
		Workspace:       w.ID,
		Holder:          w.Lock.Holder,
		LockID:          w.Lock.ID,
		Lineage:         in.Lineage,
		Status:          StatusPending,
		Content:         "",
		Outputs:         nil,
		MD5:             sum,
		Serial:          in.Serial,
		Size:            0,
		Force:           in.Force,
		UnlockedWrite:   unlocked,
		Sealed:          true,
		RestoredFrom:    restoredFrom,
		SensitiveMarks:  nil,
		FenceBy:         "",
		Origin:          decl.origin,
	}

	if err := s.states.CreateStateVersion(ctx, sv, fence, s.maxPending); err != nil {
		return PendingState{}, fmt.Errorf("create state version for %s: %w", w.Ref, err)
	}

	return PendingState{Upload: tok, Version: sv}, nil
}

// storeInput carries one content write: the workspace scope, the blob key,
// the declared MD5 of the plain bytes, and the content, which seals as it
// streams into the blob store.
type storeInput struct {
	namespace   namespace.Name
	namespaceID namespace.ID
	workspace   ID
	// version is the state version ID that binds a sealed write.
	version string
	key     blob.Key
	want    [md5.Size]byte
	content io.Reader
	// unchecked skips the MD5 comparison: a streamed write with no declared MD5
	// records the MD5 that the bytes produce.
	unchecked bool
}

// storedContent is what storeContent reports: the size and the MD5 of the
// plain bytes.
type storedContent struct {
	size int64
	sum  [md5.Size]byte
}

// store writes content under key, checking size and MD5 as it streams. The
// MD5 and the size stay those of the plain bytes. The content seals as it
// streams into the blob with the data key of the workspace scope, so the
// blob store never holds plain state. On any mismatch the object is deleted.
func (s *Service) store(ctx context.Context, in storeInput) (int64, error) {
	stored, err := s.storeContent(ctx, in)

	return stored.size, err
}

// storeContent is store, and it also reports the MD5 of the plain bytes. With
// unchecked set, it checks only the size.
func (s *Service) storeContent(ctx context.Context, in storeInput) (storedContent, error) {
	digest := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.
	counted := &countingReader{r: io.LimitReader(in.content, s.maxState+1), n: 0}
	checked := io.TeeReader(counted, digest)

	sealedReader, err := s.sealer.SealState(ctx, in.namespace, in.workspace.String(), in.version, checked)
	if err != nil {
		return storedContent{}, err
	}

	checked = sealedReader

	if err := s.blobs.put(ctx, in.namespaceID, in.workspace, in.key, checked); err != nil {
		return storedContent{}, fmt.Errorf("store content: %w", err)
	}

	sum := [md5.Size]byte(digest.Sum(nil))

	var problem error

	switch {
	case counted.n > s.maxState:
		problem = errTooLargeBody("state", counted.n, s.maxState)
	case !in.unchecked && sum != in.want:
		problem = ErrChecksumMismatch
	default:
		return storedContent{size: counted.n, sum: sum}, nil
	}

	return storedContent{}, errors.Join(problem, s.blobs.delete(ctx, in.namespaceID, in.workspace, in.key))
}

// openOutputs opens the sealed outputs of sv in place, so callers read plain
// outputs. A version without outputs keeps none. A failure reports the
// version ID only, never a key, a scope or content.
func (s *Service) openOutputs(ctx context.Context, sv *StateVersion) error {
	if len(sv.Outputs) == 0 {
		return nil
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return err
	}

	plain, err := s.sealer.OpenOutputs(ctx, w.Ref.Namespace, w.ID.String(), string(sv.ID), sv.Outputs)
	if err != nil {
		return sanitizeOpenError(sv.ID, err)
	}

	sv.Outputs = plain

	return nil
}

// sanitizeOpenError reports a decryption failure with the version ID only,
// so the error holds no key, scope or content. Anything else passes through
// unchanged, so operational failures keep their cause.
func sanitizeOpenError(id StateVersionID, err error) error {
	if !errors.Is(err, secret.ErrDecrypt) {
		return err
	}

	return fmt.Errorf("open state %s: %w", id, ErrSealedContent)
}

// closeReader reads sealed content, and at Close closes the opened stream,
// which clears its key, and the underlying blob object. A decryption failure
// while streaming maps to ErrSealedContent with the version ID only, so the
// error holds no key, scope or content; anything else passes through
// unchanged.
type closeReader struct {
	reader io.Reader
	opened io.Closer
	closer io.Closer
	id     StateVersionID
}

// Read returns the next plaintext bytes, mapping a decryption failure to
// ErrSealedContent.
func (r *closeReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && errors.Is(err, secret.ErrDecrypt) {
		return n, fmt.Errorf("open state %s: %w", r.id, ErrSealedContent)
	}

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see io.EOF unchanged.
}

// verifiedReader checks the plain bytes of a state version against the size
// and MD5 of its row as they stream. A mismatch at the end of the stream
// fails the read with ErrStoredContentMismatch, so the caller never accepts
// bytes that differ from the row.
type verifiedReader struct {
	r    io.Reader
	hash hash.Hash
	sv   StateVersion
	n    int64
}

// newVerifiedReader wraps r, the plain bytes of sv.
func newVerifiedReader(r io.Reader, sv StateVersion) *verifiedReader {
	//nolint:gosec // Required by the TFE protocol; integrity only.
	return &verifiedReader{r: r, hash: md5.New(), sv: sv, n: 0}
}

// Read returns the next plain bytes and checks them at the end of the stream.
func (v *verifiedReader) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	v.n += int64(n)
	_, _ = v.hash.Write(p[:n])

	if errors.Is(err, io.EOF) && (v.n != v.sv.Size || [md5.Size]byte(v.hash.Sum(nil)) != v.sv.MD5) {
		return n, fmt.Errorf("open state %s: %w", v.sv.ID, ErrStoredContentMismatch)
	}

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see io.EOF unchanged.
}

// Close closes the underlying blob object.
func (r *closeReader) Close() error {
	return errors.Join(r.opened.Close(), r.closer.Close())
}

// change applies a lock transition to the stored workspace and saves it.
//
// An apply that returns errRepeated changes nothing: change saves nothing and
// returns the workspace as it read it.
func (s *Service) change(
	ctx context.Context,
	id ID,
	apply func(Workspace, time.Time) (Workspace, error),
) (Workspace, error) {
	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	read := w

	w, err = apply(w, s.clock())
	if errors.Is(err, errRepeated) {
		// A repeated request changes nothing: no save, no new revision.
		return read, nil
	}

	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", id, err)
	}

	// The write carries the origin of the request. The store publishes it as
	// the origin of the state version that the write releases.
	w.Origin = originFrom(ctx)

	if err := s.workspaces.SaveWorkspace(ctx, w, fence); err != nil {
		return Workspace{}, fmt.Errorf("save workspace %s: %w", id, err)
	}

	return w, nil
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.writable(ctx, ns)
	if err != nil {
		return namespace.Fence{}, err
	}

	return n.Fence(), nil
}

// writable returns the namespace ns when this installation may write to it.
func (s *Service) writable(ctx context.Context, ns namespace.Name) (namespace.Namespace, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("write to namespace %q: %w", ns, err)
	}

	return n, nil
}

func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// releaseUnfenced releases the lock of the unit without writing a version, and
// appends the audit entry of the release in the same transaction. by is the
// subject of the principal that forced the release.
func (s *Service) releaseUnfenced(ctx context.Context, w Workspace, by string, entry audit.Entry) (Workspace, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	released, err := w.forceUnlock(s.clock(), by)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", w.ID, err)
	}

	released.Origin = originFrom(ctx)

	if err := s.workspaces.ReleaseUnfenced(ctx, released, fence, entry); err != nil {
		return Workspace{}, fmt.Errorf("release %s: %w", w.ID, err)
	}

	return released, nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)

	return n, err //nolint:wrapcheck // Transparent wrapper: callers must see io.EOF unchanged.
}

// storeUploadBody stores the bytes of an upload under key and reads the outputs
// from them (ADR 49, decision 8). The outputs come from the body, so the
// client's JSON rendering of the outputs is never stored. A refused body deletes
// its blob.
func (s *Service) storeUploadBody(
	ctx context.Context, w Workspace, sv StateVersion, key blob.Key, content io.Reader,
) (uploadedBody, error) {
	grow := func(n int) error {
		return charge(ctx, int64(n))
	}

	reader, stream := newMetaStream(content, grow)

	size, storeErr := s.store(ctx, storeInput{
		namespace:   w.Ref.Namespace,
		namespaceID: w.NamespaceID,
		workspace:   w.ID,
		version:     string(sv.ID),
		key:         key,
		want:        sv.MD5,
		content:     reader,
		unchecked:   false,
	})

	if storeErr != nil {
		// Closing the stream lets the reader beside the store finish. The
		// store error is the one the caller sees.
		abandonStream(stream, storeErr)

		return uploadedBody{}, fmt.Errorf("upload state %s: %w", sv.ID, storeErr)
	}

	// A body the reader refuses is refused here, not stored: a state that OpenTofu
	// cannot read must not become current (ADR 49, decision 8).
	meta, metaErr := stream.finish(nil)
	if metaErr != nil {
		return uploadedBody{}, errors.Join(metaErr, s.deleteOnCleanup(ctx, w, key))
	}

	outputs, err := s.streamedOutputs(ctx, w, string(sv.ID), meta)
	if err != nil {
		return uploadedBody{}, errors.Join(err, s.deleteOnCleanup(ctx, w, key))
	}

	return uploadedBody{size: size, outputs: outputs}, nil
}

// abandonStream ends a meta stream whose store failed. The stream's own result
// adds nothing: the caller returns the store error.
func abandonStream(stream *metaStream, cause error) {
	_, _ = stream.finish(cause) //nolint:errcheck // The caller returns the store error.
}

// deleteOnCleanup deletes the blob under key on the cleanup context of a refused
// upload, so a client that disconnects does not leave the blob behind.
func (s *Service) deleteOnCleanup(ctx context.Context, w Workspace, key blob.Key) error {
	cleanCtx, cancel := cleanupContext(ctx)
	defer cancel()

	return s.blobs.delete(cleanCtx, w.NamespaceID, w.ID, key)
}

// purge removes the deleted workspace id of ns with the retention check of
// dueBy, which zero turns off.
func (s *Service) purge(
	ctx context.Context, ns namespace.Name, id ID, dueBy time.Time, e audit.Entry,
) (namespace.ID, error) {
	n, err := s.writable(ctx, ns)
	if err != nil {
		return "", err
	}

	e.At = s.clock()

	if err := s.workspaces.PurgeWorkspace(ctx, id, n.Fence(), dueBy, e); err != nil {
		return "", fmt.Errorf("purge workspace %s: %w", id, err)
	}

	return n.ID, nil
}

// checkManualLock refuses a lock request on the workspace id while a manual
// lock covers it. The error matches ErrLocked and manuallock.ErrLocked, and
// names the holder and the reason of the manual lock.
func (s *Service) checkManualLock(ctx context.Context, id ID, jobID string) error {
	if s.manual == nil {
		return nil
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	return s.refuseManualLock(ctx, w, jobID)
}

// refuseManualLock is checkManualLock for a workspace that the caller has read.
func (s *Service) refuseManualLock(ctx context.Context, w Workspace, jobID string) error {
	if s.manual == nil {
		return nil
	}

	unit := lockUnitOf(w)

	l, err := s.manual.CoveringFor(ctx, unit, jobID)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of %s: %w", w.Ref, err)
	}

	return fmt.Errorf(
		"%w: %w",
		ErrLocked,
		&manuallock.LockedError{Lock: l, Unit: unit.Ref, Noun: lockNounWorkspace, Name: w.Ref.String()},
	)
}

// refuseRestoreUnderNamespaceLock returns an error that matches ErrLocked and
// manuallock.ErrLocked while a manual lock covers the namespace ns, and nil when
// there is no manual lock service or the namespace has no lock. A lock that
// cannot be read refuses the restore: the check fails closed.
func (s *Service) refuseRestoreUnderNamespaceLock(ctx context.Context, ns namespace.Name, id ID) error {
	return s.refuseUnderNamespaceLock(ctx, ns, id.String())
}

// refuseNewUnitManualLock returns an error that matches ErrLocked and
// manuallock.ErrLocked while a manual lock covers the workspace ref, which does
// not exist yet, for the request of the job jobID. Only the lock of the
// namespace can cover it. A job whose phase started before that lock passes,
// as in refuseManualLock. A lock that cannot be read refuses the request.
func (s *Service) refuseNewUnitManualLock(
	ctx context.Context, ref Ref, namespaceID namespace.ID, jobID string,
) error {
	if s.manual == nil {
		return nil
	}

	unit := newLockUnitOf(ref, namespaceID)

	l, err := s.manual.NamespaceLockFor(ctx, unit, jobID)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of namespace %s: %w", ref.Namespace, err)
	}

	return fmt.Errorf(
		"%w: %w",
		ErrLocked,
		&manuallock.LockedError{Lock: l, Unit: unit.Ref, Noun: lockNounWorkspace, Name: ref.String()},
	)
}

// newUnitRef returns the inventory reference of the workspace ref.
func newUnitRef(ref Ref) collection.UnitRef {
	return collection.UnitRef{Namespace: ref.Namespace, Tool: InventoryTool, Name: ref.Name.String()}
}

// lockUnitOf returns the lock unit of the workspace w.
func lockUnitOf(w Workspace) manuallock.Unit {
	return manuallock.Unit{Ref: newUnitRef(w.Ref), NamespaceID: w.NamespaceID, UnitID: w.ID.String()}
}

// newLockUnitOf returns the lock unit of ref, which does not exist yet, so it
// has no unit ID.
func newLockUnitOf(ref Ref, namespaceID namespace.ID) manuallock.Unit {
	return manuallock.Unit{Ref: newUnitRef(ref), NamespaceID: namespaceID, UnitID: ""}
}

// refuseUnderNamespaceLock is refuseRestoreUnderNamespaceLock for the unit
// called name, which can be a unit that does not exist yet.
func (s *Service) refuseUnderNamespaceLock(ctx context.Context, ns namespace.Name, name string) error {
	if s.manual == nil {
		return nil
	}

	l, err := s.manual.NamespaceLock(ctx, ns)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of namespace %s: %w", ns, err)
	}

	return fmt.Errorf("%w: %w", ErrLocked, &manuallock.LockedError{
		Lock: l, Unit: collection.UnitRef{Namespace: ns, Tool: InventoryTool, Name: name},
		Noun: lockNounWorkspace, Name: ns.String() + "/" + name,
	})
}

// repeatedLock returns the workspace id and true when a lock of holder under
// lockID repeats the lock that it holds, after the fence check of its
// namespace. It returns false when the request is not a repeat.
func (s *Service) repeatedLock(ctx context.Context, id ID, holder, lockID string) (Workspace, bool, error) {
	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, false, err
	}

	if !w.RepeatsLock(holder, lockID) {
		return w, false, nil
	}

	// A repeat writes nothing. It still needs a namespace that this
	// installation may write to, as the first request did, so that it fails
	// closed after a failover.
	if _, err := s.fence(ctx, w.Ref.Namespace); err != nil {
		return Workspace{}, false, err
	}

	return w, true, nil
}

// repeatedForceUnlock answers a force-unlock of the free workspace w. A
// request that repeats the last forced release by the same principal by
// succeeds without a change and without an audit entry: OpenTofu sends a
// force-unlock again when the answer of a committed one is lost. The request
// must name no lock, or the lock of the record. Each other request fails with
// ErrNotLocked, as before.
func (s *Service) repeatedForceUnlock(
	ctx context.Context, w Workspace, expect Lock, by string,
) (Workspace, bool, error) {
	repeat := ReleaseRepeat{LockID: expect.ID, Holder: expect.Holder, Caller: by, Kind: ReleaseForced}
	if !w.RepeatsRelease(s.clock(), repeat) {
		return Workspace{}, false, ErrNotLocked
	}

	// A repeat writes nothing. It still needs a namespace that this
	// installation may write to, as the first request did.
	if _, err := s.fence(ctx, w.Ref.Namespace); err != nil {
		return Workspace{}, false, err
	}

	return w, false, nil
}
