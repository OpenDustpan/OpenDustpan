package workspacetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ClaimBackend is what a claim conformance run needs from an adapter: the
// claim port, the read ports of the registry, and the writes that seed a
// namespace, a workspace and a base state version. The SQL stores implement
// it. MemoryClaimBackend implements it for the in-memory claim store.
type ClaimBackend interface {
	workspace.ClaimStore
	workspace.ClaimWorkspaces
	workspace.ClaimVersions
	workspace.ClaimDeleted
	claimAuditReader
	claimSeeder
}

// claimAuditReader reads the audit entries of a namespace, newest first, as
// the audit port of a store does.
type claimAuditReader interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

// claimSeeder is the part of a backend that seeds the rows of the cases: a
// namespace, a workspace, a base state version, a lock and a delete.
type claimSeeder interface {
	CreateNamespace(ctx context.Context, ns namespace.Namespace) error
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	CreateStateVersion(ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int) error
	// SaveWorkspace stores w, for the case that locks a workspace.
	SaveWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	// DeleteWorkspace soft-deletes the workspace of ref.
	DeleteWorkspace(ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion) error
	// PutLock puts a manual lock, as the lock store does.
	PutLock(ctx context.Context, l manuallock.Lock, fence namespace.Fence, e audit.Entry) error
}

// ClaimBackendFactory returns an empty backend for one test. The adapter
// closes it with t.Cleanup.
type ClaimBackendFactory func(t *testing.T) ClaimBackend

// claimNowMs is the fixed clock of the registries of the suite, in Unix
// milliseconds.
const claimNowMs = 1_700_000_000_000

// Values that the cases repeat.
const (
	claimPrincipal = "ada"
	claimJob1      = "job-1"
	claimJob2      = "job-2"
	// claimPair is the count of claims in the cases that register two.
	claimPair = 2
	// claimTouchVersion is the ID of the later version in the touch cases.
	claimTouchVersion = "sv-2"
)

// claimNow returns the fixed time of the suite.
func claimNow() time.Time { return time.UnixMilli(claimNowMs).UTC() }

// claimWorld holds two namespaces with one workspace each, and one base
// state version on each workspace.
type claimWorld struct {
	backend ClaimBackend
	// registry is the registry under test, over the backend.
	registry *workspace.ClaimRegistry
	// clock is the time of the registries, in Unix milliseconds. A case moves
	// it with advance.
	clock *atomic.Int64
	acme  namespace.Fence
	other namespace.Fence
	// wsAcme is a workspace of acme, wsOther a workspace of other.
	wsAcme, wsOther workspace.ID
	// wsAcme2 is a second workspace of acme.
	wsAcme2             workspace.ID
	baseAcme, baseOther workspace.StateVersionID
}

// now is the clock of the registries of the world.
func (w *claimWorld) now() time.Time { return time.UnixMilli(w.clock.Load()).UTC() }

// advance moves the clock of the registries forward by d.
func (w *claimWorld) advance(d time.Duration) { w.clock.Add(d.Milliseconds()) }

// RunClaimStore runs the conformance suite of the claim port, and of the
// claim registry over it, against the backends that newBackend returns. Every
// adapter of workspace.ClaimStore runs it. Each case sends hostile input
// straight to one layer: the store (WithClaims, Insert and the write
// methods) or the registry (Register), as if every other layer failed.
func RunClaimStore(t *testing.T, newBackend ClaimBackendFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, w *claimWorld)
	}{
		{"disjoint claims both accepted", claimDisjoint},
		{"overlap refused and names the blocker", claimOverlap},
		{"same job retry is idempotent", claimRetry},
		{"hostile register requests", claimHostileRegister},
		{"claim on a workspace of another namespace", claimOtherNamespace},
		{"stale fence", claimStaleFence},
		{"hostile store transactions", claimHostileStore},
		{"hostile claim records", claimHostileRecords},
		{"rollback on a failing transaction", claimRollback},
		{"release by job", claimReleaseByJob},
		{"lease set at register", claimLeaseAtRegister},
		{"expire by lease end", claimExpire},
		{"expire is bounded", claimExpireBounded},
		{"renew a lease", claimRenew},
		{"renew by a clock that is behind", claimRenewClockBack},
		{"touched base", claimTouchedBase},
		{"ended claim frees its addresses", claimEndedFrees},
		{"lock taken after the pre-check", claimLockAfterPreCheck},
		{"manual lock taken after the pre-check", claimManualAfterPreCheck},
		{"manual lock: a phase before the lock passes it", claimManualPhaseBeforeLock},
		{"manual lock: a phase after the lock does not pass it", claimManualPhaseAfterLock},
		{"manual lock store that fails", claimManualFailsClosed},
		{"soft-deleted workspace", claimDeletedWorkspace},
		{"parallel registrations", claimParallel},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			c.run(t, newClaimWorld(t, newBackend(t)))
		})
	}
}

// newClaimWorld seeds the backend and builds the registry.
func newClaimWorld(t *testing.T, b ClaimBackend) *claimWorld {
	t.Helper()

	var noFence namespace.Fence

	clock := new(atomic.Int64)
	clock.Store(claimNowMs)

	w := &claimWorld{
		backend: b, registry: nil, clock: clock, acme: noFence, other: noFence, wsAcme: "", wsOther: "", wsAcme2: "",
		baseAcme: "", baseOther: "",
	}

	w.acme, w.wsAcme, w.baseAcme = seedClaimNamespace(t, b, "acme", "ws-acme", "sv-acme")
	w.other, w.wsOther, w.baseOther = seedClaimNamespace(t, b, "other", "ws-other", "sv-other")
	w.wsAcme2 = w.wsAcme + "-2"

	r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: b,
		Versions:   b,
		Deleted:    b,
		Manual:     nil,
		Store:      b,
		Now:        w.now,
		Lease:      0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	w.registry = r

	return w
}

