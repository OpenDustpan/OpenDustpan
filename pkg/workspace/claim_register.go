package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
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
	// ErrClaimTooLarge reports a claim whose address set holds more than
	// MaxClaimAddresses addresses, or an address longer than
	// MaxClaimAddressLength bytes. The server answers 422 and stores no
	// claim.
	ErrClaimTooLarge = errors.New("claim address set is too large")
	// ErrClaimInvalid reports a claim record that a store refuses: an empty
	// ID, base or principal, an address set that is not normalized, or a
	// state other than accepted at insert.
	ErrClaimInvalid = errors.New("claim record is not valid")
	// ErrClaimEnded reports a use of a claim that already ended. The server
	// refuses the request.
	ErrClaimEnded = errors.New("claim already ended")
)

// MaxClaimAddresses bounds the address set of one claim. The registry, the
// stores and the HTTP handler each check the bound alone.
const MaxClaimAddresses = 10_000

// MaxClaimAddressLength bounds one address of a claim, in bytes.
const MaxClaimAddressLength = 1_024

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
	// ViaJobToken is true when the caller authenticated with a job token.
	// Such a claim names its job, so that the end of the job ends the claim:
	// the registry refuses an empty JobID with ErrClaimInvalid.
	ViaJobToken bool
	// Audit is the audit entry of the accept. The registry sets its time, its
	// object ID and its job ID. The store appends it in the transaction that
	// stores the claim: an invalid entry stores no claim.
	Audit audit.Entry
}

// ClaimRegistryConfig holds the dependencies of a ClaimRegistry. Every
// port but Deleted and Manual is required.
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
	// Store persists the claims. It is required: the registry has no
	// default store, so a server never holds claims in memory by accident.
	Store ClaimStore
	// Now is the clock. Nil means the real time.
	Now func() time.Time
}

// ClaimRegistry checks each registration against the accepted claims of the
// workspace inside one ClaimStore transaction that holds the claim lock of
// the workspace, so two registrations that race cannot both pass over the
// same accepted set. The domain rules live here; the store only persists.
type ClaimRegistry struct {
	now        func() time.Time
	workspaces ClaimWorkspaces
	versions   ClaimVersions
	deleted    ClaimDeleted
	manual     ManualLocks
	store      ClaimStore
}

