package workspace_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/internal/token"
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

// registry returns a ClaimRegistry over the fixture with a fixed clock.
// OnAccept records each accepted claim when record is not nil.
func (f *claimFixture) registry(record *[]workspace.Claim) *workspace.ClaimRegistry {
	now := time.UnixMilli(1_700_000_000_000).UTC()

	cfg := workspace.ClaimRegistryConfig{
		Workspaces: f.workspaces,
		Versions:   f.versions,
		Deleted:    f.deleted,
		Manual:     nil,
		OnAccept:   nil,
		Now:        func() time.Time { return now },
	}

	if record != nil {
		cfg.OnAccept = func(_ context.Context, c workspace.Claim) error {
			*record = append(*record, c)

			return nil
		}
	}

	r, err := workspace.NewClaimRegistry(cfg)
	if err != nil {
		panic(err)
	}

	return r
}

// register registers one claim of the fixture workspace and fails the test
// on any unexpected error.
func (f *claimFixture) register(
	t *testing.T, r *workspace.ClaimRegistry, job string, addresses ...string,
) workspace.Claim {
	t.Helper()

	claim, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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
	r := f.registry(nil)

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

	got := r.List(t.Context(), f.ws)
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
	r := f.registry(nil)

	first := f.register(t, r, "job-1", "aws_instance.a", "aws_instance.b")

	_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

	if got := r.List(t.Context(), f.ws); len(got) != 1 {
		t.Fatalf("list holds %d claims, want 1", len(got))
	}
}

// TestClaimEmptySetRejected proves that a claim with an empty resource set
// fails with 422 and stores no claim.
func TestClaimEmptySetRejected(t *testing.T) {
	t.Parallel()

	for _, addresses := range [][]string{nil, {}, {"  "}, {"", "  "}} {
		f := newClaimFixture()
		r := f.registry(nil)

		_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

		if got := r.List(t.Context(), f.ws); len(got) != 0 {
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
	r := f.registry(nil)
	r.NoteCommitted(t.Context(), f.ws, "sv-2", []string{"aws_instance.a"})

	_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

	if got := r.List(t.Context(), f.ws); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none", len(got))
	}
}

// TestClaimUntouchedOlderBaseAccepted proves that a claim on an older base
// is accepted when no later version touched an address of its disjoint
// set.
func TestClaimUntouchedOlderBaseAccepted(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry(nil)
	r.NoteCommitted(t.Context(), f.ws, "sv-2", []string{"aws_instance.other"})

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
	r := f.registry(nil)

	_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

	if got := r.List(t.Context(), f.ws); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none", len(got))
	}
}