// seedClaimNamespace creates one namespace, two workspaces and a base state
// version on each, and returns the fence, the ID of the first workspace and
// its base ID.
func seedClaimNamespace(
	t *testing.T, b ClaimBackend, name string, wsID workspace.ID, base workspace.StateVersionID,
) (namespace.Fence, workspace.ID, workspace.StateVersionID) {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:           claimNow(),
		ID:                  namespace.NewID(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "local",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	if err := b.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	fence := ns.Fence()

	createClaimWorkspace(t, b, &ns, &claimSeed{id: wsID, name: "network", base: base})
	// A second workspace in the namespace: a case sends the claim of one
	// workspace to the other.
	createClaimWorkspace(t, b, &ns, &claimSeed{id: wsID + "-2", name: "network-2", base: base + "-2"})

	return fence, wsID, base
}

// claimSeed names a workspace that a case creates, and the base version on it.
type claimSeed struct {
	id   workspace.ID
	name workspace.Name
	base workspace.StateVersionID
}

// createClaimWorkspace creates the workspace of seed in the namespace ns, and
// its base state version.
func createClaimWorkspace(t *testing.T, b ClaimBackend, ns *namespace.Namespace, seed *claimSeed) {
	t.Helper()

	fence, wsID, name, base := ns.Fence(), seed.id, seed.name, seed.base

	w := workspace.Workspace{
		NamespaceID:  ns.ID,
		CreatedAt:    claimNow(),
		UpdatedAt:    claimNow(),
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		InCollection: false,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
		ID:           wsID,
		Ref:          workspace.Ref{Namespace: ns.Name, Name: name},
		Revision:     1,
	}

	if err := b.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("create workspace %s: %v", wsID, err)
	}

	sv := workspace.StateVersion{
		CreatedAt:       time.Time{},
		FinalizedAt:     time.Time{},
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		Holder:          "",
		LockID:          "",
		ID:              base,
		Workspace:       wsID,
		Lineage:         "lin-1",
		Status:          workspace.StatusPending,
		Content:         "",
		Outputs:         nil,
		MD5:             [16]byte{},
		Serial:          1,
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

	if err := b.CreateStateVersion(t.Context(), sv, fence, workspace.DefaultMaxPendingVersions); err != nil {
		t.Fatalf("create base version %s: %v", base, err)
	}
}

// claimEntry returns a valid audit entry of an accept in the namespace of the
// fence. The registry sets its time, its object ID and its job ID.
func claimEntry(fence namespace.Fence) audit.Entry {
	return audit.Entry{
		At: claimNow(), Namespace: fence.Namespace, Subject: claimPrincipal, IDPSubject: "", TokenID: "",
		Action: string(identity.ActionUnitClaimRegister), Object: "unit:network", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// request returns a register request on the acme workspace.
func (w *claimWorld) request(job string, addresses ...string) *workspace.RegisterClaimRequest {
	return &workspace.RegisterClaimRequest{
		ViaJobToken: false,
		WorkspaceID: w.wsAcme,
		Base:        w.baseAcme,
		Addresses:   addresses,
		Replace:     false,
		Principal:   claimPrincipal,
		JobID:       job,
		Trigger:     "",
		Audit:       claimEntry(w.acme),
	}
}

// register registers a claim on the acme workspace and fails the test on an
// error.
func (w *claimWorld) register(t *testing.T, job string, addresses ...string) workspace.Claim {
	t.Helper()

	c, err := w.registry.Register(t.Context(), w.request(job, addresses...), w.acme)
	if err != nil {
		t.Fatalf("register %s: %v", job, err)
	}

	return c
}

// list returns the claims of a workspace and fails the test on an error.
func (w *claimWorld) list(t *testing.T, ws workspace.ID) []workspace.Claim {
	t.Helper()

	got, err := w.backend.ListClaims(t.Context(), ws)
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}

	return got
}

// entriesOf returns the audit entries of the acme namespace that name the
// claim id.
func (w *claimWorld) entriesOf(t *testing.T, id string) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for before := int64(0); ; {
		listing, err := w.backend.List(
			t.Context(),
			w.acme.Namespace,
			audit.Page{Size: audit.MaxPageSize, Before: before},
		)
		if err != nil {
			t.Fatalf("list audit entries: %v", err)
		}

		for i := range listing.Entries {
			if listing.Entries[i].ObjectID == id {
				out = append(out, listing.Entries[i])
			}
		}

		if n := len(listing.Entries); n > 0 {
			before = listing.Entries[n-1].Sequence
		}

		if !listing.More {
			return out
		}
	}
}

// wantEndEntry fails the test unless the audit log holds, for the claim id,
// the entry of its accept and exactly one entry of its end by the server with
// the cause and the job.
func (w *claimWorld) wantEndEntry(t *testing.T, id, cause, jobID string) {
	t.Helper()

	var accepts, ends int

	entries := w.entriesOf(t, id)

	for i := range entries {
		e := &entries[i]

		switch {
		case e.Subject == "@server" && strings.Contains(e.Reason, cause) && e.JobID == jobID:
			ends++
		case e.Subject == claimPrincipal:
			accepts++
		default:
		}
	}

	if accepts != 1 || ends != 1 {
		t.Fatalf("claim %s has %d accept entries and %d end entries by the server (cause %q), want 1 and 1",
			id, accepts, ends, cause)
	}
}

// wantCount fails the test unless the workspace holds n claims.
func (w *claimWorld) wantCount(t *testing.T, ws workspace.ID, n int) {
	t.Helper()

	if got := w.list(t, ws); len(got) != n {
		t.Fatalf("workspace %s holds %d claims, want %d: %+v", ws, len(got), n, got)
	}
}

// claimRecord returns the record of a claim that the store cases insert
// straight into the store.
func (w *claimWorld) claimRecord(id string) workspace.Claim {
	return workspace.Claim{
		ID: id, WorkspaceID: w.wsAcme, Base: w.baseAcme, Addresses: []string{"a"}, Principal: claimPrincipal,
		JobID: claimJob1, Trigger: "", State: workspace.ClaimAccepted, StartedAt: claimNow(), EndedAt: time.Time{},
		LeaseEndsAt: time.Time{}, RenewedAt: time.Time{},
	}
}

// otherRequest returns a register request on the workspace of the other
// namespace.
func (w *claimWorld) otherRequest(job string, addresses ...string) *workspace.RegisterClaimRequest {
	req := w.request(job, addresses...)
	req.WorkspaceID = w.wsOther
	req.Base = w.baseOther
	req.Audit = claimEntry(w.other)

	return req
}

func claimDisjoint(t *testing.T, w *claimWorld) {
	t.Helper()

	first := w.register(t, claimJob1, "aws_instance.b", "aws_instance.a", " aws_instance.a ")
	second := w.register(t, claimJob2, "aws_instance.c")

	got := w.list(t, w.wsAcme)
	if len(got) != claimPair || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("list = %+v, want both claims in accept order", got)
	}

	if !slices.Equal(got[0].Addresses, []string{"aws_instance.a", "aws_instance.b"}) {
		t.Fatalf("addresses = %v, want them trimmed, sorted and without duplicates", got[0].Addresses)
	}

	c := &got[0]
	if c.State != workspace.ClaimAccepted || !c.StartedAt.Equal(claimNow()) || c.Principal != claimPrincipal ||
		c.JobID != claimJob1 || c.Base != w.baseAcme || c.WorkspaceID != w.wsAcme || !c.EndedAt.IsZero() {
		t.Fatalf("claim = %+v, want an accepted claim with its record", c)
	}
}

