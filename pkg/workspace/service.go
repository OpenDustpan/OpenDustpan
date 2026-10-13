package workspace

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	// DefaultMaxStateBytes bounds one state version unless configured otherwise.
	DefaultMaxStateBytes = 512 << 20
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
type Store interface {
	// CreateWorkspace persists a new w. It returns ErrAlreadyExists,
	// namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	CreateWorkspace(ctx context.Context, w Workspace, fence namespace.Fence) error
	// GetWorkspace returns the workspace called ref, or ErrNotFound.
	GetWorkspace(ctx context.Context, ref Ref) (Workspace, error)
	// GetWorkspaceByID returns the workspace with id, or ErrNotFound.
	GetWorkspaceByID(ctx context.Context, id ID) (Workspace, error)
	// ListWorkspaces returns the namespace's workspaces, ordered by name; an
	// empty list if it has none or does not exist.
	ListWorkspaces(ctx context.Context, ns namespace.Name) ([]Workspace, error)
	// SaveWorkspace persists w if the store holds w.Revision-1, else returns
	// ErrConcurrentModification. It returns ErrNotFound if the workspace does
	// not exist, namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale.
	SaveWorkspace(ctx context.Context, w Workspace, fence namespace.Fence) error
	// ReleaseUnfenced saves a released unit like SaveWorkspace, and refuses with
	// ErrOverrideRefused unless the forced-write switches of the namespace and of
	// the unit are on, in the same transaction. It is the unfenced release of a
	// client-encrypted state under the forced-write override (ADR 44, decision 4).
	ReleaseUnfenced(ctx context.Context, w Workspace, fence namespace.Fence) error
	// DeleteWorkspace removes the workspace called ref with its state
	// versions and its wrapped data key in one fenced transaction. It
	// removes the inventory entry in the same transaction. It returns
	// ErrNotFound if the workspace does not exist, namespace.ErrNotFound
	// if the namespace does not exist, or namespace.ErrFenced if fence is
	// stale. Old sealed blobs stay for the sweeper: the caller deletes the
	// content of the deleted versions.
	DeleteWorkspace(ctx context.Context, ref Ref, fence namespace.Fence) error
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
	// stamps the workspace's UpdatedAt with sv.FinalizedAt, and makes sv the
	// workspace's current state, checking in the same transaction that:
	//   - the stored version is still pending, else ErrUploadDenied;
	//   - the stored version still names sv.Content as its latest attempt,
	//     else ErrUploadDenied: a superseded attempt never finalizes over a
	//     newer one, so the current state never points at a deleted blob;
	//   - the workspace is held by lock sv.LockID, else ErrNotLocked or
	//     ErrLockedByOther;
	//   - unless sv.Force, the current state (if any) has sv.Lineage, else
	//     ErrLineageMismatch, and a lower serial, else ErrStaleSerial.
	// Those errors, ErrNotFound, ErrStateVersionNotFound,
	// namespace.ErrNotFound and namespace.ErrFenced mean nothing was
	// written. Any other error leaves the outcome unknown: the version may
	// be current. Callers record the attempt key with RecordUploadAttempt
	// before finalizing, so the row names sv.Content when the write runs.
	FinalizeStateVersion(ctx context.Context, sv StateVersion, fence namespace.Fence) error
	// FenceAndUnlock releases the lock of w and makes sv the workspace's current
	// state, in one fenced transaction. sv is a finalized fence version of w:
	// it names the content, the new lineage, its serial and FenceBy. The stored
	// workspace must be locked and hold revision w.Revision-1, else
	// ErrNotLocked or ErrConcurrentModification. The current version must be
	// expect (the empty ID when the workspace has no state), else
	// ErrConcurrentModification, and sv.Serial must be one more than its serial,
	// else ErrStaleSerial. It returns ErrNotFound if the workspace does not
	// exist, namespace.ErrNotFound if the namespace does not exist, or
	// namespace.ErrFenced if fence is stale. An error writes nothing.
	FenceAndUnlock(
		ctx context.Context, w Workspace, sv StateVersion, expect StateVersionID, fence namespace.Fence,
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
	// workspace names, pending and finalized, without the empty ones. A
	// workspace without versions, or one that does not exist, yields an
	// empty list. It returns no sentinel errors. A sweeper compares this
	// set against the stored objects to find blobs no version references.
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
	// ListUnsealedStateVersions returns the finalized state versions that
	// still hold plain content, ordered by version ID, so the seal job
	// seals them. A version without content stays out: a pending row names
	// no content yet. It returns no sentinel errors.
	ListUnsealedStateVersions(ctx context.Context) ([]StateVersion, error)
	// SealStateVersion moves the finalized unsealed version id to the
	// sealed blob under content with the sealed outputs document, and
	// marks it sealed, in one fenced transaction. It is idempotent: a
	// version that is already sealed succeeds and changes nothing, so a
	// retry after a crash never fails. It returns ErrStateVersionNotFound
	// when id does not exist, namespace.ErrNotFound when the namespace
	// does not exist, or namespace.ErrFenced when fence is stale.
	SealStateVersion(
		ctx context.Context,
		id StateVersionID,
		content blob.Key,
		outputs []byte,
		fence namespace.Fence,
	) error
}

// Config holds the dependencies of a Service.
type Config struct {
	Workspaces Store
	States     StateStore
	// Blobs holds state content under "tofu/<workspace id>/<state version id>/<attempt>".
	Blobs      blob.Store
	Namespaces *namespace.Service
	// Sealer seals state blobs and outputs documents with the data key of
	// the workspace scope. Nil keeps state as before: plain blobs and
	// outputs for rows without the sealed flag.
	Sealer Sealer
	// Logger receives sweep failures; nil discards them.
	Logger *slog.Logger
	Now    func() time.Time
	// MaxStateBytes bounds one state version; zero means DefaultMaxStateBytes.
	MaxStateBytes int64
	// MaxPendingVersions bounds the pending state versions of one
	// workspace; zero means DefaultMaxPendingVersions.
	MaxPendingVersions int
}

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
	blobs      blob.Store
	namespaces *namespace.Service
	sealer     Sealer
	logger     *slog.Logger
	now        func() time.Time
	maxState   int64
	maxPending int
	stripes    [lockStripes]sync.Mutex
}

