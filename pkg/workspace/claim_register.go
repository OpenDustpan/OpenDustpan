package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of apply claim registration. Adapters translate driver
// errors into these sentinels and never return driver errors to callers.
var (
	// ErrClaimOverlap reports a claim that shares an address with an
	// accepted claim. The error is an OverlapError that names the blocking
	// claim. The server answers 409 and stores no claim.
	ErrClaimOverlap = errors.New("claim overlaps an accepted claim")
	// ErrClaimEmptySet reports a claim with no resource address. The server
	// answers 422 and stores no claim.
	ErrClaimEmptySet = errors.New("claim holds no resource address")
	// ErrClaimTouchedBase reports a claim whose base is stale: a later
	// version touched an address of the claim after its base. The error is
	// a TouchedBaseError that names the touching version. The server
	// answers 422 and stores no claim.
	ErrClaimTouchedBase = errors.New("claim base is touched by a later version")
	// ErrClaimReplace reports a claim that holds a replace action. This
	// change builds no closure test, so a replacement is a conflict. The
	// server answers 409 with the code claim.closure-unproven and stores
	// no claim.
	ErrClaimReplace = errors.New("claim holds a replace action")
	// ErrClaimNotFound reports a claim, workspace or version that does not
	// exist. The server answers 404.
	ErrClaimNotFound = errors.New("claim not found")
	// ErrClaimDeleted reports a claim on a deleted workspace. The server
	// answers 410.
	ErrClaimDeleted = errors.New("workspace is deleted")
	// ErrClaimLocked reports a claim on a locked workspace. The server
	// answers 422.
	ErrClaimLocked = errors.New("workspace is locked")
	// ErrClaimBadBase reports a claim with an unknown base state version.
	// The server answers 422.
	ErrClaimBadBase = errors.New("unknown base state version")
	// ErrClaimEnded reports a use of a claim that already ended. The server
	// refuses the request.
	ErrClaimEnded = errors.New("claim already ended")
)

// ClaimIDPrefix prefixes the stable ID of each apply claim.
const ClaimIDPrefix = "clm-"

// ClaimState is where an apply claim stands.
type ClaimState string

// Claim states.
const (
	// ClaimAccepted is a live claim: its job may proceed to the apply.
	ClaimAccepted ClaimState = "accepted"
	// ClaimEnded is a claim whose job ended or whose lease expired. It
	// keeps its record, so a later claim can name it as the blocker.
	ClaimEnded ClaimState = "ended"
)

// Claim is the reservation of the resource instances that one saved plan
// changes and reads: an apply claim. Two runner jobs plan from state
// versions of one workspace and register disjoint claims; the server
// records both as accepted, with no apply and no merge.
type Claim struct {
	// ID is the stable ID of the claim ("clm-...").
	ID string
	// WorkspaceID is the stable ID of the workspace.
	WorkspaceID ID
	// Base is the state version that the plan of the claim used.
	Base StateVersionID
	// Addresses is the normalized address set of the claim: trimmed,
	// sorted and deduplicated. Compare only against accepted claims.
	Addresses []string
	// Principal is the subject that registered the claim.
	Principal string
	// JobID is the job that registered the claim, or empty for the local
	// wrapper, which acts as the person who signed in.
	JobID string
	// Trigger names what started the job: a schedule, an API trigger or a
	// merge request. It is empty when the caller names none.
	Trigger string
	// State is accepted or ended.
	State ClaimState
	// StartedAt is when the server accepted the claim.
	StartedAt time.Time
	// EndedAt is when the job of the claim ended or its lease expired.
	// It is zero while the claim is accepted.
	EndedAt time.Time
}

// OverlapError is the refusal of a claim that shares an address with an
// accepted claim. Blocker is the ID of the accepted claim.
type OverlapError struct {
	// Blocker is the ID of the accepted claim that blocks the request.
	Blocker string
}

// Error reports the refusal with the blocking claim.
func (e *OverlapError) Error() string {
	return fmt.Sprintf("%s: blocked by %s", ErrClaimOverlap, e.Blocker)
}

// Is reports whether the target is ErrClaimOverlap.
func (e *OverlapError) Is(target error) bool { return target == ErrClaimOverlap }

