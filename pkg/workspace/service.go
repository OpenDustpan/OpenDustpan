package workspace

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// DefaultMaxStateBytes bounds one state version unless configured otherwise.
	DefaultMaxStateBytes = 512 << 20
	// UploadWindow is how long a state upload capability stays valid.
	UploadWindow = 10 * time.Minute
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
}

// StateStore persists state version metadata.
type StateStore interface {
	// CreateStateVersion persists a new, pending sv. It returns ErrNotFound
	// if sv.Workspace does not exist, namespace.ErrNotFound if the namespace
	// does not exist, or namespace.ErrFenced if fence is stale.
	CreateStateVersion(ctx context.Context, sv StateVersion, fence namespace.Fence) error
	// GetStateVersion returns the state version, or ErrStateVersionNotFound.
	GetStateVersion(ctx context.Context, id StateVersionID) (StateVersion, error)
	// FinalizeStateVersion saves the finalized sv (with its Content key),
	// stamps the workspace's UpdatedAt with sv.FinalizedAt, and makes sv the
	// workspace's current state, checking in the same transaction that:
	//   - the stored version is still pending, else ErrUploadDenied;
	//   - the workspace is held by lock sv.LockID, else ErrNotLocked or
	//     ErrLockedByOther;
	//   - unless sv.Force, the current state (if any) has sv.Lineage, else
	//     ErrLineageMismatch, and a lower serial, else ErrStaleSerial.
	// Those errors, namespace.ErrNotFound and namespace.ErrFenced mean
	// nothing was written. Any other error leaves the outcome unknown: the
	// version may be current.
	FinalizeStateVersion(ctx context.Context, sv StateVersion, fence namespace.Fence) error
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
}

// Config holds the dependencies of a Service.
type Config struct {
	Workspaces Store
	States     StateStore
	// Blobs holds state content under "tofu/<workspace id>/<state version id>/<attempt>".
	Blobs      blob.Store
	Namespaces *namespace.Service
	Now        func() time.Time
	// MaxStateBytes bounds one state version; zero means DefaultMaxStateBytes.
	MaxStateBytes int64
}

// Service implements the workspace use cases behind OpenTofu's cloud block:
// workspaces, locking, and state versions uploaded through single-use
// capabilities.
type Service struct {
	workspaces Store
	states     StateStore
	blobs      blob.Store
	namespaces *namespace.Service
	now        func() time.Time
	maxState   int64
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
		now:        cfg.Now,
		maxState:   cfg.MaxStateBytes,
	}

	if s.now == nil {
		s.now = time.Now
	}

	if s.maxState <= 0 {
		s.maxState = DefaultMaxStateBytes
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

// Unlock releases holder's lock.
func (s *Service) Unlock(ctx context.Context, id ID, holder string) (Workspace, error) {
	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		return w.unlock(now, holder)
	})
}

// ForceUnlock releases the lock whoever holds it, as `tofu force-unlock` does.
func (s *Service) ForceUnlock(ctx context.Context, id ID) (Workspace, error) {
	return s.change(ctx, id, func(w Workspace, now time.Time) (Workspace, error) {
		return w.forceUnlock(now)
	})
}

// PendingState is a state version waiting for its content, and the
// capability that may upload it. The token is shown once.
type PendingState struct {
	Upload  token.Token
	Version StateVersion
}

// BeginState declares a new state version for the workspace. The caller must
// hold the lock. The content follows through Upload with the returned token.
func (s *Service) BeginState(ctx context.Context, id ID, holder string, in NewState) (PendingState, error) {
	sum, err := in.parse()
	if err != nil {
		return PendingState{}, err
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return PendingState{}, err
	}

	if lockErr := w.heldBy(holder); lockErr != nil {
		return PendingState{}, fmt.Errorf("new state for %s: %w", w.Ref, lockErr)
	}

	current, err := s.states.CurrentStateVersion(ctx, id)

	switch {
	case errors.Is(err, ErrNoState):
	case err != nil:
		return PendingState{}, fmt.Errorf("current state of %s: %w", w.Ref, err)
	default:
		if followErr := in.follows(current); followErr != nil {
			return PendingState{}, fmt.Errorf("new state for %s: %w", w.Ref, followErr)
		}
	}

	return s.createPending(ctx, w, in, sum, "")
}

