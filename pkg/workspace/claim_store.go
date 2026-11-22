package workspace

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ClaimTouch is one committed version with the addresses it touched. A
// claim whose set shares an address with a touch that came after its base
// is stale.
type ClaimTouch struct {
	// Version is the state version that touched the addresses.
	Version StateVersionID
	// Addresses is the normalized set that the version touched.
	Addresses []string
}

// ClaimStore is the port of the claim registry: it persists the apply
// claims of the workspaces. The SQL adapters and MemoryClaimStore run the
// same conformance suite (workspacetest.RunClaimStore).
//
// Every method returns an error that matches namespace.ErrFenced for a stale
// fence and ErrNotFound for a workspace that does not exist in the namespace
// of the fence. An adapter never returns a driver error to a caller.
type ClaimStore interface {
	// WithClaims runs fn in one fenced transaction that holds the claim lock
	// of the workspace ws: two calls for the same workspace run one after the
	// other, so two registrations that race cannot both pass over the same
	// accepted set. The fence is checked in the same transaction, and the
	// workspace must be in the namespace of the fence. An error of fn rolls
	// back every write of the transaction, and WithClaims returns it.
	WithClaims(ctx context.Context, ws ID, fence namespace.Fence, fn func(tx ClaimTx) error) error
	// ListClaims returns the claims of the workspace ws in accept order,
	// oldest first. A workspace with no claims gives an empty list.
	ListClaims(ctx context.Context, ws ID) ([]Claim, error)
	// EndClaimsByJob marks each accepted claim of the job ended at the time
	// at, in the workspaces of the namespace of the fence, in one fenced
	// transaction. It returns the count of claims it ended. An empty job ID
	// ends nothing.
	EndClaimsByJob(ctx context.Context, fence namespace.Fence, jobID string, at time.Time) (int, error)
	// ExpireClaims marks each accepted claim of the namespace of the fence
	// whose lease ended before now as ended at now, in one fenced transaction,
	// with one audit entry for each. A claim with no lease (a claim of a job)
	// never expires here. It ends at most limit claims, and returns the count; a
	// SQL adapter ends the oldest lease first. A limit below 1 ends none.
	ExpireClaims(ctx context.Context, fence namespace.Fence, now time.Time, limit int) (int, error)
	// RenewClaim extends the lease of one claim in one fenced transaction under
	// the claim lock of its workspace, and returns the claim. It returns
	// ErrClaimNotFound for a claim that is not on the workspace or that another
	// principal registered, ErrClaimEnded for a claim that ended or whose lease
	// ended before r.Now, and ErrClaimInvalid for a claim with no lease.
	RenewClaim(ctx context.Context, fence namespace.Fence, r ClaimRenewal) (Claim, error)
	// AddClaimTouch records that the version committed on the workspace ws
	// with the addresses, in one fenced transaction. It returns ErrNotFound
	// for a workspace that is not in the namespace of the fence.
	AddClaimTouch(
		ctx context.Context, fence namespace.Fence, ws ID, version StateVersionID, addresses []string,
	) error
}

// ClaimRenewal asks a store to extend the lease of one claim.
type ClaimRenewal struct {
	// Workspace is the workspace of the claim.
	Workspace ID
	// ClaimID is the ID of the claim.
	ClaimID string
	// Principal is the subject that registered the claim.
	Principal string
	// Now is the time of the renewal on the clock of the caller.
	Now time.Time
	// Lease is how long the claim lives from Now.
	Lease time.Duration
}