// NewService returns a Service. Every port and Namespaces are required.
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
	}

	s := &Service{
		workspaces: cfg.Workspaces,
		states:     cfg.States,
		blobs:      cfg.Blobs,
		namespaces: cfg.Namespaces,
		sealer:     cfg.Sealer,
		logger:     cfg.Logger,
		now:        cfg.Now,
		maxState:   cfg.MaxStateBytes,
		maxPending: cfg.MaxPendingVersions,
		stripes:    [lockStripes]sync.Mutex{},
	}

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

	return s, nil
}

// Create creates an unlocked workspace without state.
func (s *Service) Create(ctx context.Context, ref Ref) (Workspace, error) {
	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	now := s.clock()
	w := Workspace{
		CreatedAt: now,
		UpdatedAt: now,
		Lock:      Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        ID(ident.NewID("ws-")),
		Ref:       ref,
		Revision:  1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.workspaces.CreateWorkspace(ctx, w, fence); err != nil {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", ref, err)
	}

	return w, nil
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

// Lock locks the workspace for holder, the subject of the calling principal.
func (s *Service) Lock(ctx context.Context, id ID, holder, reason string) (Workspace, error) {
	if holder == "" {
		return Workspace{}, fmt.Errorf("%w: lock without a holder", ErrNotLocked)
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
	if holder == "" {
		return Workspace{}, fmt.Errorf("%w: lock without a holder", ErrNotLocked)
	}

	if !validLockID(lockID) {
		return Workspace{}, fmt.Errorf("%w: %d characters", ErrInvalidLockID, len(lockID))
	}

	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		return w.lockAs(now, holder, reason, lockID)
	})
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
func (s *Service) UnlockLock(ctx context.Context, id ID, lockID, holder string) (Workspace, error) {
	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		if lockErr := checkLockID(w, lockID); lockErr != nil {
			return Workspace{}, lockErr
		}

		return w.unlock(now, holder)
	})
}

