package workspacetest

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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
	CreateNamespace(ctx context.Context, ns namespace.Namespace) error
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	CreateStateVersion(ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int) error
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
	acme     namespace.Fence
	other    namespace.Fence
	// wsAcme is a workspace of acme, wsOther a workspace of other.
	wsAcme, wsOther     workspace.ID
	baseAcme, baseOther workspace.StateVersionID
}

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
		{"expire by cutoff", claimExpire},
		{"touched base", claimTouchedBase},
		{"ended claim frees its addresses", claimEndedFrees},
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

	w := &claimWorld{
		backend: b, registry: nil, acme: noFence, other: noFence, wsAcme: "", wsOther: "",
		baseAcme: "", baseOther: "",
	}

	w.acme, w.wsAcme, w.baseAcme = seedClaimNamespace(t, b, "acme", "ws-acme", "sv-acme")
	w.other, w.wsOther, w.baseOther = seedClaimNamespace(t, b, "other", "ws-other", "sv-other")

	r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: b,
		Versions:   b,
		Deleted:    nil,
		Manual:     nil,
		Store:      b,
		Now:        claimNow,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	w.registry = r

	return w
}

// seedClaimNamespace creates one namespace, one workspace and one base state
// version, and returns the fence, the workspace ID and the base ID.
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
		Ref:          workspace.Ref{Namespace: ns.Name, Name: workspace.Name("network")},
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

	return fence, wsID, base
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
	req.Principal = "bob"

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
			_, expErr := w.backend.ExpireClaims(t.Context(), stale, claimNow().Add(time.Hour))

			return expErr
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

	w.register(t, claimJob1, "aws_instance.a")
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
}

func claimExpire(t *testing.T, w *claimWorld) {
	t.Helper()

	first := w.register(t, claimJob1, "aws_instance.a")

	for _, c := range []struct {
		name   string
		fence  namespace.Fence
		cutoff time.Time
		want   int
	}{
		// A cutoff at the start does not expire the claim: it started at it.
		{"at the start", w.acme, first.StartedAt, 0},
		// The cutoff of the other namespace does not reach this claim.
		{"in the other namespace", w.other, first.StartedAt.Add(time.Hour), 0},
		{"after the start", w.acme, first.StartedAt.Add(time.Minute), 1},
	} {
		if n, err := w.registry.ExpireStale(t.Context(), c.fence, c.cutoff); err != nil || n != c.want {
			t.Fatalf("expire %s = %d, %v, want %d", c.name, n, err, c.want)
		}
	}

	got := w.list(t, w.wsAcme)
	if len(got) != 1 || got[0].State != workspace.ClaimEnded || got[0].EndedAt.IsZero() {
		t.Fatalf("claims after expiry = %+v, want one ended claim with an end time", got)
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