// TouchedBaseError is the refusal of a claim whose base is stale: Version
// touched an address of the claim after its base.
type TouchedBaseError struct {
	// Version is the state version that touched the claim after its base.
	Version StateVersionID
}

// Error reports the refusal with the touching version.
func (e *TouchedBaseError) Error() string {
	return fmt.Sprintf("%s: touched by version %s", ErrClaimTouchedBase, e.Version)
}

// Is reports whether the target is ErrClaimTouchedBase.
func (e *TouchedBaseError) Is(target error) bool { return target == ErrClaimTouchedBase }

// ClaimWorkspaces is the workspace read side that claim registration
// needs: the workspace by its stable ID.
type ClaimWorkspaces interface {
	// GetWorkspaceByID returns the workspace with id, or ErrNotFound.
	GetWorkspaceByID(ctx context.Context, id ID) (Workspace, error)
}

// ClaimVersions is the state version read side that claim registration
// needs: one version of one workspace, so a read of one workspace cannot
// get the version of another.
type ClaimVersions interface {
	// GetWorkspaceStateVersion returns the state version id of workspace
	// ws in namespace ns, or ErrStateVersionNotFound.
	GetWorkspaceStateVersion(
		ctx context.Context, ns namespace.Name, ws ID, id StateVersionID,
	) (StateVersion, error)
}

// ClaimDeleted reports whether a workspace ID names a soft-deleted
// workspace. It is optional: a nil ClaimDeleted reads every missing
// workspace as missing, never as deleted.
type ClaimDeleted interface {
	// IsDeleted reports whether id names a soft-deleted workspace.
	IsDeleted(ctx context.Context, id ID) (bool, error)
}

// RegisterClaimRequest asks the registry to store one apply claim.
type RegisterClaimRequest struct {
	// WorkspaceID is the stable ID of the workspace.
	WorkspaceID ID
	// Base is the state version that the plan used.
	Base StateVersionID
	// Addresses is the address set of the plan. The registry normalizes
	// it once: it trims, sorts and deduplicates it.
	Addresses []string
	// Replace reports that the plan holds a replace action. A replacement
	// is a conflict until the closure test lands.
	Replace bool
	// Principal is the subject that registers the claim.
	Principal string
	// JobID is the job that registers the claim, or empty for the local
	// wrapper. Retries of the same job with the same base and set are
	// idempotent: they return the same claim ID with no second record.
	JobID string
	// Trigger names what started the job, or empty.
	Trigger string
}

// ClaimRegistryConfig holds the dependencies of a ClaimRegistry. Every
// port but Deleted, Manual and OnAccept is required.
type ClaimRegistryConfig struct {
	// Workspaces reads the workspace of a claim.
	Workspaces ClaimWorkspaces
	// Versions reads the base version of a claim.
	Versions ClaimVersions
	// Deleted reports soft-deleted workspaces. Nil reads every missing
	// workspace as missing.
	Deleted ClaimDeleted
	// Manual refuses a claim on a workspace that a manual lock covers.
	// Nil turns the check off.
	Manual ManualLocks
	// OnAccept runs after the registry stores an accepted claim. An error
	// rolls the store back, so no claim goes without its audit entry. Nil
	// records nothing.
	OnAccept func(ctx context.Context, c Claim) error
	// Now is the clock. Nil means the real time.
	Now func() time.Time
}

// ClaimRegistry stores apply claims and checks each registration against
// the accepted claims of the workspace in one critical section, so two
// registrations that race cannot both pass over the same accepted set.
// The mutex serializes registrations: the registry answers under
// contention, and a parallel registration test proves that two disjoint
// registrations both land and that an overlap loses.
//
// A SQL adapter will hold the workspace row lock and write the claim in
// the same transaction; this registry is the domain behavior that the
// adapter will persist.
type ClaimRegistry struct {
	now        func() time.Time
	workspaces ClaimWorkspaces
	versions   ClaimVersions
	deleted    ClaimDeleted
	manual     ManualLocks
	onAccept   func(ctx context.Context, c Claim) error

	mu sync.Mutex
	// claimSet holds every stored claim by its ID. The field is
	// unexported: callers list claims through List, never by reaching
	// into the set.
	claimSet map[string]*Claim
	// order holds the claim IDs of each workspace in accept order, so
	// List reads oldest first.
	order map[ID][]string
	// touches holds the committed versions of each workspace after the
	// registry started, oldest first. NoteCommitted records them; the
	// touched-base check reads them. Versions committed outside the
	// registry are unknown to it, and the merge check of the commit path
	// refuses those instead.
	touches map[ID][]versionTouch
	// fences holds the failover version first seen for each namespace. A
	// fence with another version is stale, as in the fenced stores.
	fences map[namespace.Name]int64
}

