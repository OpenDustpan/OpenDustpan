package postgres_test

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestRetryWithSameIdentityMakesNoDuplicate drives the workspace service over
// the PostgreSQL store. The reply of an upload is lost, and the client sends
// the same upload again with the same upload token: the service returns the
// committed version, and the store holds no second version. The store keeps
// the port contract: a finalize of a version that is not pending returns
// ErrUploadDenied.
func TestRetryWithSameIdentityMakesNoDuplicate(t *testing.T) {
	t.Parallel()

	env := newLockEnv(t, outageBase(t))
	env.lockAsAlice(t)

	ctx := t.Context()
	content := `{"v":1}`
	pending := beginStateAsAlice(t, env, content)

	committed, err := env.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// The reply is lost: the client sends the same upload again.
	retried, err := env.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("retry Upload: %v", err)
	}

	wantSameVersion(t, "retry", retried, committed)

	// A retry with the same token and other content is denied.
	_, err = env.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(`{"v":2}`))
	if !errors.Is(err, workspace.ErrUploadDenied) {
		t.Fatalf("Upload of other content = %v, want workspace.ErrUploadDenied", err)
	}

	// The store refuses a second finalize of the committed version.
	fence := namespace.Fence{Namespace: env.ws.Ref.Namespace, FailoverVersion: 1}
	if finErr := env.s.FinalizeStateVersion(ctx, committed, fence); !errors.Is(finErr, workspace.ErrUploadDenied) {
		t.Fatalf("FinalizeStateVersion of a finalized version = %v, want workspace.ErrUploadDenied", finErr)
	}

	cur, err := env.s.CurrentStateVersion(ctx, env.ws.ID)
	if err != nil {
		t.Fatalf("CurrentStateVersion: %v", err)
	}

	wantSameVersion(t, "current state version", cur, committed)

	if count := countStateVersions(t, env); count != 1 {
		t.Fatalf("state version rows = %d, want 1", count)
	}
}

// beginStateAsAlice declares serial 1 of content in the workspace of env as
// alice, who holds the lock.
func beginStateAsAlice(t *testing.T, env lockEnv, content string) workspace.PendingState {
	t.Helper()

	sum := blob.DigestBytes([]byte(content)).MD5

	pending, err := env.svc.BeginState(t.Context(), env.ws.ID, "alice", workspace.NewState{
		Lineage: "lineage-1", MD5: hex.EncodeToString(sum[:]), Outputs: nil, Serial: 1, Force: false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("BeginState: %v", err)
	}

	return pending
}

// wantSameVersion fails the test when got is not the version want.
func wantSameVersion(t *testing.T, what string, got, want workspace.StateVersion) {
	t.Helper()

	if got.ID != want.ID || got.Content != want.Content || got.MD5 != want.MD5 {
		t.Fatalf("%s = %s (%s), want %s (%s)", what, got.ID, got.Content, want.ID, want.Content)
	}
}

// countStateVersions returns the number of state version rows of the
// workspace of env.
func countStateVersions(t *testing.T, env lockEnv) int {
	t.Helper()

	var count int

	query := `SELECT count(*) FROM state_versions WHERE workspace_id = $1`
	if err := postgres.Pool(env.s).QueryRow(t.Context(), query, env.ws.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count state versions: %v", err)
	}

	return count
}
