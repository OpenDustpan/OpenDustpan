package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

var errSweepList = errors.New("the namespace store does not answer")

// fakeSweepNamespaces lists fixed namespaces, or fails.
type fakeSweepNamespaces struct {
	list []namespace.Namespace
	err  error
}

func (f fakeSweepNamespaces) List(context.Context) ([]namespace.Namespace, error) {
	return f.list, f.err
}

// sweepWorld holds a registry with a clock, and claims of the local wrapper on
// one workspace.
type sweepWorld struct {
	registry *workspace.ClaimRegistry
	backend  *workspacetest.MemoryClaimBackend
	clock    *atomic.Int64
	fence    namespace.Fence
	ws       workspace.ID
	ns       namespace.Namespace
}

func newSweepWorld(t *testing.T, claims int) *sweepWorld {
	t.Helper()

	b := workspacetest.NewMemoryClaimBackend()
	fence, ws := workspacetest.SeedClaimWorkspace(t, b)
	clock := new(atomic.Int64)
	clock.Store(1_700_000_000_000)

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: b, Versions: b, Deleted: nil, Manual: nil, Store: b,
		Now:   func() time.Time { return time.UnixMilli(clock.Load()).UTC() },
		Lease: 0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	w := &sweepWorld{
		registry: registry, backend: b, clock: clock, fence: fence, ws: ws, ns: namespace.Namespace{
			CreatedAt: time.Time{}, ID: "", Name: fence.Namespace, PrimaryInstallation: "",
			Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: fence.FailoverVersion,
		},
	}
	for i := range claims {
		addr := "aws_instance.n" + string(rune('a'+i))

		req := &workspace.RegisterClaimRequest{
			ViaJobToken: false, WorkspaceID: ws, Base: "sv-acme", Addresses: []string{addr}, Replace: false,
			Principal: "ada", JobID: "", Trigger: "", Audit: sweepAudit(fence),
		}
		if _, regErr := registry.Register(t.Context(), req, fence); regErr != nil {
			t.Fatalf("register: %v", regErr)
		}
	}

	return w
}

func sweepAudit(f namespace.Fence) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1_700_000_000_000).
			UTC(),
		Namespace:     f.Namespace,
		Subject:       "ada",
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(identity.ActionUnitClaimRegister),
		Object:        "unit:network",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
		Origin:        nil,
	}
}

func (w *sweepWorld) accepted(t *testing.T) int {
	t.Helper()

	list, err := w.registry.List(t.Context(), w.ws)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	n := 0

	for i := range list {
		if list[i].State == workspace.ClaimAccepted {
			n++
		}
	}

	return n
}

func (w *sweepWorld) sweeper(list fakeSweepNamespaces, perRun int) *claimSweeper {
	return &claimSweeper{
		claims:     w.registry,
		namespaces: list,
		logger:     slog.New(slog.DiscardHandler),
		maxPerRun:  perRun,
		cursor:     atomic.Int64{},
		every:      time.Second,
	}
}

// TestClaimSweeperEndsExpiredLeasesInBoundedRuns proves that a run ends the
// claims whose lease ended and no claim before, and that a run ends at most
// maxPerRun claims over all namespaces: the next run ends the rest. The
// lister names the namespace twice, so a run that did not carry the budget from
// one namespace to the next would end more than maxPerRun claims.
func TestClaimSweeperEndsExpiredLeasesInBoundedRuns(t *testing.T) {
	t.Parallel()

	w := newSweepWorld(t, 5)
	s := w.sweeper(fakeSweepNamespaces{list: []namespace.Namespace{w.ns, w.ns}, err: nil}, 2)

	s.sweepOnce(t.Context())

	if got := w.accepted(t); got != 5 {
		t.Fatalf("accepted claims before the lease ends = %d, want 5", got)
	}

	w.clock.Add((workspace.ClaimLease + time.Second).Milliseconds())

	for _, want := range []int{3, 1, 0, 0} {
		s.sweepOnce(t.Context())

		if got := w.accepted(t); got != want {
			t.Fatalf("accepted claims after a run = %d, want %d", got, want)
		}
	}
}