// ClaimTx is the view of one workspace inside WithClaims.
type ClaimTx interface {
	// Workspace reads the workspace as the transaction sees it, under the
	// claim lock: its lock columns are the ones that a claim must respect. It
	// returns ErrNotFound for a workspace that is gone.
	Workspace(ctx context.Context) (Workspace, error)
	// ManualLocks returns every manual lock that covers the workspace, as the
	// transaction reads the locks, and nil when none does. A workspace that the
	// inventory does not hold is an error, so the check fails closed. It does
	// not apply the rule that a phase that started before a lock passes it:
	// the caller does, with manuallock.LockForPhase. An implementation reads
	// the locks in the transaction and calls no service that has a store of
	// its own: such a call under the claim lock can wait for a lock that this
	// transaction holds.
	ManualLocks(ctx context.Context) ([]manuallock.Lock, error)
	// Claims returns every claim of the workspace in accept order.
	Claims(ctx context.Context) ([]Claim, error)
	// Touches returns every recorded touch of the workspace, oldest first.
	Touches(ctx context.Context) ([]ClaimTouch, error)
	// Insert stores the accepted claim c and appends the audit entry in the
	// same transaction, so no claim goes without its entry. An entry that
	// fails validation or names another namespace than the fence changes
	// nothing and returns audit.ErrInvalidEntry.
	Insert(ctx context.Context, c Claim, entry audit.Entry) error
}

// MemoryClaimStore is the ClaimStore that keeps claims in memory. A restart
// forgets them, so a server never uses it: it serves tests and the
// conformance suite. Its zero value is not usable; call NewMemoryClaimStore.
type MemoryClaimStore struct {
	mu      sync.Mutex
	claims  map[ID][]Claim
	touches map[ID][]ClaimTouch
	entries []audit.Entry
	// fences holds the failover version first seen for each namespace.
	fences map[namespace.Name]int64
	// manualLocks returns the manual locks that cover a workspace. Nil means
	// that none does.
	manualLocks func(ctx context.Context, ws ID) []manuallock.Lock
	// workspaces resolves the namespace of a workspace, so a fence of
	// another namespace is refused as an adapter with a workspaces table
	// refuses it.
	workspaces ClaimWorkspaces
}

// NewMemoryClaimStore returns an empty MemoryClaimStore that reads the
// namespace of each workspace from workspaces.
func NewMemoryClaimStore(workspaces ClaimWorkspaces) *MemoryClaimStore {
	return &MemoryClaimStore{
		mu:          sync.Mutex{},
		claims:      make(map[ID][]Claim),
		touches:     make(map[ID][]ClaimTouch),
		entries:     nil,
		fences:      make(map[namespace.Name]int64),
		workspaces:  workspaces,
		manualLocks: nil,
	}
}

// SetManualLocks sets the function that returns the manual locks that cover a
// workspace, for the tests of the check under the claim lock. Call it before
// the first use.
func (m *MemoryClaimStore) SetManualLocks(covering func(ctx context.Context, ws ID) []manuallock.Lock) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.manualLocks = covering
}

// Entries returns a copy of the audit entries that the store appended.
func (m *MemoryClaimStore) Entries() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.entries)
}

// List returns the audit entries that the store appended in the namespace ns,
// as the audit port of a SQL store reads them. It ignores the page: the
// store holds few entries.
func (m *MemoryClaimStore) List(_ context.Context, ns namespace.Name, _ audit.Page) (audit.Listing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out audit.Listing

	for i := range m.entries {
		if m.entries[i].Namespace == ns {
			out.Entries = append(out.Entries, m.entries[i])
		}
	}

	return out, nil
}

