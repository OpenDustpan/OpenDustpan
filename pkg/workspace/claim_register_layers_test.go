package workspace_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		mutate func(f *claimFixture, req *workspace.RegisterClaimRequest)
		want   error
	}{
		{
			"empty set",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = nil },
			workspace.ErrClaimEmptySet,
		},
		{
			"blank addresses",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = []string{" ", ""} },
			workspace.ErrClaimEmptySet,
		},
		{
			"replace",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Replace = true },
			workspace.ErrClaimReplace,
		},
		{
			"oversize address list",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Addresses = many },
			workspace.ErrClaimTooLarge,
		},
		{
			"address too long",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) {
				r.Addresses = []string{strings.Repeat("a", workspace.MaxClaimAddressLength+1)}
			},
			workspace.ErrClaimTooLarge,
		},
		{
			"unknown base",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.Base = "sv-missing" },
			workspace.ErrClaimBadBase,
		},
		{
			"unknown workspace",
			func(_ *claimFixture, r *workspace.RegisterClaimRequest) { r.WorkspaceID = "ws-missing" },
			workspace.ErrClaimNotFound,
		},
		{
			"fence of another namespace",
			func(f *claimFixture, _ *workspace.RegisterClaimRequest) {
				f.fence = namespace.Fence{Namespace: "elsewhere", FailoverVersion: f.fence.FailoverVersion}
			},
			namespace.ErrFenced,
		},
		{
			"locked workspace",
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
				Workspaces: f.workspaces, Versions: f.versions, Deleted: f.deleted, Manual: nil,
				Store: counting, Now: time.Now,
			})
			if err != nil {
				t.Fatalf("registry: %v", err)
			}

			req := &workspace.RegisterClaimRequest{
				Audit: claimAudit(), WorkspaceID: f.ws, Base: f.base, Addresses: []string{"aws_instance.a"},
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