// NewClaimRegistry returns a ClaimRegistry. It returns an error that
// matches ErrMissingDependency when Workspaces, Versions or Store is nil.
func NewClaimRegistry(cfg ClaimRegistryConfig) (*ClaimRegistry, error) {
	switch {
	case cfg.Workspaces == nil:
		return nil, fmt.Errorf("%w: claim workspace store", ErrMissingDependency)
	case cfg.Versions == nil:
		return nil, fmt.Errorf("%w: claim version store", ErrMissingDependency)
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: claim store", ErrMissingDependency)
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
		store:      cfg.Store,
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
	ctx context.Context, req *RegisterClaimRequest, fence namespace.Fence,
) (Claim, error) {
	w, normalized, err := r.checkRequest(ctx, req, fence)
	if err != nil {
		return Claim{}, err
	}

	var out Claim

	err = r.store.WithClaims(ctx, w.ID, fence, func(tx ClaimTx) error {
		claim, txErr := r.registerIn(ctx, tx, w, req, normalized)
		out = claim

		return txErr
	})
	if errors.Is(err, ErrNotFound) {
		return Claim{}, fmt.Errorf("%w: %s: %w", ErrClaimNotFound, w.ID, err)
	}

	if err != nil {
		return Claim{}, err
	}

	return out, nil
}

// Workspace returns the workspace with id, for route resolution. It
// returns an error that matches ErrClaimNotFound for a missing workspace,
// or ErrClaimDeleted for a soft-deleted one.
func (r *ClaimRegistry) Workspace(ctx context.Context, id ID) (Workspace, error) {
	w, err := r.workspaces.GetWorkspaceByID(ctx, id)
	if err != nil {
		return Workspace{}, r.missingWorkspace(ctx, id, err)
	}

	return w, nil
}

// List returns the claims of the workspace in accept order, oldest first. A
// workspace without claims reads as an empty list. A store error fails the
// call: a list never answers empty for a store that it could not read.
func (r *ClaimRegistry) List(ctx context.Context, workspace ID) ([]Claim, error) {
	claims, err := r.store.ListClaims(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list claims: %w", err)
	}

	return claims, nil
}

// ReleaseByJob marks each accepted claim of the job ended at the time now,
// for the end of its job. It returns the count of claims it ended.
func (r *ClaimRegistry) ReleaseByJob(
	ctx context.Context, fence namespace.Fence, jobID string, now time.Time,
) (int, error) {
	n, err := r.store.EndClaimsByJob(ctx, fence, jobID, now)
	if err != nil {
		return 0, fmt.Errorf("release claims of job %s: %w", jobID, err)
	}

	return n, nil
}

// ExpireStale marks each accepted claim that started before the cutoff
// ended, for lease expiry after a crash. It returns the count of claims it
// ended.
func (r *ClaimRegistry) ExpireStale(
	ctx context.Context, fence namespace.Fence, cutoff time.Time,
) (int, error) {
	n, err := r.store.ExpireClaims(ctx, fence, cutoff)
	if err != nil {
		return 0, fmt.Errorf("expire claims: %w", err)
	}

	return n, nil
}

// NoteCommitted records that version id committed on the workspace with
// the touched addresses, so later claims with an older base fail the
// touched-base check. The merge path calls it in its commit transaction;
// tests call it to stand in for a later version.
func (r *ClaimRegistry) NoteCommitted(
	ctx context.Context, fence namespace.Fence, workspace ID, id StateVersionID, addresses []string,
) error {
	if err := r.store.AddClaimTouch(ctx, fence, workspace, id, addresses); err != nil {
		return fmt.Errorf("note committed version %s: %w", id, err)
	}

	return nil
}

// registerIn runs the claim rules inside the transaction tx: a retry of the
// same job returns its claim, a set that shares an address with an accepted
// claim or a touch is refused, and any other set is stored.
func (r *ClaimRegistry) registerIn(
	ctx context.Context, tx ClaimTx, w Workspace, req *RegisterClaimRequest, normalized []string,
) (Claim, error) {
	if err := r.checkLocksIn(ctx, tx, req); err != nil {
		return Claim{}, err
	}

	claims, err := tx.Claims(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("read claims: %w", err)
	}

	if stored, ok := findClaimRetry(claims, req.JobID, req.Principal, req.Base, normalized); ok {
		return stored, nil
	}

	touches, err := tx.Touches(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("read touches: %w", err)
	}

	if disjointErr := checkClaimDisjoint(claims, touches, normalized); disjointErr != nil {
		return Claim{}, disjointErr
	}

	return r.insert(ctx, tx, w, req, normalized)
}

// checkLocksIn reads the locks again inside the transaction, under the claim
// lock of the workspace: the workspace lock and the manual locks. A lock that
// came after the checks of checkRequest refuses the claim here. Defense in
// depth: checkRequest reads the same locks before the transaction opens, to
// refuse early; this check does not rely on it. A store error fails closed.
func (r *ClaimRegistry) checkLocksIn(ctx context.Context, tx ClaimTx, req *RegisterClaimRequest) error {
	w, err := tx.Workspace(ctx)
	if err != nil {
		return fmt.Errorf("read the workspace: %w", err)
	}

	if w.Lock.Locked() {
		return fmt.Errorf("%w: %s", ErrClaimLocked, w.Lock.Holder)
	}

	if r.manual == nil {
		return nil
	}

	locked, err := tx.ManualLocked(ctx)
	if err != nil {
		return fmt.Errorf("read the manual locks: %w", err)
	}

	if !locked {
		return nil
	}

	// A manual lock covers the workspace now. A job whose phase started before
	// the lock passes it, as the rule of the lock service says.
	return r.checkManual(ctx, w, req.JobID)
}

// checkRequest checks a register request before the store transaction: the
// set is not empty, holds no replace and stays in bounds, and the workspace
// and the base are valid. It returns the normalized set.
func (r *ClaimRegistry) checkRequest(
	ctx context.Context, req *RegisterClaimRequest, fence namespace.Fence,
) (Workspace, []string, error) {
	normalized := NormalizeClaimAddresses(req.Addresses)

	if len(normalized) == 0 {
		return Workspace{}, nil, ErrClaimEmptySet
	}

	if req.Replace {
		return Workspace{}, nil, ErrClaimReplace
	}

	// Defense in depth: the handler binds a job token to its job too. This
	// check stays so that a new caller cannot store a claim of a job token
	// with no job: nothing would end it, and a retry could take the claim of
	// the person who started the job.
	if req.ViaJobToken && req.JobID == "" {
		return Workspace{}, nil, fmt.Errorf("%w: a job token registers a claim for its job", ErrClaimInvalid)
	}

	// Defense in depth: the handler bounds the raw set too. This check stays
	// so that a new caller of the registry cannot store a set that no store
	// accepts, and the stores check the bound again.
	if err := checkClaimBounds(normalized); err != nil {
		return Workspace{}, nil, err
	}

	if err := ctx.Err(); err != nil {
		return Workspace{}, nil, fmt.Errorf("register claim: %w", err)
	}

	w, err := r.checkRegistration(ctx, req, fence)
	if err != nil {
		return Workspace{}, nil, err
	}

	if baseErr := r.checkBase(ctx, w, req.Base); baseErr != nil {
		return Workspace{}, nil, baseErr
	}

	return w, normalized, nil
}

// checkRegistration checks the workspace of a register call: the fence
// names the namespace of the workspace (the store checks the failover version),
// the workspace exists and is live, and no lock covers it.
func (r *ClaimRegistry) checkRegistration(
	ctx context.Context, req *RegisterClaimRequest, fence namespace.Fence,
) (Workspace, error) {
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

	// The handler does not check manual locks, so this is the first check of
	// them. The transaction checks them again under the claim lock.
	if r.manual != nil {
		if err := r.checkManual(ctx, w, req.JobID); err != nil {
			return Workspace{}, err
		}
	}

	return w, nil
}

// checkManual refuses a workspace that a manual lock covers for the job. A
// store error fails closed. The rule that a phase that started before the
// lock passes it lives in the lock service that CoveringFor calls.
func (r *ClaimRegistry) checkManual(ctx context.Context, w Workspace, jobID string) error {
	covering, err := r.manual.CoveringFor(ctx, manuallockUnit(w), jobID)

	switch {
	case err == nil:
		return fmt.Errorf("%w: manual lock of %q covers the workspace", ErrClaimLocked, covering.Holder)
	case !errors.Is(err, manuallock.ErrNotLocked):
		return err
	default:
		return nil
	}
}

// checkBase refuses an unknown base state version.
func (r *ClaimRegistry) checkBase(ctx context.Context, w Workspace, base StateVersionID) error {
	if _, err := r.versions.GetWorkspaceStateVersion(ctx, w.Ref.Namespace, w.ID, base); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrClaimBadBase, base, err)
	}

	return nil
}