// WithClaims implements ClaimStore.
func (m *MemoryClaimStore) WithClaims(
	ctx context.Context, ws ID, fence namespace.Fence, fn func(tx ClaimTx) error,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("claims of %s: %w", ws, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	w, err := m.workspaces.GetWorkspaceByID(ctx, ws)
	if err != nil {
		return err
	}

	if w.Ref.Namespace != fence.Namespace {
		return fmt.Errorf("%w: workspace %s is not in namespace %q", ErrNotFound, ws, fence.Namespace)
	}

	tx := &memoryClaimTx{store: m, ws: ws, fence: fence, pending: nil, entry: nil}

	if err := fn(tx); err != nil {
		return err
	}

	// The transaction commits only when fn returns nil.
	for i := range tx.pending {
		m.claims[ws] = append(m.claims[ws], tx.pending[i])
		m.entries = append(m.entries, *tx.entry[i])
	}

	return nil
}

// ListClaims implements ClaimStore.
func (m *MemoryClaimStore) ListClaims(_ context.Context, ws ID) ([]Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return cloneClaims(m.claims[ws]), nil
}

// EndClaimsByJob implements ClaimStore.
func (m *MemoryClaimStore) EndClaimsByJob(
	ctx context.Context, fence namespace.Fence, jobID string, at time.Time,
) (int, error) {
	ended := at.UTC().Truncate(time.Millisecond)

	return m.endWhere(ctx, fence, &endSpec{
		at: ended, cause: ClaimEndedByJob, limit: math.MaxInt,
		match: func(c *Claim) bool { return jobID != "" && c.JobID == jobID && c.State == ClaimAccepted },
	})
}

// ExpireClaims implements ClaimStore.
func (m *MemoryClaimStore) ExpireClaims(
	ctx context.Context, fence namespace.Fence, now time.Time, limit int,
) (int, error) {
	ended := now.UTC().Truncate(time.Millisecond)

	return m.endWhere(ctx, fence, &endSpec{
		at: ended, cause: ClaimEndedByExpiry, limit: limit,
		match: func(c *Claim) bool {
			return c.State == ClaimAccepted && !c.LeaseEndsAt.IsZero() && c.LeaseEndsAt.Before(now)
		},
	})
}

// RenewClaim implements ClaimStore.
func (m *MemoryClaimStore) RenewClaim(
	ctx context.Context, fence namespace.Fence, r ClaimRenewal,
) (Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return Claim{}, err
	}

	w, err := m.workspaces.GetWorkspaceByID(ctx, r.Workspace)
	if err != nil {
		return Claim{}, err
	}

	if w.Ref.Namespace != fence.Namespace {
		return Claim{}, fmt.Errorf("%w: workspace %s is not in namespace %q", ErrNotFound, r.Workspace, fence.Namespace)
	}

	list := m.claims[r.Workspace]
	for i := range list {
		if list[i].ID != r.ClaimID {
			continue
		}

		if checkErr := CheckRenewable(&list[i], r); checkErr != nil {
			return Claim{}, checkErr
		}

		list[i].LeaseEndsAt = r.Now.Add(r.Lease).UTC().Truncate(time.Millisecond)
		list[i].RenewedAt = r.Now.UTC().Truncate(time.Millisecond)

		return cloneClaims(list[i : i+1])[0], nil
	}

	return Claim{}, fmt.Errorf("%w: claim %s", ErrClaimNotFound, r.ClaimID)
}

// CheckRenewed checks a claim that a renewal returned, against the renewal
// that asked for it: it must be the claim of the request, on the workspace of
// the request, of the principal of the request, and it must hold a lease. A
// claim that is not is a claim that the caller may not renew, and the answer
// is ErrClaimNotFound, as for a claim that does not exist. It reads no store.
//
// Defense in depth: the renew statement holds the same rules in its WHERE
// clause, and the store checks the rules before the statement. Each layer
// applies this check to the row that the layer below gave, so a row of another
// principal never leaves a layer that was handed one.
func CheckRenewed(c *Claim, r ClaimRenewal) error {
	if c.ID != r.ClaimID || c.WorkspaceID != r.Workspace || c.Principal != r.Principal || c.LeaseEndsAt.IsZero() {
		return fmt.Errorf("%w: claim %s", ErrClaimNotFound, r.ClaimID)
	}

	return nil
}

// CheckRenewable returns the error of RenewClaim for a claim that cannot be
// renewed as r asks, or nil. The adapters of ClaimStore call it, so each
// refuses the same claims as MemoryClaimStore: a claim of another principal is
// ErrClaimNotFound, a claim that ended or whose lease ended is ErrClaimEnded,
// and a claim with no lease is ErrClaimInvalid.
func CheckRenewable(c *Claim, r ClaimRenewal) error {
	switch {
	case c.Principal != r.Principal:
		return fmt.Errorf("%w: claim %s", ErrClaimNotFound, c.ID)
	case c.State != ClaimAccepted:
		return fmt.Errorf("%w: claim %s", ErrClaimEnded, c.ID)
	case c.LeaseEndsAt.IsZero():
		return fmt.Errorf("%w: claim %s has no lease", ErrClaimInvalid, c.ID)
	case !c.LeaseEndsAt.After(r.Now):
		return fmt.Errorf("%w: the lease of claim %s ended", ErrClaimEnded, c.ID)
	default:
		return nil
	}
}