func claimOverlap(t *testing.T, w *claimWorld) {
	t.Helper()

	first := w.register(t, claimJob1, "aws_instance.a", "aws_instance.b")

	_, err := w.registry.Register(t.Context(), w.request(claimJob2, "aws_instance.b", "aws_instance.z"), w.acme)
	if !errors.Is(err, workspace.ErrClaimOverlap) {
		t.Fatalf("overlap = %v, want ErrClaimOverlap", err)
	}

	overlap, _ := errors.AsType[*workspace.OverlapError](err)
	if overlap == nil || overlap.Blocker != first.ID {
		t.Fatalf("overlap error = %v, want blocker %s", err, first.ID)
	}

	w.wantCount(t, w.wsAcme, 1)
}

func claimRetry(t *testing.T, w *claimWorld) {
	t.Helper()

	first := w.register(t, claimJob1, "aws_instance.a")
	again := w.register(t, claimJob1, "aws_instance.a")

	if first.ID != again.ID {
		t.Fatalf("retry ID = %s, want %s", again.ID, first.ID)
	}

	w.wantCount(t, w.wsAcme, 1)

	// The same job with another principal is not a retry: it overlaps.
	req := w.request(claimJob1, "aws_instance.a")
	req.Principal = claimBob

	if _, err := w.registry.Register(t.Context(), req, w.acme); !errors.Is(err, workspace.ErrClaimOverlap) {
		t.Fatalf("same job, other principal = %v, want ErrClaimOverlap", err)
	}

	w.wantCount(t, w.wsAcme, 1)
}

// manyAddresses returns n distinct addresses.
func manyAddresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "aws_instance.n" + strconv.Itoa(i)
	}

	return out
}

// longAddress returns an address one byte over the bound.
func longAddress() string {
	return strings.Repeat("a", workspace.MaxClaimAddressLength+1)
}

// registerCase is one hostile register request and the error that the
// registry answers.
type registerCase struct {
	name   string
	mutate func(r *workspace.RegisterClaimRequest)
	want   error
}

// hostileRegisterCases lists the hostile requests of the registry layer.
func (w *claimWorld) hostileRegisterCases() []registerCase {
	return []registerCase{
		{"empty set", func(r *workspace.RegisterClaimRequest) { r.Addresses = nil }, workspace.ErrClaimEmptySet},
		{
			"blank addresses",
			func(r *workspace.RegisterClaimRequest) { r.Addresses = []string{"  ", ""} },
			workspace.ErrClaimEmptySet,
		},
		{
			"replace of the claim of another job",
			func(r *workspace.RegisterClaimRequest) { r.Replace = true; r.Addresses = []string{"aws_instance.held"} },
			workspace.ErrClaimReplace,
		},
		{
			"oversize address list",
			func(r *workspace.RegisterClaimRequest) { r.Addresses = manyAddresses(workspace.MaxClaimAddresses + 1) },
			workspace.ErrClaimTooLarge,
		},
		{
			"address too long",
			func(r *workspace.RegisterClaimRequest) { r.Addresses = []string{longAddress()} },
			workspace.ErrClaimTooLarge,
		},
		{"unknown base", func(r *workspace.RegisterClaimRequest) { r.Base = "sv-missing" }, workspace.ErrClaimBadBase},
		{
			"base of another workspace",
			func(r *workspace.RegisterClaimRequest) { r.Base = w.baseOther },
			workspace.ErrClaimBadBase,
		},
		{
			"unknown workspace",
			func(r *workspace.RegisterClaimRequest) { r.WorkspaceID = "ws-missing" },
			workspace.ErrClaimNotFound,
		},
		{
			"invalid audit entry",
			func(r *workspace.RegisterClaimRequest) { r.Audit = zeroEntry() },
			audit.ErrInvalidEntry,
		},
		{
			"audit entry of another namespace",
			func(r *workspace.RegisterClaimRequest) { r.Audit = claimEntry(w.other) },
			audit.ErrInvalidEntry,
		},
	}
}

// zeroEntry returns the zero audit entry, which no store accepts.
func zeroEntry() audit.Entry {
	var e audit.Entry

	return e
}

func claimHostileRegister(t *testing.T, w *claimWorld) {
	t.Helper()

	held := w.register(t, claimJob1, "aws_instance.held")

	cases := w.hostileRegisterCases()

	for i := range cases {
		c := &cases[i]
		req := w.request(claimJob2, "aws_instance.fresh")
		c.mutate(req)

		if _, err := w.registry.Register(t.Context(), req, w.acme); !errors.Is(err, c.want) {
			t.Errorf("%s: register = %v, want %v", c.name, err, c.want)
		}
	}

	got := w.list(t, w.wsAcme)
	if len(got) != 1 || got[0].ID != held.ID || got[0].State != workspace.ClaimAccepted {
		t.Fatalf("claims after the refusals = %+v, want only the first claim, untouched", got)
	}
}

func claimOtherNamespace(t *testing.T, w *claimWorld) {
	t.Helper()

	// The fence of acme names a workspace of other: the registry refuses it.
	cross := w.otherRequest(claimJob1, "aws_instance.a")
	cross.Audit = claimEntry(w.acme)

	if _, err := w.registry.Register(t.Context(), cross, w.acme); err == nil {
		t.Fatal("register on a workspace of another namespace = nil, want an error")
	}

	// The same call straight to the store, as if the registry failed: the
	// workspace reads as missing in the namespace of the fence.
	err := w.backend.WithClaims(t.Context(), w.wsOther, w.acme, func(workspace.ClaimTx) error { return nil })
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("WithClaims on another namespace = %v, want ErrNotFound", err)
	}

	w.wantCount(t, w.wsOther, 0)
	w.wantCount(t, w.wsAcme, 0)

	// A claim on each workspace does not block the other: the namespaces do
	// not share addresses.
	w.register(t, claimJob1, "aws_instance.a")

	if _, err = w.registry.Register(t.Context(), w.otherRequest("job-9", "aws_instance.a"), w.other); err != nil {
		t.Fatalf("register on the other namespace = %v, want accepted", err)
	}
}