// checkLockID refuses a lock ID other than the current lock. An empty lockID
// accepts any lock.
func checkLockID(w Workspace, lockID string) error {
	if lockID != "" && w.Lock.ID != lockID {
		return fmt.Errorf("%w: unit %s", ErrLockMismatch, w.Ref)
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
// an unfenced release and writes its audit entry; a failure stops the release
// (ADR 41 decision 5). A nil record writes nothing.
func (s *Service) ForceUnlock(
	ctx context.Context, id ID, by string, allow namespace.Overrides, record func() error,
) (Workspace, bool, error) {
	return s.ForceUnlockLock(ctx, id, "", by, allow, record)
}

// ForceUnlockLock is ForceUnlock for the lock lockID. An empty lockID releases
// whatever lock the unit holds. A different lock is refused with ErrLockMismatch,
// before the fence and in the same revision the release saves, so a lock taken
// after the caller read the unit is not released.
//
//nolint:revive // The lock ID is one argument more than the port takes, and the call names each part.
func (s *Service) ForceUnlockLock(
	ctx context.Context, id ID, lockID, by string, allow namespace.Overrides, record func() error,
) (Workspace, bool, error) {
	w, err := s.GetByID(ctx, id)
	if err != nil {
		return Workspace{}, false, err
	}

	if !w.Lock.Locked() {
		return Workspace{}, false, ErrNotLocked
	}

	if lockErr := checkLockID(w, lockID); lockErr != nil {
		return Workspace{}, false, lockErr
	}

	cur, err := s.states.CurrentStateVersion(ctx, id)

	switch {
	case errors.Is(err, ErrNoState):
		// A unit without state is a plain release: no override is used.
		released, changeErr := s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
			if lockErr := checkLockID(w, lockID); lockErr != nil {
				return Workspace{}, lockErr
			}

			return w.forceUnlock(now)
		})

		return released, false, changeErr
	case err != nil:
		return Workspace{}, false, fmt.Errorf("current state of %s: %w", w.Ref, err)
	}

	released, err := s.releaseWithFence(ctx, w, cur, by)
	if errors.Is(err, ErrClientEncrypted) && allow.Forced {
		if record != nil {
			if recordErr := record(); recordErr != nil {
				return Workspace{}, false, fmt.Errorf("record override of %s: %w", w.Ref, recordErr)
			}
		}

		unfenced, changeErr := s.releaseUnfenced(ctx, w)

		return unfenced, true, changeErr
	}

	return released, false, err
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
// content follows through Upload with the returned token.
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
// declare, so no override writes without its entry (ADR 41, decision 5). A nil
// auth.Record writes nothing.
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

	if recordErr := recordOverrides(w, used, auth.Record); recordErr != nil {
		return PendingState{}, recordErr
	}

	// Declares of one workspace share a stripe from here to the create,
	// which trims contention on the cap check below. The store enforces
	// the cap where concurrent declares serialize, so the check below is
	// only an early refusal with a count.
	unlock, err := s.admitDeclare(ctx, w)
	if err != nil {
		return PendingState{}, err
	}

	defer unlock()

	return s.createPending(ctx, w, in, sum, pendingDeclare{restoredFrom: "", unlocked: unlocked})
}

// Authority is the caller's side of a declare: the overrides the caller may
// use, and the callback that writes the audit entry of each override that the
// declare uses.
type Authority struct {
	// Allow holds the overrides the caller may use.
	Allow namespace.Overrides
	// Record writes the audit entry of one override. A nil Record writes nothing.
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
		return nil
	}

	for _, override := range used {
		if err := record(override); err != nil {
			return fmt.Errorf("record override of %s: %w", w.Ref, err)
		}
	}

	return nil
}

// pendingDeclare holds the optional parts of a declare: the version it restores
// and whether it is an unlocked write.
type pendingDeclare struct {
	restoredFrom StateVersionID
	unlocked     bool
}

// forcedRefusal names the action and the switches that allow a forced write.
func forcedRefusal() error {
	return fmt.Errorf(
		"%w: a forced write needs the unit:write-forced action and both forced-write switches on",
		ErrOverrideRefused,
	)
}