// AddClaimTouch implements ClaimStore.
func (m *MemoryClaimStore) AddClaimTouch(
	ctx context.Context, fence namespace.Fence, ws ID, version StateVersionID, addresses []string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	w, err := m.workspaces.GetWorkspaceByID(ctx, ws)
	if err != nil {
		return err
	}

	if w.Ref.Namespace != fence.Namespace {
		return fmt.Errorf("%w: workspace %s is not in namespace %q", ErrNotFound, ws, fence.Namespace)
	}

	if version == "" {
		return fmt.Errorf("%w: a touch names its version", ErrClaimInvalid)
	}

	m.touches[ws] = append(m.touches[ws], ClaimTouch{
		Version:   version,
		Addresses: NormalizeClaimAddresses(addresses),
	})

	return nil
}

// endSpec says which claims endWhere ends, when and why.
type endSpec struct {
	at    time.Time
	cause string
	// limit is the most claims that one call ends.
	limit int
	match func(c *Claim) bool
}

// endWhere ends each claim that the match of spec accepts, in the workspaces
// of the namespace of the fence, up to the limit of spec.
func (m *MemoryClaimStore) endWhere(
	ctx context.Context, fence namespace.Fence, spec *endSpec,
) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return 0, err
	}

	count := 0

	for ws, list := range m.claims {
		w, err := m.workspaces.GetWorkspaceByID(ctx, ws)
		if err != nil || w.Ref.Namespace != fence.Namespace {
			continue
		}

		for i := range list {
			if count >= spec.limit || !spec.match(&list[i]) {
				continue
			}

			entry, err := ClaimEndEntry(&ClaimEnd{
				Namespace: fence.Namespace, Workspace: w.Ref.Name, ClaimID: list[i].ID, JobID: list[i].JobID,
				Cause: spec.cause, At: spec.at,
			})
			if err != nil {
				return 0, err
			}

			list[i].State = ClaimEnded
			list[i].EndedAt = spec.at
			m.entries = append(m.entries, entry)
			count++
		}
	}

	return count, nil
}

// checkFence refuses a fence whose failover version differs from the one
// first seen for its namespace, as the fenced stores do. The first fence of a
// namespace sets the version.
func (m *MemoryClaimStore) checkFence(fence namespace.Fence) error {
	seen, ok := m.fences[fence.Namespace]
	if !ok {
		m.fences[fence.Namespace] = fence.FailoverVersion

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

// memoryClaimTx is the transaction of a MemoryClaimStore: its writes stay
// pending until fn returns nil.
type memoryClaimTx struct {
	store   *MemoryClaimStore
	ws      ID
	fence   namespace.Fence
	pending []Claim
	entry   []*audit.Entry
}

// Workspace implements ClaimTx.
func (t *memoryClaimTx) Workspace(ctx context.Context) (Workspace, error) {
	return t.store.workspaces.GetWorkspaceByID(ctx, t.ws)
}

// ManualLocks implements ClaimTx.
func (t *memoryClaimTx) ManualLocks(ctx context.Context) ([]manuallock.Lock, error) {
	if t.store.manualLocks == nil {
		return nil, nil
	}

	return t.store.manualLocks(ctx, t.ws), nil
}

// Claims implements ClaimTx.
func (t *memoryClaimTx) Claims(context.Context) ([]Claim, error) {
	return cloneClaims(append(slices.Clone(t.store.claims[t.ws]), t.pending...)), nil
}

// Touches implements ClaimTx.
func (t *memoryClaimTx) Touches(context.Context) ([]ClaimTouch, error) {
	list := t.store.touches[t.ws]
	out := make([]ClaimTouch, len(list))

	for i := range list {
		out[i] = ClaimTouch{Version: list[i].Version, Addresses: slices.Clone(list[i].Addresses)}
	}

	return out, nil
}

// Insert implements ClaimTx.
func (t *memoryClaimTx) Insert(_ context.Context, c Claim, entry audit.Entry) error {
	// Defense in depth: the registry validates the claim and the entry too.
	// These checks stay so that a new caller of the store cannot insert a
	// claim that the registry refuses, as the SQL adapters do.
	if err := ValidateNewClaim(c); err != nil {
		return err
	}

	if c.WorkspaceID != t.ws {
		return fmt.Errorf("%w: claim names workspace %s, transaction holds %s", ErrClaimInvalid, c.WorkspaceID, t.ws)
	}

	if err := validateClaimEntry(t.fence, entry); err != nil {
		return err
	}

	c.Addresses = slices.Clone(c.Addresses)
	t.pending = append(t.pending, c)
	t.entry = append(t.entry, &entry)

	return nil
}

// validateClaimEntry returns audit.ErrInvalidEntry when the entry fails
// validation or names another namespace than the fence. The SQL adapters
// check the same.
func validateClaimEntry(fence namespace.Fence, entry audit.Entry) error {
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if entry.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, entry.Namespace, fence.Namespace)
	}

	return nil
}