func claimStaleFence(t *testing.T, w *claimWorld) {
	t.Helper()

	w.register(t, claimJob1, "aws_instance.a")

	stale := namespace.Fence{Namespace: w.acme.Namespace, FailoverVersion: w.acme.FailoverVersion + 1}

	_, err := w.registry.Register(t.Context(), w.request(claimJob2, "aws_instance.b"), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("register with a stale fence = %v, want ErrFenced", err)
	}

	steps := []struct {
		name string
		call func() error
	}{
		{"WithClaims", func() error {
			return w.backend.WithClaims(t.Context(), w.wsAcme, stale, func(workspace.ClaimTx) error { return nil })
		}},
		{"EndClaimsByJob", func() error {
			_, endErr := w.backend.EndClaimsByJob(t.Context(), stale, claimJob1, claimNow())

			return endErr
		}},
		{"ExpireClaims", func() error {
			_, expErr := w.backend.ExpireClaims(t.Context(), stale, claimNow().Add(time.Hour), claimBatch)

			return expErr
		}},
		{"RenewClaim", func() error {
			_, renewErr := w.backend.RenewClaim(t.Context(), stale, workspace.ClaimRenewal{
				Workspace: w.wsAcme,
				ClaimID:   "clm-x",
				Principal: claimPrincipal,
				Now:       claimNow(),
				Lease:     workspace.ClaimLease,
			})

			return renewErr
		}},
		{"AddClaimTouch", func() error {
			return w.backend.AddClaimTouch(t.Context(), stale, w.wsAcme, claimTouchVersion, []string{"aws_instance.b"})
		}},
	}

	for _, step := range steps {
		if got := step.call(); !errors.Is(got, namespace.ErrFenced) {
			t.Errorf("%s with a stale fence = %v, want ErrFenced", step.name, got)
		}
	}

	got := w.list(t, w.wsAcme)
	if len(got) != 1 || got[0].State != workspace.ClaimAccepted {
		t.Fatalf("claims after the stale fence = %+v, want one accepted claim", got)
	}
}

// insertClaim inserts c with e in one transaction of the acme workspace.
func (w *claimWorld) insertClaim(t *testing.T, c workspace.Claim, e audit.Entry) error {
	t.Helper()

	return w.backend.WithClaims(t.Context(), w.wsAcme, w.acme, func(tx workspace.ClaimTx) error {
		return tx.Insert(t.Context(), c, e)
	})
}

func claimHostileStore(t *testing.T, w *claimWorld) {
	t.Helper()

	good := w.claimRecord("clm-direct")

	zero, foreign := zeroEntry(), claimEntry(w.other)

	if err := w.insertClaim(t, good, zero); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Errorf("zero audit entry: insert = %v, want ErrInvalidEntry", err)
	}

	if err := w.insertClaim(t, good, foreign); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Errorf("audit entry of another namespace: insert = %v, want ErrInvalidEntry", err)
	}

	w.wantCount(t, w.wsAcme, 0)

	// The valid claim goes in, so that the cases above are not a store that
	// refuses everything.
	if err := w.insertClaim(t, good, claimEntry(w.acme)); err != nil {
		t.Fatalf("insert of a valid claim = %v", err)
	}

	w.wantCount(t, w.wsAcme, 1)

	for _, c := range []struct {
		name    string
		fence   namespace.Fence
		ws      workspace.ID
		version workspace.StateVersionID
		want    error
	}{
		{"unknown workspace", w.acme, "ws-missing", claimTouchVersion, workspace.ErrNotFound},
		{"no version", w.acme, w.wsAcme, "", workspace.ErrClaimInvalid},
		{"workspace of another namespace", w.acme, w.wsOther, claimTouchVersion, workspace.ErrNotFound},
	} {
		err := w.backend.AddClaimTouch(t.Context(), c.fence, c.ws, c.version, []string{"a"})
		if !errors.Is(err, c.want) {
			t.Errorf("touch with %s = %v, want %v", c.name, err, c.want)
		}
	}
}

// recordCase is one hostile claim record and the error that the store
// answers.
type recordCase struct {
	name   string
	mutate func(c *workspace.Claim)
	want   error
}

// hostileRecordCases lists the hostile claim records of the store layer.
func (w *claimWorld) hostileRecordCases() []recordCase {
	return []recordCase{
		{"empty ID", func(c *workspace.Claim) { c.ID = "" }, workspace.ErrClaimInvalid},
		{"empty base", func(c *workspace.Claim) { c.Base = "" }, workspace.ErrClaimInvalid},
		{"empty principal", func(c *workspace.Claim) { c.Principal = "" }, workspace.ErrClaimInvalid},
		{"empty workspace", func(c *workspace.Claim) { c.WorkspaceID = "" }, workspace.ErrClaimInvalid},
		{
			"workspace of another transaction",
			func(c *workspace.Claim) { c.WorkspaceID = w.wsOther },
			workspace.ErrClaimInvalid,
		},
		{
			"lease on the claim of a job",
			func(c *workspace.Claim) { c.LeaseEndsAt = claimNow().Add(workspace.ClaimLease) },
			workspace.ErrClaimInvalid,
		},
		{"no lease on a claim with no job", func(c *workspace.Claim) { c.JobID = "" }, workspace.ErrClaimInvalid},
		{
			"renewal time at insert",
			func(c *workspace.Claim) {
				c.JobID, c.LeaseEndsAt, c.RenewedAt = "", claimNow().Add(workspace.ClaimLease), claimNow()
			},
			workspace.ErrClaimInvalid,
		},
		{"ended at insert", func(c *workspace.Claim) { c.State = workspace.ClaimEnded }, workspace.ErrClaimInvalid},
		{"end time at insert", func(c *workspace.Claim) { c.EndedAt = claimNow() }, workspace.ErrClaimInvalid},
		{"empty set", func(c *workspace.Claim) { c.Addresses = nil }, workspace.ErrClaimEmptySet},
		{"unsorted set", func(c *workspace.Claim) { c.Addresses = []string{"b", "a"} }, workspace.ErrClaimInvalid},
		{"duplicate address", func(c *workspace.Claim) { c.Addresses = []string{"a", "a"} }, workspace.ErrClaimInvalid},
		{"untrimmed address", func(c *workspace.Claim) { c.Addresses = []string{" a"} }, workspace.ErrClaimInvalid},
		{
			"oversize address list",
			func(c *workspace.Claim) { c.Addresses = manyAddresses(workspace.MaxClaimAddresses + 1) },
			workspace.ErrClaimTooLarge,
		},
		{
			"address too long",
			func(c *workspace.Claim) { c.Addresses = []string{longAddress()} },
			workspace.ErrClaimTooLarge,
		},
	}
}