// unlockedRefusal keeps the status of a refused declare on an unlocked unit
// and adds the way out: the switch and the action of an unlocked write.
func unlockedRefusal(lockErr error) error {
	if !errors.Is(lockErr, ErrNotLocked) {
		return lockErr
	}

	return fmt.Errorf(
		"%w: a write without the lock is refused; turn on the unlocked-write switch of the namespace and of the unit, "+
			"and grant unit:write-unlocked",
		lockErr,
	)
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

	if sv.retriedBy(tok) {
		return s.confirmRetry(sv, content)
	}

	if acceptErr := sv.accepts(tok, s.clock()); acceptErr != nil {
		return StateVersion{}, acceptErr
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return StateVersion{}, err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return StateVersion{}, err
	}

	key, err := attemptKey(sv)
	if err != nil {
		return StateVersion{}, err
	}

	// Note the attempt before writing: when the finalize outcome stays
	// unknown, the row still names the content, so a later sweep deletes it.
	if noteErr := s.noteAttempt(ctx, id, key, fence); noteErr != nil {
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
			return StateVersion{}, errors.Join(err, s.blobs.Delete(ctx, key))
		}

		// The outcome is unknown; the version may point at this blob. Keep it.
		return StateVersion{}, err
	}

	return sv, nil
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

	if !sv.Upload.Matches(tok) {
		return fmt.Errorf("%w: wrong capability for state version %s", ErrUploadDenied, id)
	}

	if !s.clock().Before(sv.UploadExpiresAt) {
		return fmt.Errorf("%w: capability for state version %s expired", ErrUploadDenied, id)
	}

	raw, err := io.ReadAll(io.LimitReader(content, s.maxState+1))
	if err != nil {
		return fmt.Errorf("read JSON rendering of %s: %w", id, err)
	}

	if int64(len(raw)) > s.maxState {
		return fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxState)
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
// A sealed version opens its sealed blob, so the caller reads the same bytes
// the CLI sent. A decryption failure reports the version ID only, never a
// key, a scope or content.
func (s *Service) Open(ctx context.Context, sv StateVersion) (io.ReadCloser, error) {
	if sv.Status != StatusFinalized {
		return nil, fmt.Errorf("%w: state version %s is %s", ErrStateVersionNotFound, sv.ID, sv.Status)
	}

	if sv.Sealed && s.sealer == nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, ErrSealerNotConfigured)
	}

	r, err := s.blobs.Get(ctx, sv.Content)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, err)
	}

	if !sv.Sealed {
		return r, nil
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}

	opened, err := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), r)
	if err != nil {
		return nil, errors.Join(sanitizeOpenError(sv.ID, err), r.Close())
	}

	return &closeReader{reader: opened, closer: r, id: sv.ID}, nil
}

