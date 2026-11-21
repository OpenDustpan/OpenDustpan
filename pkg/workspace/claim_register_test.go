package workspace_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// claimWorkspaces is a ClaimWorkspaces fake: the workspaces it holds, or
// an injected lookup error.
type claimWorkspaces struct {
	mu     sync.Mutex
	spaces map[workspace.ID]workspace.Workspace
	err    error
}

func (f *claimWorkspaces) GetWorkspaceByID(
	_ context.Context, id workspace.ID,
) (workspace.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return workspace.Workspace{}, f.err
	}

	w, ok := f.spaces[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

// claimVersions is a ClaimVersions fake: the versions it holds by
// namespace, workspace and ID.
type claimVersions struct {
	mu       sync.Mutex
	versions map[versionKey]workspace.StateVersion
}

type versionKey struct {
	ns namespace.Name
	ws workspace.ID
	id workspace.StateVersionID
}

func (f *claimVersions) GetWorkspaceStateVersion(
	_ context.Context, ns namespace.Name, ws workspace.ID, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sv, ok := f.versions[versionKey{ns: ns, ws: ws, id: id}]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sv, nil
}

// claimDeleted is a ClaimDeleted fake: the IDs it reports as deleted.
type claimDeleted struct {
	deleted map[workspace.ID]bool
}

func (f *claimDeleted) IsDeleted(_ context.Context, id workspace.ID) (bool, error) {
	return f.deleted[id], nil
}

// claimFixture holds one live workspace and one base version.
type claimFixture struct {
	workspaces *claimWorkspaces
	versions   *claimVersions
	deleted    *claimDeleted
	ws         workspace.ID
	base       workspace.StateVersionID
	fence      namespace.Fence
	ns         namespace.Name
	store      *workspace.MemoryClaimStore
}

// newClaimFixture returns a fixture with workspace ws-1 in namespace acme
// and base version sv-1.
func newClaimFixture() *claimFixture {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		panic(err)
	}

	wsID := workspace.ID("ws-1")
	now := time.UnixMilli(1_700_000_000_000).UTC()
	w := workspace.Workspace{
		ID:           wsID,
		NamespaceID:  namespace.ID("ns-acme"),
		CreatedAt:    now,
		UpdatedAt:    now,
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		InCollection: false,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
		Ref: workspace.Ref{
			Namespace: ns,
			Name:      workspace.Name("network"),
		},
		Revision: 1,
	}
	base := workspace.StateVersionID("sv-1")

	return &claimFixture{
		workspaces: &claimWorkspaces{
			mu:     sync.Mutex{},
			spaces: map[workspace.ID]workspace.Workspace{wsID: w},
			err:    nil,
		},
		versions: &claimVersions{
			mu: sync.Mutex{},
			versions: map[versionKey]workspace.StateVersion{
				{ns: ns, ws: wsID, id: base}: fixtureBaseVersion(base, wsID),
			},
		},
		deleted: &claimDeleted{deleted: map[workspace.ID]bool{}},
		ws:      wsID,
		base:    base,
		fence:   namespace.Fence{Namespace: ns, FailoverVersion: 1},
		ns:      ns,
		store:   nil,
	}
}

// fixtureBaseVersion returns a finalized base state version with every
// field set explicitly.
func fixtureBaseVersion(base workspace.StateVersionID, wsID workspace.ID) workspace.StateVersion {
	return workspace.StateVersion{
		CreatedAt:       time.Time{},
		FinalizedAt:     time.Time{},
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		Holder:          "",
		LockID:          "",
		ID:              base,
		Workspace:       wsID,
		Lineage:         "",
		Status:          workspace.StatusFinalized,
		Content:         "",
		Outputs:         nil,
		MD5:             [16]byte{},
		Serial:          0,
		Size:            0,
		Force:           false,
		UnlockedWrite:   false,
		Sealed:          false,
		ClientEncrypted: workspace.ClientEncryptedUnknown,
		SensitiveMarks:  nil,
		RestoredFrom:    "",
		FenceBy:         "",
		Origin:          identity.StateOrigin{Job: "", Commit: ""},
	}
}

// registry returns a ClaimRegistry over the fixture with a fixed clock. The
// registry keeps its claims in the fixture store.
func (f *claimFixture) registry() *workspace.ClaimRegistry {
	now := time.UnixMilli(1_700_000_000_000).UTC()

	f.store = workspace.NewMemoryClaimStore(f.workspaces)

	r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: f.workspaces,
		Versions:   f.versions,
		Deleted:    f.deleted,
		Manual:     nil,
		Store:      f.store,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		panic(err)
	}

	return r
}