func claimHostileRecords(t *testing.T, w *claimWorld) {
	t.Helper()

	for i, c := range w.hostileRecordCases() {
		claim := w.claimRecord("clm-x" + strconv.Itoa(i))
		c.mutate(&claim)

		if err := w.insertClaim(t, claim, claimEntry(w.acme)); !errors.Is(err, c.want) {
			t.Errorf("%s: insert = %v, want %v", c.name, err, c.want)
		}
	}

	w.wantCount(t, w.wsAcme, 0)
}

var errClaimAbort = errors.New("the transaction aborts")

func claimRollback(t *testing.T, w *claimWorld) {
	t.Helper()

	claim := w.claimRecord("clm-rollback")

	err := w.backend.WithClaims(t.Context(), w.wsAcme, w.acme, func(tx workspace.ClaimTx) error {
		if insertErr := tx.Insert(t.Context(), claim, claimEntry(w.acme)); insertErr != nil {
			return insertErr
		}

		return errClaimAbort
	})
	if !errors.Is(err, errClaimAbort) {
		t.Fatalf("WithClaims = %v, want the error of the function", err)
	}

	w.wantCount(t, w.wsAcme, 0)
}

// checkReleased checks the claims of the acme workspace after the release of
// claimJob1 at end: that claim is ended, the other stays accepted.
func (w *claimWorld) checkReleased(t *testing.T, end time.Time) {
	t.Helper()

	list := w.list(t, w.wsAcme)

	for i := range list {
		claim := &list[i]
		ended := claim.State == workspace.ClaimEnded && claim.EndedAt.Equal(end)
		running := claim.State == workspace.ClaimAccepted && claim.EndedAt.IsZero()

		if (claim.JobID == claimJob1 && !ended) || (claim.JobID == claimJob2 && !running) {
			t.Errorf("claim of %s = %+v, want job 1 ended at %v and job 2 accepted", claim.JobID, claim, end)
		}
	}

	// The same job ID in another namespace keeps its claim.
	if got := w.list(t, w.wsOther); len(got) != 1 || got[0].State != workspace.ClaimAccepted {
		t.Fatalf("claims of the other namespace = %+v, want one accepted claim", got)
	}
}

func claimReleaseByJob(t *testing.T, w *claimWorld) {
	t.Helper()

	firstJobClaim := w.register(t, claimJob1, "aws_instance.a").ID
	w.register(t, claimJob2, "aws_instance.b")

	if _, err := w.registry.Register(t.Context(), w.otherRequest(claimJob1, "aws_instance.a"), w.other); err != nil {
		t.Fatalf("register on the other namespace: %v", err)
	}

	end := claimNow().Add(time.Minute)

	for _, c := range []struct {
		name string
		job  string
		want int
	}{
		{"empty job", "", 0},
		{"unknown job", "job-unknown", 0},
		{"job of the namespace", claimJob1, 1},
		{"job already ended", claimJob1, 0},
	} {
		n, err := w.registry.ReleaseByJob(t.Context(), w.acme, c.job, end)
		if err != nil || n != c.want {
			t.Fatalf("%s: released = %d, %v, want %d", c.name, n, err, c.want)
		}
	}

	w.checkReleased(t, end)

	w.wantEndEntry(t, firstJobClaim, workspace.ClaimEndedByJob, claimJob1)
}

// registerWrapper registers a claim with no job, the claim of the local
// wrapper, which has a lease.
func (w *claimWorld) registerWrapper(t *testing.T, addresses ...string) workspace.Claim {
	t.Helper()

	return w.register(t, "", addresses...)
}

func claimLeaseAtRegister(t *testing.T, w *claimWorld) {
	t.Helper()

	wrapper := w.registerWrapper(t, "aws_instance.a")
	jobClaim := w.register(t, claimJob1, "aws_instance.b")

	if want := w.now().Add(workspace.ClaimLease); !wrapper.LeaseEndsAt.Equal(want) {
		t.Fatalf("lease end of a claim with no job = %v, want %v", wrapper.LeaseEndsAt, want)
	}

	if !jobClaim.LeaseEndsAt.IsZero() {
		t.Fatalf("lease end of a claim of a job = %v, want none", jobClaim.LeaseEndsAt)
	}

	stored := w.list(t, w.wsAcme)

	for i := range stored {
		if c := &stored[i]; (c.JobID == "") == c.LeaseEndsAt.IsZero() {
			t.Fatalf("stored claim %+v: a claim with no job has a lease, and a claim of a job has none", *c)
		}
	}
}

const (
	// claimLockHolder and claimLockReason are the holder and the reason of the
	// manual locks of the cases.
	claimLockHolder = "ops"
	claimLockReason = "freeze"
	// claimBob is a second person.
	claimBob = "bob"
	// claimBatch is a limit that no case reaches.
	claimBatch = 10
	// claimRenewAfter is the time from a registration to a renewal.
	claimRenewAfter = 60 * time.Second
)

func claimExpire(t *testing.T, w *claimWorld) {
	t.Helper()

	wrapper := w.registerWrapper(t, "aws_instance.a")
	jobClaim := w.register(t, claimJob1, "aws_instance.b")

	// Each step moves the clock and runs the expiry in one namespace.
	for _, c := range []struct {
		name    string
		advance time.Duration
		fence   namespace.Fence
		want    int
	}{
		{"before the lease ends", workspace.ClaimLease - time.Second, w.acme, 0},
		{"at the lease end", time.Second, w.acme, 0},
		{"after the lease end, in the other namespace", time.Second, w.other, 0},
		{"after the lease end", 0, w.acme, 1},
		{"again", 0, w.acme, 0},
	} {
		w.advance(c.advance)

		if n, err := w.registry.ExpireLeases(t.Context(), c.fence, claimBatch); err != nil || n != c.want {
			t.Fatalf("expire %s = %d, %v, want %d", c.name, n, err, c.want)
		}
	}

	// The claim of a job stays: only its job ends it. The wrapper claim ended
	// with an end time and one audit entry of the expiry.
	wantClaimState(t, w, wrapper.ID, workspace.ClaimEnded)
	wantClaimState(t, w, jobClaim.ID, workspace.ClaimAccepted)
	w.wantEndEntry(t, wrapper.ID, workspace.ClaimEndedByExpiry, "")

	// The expired claim frees its addresses.
	again := w.registerWrapper(t, "aws_instance.a")
	if again.ID == wrapper.ID || again.State != workspace.ClaimAccepted {
		t.Fatalf("claim on the freed address = %+v, want a new accepted claim", again)
	}
}