// OpenStateBlob returns the plain content of the sealed blob under key, for
// the backup copy check. The key names one state version attempt as
// "tofu/<workspace id>/<state version id>/<attempt>". An unsealed version
// reads as before. A failure reports the version ID only, never a key, a
// scope or content. The caller closes the result.
func (s *Service) OpenStateBlob(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	parts := blobKeyParts(key)
	if len(parts) != 4 || parts[0] != "tofu" {
		return nil, fmt.Errorf("open state blob %q: %w", key, ErrStateVersionNotFound)
	}

	sv, err := s.states.GetStateVersion(ctx, StateVersionID(parts[2]))
	if err != nil {
		return nil, fmt.Errorf("open state blob %q: %w", key, err)
	}

	// The key must name the workspace and the version it resolves to:
	// a key whose workspace segment names another workspace opens
	// nothing, so one workspace never opens another's sealed bytes.
	if parts[1] != sv.Workspace.String() || parts[2] != sv.ID.String() {
		return nil, fmt.Errorf("open state blob %q: %w", key, ErrStateVersionNotFound)
	}

	if sv.Sealed && s.sealer == nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, ErrSealerNotConfigured)
	}

	r, err := s.blobs.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, err)
	}

	if !sv.Sealed {
		return r, nil
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}

	opened, err := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), r)
	if err != nil {
		return nil, errors.Join(sanitizeOpenError(sv.ID, err), r.Close())
	}

	return &closeReader{reader: opened, closer: r, id: sv.ID}, nil
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

	sv, err := s.copyState(ctx, w, src)
	if !release {
		return sv, err
	}

	_, unlockErr := s.Unlock(ctx, id, holder)

	return sv, errors.Join(err, unlockErr)
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
// outputs list still works.
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
		return nil, StateVersion{}, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxState)
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
func (s *Service) copyState(ctx context.Context, w Workspace, src StateVersion) (StateVersion, error) {
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
	}, src.MD5, pendingDeclare{restoredFrom: src.ID, unlocked: false})
	if err != nil {
		return StateVersion{}, err
	}

	// createPending returns plain outputs for rendering; the stored row
	// holds the sealed document. Finalize the stored row, so the commit
	// keeps the sealed bytes.
	stored, err := s.states.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		return StateVersion{}, fmt.Errorf("restore %s from %s: %w", w.ID, src.ID, err)
	}

	key, err := attemptKey(stored)
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
	if noteErr := s.noteAttempt(ctx, pending.Version.ID, key, fence); noteErr != nil {
		return StateVersion{}, noteErr
	}

	size, err := s.copyBlob(ctx, w, key, src, stored.Sealed)
	if err != nil {
		return StateVersion{}, err
	}

	sv := stored
	sv.Status = StatusFinalized
	sv.FinalizedAt = s.clock()
	sv.Size = size
	sv.Content = key

	if err := s.states.FinalizeStateVersion(ctx, sv, fence); err != nil {
		err = fmt.Errorf("finalize restored state %s: %w", sv.ID, err)
		if rejected(err) {
			// Nothing was written, so this attempt's blob is an orphan.
			return StateVersion{}, errors.Join(err, s.blobs.Delete(ctx, key))
		}

		// The outcome is unknown; the version may point at this blob. Keep it.
		return StateVersion{}, err
	}

	// The caller renders the new version: carry the plain outputs, while the
	// committed row keeps the sealed document.
	sv.Outputs = outputs

	return sv, nil
}

// openRestoreOutputs opens the stored outputs of src, so the new version
// seals the plain document again, never the ciphertext. Rows without the
// sealed flag pass through unchanged. Sealed outputs on a service without a
// sealer fail with ErrSealerNotConfigured: ciphertext never reads as
// outputs. A failure reports the version ID only, never a key, a scope or
// content.
func (s *Service) openRestoreOutputs(
	ctx context.Context, w Workspace, src StateVersion,
) ([]byte, error) {
	if !src.Sealed || len(src.Outputs) == 0 {
		return src.Outputs, nil
	}

	if s.sealer == nil {
		return nil, fmt.Errorf("restore %s: %w", src.ID, ErrSealerNotConfigured)
	}

	plain, err := s.sealer.OpenOutputs(ctx, w.Ref.Namespace, w.ID.String(), src.Outputs)
	if err != nil {
		return nil, sanitizeOpenError(src.ID, fmt.Errorf("restore %s: %w", src.ID, err))
	}

	return plain, nil
}

// copyBlob copies the content of src under key, checking size and MD5 as it
// streams. A sealed source opens before the check, so the check runs on the
// plain bytes; the copy seals again when sealed holds, so a restore of an
// old unsealed version still lands sealed. On any mismatch the object is
// deleted.
func (s *Service) copyBlob(
	ctx context.Context, w Workspace, key blob.Key, src StateVersion, sealed bool,
) (int64, error) {
	if src.Sealed && s.sealer == nil {
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, ErrSealerNotConfigured)
	}

	r, err := s.blobs.Get(ctx, src.Content)
	if err != nil {
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, err)
	}

	if src.Sealed {
		opened, oerr := s.sealer.OpenState(ctx, w.Ref.Namespace, w.ID.String(), r)
		if oerr != nil {
			return 0, errors.Join(
				sanitizeOpenError(src.ID, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, oerr)),
				r.Close(),
			)
		}

		r = &closeReader{reader: opened, closer: r, id: src.ID}
	}

	size, storeErr := s.store(ctx, storeInput{
		namespace: w.Ref.Namespace,
		workspace: w.ID,
		seal:      sealed,
		key:       key,
		want:      src.MD5,
		content:   r,
		unchecked: false,
	})
	closeErr := r.Close()

	switch {
	case storeErr != nil:
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, errors.Join(storeErr, closeErr))
	case closeErr != nil:
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, errors.Join(
			closeErr,
			s.blobs.Delete(ctx, key),
		))
	default:
		return size, nil
	}
}

