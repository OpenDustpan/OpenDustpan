package tfe_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// manualLockedWorkspaces refuses every lock request with a manual lock.
type manualLockedWorkspaces struct {
	conflictWorkspaces

	lock manuallock.Lock
}

func (m manualLockedWorkspaces) Lock(_ context.Context, id workspace.ID, _, _ string) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return workspace.Workspace{}, fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{Lock: m.lock})
}

// TestLockRefusedByAManualLock proves a lock request that a manual lock refuses
// answers 409 in the TFE error shape, and that the detail names the object, the
// holder and the reason of the manual lock, not the holder of a tool lock.
func TestLockRefusedByAManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target manuallock.Target
		want   []string
	}{
		{
			name:   "lock on the unit",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}),
			want:   []string{"unit opentofu:acme/web", "alice", "change window closed"},
		},
		{
			name:   "lock on the collection",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
			want:   []string{"collection acme/payments", "alice", "change window closed"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lock := manuallock.Lock{
				Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
				Target: tc.target, Holder: "alice", Reason: "change window closed",
			}

			handler, err := tfe.NewHandler(tfe.Config{
				Authenticator: tokenAuthenticator{},
				Authorizer:    allowAll(),
				AddressGate:   nil,
				Logger:        slog.New(slog.DiscardHandler),
				Namespaces:    newTestNamespaces(t),
				Workspaces: manualLockedWorkspaces{
					conflictWorkspaces: conflictWorkspaces{stubWorkspaces: stubWorkspaces{}},
					lock:               lock,
				},
				BaseURL:        baseurl.Resolver{},
				Audit:          nil,
				Gate:           nil,
				MaxStateBytes:  0,
				OnAuditFailure: nil,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/lock")

			for _, want := range tc.want {
				if !strings.Contains(detail, want) {
					t.Errorf("detail = %q, want it to hold %q", detail, want)
				}
			}

			if strings.Contains(detail, "is locked by User") {
				t.Errorf("detail = %q: a manual lock does not use the words of a tool lock", detail)
			}
		})
	}
}