// Upload stores the content of a pending state version and makes it current.
// The content must match the declared MD5 and fit the size limit, and the
// lock it was declared under must still be held when it finalizes; otherwise
// the content is discarded. Concurrent uploads of one version are safe: each
// writes its own blob, exactly one finalizes, and the others delete only their
// own blob. A retry after success (same capability, same content) succeeds
// without storing anything, because go-tfe retries when a response is lost.
func (s *Service) Upload(
	ctx context.Context,
	id StateVersionID,
	tok token.Token,
	content io.Reader,
) (StateVersion, error) {
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

	size, err := s.store(ctx, key, sv.MD5, content)
	if err != nil {
		return StateVersion{}, fmt.Errorf("upload state %s: %w", id, err)
	}

	sv.Status = StatusFinalized
	sv.FinalizedAt = s.clock()
	sv.Size = size
	sv.Content = key

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

// DiscardJSONRendering accepts the JSON rendering the CLI uploads alongside
// a state version and discards it: the server keeps the state itself and the
// outputs declared at creation, never the rendering. The capability rules
// match Upload, except that a retry of a finalized version with the same
// capability succeeds whatever the rendering holds: the two PUTs of one
// upload carry other content by design. A rendering over the size limit
// fails with ErrTooLarge, and an expired capability fails whatever the
// version status. Unlike Upload it never finalizes and
// never consumes the capability, so the state upload with the same
// capability still works afterwards.
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

	n, err := io.Copy(io.Discard, io.LimitReader(content, s.maxState+1))
	if err != nil {
		return fmt.Errorf("read JSON rendering of %s: %w", id, err)
	}

	if n > s.maxState {
		return fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxState)
	}

	return nil
}

// Current returns the workspace's current state version, or ErrNoState.
func (s *Service) Current(ctx context.Context, id ID) (StateVersion, error) {
	sv, err := s.states.CurrentStateVersion(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("current state of %s: %w", id, err)
	}

	return sv, nil
}

// ReadState returns the finalized state version with id, or
// ErrStateVersionNotFound when it does not exist or is not finalized. A
// pending version is never readable.
func (s *Service) ReadState(ctx context.Context, id StateVersionID) (StateVersion, error) {
	sv, err := s.states.GetStateVersion(ctx, id)
	if err != nil {
		return StateVersion{}, fmt.Errorf("read state %s: %w", id, err)
	}

	if sv.Status != StatusFinalized {
		return StateVersion{}, fmt.Errorf("%w: state version %s is %s", ErrStateVersionNotFound, id, sv.Status)
	}

	return sv, nil
}

// Open returns the content of a finalized state version. The caller closes it.
func (s *Service) Open(ctx context.Context, sv StateVersion) (io.ReadCloser, error) {
	if sv.Status != StatusFinalized {
		return nil, fmt.Errorf("%w: state version %s is %s", ErrStateVersionNotFound, sv.ID, sv.Status)
	}

	r, err := s.blobs.Get(ctx, sv.Content)
	if err != nil {
		return nil, fmt.Errorf("open state %s: %w", sv.ID, err)
	}

	return r, nil
}