// TestClaimSweeperSurvivesAFailingNamespaceList proves that a failing list of
// namespaces ends no claim, does not panic, and the next run works.
func TestClaimSweeperSurvivesAFailingNamespaceList(t *testing.T) {
	t.Parallel()

	w := newSweepWorld(t, 1)
	w.clock.Add((workspace.ClaimLease + time.Second).Milliseconds())

	w.sweeper(fakeSweepNamespaces{list: nil, err: errSweepList}, 10).sweepOnce(t.Context())

	if got := w.accepted(t); got != 1 {
		t.Fatalf("accepted claims after a failing run = %d, want 1", got)
	}

	w.sweeper(fakeSweepNamespaces{list: []namespace.Namespace{w.ns}, err: nil}, 10).sweepOnce(t.Context())

	if got := w.accepted(t); got != 0 {
		t.Fatalf("accepted claims after a run = %d, want 0", got)
	}
}

// pendingExpirer is a claim expirer with a count of claims to end in each
// namespace. It records the order in which a run asked the namespaces.
type pendingExpirer struct {
	pending map[namespace.Name]int
	asked   []namespace.Name
}

func (p *pendingExpirer) ExpireLeases(_ context.Context, fence namespace.Fence, limit int) (int, error) {
	p.asked = append(p.asked, fence.Namespace)

	n := min(limit, p.pending[fence.Namespace])
	p.pending[fence.Namespace] -= n

	return n, nil
}

// TestClaimSweeperRotatesTheFirstNamespace proves that no namespace starves the
// others: three namespaces with five claims each, and a budget of two claims in
// each run. A run that always started at the first namespace would end claims
// of that namespace only, until it had none; with the rotation, each of the
// three namespaces has its claims ended in the first three runs.
func TestClaimSweeperRotatesTheFirstNamespace(t *testing.T) {
	t.Parallel()

	names := []namespace.Name{"a", "b", "c"}
	expirer := &pendingExpirer{pending: map[namespace.Name]int{"a": 5, "b": 5, "c": 5}, asked: nil}

	list := make([]namespace.Namespace, 0, len(names))
	for _, name := range names {
		list = append(list, namespace.Namespace{
			CreatedAt: time.Time{}, ID: "", Name: name, PrimaryInstallation: "",
			Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
		})
	}

	s := &claimSweeper{
		claims: expirer, namespaces: fakeSweepNamespaces{list: list, err: nil}, logger: slog.New(slog.DiscardHandler),
		maxPerRun: 2, every: time.Second, cursor: atomic.Int64{},
	}

	for range 3 {
		s.sweepOnce(t.Context())
	}

	for _, name := range names {
		if got := expirer.pending[name]; got != 3 {
			t.Errorf("claims left in %s after three runs = %d, want 3: every namespace gets a first turn", name, got)
		}
	}

	if got := expirer.asked[0]; got != "a" {
		t.Errorf("first namespace asked = %q, want a", got)
	}
}

// TestClaimSweeperWithNoNamespace proves that a run over an empty list ends
// nothing and does not divide by zero.
func TestClaimSweeperWithNoNamespace(t *testing.T) {
	t.Parallel()

	expirer := &pendingExpirer{pending: map[namespace.Name]int{}, asked: nil}
	s := &claimSweeper{
		claims: expirer, namespaces: fakeSweepNamespaces{list: nil, err: nil}, logger: slog.New(slog.DiscardHandler),
		maxPerRun: 2, every: time.Second, cursor: atomic.Int64{},
	}

	s.sweepOnce(t.Context())

	if len(expirer.asked) != 0 {
		t.Fatalf("namespaces asked = %v, want none", expirer.asked)
	}
}