// TestClaimSameJobRetryIdempotent proves that the same job with the same
// base and the same normalized set gets the same claim ID with no second
// record, and that a different set is a new claim.
func TestClaimSameJobRetryIdempotent(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry(nil)

	first := f.register(t, r, "job-1", "aws_instance.b", "aws_instance.a")
	retry := f.register(t, r, "job-1", "aws_instance.a", "aws_instance.b")

	if retry.ID != first.ID {
		t.Fatalf("retry ID = %q, want %q", retry.ID, first.ID)
	}

	if got := r.List(t.Context(), f.ws); len(got) != 1 {
		t.Fatalf("list holds %d claims, want 1", len(got))
	}

	other := f.register(t, r, "job-1", "aws_instance.c")

	if other.ID == first.ID {
		t.Fatalf("different set ID = %q, want a new claim ID", other.ID)
	}

	if got := r.List(t.Context(), f.ws); len(got) != 2 {
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
	r := f.registry(nil)

	registerAs := func(principal string) workspace.Claim {
		t.Helper()

		claim, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

	if n := r.ExpireStale(t.Context(), first.StartedAt.Add(time.Minute)); n != 1 {
		t.Fatalf("expired claims = %d, want 1", n)
	}

	other := registerAs("bob")

	if other.ID == first.ID {
		t.Fatalf("other principal ID = %q, want a new claim ID", other.ID)
	}

	if other.State != workspace.ClaimAccepted || other.Principal != "bob" {
		t.Fatalf("other claim = %+v, want accepted with principal bob", other)
	}

	if got := r.List(t.Context(), f.ws); len(got) != 2 {
		t.Fatalf("list holds %d claims, want 2", len(got))
	}
}

// TestClaimAcceptAuditEntry proves that the server writes one audit entry
// with the claim ID and the job ID when it accepts a claim, and that a
// failing audit entry rolls the accept back.
func TestClaimAcceptAuditEntry(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()

	var accepted []workspace.Claim

	r := f.registry(&accepted)
	claim := f.register(t, r, "job-1", "aws_instance.a")

	if len(accepted) != 1 || accepted[0].ID != claim.ID || accepted[0].JobID != "job-1" {
		t.Fatalf("audit entries = %+v, want one entry with the claim and job IDs", accepted)
	}

	if accepted[0].Base != f.base {
		t.Fatalf("audit entry base = %q, want %q", accepted[0].Base, f.base)
	}

	failing, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: f.workspaces,
		Versions:   f.versions,
		Deleted:    f.deleted,
		Manual:     nil,
		OnAccept: func(context.Context, workspace.Claim) error {
			return errAcceptUnavailable
		},
		Now: func() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() },
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	_, err = failing.Register(t.Context(), workspace.RegisterClaimRequest{
		WorkspaceID: f.ws,
		Base:        f.base,
		Addresses:   []string{"aws_instance.a"},
		Principal:   "ada",
		JobID:       "job-1",
		Replace:     false,
		Trigger:     "",
	}, f.fence)
	if err == nil {
		t.Fatal("register with failing audit = nil, want an error")
	}

	if got := failing.List(t.Context(), f.ws); len(got) != 0 {
		t.Fatalf("list holds %d claims, want none after the audit failure", len(got))
	}
}

var errAcceptUnavailable = errors.New("audit store does not answer")

// TestClaimBadWorkspaceOrBase proves that a claim on a missing workspace
// fails with 404, on a deleted workspace with 410, on a locked workspace
// with 422, and with an unknown base version with 422. The server stores
// no claim.
func TestClaimBadWorkspaceOrBase(t *testing.T) {
	t.Parallel()

	t.Run("missing workspace", func(t *testing.T) {
		t.Parallel()

		f := newClaimFixture()
		r := f.registry(nil)

		_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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
		r := f.registry(nil)

		_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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
		r := f.registry(nil)

		_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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
		r := f.registry(nil)

		_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

		if got := r.List(t.Context(), f.ws); len(got) != 0 {
			t.Fatalf("list holds %d claims, want none", len(got))
		}
	})
}

// TestClaimStaleFenceRefused proves that a write with a stale fence is
// refused, as in the fenced stores.
func TestClaimStaleFenceRefused(t *testing.T) {
	t.Parallel()

	f := newClaimFixture()
	r := f.registry(nil)

	f.register(t, r, "job-1", "aws_instance.a")

	stale := f.fence
	stale.FailoverVersion++

	_, err := r.Register(t.Context(), workspace.RegisterClaimRequest{
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

// claimDialectStore is the store surface that the parallel registration
// test needs: the workspace and version writes of the fixture plus the
// read sides of the registry.
type claimDialectStore interface {
	workspace.ClaimWorkspaces
	workspace.ClaimVersions
	CreateNamespace(ctx context.Context, ns namespace.Namespace) error
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	CreateStateVersion(ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int) error
}

// claimDialect is one SQL dialect of the parallel registration test.
type claimDialect struct {
	name string
	open func(t *testing.T) claimDialectStore
}

// claimDialects returns SQLite, and PostgreSQL when
// DUSTPAN_TEST_POSTGRES_URL names a server.
func claimDialects() []claimDialect {
	list := []claimDialect{{name: "sqlite", open: openClaimSQLite}}

	if os.Getenv(claimPostgresEnv) != "" {
		list = append(list, claimDialect{name: "postgres", open: openClaimPostgres})
	}

	return list
}

// claimPostgresEnv names the variable with the URL of a PostgreSQL server
// for the tests. Each test uses a schema of its own.
const claimPostgresEnv = "DUSTPAN_TEST_POSTGRES_URL"

// openClaimSQLite opens an empty SQLite store.
//
//nolint:ireturn // The dialect is chosen at runtime: SQLite here, PostgreSQL below.
func openClaimSQLite(t *testing.T) claimDialectStore {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s
}

// openClaimPostgres opens a PostgreSQL store on a schema of its own.
//
//nolint:ireturn // The dialect is chosen at runtime: PostgreSQL here, SQLite above.
func openClaimPostgres(t *testing.T) claimDialectStore {
	t.Helper()

	base := os.Getenv(claimPostgresEnv)

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "claim_" + hex.EncodeToString(suffix)

	if err := claimExecSQL(t.Context(), base, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	t.Cleanup(func() {
		for _, schema := range []string{name, name + "_control"} {
			drop := "DROP SCHEMA IF EXISTS " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
			if dropErr := claimExecSQL(context.Background(), base, drop); dropErr != nil {
				t.Errorf("drop schema %s: %v", schema, dropErr)
			}
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", claimPostgresEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	s, err := postgres.Open(t.Context(), u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// claimExecSQL runs one statement on the database at dbURL.
func claimExecSQL(ctx context.Context, dbURL, stmt string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	_, execErr := conn.Exec(ctx, stmt)

	if closeErr := conn.Close(ctx); closeErr != nil {
		return errors.Join(execErr, fmt.Errorf("close: %w", closeErr))
	}

	if execErr != nil {
		return fmt.Errorf("exec: %w", execErr)
	}

	return nil
}

// seedClaimWorkspace creates namespace acme, workspace ws-9 and base
// version sv-9 in the store, and returns the fence, the workspace ID and
// the base version ID.
func seedClaimWorkspace(
	t *testing.T, s claimDialectStore,
) (namespace.Fence, workspace.ID, workspace.StateVersionID) {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	ns := namespace.Namespace{
		CreatedAt:           now,
		ID:                  namespace.NewID(),
		Name:                "acme",
		PrimaryInstallation: "local",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	fence := ns.Fence()
	wsID := workspace.ID("ws-9")
	w := workspace.Workspace{
		NamespaceID:  ns.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		InCollection: false,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
		ID:           wsID,
		Ref:          workspace.Ref{Namespace: ns.Name, Name: workspace.Name("network")},
		Revision:     1,
	}

	if err := s.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	base := workspace.StateVersionID("sv-9")

	versionErr := s.CreateStateVersion(t.Context(), fixturePendingVersion(base, wsID), fence,
		workspace.DefaultMaxPendingVersions)
	if versionErr != nil {
		t.Fatalf("create base version: %v", versionErr)
	}

	return fence, wsID, base
}

// fixturePendingVersion returns a pending base state version with every
// field set explicitly.
func fixturePendingVersion(base workspace.StateVersionID, wsID workspace.ID) workspace.StateVersion {
	return workspace.StateVersion{
		CreatedAt:       time.Time{},
		FinalizedAt:     time.Time{},
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		Holder:          "",
		LockID:          "",
		ID:              base,
		Workspace:       wsID,
		Lineage:         "lin-9",
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
}

// claimParallelSetup holds the registry and the IDs of one dialect run
// of the parallel registration test.
type claimParallelSetup struct {
	registry *workspace.ClaimRegistry
	fence    namespace.Fence
	wsID     workspace.ID
	base     workspace.StateVersionID
}

// newClaimParallelSetup opens the dialect store, seeds one workspace with
// one base version, and returns the registry over it.
func newClaimParallelSetup(t *testing.T, dialect claimDialect) *claimParallelSetup {
	t.Helper()

	s := dialect.open(t)
	fence, wsID, base := seedClaimWorkspace(t, s)

	r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: s,
		Versions:   s,
		Deleted:    nil,
		Manual:     nil,
		OnAccept:   nil,
		Now:        time.Now,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	return &claimParallelSetup{registry: r, fence: fence, wsID: wsID, base: base}
}

// registerDisjointFanout registers one disjoint claim per caller in
// parallel and returns the claim IDs in caller order.
func registerDisjointFanout(
	t *testing.T, setup *claimParallelSetup, callers int,
) []string {
	t.Helper()

	var wg sync.WaitGroup

	accepted := make([]string, callers)
	errs := make([]error, callers)

	for i := range callers {
		wg.Go(func() {
			claim, err := setup.registry.Register(context.Background(), workspace.RegisterClaimRequest{
				WorkspaceID: setup.wsID,
				Base:        setup.base,
				Addresses:   []string{fmt.Sprintf("aws_instance.node_%d", i)},
				Principal:   "ada",
				JobID:       fmt.Sprintf("job-%d", i),
				Replace:     false,
				Trigger:     "",
			}, setup.fence)
			if err == nil {
				accepted[i] = claim.ID
			} else {
				errs[i] = err
			}
		})
	}

	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("register %d = %v, want accepted", i, errs[i])
		}

		if accepted[i] == "" {
			t.Fatalf("register %d stored no claim ID", i)
		}
	}

	return accepted
}

// raceSharedAddress races two registrations over one address and proves
// that exactly one wins, and that the loser names the winner as the
// blocker.
func raceSharedAddress(t *testing.T, setup *claimParallelSetup) {
	t.Helper()

	var race sync.WaitGroup

	raceErrs := make([]error, 2)
	raceIDs := make([]string, 2)

	for i := range 2 {
		race.Go(func() {
			claim, err := setup.registry.Register(context.Background(), workspace.RegisterClaimRequest{
				WorkspaceID: setup.wsID,
				Base:        setup.base,
				Addresses:   []string{"aws_instance.shared"},
				Principal:   "ada",
				JobID:       fmt.Sprintf("race-%d", i),
				Replace:     false,
				Trigger:     "",
			}, setup.fence)
			if err == nil {
				raceIDs[i] = claim.ID
			} else {
				raceErrs[i] = err
			}
		})
	}

	race.Wait()

	wins := 0

	for i := range 2 {
		if raceIDs[i] != "" {
			wins++
		}

		if raceErrs[i] != nil && !errors.Is(raceErrs[i], workspace.ErrClaimOverlap) {
			t.Fatalf("race register %d = %v, want accepted or overlap", i, raceErrs[i])
		}
	}

	if wins != 1 {
		t.Fatalf("race winners = %d, want exactly 1", wins)
	}
}

// TestClaimParallelDisjoint proves that parallel registrations of disjoint
// claims serialize on the workspace row lock and all land, on each SQL
// dialect: two registrations that race over the same accepted set cannot
// both pass.
func TestClaimParallelDisjoint(t *testing.T) {
	t.Parallel()

	const callers = 24

	for _, dialect := range claimDialects() {
		t.Run(dialect.name, func(t *testing.T) {
			t.Parallel()

			setup := newClaimParallelSetup(t, dialect)
			registerDisjointFanout(t, setup, callers)

			if got := setup.registry.List(context.Background(), setup.wsID); len(got) != callers {
				t.Fatalf("list holds %d claims, want %d", len(got), callers)
			}

			raceSharedAddress(t, setup)
		})
	}
}