// History returns the finalized state versions of the workspace, newest
// first, with the total count of finalized versions.
func (s *Service) History(ctx context.Context, id ID, page Page) ([]StateVersion, int, error) {
	list, total, err := s.states.ListStateVersions(ctx, id, page)
	if err != nil {
		return nil, 0, fmt.Errorf("history of %s: %w", id, err)
	}

	return list, total, nil
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
// version that records src. The blob lands before the finalize, so a
// committed version never names a missing blob.
func (s *Service) copyState(ctx context.Context, w Workspace, src StateVersion) (StateVersion, error) {
	// The hex MD5 in NewState is display only; createPending stores the
	// validated sum passed alongside it, which is src.MD5 here.
	pending, err := s.createPending(ctx, w, NewState{
		Lineage: src.Lineage,
		MD5:     hex.EncodeToString(src.MD5[:]),
		Outputs: src.Outputs,
		Serial:  src.Serial,
		Force:   true,
	}, src.MD5, src.ID)
	if err != nil {
		return StateVersion{}, err
	}

	key, err := attemptKey(pending.Version)
	if err != nil {
		return StateVersion{}, err
	}

	size, err := s.copyBlob(ctx, key, src)
	if err != nil {
		return StateVersion{}, err
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return StateVersion{}, errors.Join(err, s.blobs.Delete(ctx, key))
	}

	sv := pending.Version
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

	return sv, nil
}

// copyBlob copies the content of src under key, checking size and MD5 as it
// streams. On any mismatch the object is deleted.
func (s *Service) copyBlob(ctx context.Context, key blob.Key, src StateVersion) (int64, error) {
	r, err := s.blobs.Get(ctx, src.Content)
	if err != nil {
		return 0, fmt.Errorf("restore %s from %s: %w", src.Workspace, src.ID, err)
	}

	size, storeErr := s.store(ctx, key, src.MD5, r)
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

// rejected reports whether a finalize error proves nothing was written.
func rejected(err error) bool {
	for _, sentinel := range []error{
		ErrUploadDenied, ErrNotLocked, ErrLockedByOther, ErrLineageMismatch, ErrStaleSerial, namespace.ErrFenced,
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
	restoredFrom StateVersionID,
) (PendingState, error) {
	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return PendingState{}, err
	}

	tok, err := token.New()
	if err != nil {
		return PendingState{}, err
	}

	now := s.clock()
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
		Outputs:         in.Outputs,
		MD5:             sum,
		Serial:          in.Serial,
		Size:            0,
		Force:           in.Force,
		RestoredFrom:    restoredFrom,
	}

	if err := s.states.CreateStateVersion(ctx, sv, fence); err != nil {
		return PendingState{}, fmt.Errorf("create state version for %s: %w", w.Ref, err)
	}

	return PendingState{Upload: tok, Version: sv}, nil
}

// store writes content under key, checking size and MD5 as it streams. On any
// mismatch the object is deleted.
func (s *Service) store(ctx context.Context, key blob.Key, want [md5.Size]byte, content io.Reader) (int64, error) {
	hash := md5.New() //nolint:gosec // Required by the TFE protocol; integrity only.
	counted := &countingReader{r: io.LimitReader(content, s.maxState+1), n: 0}

	if err := s.blobs.Put(ctx, key, io.TeeReader(counted, hash)); err != nil {
		return 0, fmt.Errorf("store content: %w", err)
	}

	var problem error

	switch {
	case counted.n > s.maxState:
		problem = fmt.Errorf("%w: limit %d bytes", ErrTooLarge, s.maxState)
	case [md5.Size]byte(hash.Sum(nil)) != want:
		problem = ErrChecksumMismatch
	default:
		return counted.n, nil
	}

	return 0, errors.Join(problem, s.blobs.Delete(ctx, key))
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

// attemptKey is a fresh blob key for one upload attempt of sv. IDs are
// lowercase, so the key is valid whatever the namespace is called.
func attemptKey(sv StateVersion) (blob.Key, error) {
	key, err := blob.ParseKey("tofu/" + sv.Workspace.String() + "/" + sv.ID.String() + "/" + ident.NewID(""))
	if err != nil {
		return "", fmt.Errorf("state content key: %w", err)
	}

	return key, nil
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