// wantClaimState fails the test unless the claim id has the state want.
func wantClaimState(t *testing.T, w *claimWorld, id string, want workspace.ClaimState) {
	t.Helper()

	list := w.list(t, w.wsAcme)
	for i := range list {
		if list[i].ID == id {
			if list[i].State != want {
				t.Fatalf("claim %s is %s, want %s", id, list[i].State, want)
			}

			return
		}
	}

	t.Fatalf("claim %s is not on the workspace", id)
}

func claimExpireBounded(t *testing.T, w *claimWorld) {
	t.Helper()

	const total = 5

	for i := range total {
		w.registerWrapper(t, fmt.Sprintf("aws_instance.n%d", i))
	}

	w.advance(workspace.ClaimLease + time.Second)

	for _, c := range []struct {
		limit int
		want  int
	}{
		{0, 0}, {-1, 0}, {2, 2}, {2, 2}, {2, 1}, {2, 0},
	} {
		if n, err := w.registry.ExpireLeases(t.Context(), w.acme, c.limit); err != nil || n != c.want {
			t.Fatalf("expire with limit %d = %d, %v, want %d", c.limit, n, err, c.want)
		}
	}
}

// storedClaim returns the claim id of the workspace acme as the store lists it.
func (w *claimWorld) storedClaim(t *testing.T, id string) workspace.Claim {
	t.Helper()

	list, err := w.registry.List(t.Context(), w.wsAcme)
	if err != nil {
		t.Fatalf("list claims: %v", err)
	}

	for i := range list {
		if list[i].ID == id {
			return list[i]
		}
	}

	t.Errorf("the list misses claim %s", id)

	var none workspace.Claim

	return none
}

// renew renews claim id of workspace ws for the principal and returns the
// error of the registry.
func (w *claimWorld) renew(t *testing.T, ws workspace.ID, id, principal string) error {
	t.Helper()

	_, err := w.registry.Renew(t.Context(), ws, id, principal, w.acme)

	return err
}

// wantStored fails the test unless the store holds the claim with the end of
// the lease and the time of the renewal of want.
func (w *claimWorld) wantStored(t *testing.T, want *workspace.Claim) {
	t.Helper()

	got := w.storedClaim(t, want.ID)
	if !got.RenewedAt.Equal(want.RenewedAt) || !got.LeaseEndsAt.Equal(want.LeaseEndsAt) {
		t.Fatalf("stored claim = %+v, want lease %v renewed %v", got, want.LeaseEndsAt, want.RenewedAt)
	}
}

func claimRenew(t *testing.T, w *claimWorld) {
	t.Helper()

	claim := w.registerWrapper(t, "aws_instance.a")
	jobClaim := w.register(t, claimJob1, "aws_instance.b")

	// A renewal 60 seconds in extends the lease to 90 seconds from then.
	w.advance(claimRenewAfter)

	renewed, err := w.registry.Renew(t.Context(), w.wsAcme, claim.ID, claimPrincipal, w.acme)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}

	w.wantRenewed(t, &claim, &renewed)

	// Past the first lease end and inside the second: the claim lives.
	w.advance(claimRenewAfter)

	if n, expErr := w.registry.ExpireLeases(t.Context(), w.acme, claimBatch); expErr != nil || n != 0 {
		t.Fatalf("expire after a renewal = %d, %v, want 0", n, expErr)
	}

	w.wantRenewalsRefused(t, claim.ID, jobClaim.ID)

	// A refused renewal changes nothing: neither the lease nor the time of the
	// last renewal of the claim.
	w.wantStored(t, &renewed)

	if got := w.storedClaim(t, jobClaim.ID); !got.LeaseEndsAt.IsZero() || !got.RenewedAt.IsZero() {
		t.Fatalf("stored claim of a job = %+v, want no lease and no renewal", got)
	}

	w.wantNoRenewalAfterTheLease(t, claim.ID)
}

// claimRenewClockBack proves that a claim that one instance ended is not
// renewed by an instance whose clock is behind: the clock reads a time before
// the end of the lease, and the claim is ended, so the renewal is refused.
func claimRenewClockBack(t *testing.T, w *claimWorld) {
	t.Helper()

	claim := w.registerWrapper(t, "aws_instance.a")

	w.advance(workspace.ClaimLease + time.Second)

	if n, err := w.registry.ExpireLeases(t.Context(), w.acme, claimBatch); err != nil || n != 1 {
		t.Fatalf("expire = %d, %v, want 1", n, err)
	}

	w.advance(-2 * time.Second)

	if err := w.renew(t, w.wsAcme, claim.ID, claimPrincipal); !errors.Is(err, workspace.ErrClaimEnded) {
		t.Fatalf("renew of an ended claim by a clock that is behind = %v, want ErrClaimEnded", err)
	}

	if got := w.storedClaim(t, claim.ID); got.State != workspace.ClaimEnded || !got.RenewedAt.IsZero() {
		t.Fatalf("stored claim = %+v, want an ended claim that no one renewed", got)
	}
}

// wantRenewed checks the claim that a renewal returned and the stored claim:
// the lease runs one lease from the time of the renewal, and the time of the
// renewal is set.
func (w *claimWorld) wantRenewed(t *testing.T, before, renewed *workspace.Claim) {
	t.Helper()

	wantEnd := w.now().Add(workspace.ClaimLease)
	if !renewed.LeaseEndsAt.Equal(wantEnd) || !renewed.LeaseEndsAt.After(before.LeaseEndsAt) {
		t.Fatalf(
			"lease end after the renewal = %v, want %v, after %v",
			renewed.LeaseEndsAt,
			wantEnd,
			before.LeaseEndsAt,
		)
	}

	if !before.RenewedAt.IsZero() || !renewed.RenewedAt.Equal(w.now()) {
		t.Fatalf("renewal time = %v before, %v after, want none and %v", before.RenewedAt, renewed.RenewedAt, w.now())
	}

	// The store keeps the end of the lease and the time of the renewal.
	w.wantStored(t, renewed)
}

// wantRenewalsRefused sends the hostile renewals: another principal, an
// unknown claim, a claim of a job, and a claim on another workspace.
func (w *claimWorld) wantRenewalsRefused(t *testing.T, claimID, jobClaimID string) {
	t.Helper()

	for _, c := range []struct {
		name      string
		ws        workspace.ID
		id        string
		principal string
		want      error
	}{
		{"another principal", w.wsAcme, claimID, claimBob, workspace.ErrClaimNotFound},
		{"unknown claim", w.wsAcme, "clm-unknown", claimPrincipal, workspace.ErrClaimNotFound},
		{"claim of a job", w.wsAcme, jobClaimID, claimPrincipal, workspace.ErrClaimInvalid},
		{"claim on a workspace of another namespace", w.wsOther, claimID, claimPrincipal, workspace.ErrClaimNotFound},
		{"claim on another workspace of the namespace", w.wsAcme2, claimID, claimPrincipal, workspace.ErrClaimNotFound},
	} {
		if err := w.renew(t, c.ws, c.id, c.principal); !errors.Is(err, c.want) {
			t.Errorf("renew of %s = %v, want %v", c.name, err, c.want)
		}
	}
}