// confirmRetry answers a retried upload of a finalized version: the same
// content succeeds without being stored again, other content is denied.
func (s *Service) confirmRetry(sv StateVersion, content io.Reader) (StateVersion, error) {
	hash := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.

	n, err := io.Copy(hash, io.LimitReader(content, s.maxState+1))
	if err != nil {
		return StateVersion{}, fmt.Errorf("read retried upload of %s: %w", sv.ID, err)
	}

	if n != sv.Size || [md5.Size]byte(hash.Sum(nil)) != sv.MD5 {
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
// that no version references, and returns the swept version IDs. A version
// expires with its upload capability; the sweep keeps it for
// SweepGracePeriod past expiry, so an upload that started before expiry
// still finishes. A version that never recorded an attempt sweeps its row
// with no blob to delete. A blob whose row delete already committed, or
// whose superseded delete never ran, stays unreferenced: the second pass
// collects it once it is older than the grace period, so a cleanup failure
// or a crash between the row delete and the blob delete never leaves an
// orphan for ever. A cleanup failure fails the sweep, so the caller
// decides: BeginState treats the sweep as best effort and declares anyway,
// leaving the rows for the next sweep.
func (s *Service) sweepExpired(ctx context.Context, w Workspace) ([]StateVersionID, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return nil, err
	}

	expired, err := s.states.DeleteExpiredPendingStateVersions(ctx, w.ID, s.clock().Add(-SweepGracePeriod), fence)
	if err != nil {
		return nil, fmt.Errorf("sweep expired state of %s: %w", w.Ref, err)
	}

	ids := make([]StateVersionID, 0, len(expired))

	var cleanupErr error

	for i := range expired {
		ids = append(ids, expired[i].ID)

		if expired[i].Content == "" {
			continue
		}

		if blobErr := s.deleteBlobRetried(ctx, expired[i].Content); blobErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete content of %s: %w", expired[i].ID, blobErr))
		}
	}

	// A blob delete above may have failed, or the process may have crashed
	// between an earlier row delete and its blob delete: the unreferenced
	// pass retries those blobs once they are past the grace period.
	if unrefErr := s.sweepUnreferenced(ctx, w); unrefErr != nil {
		cleanupErr = errors.Join(cleanupErr, unrefErr)
	}

	if cleanupErr != nil {
		return nil, fmt.Errorf("sweep expired state of %s: %w", w.Ref, cleanupErr)
	}

	return ids, nil
}