// CheckClaimEntry is validateClaimEntry for the adapters of ClaimStore, so that
// each adapter refuses the same entries as MemoryClaimStore.
func CheckClaimEntry(fence namespace.Fence, entry audit.Entry) error {
	return validateClaimEntry(fence, entry)
}

// cloneClaims returns a deep copy of the claims.
func cloneClaims(list []Claim) []Claim {
	out := make([]Claim, len(list))

	for i := range list {
		out[i] = list[i]
		out[i].Addresses = slices.Clone(list[i].Addresses)
	}

	return out
}

// checkClaimBounds returns ErrClaimTooLarge when the set holds more than
// MaxClaimAddresses addresses or an address longer than MaxClaimAddressLength
// bytes.
func checkClaimBounds(addresses []string) error {
	if len(addresses) > MaxClaimAddresses {
		return fmt.Errorf("%w: %d addresses, at most %d", ErrClaimTooLarge, len(addresses), MaxClaimAddresses)
	}

	for _, a := range addresses {
		if len(a) > MaxClaimAddressLength {
			return fmt.Errorf("%w: an address is longer than %d bytes", ErrClaimTooLarge, MaxClaimAddressLength)
		}
	}

	return nil
}

// ValidateNewClaim checks a claim that a store is about to insert. It
// returns ErrClaimTooLarge for a set over the bounds, and ErrClaimInvalid
// for an empty ID, workspace, base or principal, a state other than
// accepted, or an address set that is empty or is not normalized (trimmed,
// sorted and without duplicates). Each store calls it before the insert, as
// the last layer under the registry.
func ValidateNewClaim(c Claim) error {
	switch {
	case c.ID == "" || c.WorkspaceID == "" || c.Base == "" || c.Principal == "":
		return fmt.Errorf("%w: ID, workspace, base and principal are required", ErrClaimInvalid)
	case c.State != ClaimAccepted || !c.EndedAt.IsZero():
		return fmt.Errorf("%w: a new claim is accepted and has no end time", ErrClaimInvalid)
	case (c.JobID == "") == c.LeaseEndsAt.IsZero():
		return fmt.Errorf("%w: a claim of a job has no lease, and a claim with no job has one", ErrClaimInvalid)
	case !c.RenewedAt.IsZero():
		return fmt.Errorf("%w: a new claim has no renewal time", ErrClaimInvalid)
	case len(c.Addresses) == 0:
		return ErrClaimEmptySet
	}

	if err := checkClaimBounds(c.Addresses); err != nil {
		return err
	}

	if !slices.Equal(c.Addresses, NormalizeClaimAddresses(c.Addresses)) {
		return fmt.Errorf("%w: the address set is not normalized", ErrClaimInvalid)
	}

	return nil
}