// claimAudit returns a valid audit entry of a claim accept in namespace
// acme. The registry sets its time, object ID and job ID.
func claimAudit() audit.Entry {
	ns, err := namespace.ParseName("acme")
	if err != nil {
		panic(err)
	}

	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: ns, Subject: "ada", IDPSubject: "", TokenID: "",
		Action: "unit:claim-register", Object: "unit:network", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// list returns the claims of the fixture workspace and fails the test on a
// store error.
func (f *claimFixture) list(t *testing.T, r *workspace.ClaimRegistry) []workspace.Claim {
	t.Helper()

	got, err := r.List(t.Context(), f.ws)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return got
}

// noteCommitted records a later version on the fixture workspace.
func (f *claimFixture) noteCommitted(
	t *testing.T, r *workspace.ClaimRegistry, id workspace.StateVersionID, addresses ...string,
) {
	t.Helper()

	if err := r.NoteCommitted(t.Context(), f.fence, f.ws, id, addresses); err != nil {
		t.Fatalf("note committed: %v", err)
	}
}

// register registers one claim of the fixture workspace and fails the test
// on any unexpected error.
func (f *claimFixture) register(
	t *testing.T, r *workspace.ClaimRegistry, job string, addresses ...string,
) workspace.Claim {
	t.Helper()

	claim, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       claimAudit(),
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   addresses,
		Principal:   "ada",
		JobID:       job,
		Replace:     false,
		Trigger:     "",
	}, f.fence)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	return claim
}

// TestClaimSecondDisjointAccepted proves that the server stores a second
// non-replace claim as accepted when its address set shares no address
// with each accepted claim, and keeps the first accepted.
func TestClaimSecondDisjointAccepted(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	first := f.register(t, r, "job-1", "aws_instance.a")
	second := f.register(t, r, "job-2", "aws_instance.b")

	if second.State != workspace.ClaimAccepted {
		t.Fatalf("second claim state = %q, want accepted", second.State)
	}

	if first.ID == second.ID {
		t.Fatalf("second claim ID = %q, want a new ID", second.ID)
	}

	if second.StartedAt.IsZero() || second.Principal != "ada" {
		t.Fatalf("second claim = %+v, want start time and principal", second)
	}

	got := f.list(t, r)
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("list = %v, want both claims in accept order", got)
	}
}

// TestClaimOverlapRefused proves that a claim that shares an address with
// an accepted claim fails with 409, names the blocking claim ID, and
// stores no claim.
func TestClaimOverlapRefused(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	first := f.register(t, r, "job-1", "aws_instance.a", "aws_instance.b")

	_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       claimAudit(),
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   []string{"aws_instance.b", "aws_instance.c"},
		Principal:   "ada",
		JobID:       "job-2",
		Replace:     false,
		Trigger:     "",
	}, f.fence)

	overlap, _ := errors.AsType[*workspace.OverlapError](err)
	if overlap == nil {
		t.Fatalf("register overlap = %v, want an overlap error", err)
	}

	if !errors.Is(err, workspace.ErrClaimOverlap) {
		t.Fatalf("register overlap = %v, want ErrClaimOverlap", err)
	}

	if overlap.Blocker != first.ID {
		t.Fatalf("blocker = %q, want %q", overlap.Blocker, first.ID)
	}

	if got := f.list(t, r); len(got) != 1 {
		t.Fatalf("list holds %d claims, want 1", len(got))
	}
}

// TestClaimEmptySetRejected proves that a claim with an empty resource set
// fails with 422 and stores no claim.
func TestClaimEmptySetRejected(t *testing.T) {
	t.Parallel()

	for _, addresses := range [][]string{nil, {}, {"  "}, {"", "  "}} {
		f := newClaimFixture()
		r := f.registry()

		_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: f.ws,
			Base:        f.base,
			Addresses:   addresses,
			Principal:   "ada",
			JobID:       "job-1",
			Replace:     false,
			Trigger:     "",
		}, f.fence)

		if !errors.Is(err, workspace.ErrClaimEmptySet) {
			t.Fatalf("register %q = %v, want ErrClaimEmptySet", addresses, err)
		}

		if got := f.list(t, r); len(got) != 0 {
			t.Fatalf("list holds %d claims, want none", len(got))
		}
	}
}

