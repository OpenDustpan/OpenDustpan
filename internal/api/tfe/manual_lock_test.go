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

	refusal := &manuallock.LockedError{Lock: m.lock, Noun: "workspace", Name: "acme/web"}

	return workspace.Workspace{}, fmt.Errorf("%w: %w", workspace.ErrLocked, refusal)
}

// TestLockRefusedByAManualLock proves a lock request that a manual lock refuses
// answers 409 in the TFE error shape, and that the detail names the object, the
// holder and the reason of the manual lock, not the holder of a tool lock.
func TestLockRefusedByAManualLock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target manuallock.Target
		want   string
	}{
		{
			name:   "lock on the unit",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"}),
			want:   "workspace acme/web has a manual lock by alice since 2026-10-19T10:00:00Z: change window closed",
		},
		{
			name:   "lock on the collection",
			target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
			want: "workspace acme/web is in collection acme/payments, which has a manual lock by alice since " +
				"2026-10-19T10:00:00Z: change window closed",
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
				DenialLimiter:  nil,
			})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}

			detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/lock")

			checkManualDetail(t, detail, tc.want)
		})
	}
}

// checkManualDetail checks that the detail holds the text of the manual lock in
// the words of the CLI: workspace, not unit, and not the words of a tool lock.
func checkManualDetail(t *testing.T, detail, want string) {
	t.Helper()

	if !strings.Contains(detail, want) {
		t.Errorf("detail = %q, want it to hold %q", detail, want)
	}

	if strings.Contains(detail, "unit") {
		t.Errorf("detail = %q: the CLI shows the word workspace, not unit", detail)
	}

	if strings.Contains(detail, "is locked by User") {
		t.Errorf("detail = %q: a manual lock does not use the words of a tool lock", detail)
	}
}
