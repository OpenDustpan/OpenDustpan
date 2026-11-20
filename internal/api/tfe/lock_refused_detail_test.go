package tfe_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// lateLockWorkspaces models a lock that another principal takes after the
// handler read the workspace as free. Until LockForJobInCollection runs, GetByID reports
// a free workspace. LockForJobInCollection then refuses with refusal. Later reads report
// the workspace as held by laterHolder, or as free when laterHolder is empty.
type lateLockWorkspaces struct {
	stubWorkspaces

	refusal      error
	laterHolder  string
	lockCalls    atomic.Int32
	readsPreLock atomic.Int32
}

func (l *lateLockWorkspaces) LockForJobInCollection(
	_ context.Context, id workspace.ID, _, _, _ string,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	l.lockCalls.Add(1)

	return workspace.Workspace{}, l.refusal
}

func (l *lateLockWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if l.lockCalls.Load() == 0 {
		l.readsPreLock.Add(1)

		return stubWorkspaceByID(id)
	}

	if l.laterHolder == "" {
		return stubWorkspaceByID(id)
	}

	held := &raceWorkspaces{stubWorkspaces: stubWorkspaces{}, mu: sync.Mutex{}, holder: l.laterHolder}

	return held.lockedWorkspace(l.laterHolder), nil
}

// TestLockRefusedAfterFreeReadKeepsTheHolderDetail proves that a lock request
// answers 409 with the words that the OpenTofu CLI parses, "is locked by
// User" and the holder, when the workspace port refuses with ErrLocked after
// the first read saw no holder. Each case sends the refusal straight to the
// ErrLocked branch of the lock step: the first read is free, so no other
// branch of the handler answers.
func TestLockRefusedAfterFreeReadKeepsTheHolderDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		refusal     error
		laterHolder string
		wantHolder  string
	}{
		{
			name:        "refusal without a lock names the holder of a later read",
			refusal:     workspace.ErrLocked,
			laterHolder: "alice",
			wantHolder:  "alice",
		},
		{
			name: "refusal with a lock names the holder of that lock, not of a later read",
			refusal: &workspace.LockRefusedError{
				Lock: workspace.Lock{LockedAt: stubWorkspaceTime(), ID: "lock-dana", Holder: "dana", Reason: ""},
				Err:  workspace.ErrLocked,
			},
			laterHolder: "alice",
			wantHolder:  "dana",
		},
		{
			name: "refusal with a lock names its holder when the later read sees no lock",
			refusal: &workspace.LockRefusedError{
				Lock: workspace.Lock{LockedAt: stubWorkspaceTime(), ID: "lock-dana", Holder: "dana", Reason: ""},
				Err:  workspace.ErrLocked,
			},
			laterHolder: "",
			wantHolder:  "dana",
		},
		{
			name:        "refusal without a lock keeps the phrase when the later read sees no lock",
			refusal:     workspace.ErrLocked,
			laterHolder: "",
			wantHolder:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workspaces := &lateLockWorkspaces{
				stubWorkspaces: stubWorkspaces{},
				refusal:        tt.refusal,
				laterHolder:    tt.laterHolder,
				lockCalls:      atomic.Int32{},
				readsPreLock:   atomic.Int32{},
			}
			handler := newConflictHandlerOver(t, workspaces)
			detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/lock")

			if workspaces.readsPreLock.Load() == 0 {
				t.Fatal("the handler did not read the workspace before the lock call")
			}

			if got := workspaces.lockCalls.Load(); got != 1 {
				t.Fatalf("LockForJobInCollection called %d times, want 1: a refusal ends the request", got)
			}

			want := "Workspace ws-test123 is locked by User " + tt.wantHolder
			if !strings.Contains(detail, want) {
				t.Fatalf("lock detail = %q, want it to contain %q", detail, want)
			}
		})
	}
}