// TestClaimTouchedBaseRejected proves that a claim whose base is stale, a
// later version touched an address of the claim after its base, fails with
// 422, names the touching version, and stores no claim.
func TestClaimTouchedBaseRejected(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()
	f.noteCommitted(t, r, "sv-2", "aws_instance.a")

	_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       claimAudit(),
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   []string{"aws_instance.a"},
		Principal:   "ada",
		JobID:       "job-1",
		Replace:     false,
		Trigger:     "",
	}, f.fence)

	touched, _ := errors.AsType[*workspace.TouchedBaseError](err)
	if touched == nil {
		t.Fatalf("register touched base = %v, want a touched-base error", err)
	}

	if !errors.Is(err, workspace.ErrClaimTouchedBase) {
		t.Fatalf("register touched base = %v, want ErrClaimTouchedBase", err)
	}

	if touched.Version != "sv-2" {
		t.Fatalf("touching version = %q, want sv-2", touched.Version)
	}

	if got := f.list(t, r); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none", len(got))
	}
}

// TestClaimUntouchedOlderBaseAccepted proves that a claim on an older base
// is accepted when no later version touched an address of its disjoint
// set.
func TestClaimUntouchedOlderBaseAccepted(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()
	f.noteCommitted(t, r, "sv-2", "aws_instance.other")

	claim := f.register(t, r, "job-1", "aws_instance.a")

	if claim.State != workspace.ClaimAccepted || claim.Base != f.base {
		t.Fatalf("claim = %+v, want accepted on the older base", claim)
	}
}

// TestClaimReplaceRefused proves that a claim that holds a replace action
// fails with 409: the closure test cannot prove the replacement disjoint,
// and an unproven closure is a conflict. The server stores no claim.
func TestClaimReplaceRefused(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       claimAudit(),
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   []string{"aws_instance.a"},
		Replace:     true,
		Principal:   "ada",
		JobID:       "job-1",
		Trigger:     "",
	}, f.fence)

	if !errors.Is(err, workspace.ErrClaimReplace) {
		t.Fatalf("register replace = %v, want ErrClaimReplace", err)
	}

	if got := f.list(t, r); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none", len(got))
	}
}

// TestClaimSameJobRetryIdempotent proves that the same job with the same
// base and the same normalized set gets the same claim ID with no second
// record, and that a different set is a new claim.
func TestClaimSameJobRetryIdempotent(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	first := f.register(t, r, "job-1", "aws_instance.b", "aws_instance.a")
	retry := f.register(t, r, "job-1", "aws_instance.a", "aws_instance.b")

	if retry.ID != first.ID {
		t.Fatalf("retry ID = %q, want %q", retry.ID, first.ID)
	}

	if got := f.list(t, r); len(got) != 1 {
		t.Fatalf("list holds %d claims, want 1", len(got))
	}

	other := f.register(t, r, "job-1", "aws_instance.c")

	if other.ID == first.ID {
		t.Fatalf("different set ID = %q, want a new claim ID", other.ID)
	}

	if got := f.list(t, r); len(got) != 2 {
		t.Fatalf("list holds %d claims, want 2", len(got))
	}
}

// TestClaimWrapperRetryIsPerPrincipal proves that the local wrapper, which
// registers with no job ID, never gets the claim of another person: after
// the first claim ends, the same base and the same set from another
// principal is a new claim, while a retry from the same principal keeps
// the same claim ID.
func TestClaimWrapperRetryIsPerPrincipal(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	registerAs := func(principal string) workspace.Claim {
		t.Helper()

		claim, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: f.ws,
			Base:        f.base,
			Addresses:   []string{"aws_instance.a"},
			Principal:   principal,
			JobID:       "",
			Replace:     false,
			Trigger:     "",
		}, f.fence)
		if err != nil {
			t.Fatalf("register as %s: %v", principal, err)
		}

		return claim
	}

	first := registerAs("ada")
	retry := registerAs("ada")

	if retry.ID != first.ID {
		t.Fatalf("retry ID = %q, want %q", retry.ID, first.ID)
	}

	if n, err := r.ExpireStale(t.Context(), f.fence, first.StartedAt.Add(time.Minute)); err != nil || n != 1 {
		t.Fatalf("expired claims = %d, %v, want 1", n, err)
	}

	other := registerAs("bob")

	if other.ID == first.ID {
		t.Fatalf("other principal ID = %q, want a new claim ID", other.ID)
	}

	if other.State != workspace.ClaimAccepted || other.Principal != "bob" {
		t.Fatalf("other claim = %+v, want accepted with principal bob", other)
	}

	if got := f.list(t, r); len(got) != 2 {
		t.Fatalf("list holds %d claims, want 2", len(got))
	}
}