// sweepUnreferenced deletes the blobs under the workspace prefix that no
// version names and that are older than the grace period: superseded
// attempts whose delete never ran, and swept attempts whose row delete
// committed before the blob delete. Blobs younger than the grace period
// stay, so an upload that still streams is never collected: every attempt
// blob is fresh, and the row names it before the bytes land. Blob
// modification times share the service clock wherever the store runs. The
// sweep filters by age before reading the referenced keys, so a declare
// with only fresh blobs pays for one listing and no history read.
func (s *Service) sweepUnreferenced(ctx context.Context, w Workspace) error {
	listed, err := s.blobs.List(ctx, "tofu/"+w.ID.String()+"/")
	if err != nil {
		return fmt.Errorf("list state content of %s: %w", w.Ref, err)
	}

	if len(listed) == 0 {
		return nil
	}

	now := s.clock()

	candidates := make([]blob.Object, 0, len(listed))

	for _, obj := range listed {
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

		if blobErr := s.deleteBlobRetried(ctx, obj.Key); blobErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete unreferenced content %q: %w", obj.Key, blobErr))
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
func (s *Service) noteAttempt(ctx context.Context, id StateVersionID, key blob.Key, fence namespace.Fence) error {
	superseded, err := s.states.RecordUploadAttempt(ctx, id, key, fence)
	if err != nil {
		return fmt.Errorf("upload state %s: %w", id, err)
	}

	if superseded != "" && superseded != key {
		if deleteErr := s.deleteBlobRetried(ctx, superseded); deleteErr != nil {
			return fmt.Errorf("upload state %s: %w", id, deleteErr)
		}
	}

	return nil
}

// sweepBlobRetries bounds the blob deletes of one cleanup: a transient
// object store failure retries instead of orphaning the blob.
const sweepBlobRetries = 3

// deleteBlobRetried removes the object under key, retrying a transient
// failure. Deleting a missing key is not an error.
func (s *Service) deleteBlobRetried(ctx context.Context, key blob.Key) error {
	var err error

	for range sweepBlobRetries {
		if err = s.blobs.Delete(ctx, key); err == nil {
			return nil
		}
	}

	return err
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
	var outputs []byte

	// New rows land sealed when the service seals: the outputs document is
	// sealed with the data key of the workspace scope. The stored row keeps
	// the sealed document; the returned version carries the plain outputs,
	// so the caller renders the create response without opening sealed
	// bytes.
	sealed := s.sealer != nil
	stored := outputs

	if sealed && len(outputs) > 0 {
		sealedOutputs, err := s.sealer.SealOutputs(ctx, w.Ref.Namespace, w.ID.String(), outputs)
		if err != nil {
			return PendingState{}, fmt.Errorf("seal outputs of %s: %w", w.Ref, err)
		}

		stored = sealedOutputs
	}

	sv := StateVersion{
		CreatedAt:       now,
		FinalizedAt:     time.Time{},
		UploadExpiresAt: now.Add(UploadWindow),
		Upload:          tok.Digest(),
		ID:              StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          w.Lock.Holder,
		LockID:          w.Lock.ID,
		Lineage:         in.Lineage,
		Status:          StatusPending,
		Content:         "",
		Outputs:         stored,
		MD5:             sum,
		Serial:          in.Serial,
		Size:            0,
		Force:           in.Force,
		UnlockedWrite:   unlocked,
		Sealed:          sealed,
		RestoredFrom:    restoredFrom,
		SensitiveMarks:  nil,
		FenceBy:         "",
	}

	if err := s.states.CreateStateVersion(ctx, sv, fence, s.maxPending); err != nil {
		return PendingState{}, fmt.Errorf("create state version for %s: %w", w.Ref, err)
	}

	sv.Outputs = outputs

	return PendingState{Upload: tok, Version: sv}, nil
}

// storeInput carries one content write: the workspace scope, the blob key,
// the declared MD5 of the plain bytes, the content, and whether the content
// seals as it streams into the blob store.
type storeInput struct {
	namespace namespace.Name
	workspace ID
	seal      bool
	key       blob.Key
	want      [md5.Size]byte
	content   io.Reader
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
// MD5 and the size stay those of the plain bytes. When seal holds, the
// content seals as it streams into the blob with the data key of the
// workspace scope, so the blob store never holds plain state. A sealed write
// without a sealer fails closed instead of storing plain bytes under a
// sealed flag. On any mismatch the object is deleted.
func (s *Service) store(ctx context.Context, in storeInput) (int64, error) {
	stored, err := s.storeContent(ctx, in)

	return stored.size, err
}

// storeContent is store, and it also reports the MD5 of the plain bytes. With
// unchecked set, it checks only the size.
func (s *Service) storeContent(ctx context.Context, in storeInput) (storedContent, error) {
	if in.seal && s.sealer == nil {
		return storedContent{}, fmt.Errorf("store sealed state of %s: %w", in.workspace, ErrSealerNotConfigured)
	}

	hash := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.
	counted := &countingReader{r: io.LimitReader(in.content, s.maxState+1), n: 0}
	checked := io.TeeReader(counted, hash)

	if in.seal {
		sealedReader, err := s.sealer.SealState(ctx, in.namespace, in.workspace.String(), checked)
		if err != nil {
			return storedContent{}, err
		}

		checked = sealedReader
	}

	if err := s.blobs.Put(ctx, in.key, checked); err != nil {
		return storedContent{}, fmt.Errorf("store content: %w", err)
	}

	sum := [md5.Size]byte(hash.Sum(nil))

	var problem error

	switch {
	case counted.n > s.maxState:
		problem = fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxState)
	case !in.unchecked && sum != in.want:
		problem = ErrChecksumMismatch
	default:
		return storedContent{size: counted.n, sum: sum}, nil
	}

	return storedContent{}, errors.Join(problem, s.blobs.Delete(ctx, in.key))
}

// openOutputs opens the sealed outputs of sv in place, so callers read plain
// outputs. Rows without the sealed flag read as before. Sealed outputs on a
// service without a sealer fail with ErrSealerNotConfigured: ciphertext
// never reads as outputs. A failure reports the version ID only, never a
// key, a scope or content.
func (s *Service) openOutputs(ctx context.Context, sv *StateVersion) error {
	if !sv.Sealed || len(sv.Outputs) == 0 {
		return nil
	}

	if s.sealer == nil {
		return fmt.Errorf("open outputs of %s: %w", sv.ID, ErrSealerNotConfigured)
	}

	w, err := s.GetByID(ctx, sv.Workspace)
	if err != nil {
		return err
	}

	plain, err := s.sealer.OpenOutputs(ctx, w.Ref.Namespace, w.ID.String(), sv.Outputs)
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

// closeReader reads sealed content and closes the underlying blob object. A
// decryption failure while streaming maps to ErrSealedContent with the
// version ID only, so the error holds no key, scope or content; anything
// else passes through unchanged.
type closeReader struct {
	reader io.Reader
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

// Close closes the underlying blob object.
func (r *closeReader) Close() error {
	return r.closer.Close() //nolint:wrapcheck // Transparent wrapper: callers must see the original error.
}

// change applies a lock transition to the stored workspace and saves it.
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

	w, err = apply(w, s.clock())
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", id, err)
	}

	if err := s.workspaces.SaveWorkspace(ctx, w, fence); err != nil {
		return Workspace{}, fmt.Errorf("save workspace %s: %w", id, err)
	}

	return w, nil
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("write to namespace %q: %w", ns, err)
	}

	return n.Fence(), nil
}

func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// releaseUnfenced releases the lock of the unit without writing a version.
func (s *Service) releaseUnfenced(ctx context.Context, w Workspace) (Workspace, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return Workspace{}, err
	}

	released, err := w.forceUnlock(s.clock())
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", w.ID, err)
	}

	if err := s.workspaces.ReleaseUnfenced(ctx, released, fence); err != nil {
		return Workspace{}, fmt.Errorf("release %s: %w", w.ID, err)
	}

	return released, nil
}