// versionTouch is one committed version with the addresses it touched.
type versionTouch struct {
	id        StateVersionID
	addresses []string
}

// NewClaimRegistry returns a ClaimRegistry. It returns an error that
// matches ErrMissingDependency when Workspaces or Versions is nil.
func NewClaimRegistry(cfg ClaimRegistryConfig) (*ClaimRegistry, error) {
	switch {
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: claim workspace store", ErrMissingDependency)
	case cfg.Versions == nil:
		return nil, fmt.Errorf("%w: claim version store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &ClaimRegistry{
		now:        now,
		workspaces: cfg.Workspaces,
		versions:   cfg.Versions,
		deleted:    cfg.Deleted,
		manual:     cfg.Manual,
		onAccept:   cfg.OnAccept,
		mu:         sync.Mutex{},
		claimSet:   make(map[string]*Claim),
		order:      make(map[ID][]string),
		touches:    make(map[ID][]versionTouch),
		fences:     make(map[namespace.Name]int64),
	}, nil
}

// NormalizeClaimAddresses normalizes an address set once: it trims each
// address, drops empties, sorts and deduplicates. OpenTofu addresses are
// case-sensitive, so it never folds case.
func NormalizeClaimAddresses(addresses []string) []string {
	out := make([]string, 0, len(addresses))

	for _, a := range addresses {
		if trimmed := strings.TrimSpace(a); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// Register stores one apply claim and returns it. It is idempotent for one
// job: the same job with the same base and the same normalized set gets
// the same claim ID with no second record. A different set is a new
// claim, checked again for overlap.
//
// It returns an error that matches ErrClaimEmptySet for an empty set,
// ErrClaimReplace for a replace plan, ErrClaimNotFound for a missing
// workspace, ErrClaimDeleted for a deleted workspace, ErrClaimLocked for
// a locked workspace, ErrClaimBadBase for an unknown base, an
// OverlapError for a shared address, a TouchedBaseError for a base that
// a later version touched, or namespace.ErrFenced for a stale fence.
func (r *ClaimRegistry) Register(
	ctx context.Context, req RegisterClaimRequest, fence namespace.Fence,
) (Claim, error) {
	normalized := NormalizeClaimAddresses(req.Addresses)

	if len(normalized) == 0 {
		return Claim{}, ErrClaimEmptySet
	}

	if req.Replace {
		return Claim{}, ErrClaimReplace
	}

	if err := ctx.Err(); err != nil {
		return Claim{}, fmt.Errorf("register claim: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	w, err := r.checkRegistration(ctx, req, fence)
	if err != nil {
		return Claim{}, err
	}

	if err := r.checkBase(ctx, w, req.Base); err != nil {
		return Claim{}, err
	}

	if stored, ok := r.findRetry(w.ID, req.JobID, req.Base, normalized); ok {
		return stored, nil
	}

	if err := r.checkDisjoint(w.ID, normalized); err != nil {
		return Claim{}, err
	}

	return r.store(ctx, w, req, normalized)
}

// Workspace returns the workspace with id, for route resolution. It
// returns an error that matches ErrClaimNotFound for a missing workspace,
// or ErrClaimDeleted for a soft-deleted one.
func (r *ClaimRegistry) Workspace(ctx context.Context, id ID) (Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	w, err := r.workspaces.GetWorkspaceByID(ctx, id)
	if err != nil {
		return Workspace{}, r.missingWorkspace(ctx, id, err)
	}

	return w, nil
}

// List returns the claims of the workspace in accept order, oldest first.
// It returns no sentinel errors: a workspace without claims reads as an
// empty list.
func (r *ClaimRegistry) List(_ context.Context, workspace ID) []Claim {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := r.order[workspace]
	out := make([]Claim, 0, len(ids))

	for _, id := range ids {
		stored := r.claimSet[id]
		if stored == nil {
			continue
		}

		out = append(out, *stored)
	}

	return out
}

// ReleaseByJob marks each accepted claim of the job ended at the time now,
// for the end of its job. It returns the count of claims it ended.
func (r *ClaimRegistry) ReleaseByJob(_ context.Context, jobID string, now time.Time) int {
	if jobID == "" {
		return 0
	}

	ended := now.UTC().Truncate(time.Millisecond)

	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0

	for _, claim := range r.claimSet {
		if claim.JobID == jobID && claim.State == ClaimAccepted {
			claim.State = ClaimEnded
			claim.EndedAt = ended
			count++
		}
	}

	return count
}

// ExpireStale marks each accepted claim that started before the cutoff
// ended, for lease expiry after a crash. It returns the count of claims
// it ended.
func (r *ClaimRegistry) ExpireStale(_ context.Context, cutoff time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0

	for _, claim := range r.claimSet {
		if claim.State == ClaimAccepted && claim.StartedAt.Before(cutoff) {
			claim.State = ClaimEnded
			claim.EndedAt = cutoff.UTC().Truncate(time.Millisecond)
			count++
		}
	}

	return count
}

// NoteCommitted records that version id committed on the workspace with
// the touched addresses, so later claims with an older base fail the
// touched-base check. The merge path calls it in its commit transaction;
// tests call it to stand in for a later version.
func (r *ClaimRegistry) NoteCommitted(_ context.Context, workspace ID, id StateVersionID, addresses []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.touches[workspace] = append(r.touches[workspace], versionTouch{
		id:        id,
		addresses: NormalizeClaimAddresses(addresses),
	})
}

// checkRegistration checks the fence and the workspace of a register
// call: the fence is current and names the namespace of the workspace,
// the workspace exists and is live, and no lock covers it.
func (r *ClaimRegistry) checkRegistration(
	ctx context.Context, req RegisterClaimRequest, fence namespace.Fence,
) (Workspace, error) {
	if err := r.checkFence(fence); err != nil {
		return Workspace{}, err
	}

	w, err := r.workspaces.GetWorkspaceByID(ctx, req.WorkspaceID)
	if err != nil {
		return Workspace{}, r.missingWorkspace(ctx, req.WorkspaceID, err)
	}

	if fence.Namespace != w.Ref.Namespace {
		return Workspace{}, fmt.Errorf(
			"%w: fence names %q, workspace is in %q",
			namespace.ErrFenced,
			fence.Namespace,
			w.Ref.Namespace,
		)
	}

	if w.Lock.Locked() {
		return Workspace{}, fmt.Errorf("%w: %s", ErrClaimLocked, w.Lock.Holder)
	}

	if r.manual != nil {
		// Defense in depth: the handler checks the whole-unit lock too;
		// this check stays so that a new caller cannot skip the manual
		// lock. A store error fails closed.
		covering, coverErr := r.manual.CoveringFor(ctx, manuallockUnit(w), req.JobID)

		switch {
		case coverErr == nil:
			return Workspace{}, fmt.Errorf(
				"%w: manual lock of %q covers the workspace",
				ErrClaimLocked,
				covering.Holder,
			)
		case !errors.Is(coverErr, manuallock.ErrNotLocked):
			return Workspace{}, coverErr
		}
	}

	return w, nil
}

// checkBase refuses an unknown base state version.
func (r *ClaimRegistry) checkBase(ctx context.Context, w Workspace, base StateVersionID) error {
	if _, err := r.versions.GetWorkspaceStateVersion(ctx, w.Ref.Namespace, w.ID, base); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrClaimBadBase, base, err)
	}

	return nil
}

// findRetry returns the stored claim when the same job sends the same base
// and the same normalized set again: the retry is idempotent.
func (r *ClaimRegistry) findRetry(
	workspaceID ID, jobID string, base StateVersionID, normalized []string,
) (Claim, bool) {
	for _, id := range r.order[workspaceID] {
		stored := r.claimSet[id]
		if stored == nil {
			continue
		}

		if stored.JobID == jobID && stored.Base == base && slices.Equal(stored.Addresses, normalized) {
			return *stored, true
		}
	}

	var missing Claim

	return missing, false
}

// checkDisjoint refuses a set that shares an address with an accepted
// claim, or with a version committed after the base of the call.
func (r *ClaimRegistry) checkDisjoint(workspaceID ID, normalized []string) error {
	for _, id := range r.order[workspaceID] {
		stored := r.claimSet[id]
		if stored == nil {
			continue
		}

		if stored.State != ClaimAccepted {
			continue
		}

		if shared := firstShared(stored.Addresses, normalized); shared != "" {
			return &OverlapError{Blocker: stored.ID}
		}
	}

	for _, touch := range r.touches[workspaceID] {
		if firstShared(touch.addresses, normalized) != "" {
			return &TouchedBaseError{Version: touch.id}
		}
	}

	return nil
}

// store appends one accepted claim and runs the accept hook. A failing
// hook rolls the store back, so no claim goes without its audit entry.
func (r *ClaimRegistry) store(
	ctx context.Context, w Workspace, req RegisterClaimRequest, normalized []string,
) (Claim, error) {
	now := r.now().UTC().Truncate(time.Millisecond)

	claim := Claim{
		ID:          ident.NewID(ClaimIDPrefix),
		WorkspaceID: w.ID,
		Base:        req.Base,
		Addresses:   normalized,
		Principal:   req.Principal,
		JobID:       req.JobID,
		Trigger:     req.Trigger,
		State:       ClaimAccepted,
		StartedAt:   now,
		EndedAt:     time.Time{},
	}

	r.claimSet[claim.ID] = &claim
	r.order[w.ID] = append(r.order[w.ID], claim.ID)

	if r.onAccept != nil {
		if hookErr := r.onAccept(ctx, claim); hookErr != nil {
			delete(r.claimSet, claim.ID)
			r.order[w.ID] = r.order[w.ID][:len(r.order[w.ID])-1]

			return Claim{}, fmt.Errorf("record the accept of claim %s: %w", claim.ID, hookErr)
		}
	}

	return claim, nil
}

// checkFence refuses a fence whose failover version differs from the one
// first seen for its namespace, as the fenced stores do. The first fence
// of a namespace sets the version.
func (r *ClaimRegistry) checkFence(fence namespace.Fence) error {
	seen, ok := r.fences[fence.Namespace]
	if !ok {
		r.fences[fence.Namespace] = fence.FailoverVersion

		return nil
	}

	if fence.FailoverVersion != seen {
		return fmt.Errorf(
			"%w: fence carries failover version %d, want %d",
			namespace.ErrFenced,
			fence.FailoverVersion,
			seen,
		)
	}

	return nil
}

// missingWorkspace maps a workspace lookup failure to 404 or 410: a
// soft-deleted workspace reads as deleted when the registry knows deleted
// workspaces, else as missing.
func (r *ClaimRegistry) missingWorkspace(ctx context.Context, id ID, err error) error {
	if !errors.Is(err, ErrNotFound) {
		return err
	}

	if r.deleted == nil {
		return fmt.Errorf("%w: %s", ErrClaimNotFound, id)
	}

	deleted, delErr := r.deleted.IsDeleted(ctx, id)
	if delErr != nil {
		return delErr
	}

	if deleted {
		return fmt.Errorf("%w: %s", ErrClaimDeleted, id)
	}

	return fmt.Errorf("%w: %s", ErrClaimNotFound, id)
}

// manuallockUnit returns the manual lock unit of the workspace.
func manuallockUnit(w Workspace) manuallock.Unit {
	return manuallock.Unit{
		Ref: collection.UnitRef{
			Namespace: w.Ref.Namespace,
			Tool:      "opentofu",
			Name:      w.Ref.Name.String(),
		},
		NamespaceID: w.NamespaceID,
		UnitID:      string(w.ID),
	}
}

func firstShared(set, want []string) string {
	for _, a := range want {
		if slices.Contains(set, a) {
			return a
		}
	}

	return ""
}