// findClaimRetry returns the stored claim when the same job sends the same
// base and the same normalized set again: the retry is idempotent. The match
// includes the principal, so the local wrapper, which registers with no job
// ID, never gets the claim of another person.
func findClaimRetry(
	claims []Claim, jobID, principal string, base StateVersionID, normalized []string,
) (Claim, bool) {
	for i := range claims {
		stored := &claims[i]

		if stored.JobID == jobID && stored.Principal == principal && stored.Base == base &&
			slices.Equal(stored.Addresses, normalized) {
			return *stored, true
		}
	}

	var missing Claim

	return missing, false
}

// checkClaimDisjoint refuses a set that shares an address with an accepted
// claim, or with a version committed after the base of the call.
func checkClaimDisjoint(claims []Claim, touches []ClaimTouch, normalized []string) error {
	for i := range claims {
		if claims[i].State != ClaimAccepted {
			continue
		}

		if shared := firstShared(claims[i].Addresses, normalized); shared != "" {
			return &OverlapError{Blocker: claims[i].ID}
		}
	}

	for _, touch := range touches {
		if firstShared(touch.Addresses, normalized) != "" {
			return &TouchedBaseError{Version: touch.Version}
		}
	}

	return nil
}

// insert builds one accepted claim and stores it with its audit entry in the
// transaction tx. A store error, an invalid entry included, stores no claim.
func (r *ClaimRegistry) insert(
	ctx context.Context, tx ClaimTx, w Workspace, req *RegisterClaimRequest, normalized []string,
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

	entry := req.Audit
	entry.At = now
	entry.ObjectID = claim.ID
	entry.JobID = claim.JobID

	if err := tx.Insert(ctx, claim, entry); err != nil {
		return Claim{}, fmt.Errorf("record the accept of claim %s: %w", claim.ID, err)
	}

	return claim, nil
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
