package workspace

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
	// that started before the cutoff ended at the time cutoff, in one fenced
	// transaction. It returns the count of claims it ended.
	ExpireClaims(ctx context.Context, fence namespace.Fence, cutoff time.Time) (int, error)
	// AddClaimTouch records that the version committed on the workspace ws
	// with the addresses, in one fenced transaction. It returns ErrNotFound
	// for a workspace that is not in the namespace of the fence.
	AddClaimTouch(
		ctx context.Context, fence namespace.Fence, ws ID, version StateVersionID, addresses []string,
	) error
}

// ClaimTx is the view of one workspace inside WithClaims.
type ClaimTx interface {
	// Workspace reads the workspace as the transaction sees it, under the
	// claim lock: its lock columns are the ones that a claim must respect. It
	// returns ErrNotFound for a workspace that is gone.
	Workspace(ctx context.Context) (Workspace, error)
	// ManualLocked reports whether a manual lock covers the workspace, as the
	// transaction reads the locks. It does not apply the rule that a phase
	// that started before the lock passes it: the caller does.
	ManualLocked(ctx context.Context) (bool, error)
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
	// manualLocked tells whether a manual lock covers a workspace. Nil means
	// that none does.
	manualLocked func(ctx context.Context, ws ID) bool
	// workspaces resolves the namespace of a workspace, so a fence of
	// another namespace is refused as an adapter with a workspaces table
	// refuses it.
	workspaces ClaimWorkspaces
}

// NewMemoryClaimStore returns an empty MemoryClaimStore that reads the
// namespace of each workspace from workspaces.
func NewMemoryClaimStore(workspaces ClaimWorkspaces) *MemoryClaimStore {
	return &MemoryClaimStore{
		mu:           sync.Mutex{},
		claims:       make(map[ID][]Claim),
		touches:      make(map[ID][]ClaimTouch),
		entries:      nil,
		fences:       make(map[namespace.Name]int64),
		workspaces:   workspaces,
		manualLocked: nil,
	}
}

// SetManualLocked sets the function that tells which workspaces a manual
// lock covers, for the tests of the check under the claim lock. Call it
// before the first use.
func (m *MemoryClaimStore) SetManualLocked(covers func(ctx context.Context, ws ID) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.manualLocked = covers
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

	return m.endWhere(ctx, fence, ended, ClaimEndedByJob, func(c *Claim) bool {
		return jobID != "" && c.JobID == jobID && c.State == ClaimAccepted
	})
}

// ExpireClaims implements ClaimStore.
func (m *MemoryClaimStore) ExpireClaims(ctx context.Context, fence namespace.Fence, cutoff time.Time) (int, error) {
	ended := cutoff.UTC().Truncate(time.Millisecond)

	return m.endWhere(ctx, fence, ended, ClaimEndedByExpiry, func(c *Claim) bool {
		return c.State == ClaimAccepted && c.StartedAt.Before(cutoff)
	})
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

// endWhere ends each claim that match accepts, in the workspaces of the
// namespace of the fence.
func (m *MemoryClaimStore) endWhere(
	ctx context.Context, fence namespace.Fence, ended time.Time, cause string, match func(c *Claim) bool,
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
			if !match(&list[i]) {
				continue
			}

			entry, err := ClaimEndEntry(&ClaimEnd{
				Namespace: fence.Namespace, Workspace: w.Ref.Name, ClaimID: list[i].ID, JobID: list[i].JobID,
				Cause: cause, At: ended,
			})
			if err != nil {
				return 0, err
			}

			list[i].State = ClaimEnded
			list[i].EndedAt = ended
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

// ManualLocked implements ClaimTx.
func (t *memoryClaimTx) ManualLocked(ctx context.Context) (bool, error) {
	return t.store.manualLocked != nil && t.store.manualLocked(ctx, t.ws), nil
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