// attemptKey is a fresh blob key for one upload attempt of sv. IDs are
// lowercase, so the key is valid whatever the namespace is called.
func attemptKey(sv StateVersion) (blob.Key, error) {
	key, err := blob.ParseKey("tofu/" + sv.Workspace.String() + "/" + sv.ID.String() + "/" + ident.NewID(""))
	if err != nil {
		return "", fmt.Errorf("state content key: %w", err)
	}

	return key, nil
}

// blobKeyParts splits a blob key into its slash-separated parts.
func blobKeyParts(key blob.Key) []string {
	return strings.Split(key.String(), "/")
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
	reader, stream := newMetaStream(content)

	size, storeErr := s.store(ctx, storeInput{
		namespace: w.Ref.Namespace,
		workspace: w.ID,
		seal:      sv.Sealed,
		key:       key,
		want:      sv.MD5,
		content:   reader,
		unchecked: false,
	})

	if storeErr != nil {
		// Closing the stream lets the reader beside the store finish. The
		// store error is the one the caller sees.
		abandonStream(stream, storeErr)

		return uploadedBody{}, fmt.Errorf("upload state %s: %w", sv.ID, storeErr)
	}

	// A body that is not a state object stores no outputs; the upload still
	// stores the bytes the client sent, as before.
	meta, metaErr := stream.finish(nil)
	if metaErr != nil {
		meta = streamedMeta{
			Lineage:         "",
			Outputs:         nil,
			Serial:          0,
			HasSerial:       false,
			HasLineage:      false,
			ClientEncrypted: false,
		}
	}

	outputs, err := s.streamedOutputs(ctx, w, meta)
	if err != nil {
		return uploadedBody{}, errors.Join(err, s.blobs.Delete(ctx, key))
	}

	return uploadedBody{size: size, outputs: outputs}, nil
}

// abandonStream ends a meta stream whose store failed. The stream's own result
// adds nothing: the caller returns the store error.
func abandonStream(stream *metaStream, cause error) {
	_, _ = stream.finish(cause) //nolint:errcheck // The caller returns the store error.
}