// TestClaimAcceptAuditEntry proves that the server writes one audit entry
// with the claim ID and the job ID when it accepts a claim, and that an
// invalid audit entry rolls the accept back.
func TestClaimAcceptAuditEntry(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()
	claim := f.register(t, r, "job-1", "aws_instance.a")

	entries := f.store.Entries()
	if len(entries) != 1 || entries[0].ObjectID != claim.ID || entries[0].JobID != "job-1" {
		t.Fatalf("audit entries = %+v, want one entry with the claim and job IDs", entries)
	}

	if !entries[0].At.Equal(claim.StartedAt) {
		t.Fatalf("audit entry time = %v, want %v", entries[0].At, claim.StartedAt)
	}

	failing := newClaimFixture()
	fr := failing.registry()

	var invalid audit.Entry

	_, err := fr.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       invalid,
		WorkspaceID: failing.ws,
		Base:        failing.base,
		Addresses:   []string{"aws_instance.a"},
		Principal:   "ada",
		JobID:       "job-1",
		Replace:     false,
		Trigger:     "",
	}, failing.fence)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("register with invalid audit entry = %v, want audit.ErrInvalidEntry", err)
	}

	if got := failing.list(t, fr); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none after the audit failure", len(got))
	}
}

// TestClaimBadWorkspaceOrBase proves that a claim on a missing workspace
// fails with 404, on a deleted workspace with 410, on a locked workspace
// with 422, and with an unknown base version with 422. The server stores
// no claim.
func TestClaimBadWorkspaceOrBase(t *testing.T) {
	t.Parallel()

	t.Run("missing workspace", func(t *testing.T) {
		t.Parallel()

		f := newClaimFixture()
		r := f.registry()

		_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: "ws-missing",
			Base:        f.base,
			Addresses:   []string{"aws_instance.a"},
			Principal:   "ada",
			JobID:       "job-1",
			Replace:     false,
			Trigger:     "",
		}, f.fence)

		if !errors.Is(err, workspace.ErrClaimNotFound) {
			t.Fatalf("register missing workspace = %v, want ErrClaimNotFound", err)
		}
	})

	t.Run("deleted workspace", func(t *testing.T) {
		t.Parallel()

		f := newClaimFixture()
		f.deleted.deleted[f.ws] = true
		delete(f.workspaces.spaces, f.ws)
		r := f.registry()

		_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: f.ws,
			Base:        f.base,
			Addresses:   []string{"aws_instance.a"},
			Principal:   "ada",
			JobID:       "job-1",
			Replace:     false,
			Trigger:     "",
		}, f.fence)

		if !errors.Is(err, workspace.ErrClaimDeleted) {
			t.Fatalf("register deleted workspace = %v, want ErrClaimDeleted", err)
		}
	})

	t.Run("locked workspace", func(t *testing.T) {
		t.Parallel()

		f := newClaimFixture()
		w := f.workspaces.spaces[f.ws]
		w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "lock-1", Holder: "bob", Reason: ""}
		f.workspaces.spaces[f.ws] = w
		r := f.registry()

		_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: f.ws,
			Base:        f.base,
			Addresses:   []string{"aws_instance.a"},
			Principal:   "ada",
			JobID:       "job-1",
			Replace:     false,
			Trigger:     "",
		}, f.fence)

		if !errors.Is(err, workspace.ErrClaimLocked) {
			t.Fatalf("register locked workspace = %v, want ErrClaimLocked", err)
		}
	})

	t.Run("unknown base", func(t *testing.T) {
		t.Parallel()

		f := newClaimFixture()
		r := f.registry()

		_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
			ViaJobToken: false,
			Audit:       claimAudit(),
			WorkspaceID: f.ws,
			Base:        "sv-unknown",
			Addresses:   []string{"aws_instance.a"},
			Principal:   "ada",
			JobID:       "job-1",
			Replace:     false,
			Trigger:     "",
		}, f.fence)

		if !errors.Is(err, workspace.ErrClaimBadBase) {
			t.Fatalf("register unknown base = %v, want ErrClaimBadBase", err)
		}

		if got := f.list(t, r); len(got) != 0 {
			t.Fatalf("list holds %d claims, want none", len(got))
		}
	})
}

// TestClaimStaleFenceRefused proves that a write with a stale fence is
// refused, as in the fenced stores.
func TestClaimStaleFenceRefused(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry()

	f.register(t, r, "job-1", "aws_instance.a")

	stale := f.fence
	stale.FailoverVersion++

	_, err := r.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		Audit:       claimAudit(),
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   []string{"aws_instance.b"},
		Principal:   "ada",
		JobID:       "job-2",
		Replace:     false,
		Trigger:     "",
	}, stale)

	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("register stale fence = %v, want ErrFenced", err)
	}
}
