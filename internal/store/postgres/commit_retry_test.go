package postgres_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/workspace"
)

func TestRetryWithSameIdentityMakesNoDuplicate(t *testing.T) {
	t.Parallel()

	base := outageBase(t)
	schemaURL := pgtest.MigratedURL(t, base)
	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(s.Close)

	ctx := t.Context()
	ws, fence := seedStateCommitWorkspace(t, s)

	sv := pendingStateVersion(ws)
	if createErr := s.CreateStateVersion(ctx, sv, fence, 10); createErr != nil {
		t.Fatalf("CreateStateVersion: %v", createErr)
	}

	if finErr := s.FinalizeStateVersion(ctx, sv, fence); finErr != nil {
		t.Fatalf("FinalizeStateVersion: %v", finErr)
	}
	if retryErr := s.FinalizeStateVersion(ctx, sv, fence); retryErr != nil {
		t.Fatalf("retry FinalizeStateVersion: %v", retryErr)
	}

	// A hostile finalize names the committed version ID with other content.
	hostile := sv
	hostile.Content = blob.Key("states/hostile")
	hostile.MD5 = [16]byte{9, 9, 9}
	if hostileErr := s.FinalizeStateVersion(ctx, hostile, fence); !errors.Is(hostileErr, workspace.ErrUploadDenied) {
		t.Fatalf("hostile FinalizeStateVersion = %v, want ErrUploadDenied", hostileErr)
	}

	cur, err := s.CurrentStateVersion(ctx, ws.ID)
	if err != nil {
		t.Fatalf("CurrentStateVersion: %v", err)
	}
	if cur.ID != sv.ID || cur.Content != sv.Content || cur.MD5 != sv.MD5 {
		t.Fatalf("current state version = %s (%s), want %s (%s)", cur.ID, cur.Content, sv.ID, sv.Content)
	}

	var count int
	query := `SELECT count(*) FROM state_versions WHERE workspace_id = $1`
	if err := postgres.Pool(s).QueryRow(ctx, query, ws.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count state versions: %v", err)
	}
	if count != 1 {
		t.Fatalf("state version rows = %d, want 1", count)
	}
}