// wantNoRenewalAfterTheLease proves that a lease that ended is gone, before
// and after the expiry runs.
func (w *claimWorld) wantNoRenewalAfterTheLease(t *testing.T, claimID string) {
	t.Helper()

	w.advance(workspace.ClaimLease)

	if err := w.renew(t, w.wsAcme, claimID, claimPrincipal); !errors.Is(err, workspace.ErrClaimEnded) {
		t.Fatalf("renew after the lease end = %v, want ErrClaimEnded", err)
	}

	if n, err := w.registry.ExpireLeases(t.Context(), w.acme, claimBatch); err != nil || n != 1 {
		t.Fatalf("expire after the lease end = %d, %v, want 1", n, err)
	}

	if err := w.renew(t, w.wsAcme, claimID, claimPrincipal); !errors.Is(err, workspace.ErrClaimEnded) {
		t.Fatalf("renew of an ended claim = %v, want ErrClaimEnded", err)
	}
}

func claimTouchedBase(t *testing.T, w *claimWorld) {
	t.Helper()

	err := w.registry.NoteCommitted(
		t.Context(),
		w.acme,
		w.wsAcme,
		claimTouchVersion,
		[]string{"aws_instance.a"},
	)
	if err != nil {
		t.Fatalf("note committed: %v", err)
	}

	_, err = w.registry.Register(t.Context(), w.request(claimJob1, "aws_instance.a"), w.acme)
	if !errors.Is(err, workspace.ErrClaimTouchedBase) {
		t.Fatalf("claim on a touched address = %v, want ErrClaimTouchedBase", err)
	}

	touched, _ := errors.AsType[*workspace.TouchedBaseError](err)
	if touched == nil || touched.Version != claimTouchVersion {
		t.Fatalf("touched error = %v, want version sv-2", err)
	}

	w.wantCount(t, w.wsAcme, 0)
	w.register(t, claimJob1, "aws_instance.untouched")
}

func claimEndedFrees(t *testing.T, w *claimWorld) {
	t.Helper()

	w.register(t, claimJob1, "aws_instance.a")

	released := claimNow().Add(time.Second)

	n, err := w.registry.ReleaseByJob(t.Context(), w.acme, claimJob1, released)
	if err != nil || n != 1 {
		t.Fatalf("release = %d, %v, want 1", n, err)
	}

	second := w.register(t, claimJob2, "aws_instance.a")
	if second.State != workspace.ClaimAccepted {
		t.Fatalf("claim after the release = %+v, want accepted", second)
	}

	w.wantCount(t, w.wsAcme, claimPair)
}

// registerAll registers one claim for each address in parallel, as the jobs
// of the same names, and returns the error of each call.
func (w *claimWorld) registerAll(prefix string, addresses []string) []error {
	var wg sync.WaitGroup

	errs := make([]error, len(addresses))

	for i, address := range addresses {
		wg.Go(func() {
			req := w.request(prefix+strconv.Itoa(i), address)
			_, errs[i] = w.registry.Register(context.Background(), req, w.acme)
		})
	}

	wg.Wait()

	return errs
}

func claimParallel(t *testing.T, w *claimWorld) {
	t.Helper()

	const callers = 24

	for i, err := range w.registerAll("job-", manyAddresses(callers)) {
		if err != nil {
			t.Fatalf("disjoint register %d = %v, want accepted", i, err)
		}
	}

	w.wantCount(t, w.wsAcme, callers)

	// Two registrations race over one address: exactly one wins.
	wins := 0

	for _, err := range w.registerAll("race-", []string{"aws_instance.shared", "aws_instance.shared"}) {
		if err == nil {
			wins++

			continue
		}

		if !errors.Is(err, workspace.ErrClaimOverlap) {
			t.Fatalf("race register = %v, want accepted or overlap", err)
		}
	}

	if wins != 1 {
		t.Fatalf("race winners = %d, want exactly 1", wins)
	}

	w.wantCount(t, w.wsAcme, callers+1)
}

// hookClaimStore runs before just before each transaction of the store opens,
// after the registry made its checks outside the transaction.
type hookClaimStore struct {
	workspace.ClaimStore

	before func()
}

func (s *hookClaimStore) WithClaims(
	ctx context.Context, ws workspace.ID, fence namespace.Fence, fn func(tx workspace.ClaimTx) error,
) error {
	s.before()

	return s.ClaimStore.WithClaims(ctx, ws, fence, fn)
}

// switchManual is a ManualLocks fake: it covers each unit once on is set, and
// answers err when err is set.
type switchManual struct {
	on  atomic.Bool
	err error
	// phase is when the phase of any job started, or the zero time.
	phase time.Time
}

func (m *switchManual) CoveringFor(context.Context, manuallock.Unit, string) (manuallock.Lock, error) {
	if m.err != nil {
		var none manuallock.Lock

		return none, m.err
	}

	if m.on.Load() {
		return manuallock.Lock{
			Since:  claimNow(),
			Target: noTarget(),
			Holder: claimLockHolder,
			Reason: claimLockReason,
		}, nil
	}

	var none manuallock.Lock

	return none, manuallock.ErrNotLocked
}

func (m *switchManual) NamespaceLock(context.Context, namespace.Name) (manuallock.Lock, error) {
	var none manuallock.Lock

	return none, manuallock.ErrNotLocked
}

func (m *switchManual) NamespaceLockFor(
	ctx context.Context, unit manuallock.Unit, jobID string,
) (manuallock.Lock, error) {
	return m.CoveringFor(ctx, unit, jobID)
}

func (m *switchManual) PhaseStart(context.Context, manuallock.Unit, string) time.Time {
	return m.phase
}

// registryOver returns a registry over the world backend, with the store st
// and the manual lock source manual.
func (w *claimWorld) registryOver(
	t *testing.T,
	st workspace.ClaimStore,
	manual workspace.ManualLocks,
) *workspace.ClaimRegistry {
	t.Helper()

	r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: w.backend, Versions: w.backend, Deleted: w.backend, Manual: manual, Store: st, Now: w.now,
		Lease: 0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return r
}

