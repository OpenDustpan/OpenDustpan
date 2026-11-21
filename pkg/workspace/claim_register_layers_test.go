package workspace_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// countingClaimStore counts the transactions that reach the store. A hostile
// request that a registry guard refuses never reaches it: the count shows
// that the registry layer refused it alone, whatever the store would do.
type countingClaimStore struct {
	workspace.ClaimStore

	transactions atomic.Int32
}

func (s *countingClaimStore) WithClaims(
	ctx context.Context, ws workspace.ID, fence namespace.Fence, fn func(tx workspace.ClaimTx) error,
) error {
	s.transactions.Add(1)

	return s.ClaimStore.WithClaims(ctx, ws, fence, fn)
}

// TestClaimRegistryGuardsRefuseBeforeTheStore sends hostile requests to the
// registry layer. Each guard of the registry refuses its request before the
// store transaction opens, so the store never sees it.
func TestClaimRegistryGuardsRefuseBeforeTheStore(t *testing.T) {
	t.Parallel()

	many := make([]string, workspace.MaxClaimAddresses+1)
	for i := range many {
		many[i] = "aws_instance.n" + strconv.Itoa(i)
	}

	cases := []struct {
		name   string
		manual workspace.ManualLocks
		mutate func(f *claimFixture, req *workspace.RegisterClaimRequest)
		want   error
	}{
		{
			"manual lock that covers the workspace",
			fakeClaimManual{lock: true, err: nil},
			func(*claimFixture, *workspace.RegisterClaimRequest) {}, workspace.ErrClaimLocked,
		},
		{
			"manual lock store that fails",
			fakeClaimManual{lock: false, err: errClaimManualStore},
			func(*claimFixture, *workspace.RegisterClaimRequest) {}, errClaimManualStore,
		},
		{
			"empty set", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = nil },
			workspace.ErrClaimEmptySet,
		},
		{
			"blank addresses", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = []string{" ", ""} },
			workspace.ErrClaimEmptySet,
		},
		{
			"job token with no job", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.ViaJobToken = true; r.JobID = "" },
			workspace.ErrClaimInvalid,
		},
		{
			"replace", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Replace = true },
			workspace.ErrClaimReplace,
		},
		{
			"oversize address list", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = many },
			workspace.ErrClaimTooLarge,
		},
		{
			"address too long", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) {
				r.Addresses = []string{strings.Repeat("a", workspace.MaxClaimAddressLength+1)}
			},
			workspace.ErrClaimTooLarge,
		},
		{
			"unknown base", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Base = "sv-missing" },
			workspace.ErrClaimBadBase,
		},
		{
			"unknown workspace", nil,
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.WorkspaceID = "ws-missing" },
			workspace.ErrClaimNotFound,
		},
		{
			"fence of another namespace", nil,
			func(f *claimFixture, _ *workspace.RegisterClaimRequest) {
				f.fence = namespace.Fence{Namespace: "elsewhere", FailoverVersion: f.fence.FailoverVersion}
			},
			namespace.ErrFenced,
		},
		{
			"locked workspace", nil,
			func(f *claimFixture, _ *workspace.RegisterClaimRequest) {
				w := f.workspaces.spaces[f.ws]
				w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "lock-1", Holder: "bob", Reason: ""}
				f.workspaces.spaces[f.ws] = w
			},
			workspace.ErrClaimLocked,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newClaimFixture()
			counting := &countingClaimStore{
				ClaimStore:   workspace.NewMemoryClaimStore(f.workspaces),
				transactions: atomic.Int32{},
			}

			r, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
				Workspaces: f.workspaces, Versions: f.versions, Deleted: f.deleted, Manual: c.manual,
				Store: counting, Now: time.Now,
				Lease: 0,
			})
			if err != nil {
				t.Fatalf("registry: %v", err)
			}

			req := &workspace.RegisterClaimRequest{
				ViaJobToken: false,
				Audit:       claimAudit(), WorkspaceID: f.ws, Base: f.base, Addresses: []string{"aws_instance.a"},
				Replace: false, Principal: "ada", JobID: "job-1", Trigger: "",
			}
			c.mutate(f, req)

			if _, err = r.Register(t.Context(), req, f.fence); !errors.Is(err, c.want) {
				t.Fatalf("register = %v, want %v", err, c.want)
			}

			if n := counting.transactions.Load(); n != 0 {
				t.Fatalf("the store opened %d transactions, want 0: the registry guard must refuse first", n)
			}
		})
	}
}

var errClaimManualStore = errors.New("the manual lock store does not answer")

// fakeClaimManual is a ManualLocks fake: it covers each unit when lock is set,
// and answers err when err is set.
type fakeClaimManual struct {
	lock bool
	err  error
}

func (f fakeClaimManual) CoveringFor(context.Context, manuallock.Unit, string) (manuallock.Lock, error) {
	var none manuallock.Lock

	switch {
	case f.err != nil:
		return none, f.err
	case f.lock:
		return manuallock.Lock{Since: time.Time{}, Target: noTarget(), Holder: "ops", Reason: "freeze"}, nil
	default:
		return none, manuallock.ErrNotLocked
	}
}

func (f fakeClaimManual) NamespaceLock(context.Context, namespace.Name) (manuallock.Lock, error) {
	var none manuallock.Lock

	return none, manuallock.ErrNotLocked
}

func (f fakeClaimManual) NamespaceLockFor(
	ctx context.Context, unit manuallock.Unit, jobID string,
) (manuallock.Lock, error) {
	return f.CoveringFor(ctx, unit, jobID)
}

func (f fakeClaimManual) PhaseStart(context.Context, manuallock.Unit, string) time.Time {
	var none time.Time

	return none
}

// noTarget returns the zero manual lock target.
func noTarget() manuallock.Target {
	var t manuallock.Target

	return t
}