func claimLockAfterPreCheck(t *testing.T, w *claimWorld) {
	t.Helper()

	// The lock arrives after the registry read the workspace and before the
	// transaction opens: the checks of the registry outside the transaction
	// passed, and only the check under the claim lock sees the lock.
	hook := &hookClaimStore{ClaimStore: w.backend, before: func() {
		ws, err := w.backend.GetWorkspaceByID(t.Context(), w.wsAcme)
		if err != nil {
			t.Errorf("read the workspace: %v", err)

			return
		}

		ws.Lock = workspace.Lock{LockedAt: claimNow(), ID: "lock-1", Holder: claimBob, Reason: "plan"}
		ws.Revision++

		if err = w.backend.SaveWorkspace(t.Context(), ws, w.acme); err != nil {
			t.Errorf("lock the workspace: %v", err)
		}
	}}

	r := w.registryOver(t, hook, nil)

	req := w.request(claimJob1, "aws_instance.a")

	_, err := r.Register(t.Context(), req, w.acme)
	if !errors.Is(err, workspace.ErrClaimLocked) {
		t.Fatalf("register after the lock = %v, want ErrClaimLocked", err)
	}

	w.wantCount(t, w.wsAcme, 0)
}

func claimManualAfterPreCheck(t *testing.T, w *claimWorld) {
	t.Helper()

	w.registerAfterManualLock(t, time.Time{}, workspace.ErrClaimLocked)
}

// claimManualPhaseBeforeLock proves the rule of the phase under the claim lock:
// a job whose phase started before the lock passes it.
func claimManualPhaseBeforeLock(t *testing.T, w *claimWorld) {
	t.Helper()

	w.registerAfterManualLock(t, claimNow().Add(-time.Hour), nil)
}

// claimManualPhaseAfterLock proves the other half: a job whose phase started
// after the lock does not pass it.
func claimManualPhaseAfterLock(t *testing.T, w *claimWorld) {
	t.Helper()

	w.registerAfterManualLock(t, claimNow().Add(time.Hour), workspace.ErrClaimLocked)
}

// registerAfterManualLock registers a claim of a job whose phase started at
// phase, while a hook puts a manual lock of the namespace after the checks that
// run before the transaction. It fails the test unless the register ends with
// want, and the workspace holds a claim only when want is nil.
func (w *claimWorld) registerAfterManualLock(t *testing.T, phase time.Time, want error) {
	t.Helper()

	manual := &switchManual{on: atomic.Bool{}, err: nil, phase: phase}

	hook := &hookClaimStore{ClaimStore: w.backend, before: func() {
		target := manuallock.NamespaceTarget(w.acme.Namespace)
		l := manuallock.Lock{Since: claimNow(), Target: target, Holder: claimLockHolder, Reason: claimLockReason}

		if err := w.backend.PutLock(t.Context(), l, w.acme, claimEntry(w.acme)); err != nil {
			t.Errorf("put the manual lock: %v", err)
		}

		manual.on.Store(true)
	}}

	r := w.registryOver(t, hook, manual)

	req := w.request(claimJob1, "aws_instance.a")

	_, err := r.Register(t.Context(), req, w.acme)
	if want == nil {
		if err != nil {
			t.Fatalf("register of a phase before the manual lock = %v, want nil", err)
		}

		w.wantCount(t, w.wsAcme, 1)

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("register after the manual lock = %v, want %v", err, want)
	}

	w.wantCount(t, w.wsAcme, 0)
}

var errClaimManual = errors.New("the manual lock store does not answer")

func claimManualFailsClosed(t *testing.T, w *claimWorld) {
	t.Helper()

	r := w.registryOver(t, w.backend, &switchManual{on: atomic.Bool{}, err: errClaimManual, phase: time.Time{}})

	req := w.request(claimJob1, "aws_instance.a")

	_, err := r.Register(t.Context(), req, w.acme)
	if !errors.Is(err, errClaimManual) {
		t.Fatalf("register with a failing manual lock store = %v, want its error", err)
	}

	w.wantCount(t, w.wsAcme, 0)
}

func claimDeletedWorkspace(t *testing.T, w *claimWorld) {
	t.Helper()

	// A workspace with no state version, so that its delete is allowed.
	gone := workspace.Workspace{
		NamespaceID: "", CreatedAt: claimNow(), UpdatedAt: claimNow(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(), InCollection: false,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		Origin:    identity.StateOrigin{Job: "", Commit: ""}, ID: "ws-gone",
		Ref: workspace.Ref{Namespace: w.acme.Namespace, Name: "gone"}, Revision: 1,
	}

	if err := w.backend.CreateWorkspace(t.Context(), gone, w.acme); err != nil {
		t.Fatalf("create the workspace: %v", err)
	}

	d := inventory.Deletion{Entry: claimEntry(w.acme), PurgeAfter: claimNow().Add(time.Hour)}

	if err := w.backend.DeleteWorkspace(t.Context(), gone.Ref, w.acme, d); err != nil {
		t.Fatalf("delete the workspace: %v", err)
	}

	// Straight to the store, as if the registry failed: a soft-deleted
	// workspace is missing under the claim lock.
	err := w.backend.WithClaims(t.Context(), gone.ID, w.acme, func(workspace.ClaimTx) error { return nil })
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("WithClaims on a deleted workspace = %v, want ErrNotFound", err)
	}

	err = w.backend.AddClaimTouch(t.Context(), w.acme, gone.ID, claimTouchVersion, []string{"a"})
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("AddClaimTouch on a deleted workspace = %v, want ErrNotFound", err)
	}

	req := w.request(claimJob1, "aws_instance.a")
	req.WorkspaceID = gone.ID

	if _, err = w.registry.Register(t.Context(), req, w.acme); !errors.Is(err, workspace.ErrClaimDeleted) {
		t.Fatalf("register on a deleted workspace = %v, want ErrClaimDeleted", err)
	}
}

// SeedClaimWorkspace creates the namespace acme with the workspace ws-acme and
// one base state version in b, and returns the fence and the workspace ID. A
// test of one adapter uses it to set up a case that the suite cannot run on
// every adapter.
func SeedClaimWorkspace(t *testing.T, b ClaimBackend) (namespace.Fence, workspace.ID) {
	t.Helper()

	fence, ws, _ := seedClaimNamespace(t, b, "acme", "ws-acme", "sv-acme")

	return fence, ws
}

// noTarget returns the zero manual lock target.
func noTarget() manuallock.Target {
	var t manuallock.Target

	return t
}
