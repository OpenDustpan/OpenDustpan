package postgres_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
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

	commit := postgres.NewStateCommitForTest(s, fence)
	if finErr := commit.FinalizeStateVersion(ctx, sv); finErr != nil {
		t.Fatalf("FinalizeStateVersion: %v", finErr)
	}
	if retryErr := commit.FinalizeStateVersion(ctx, sv); retryErr != nil {
		t.Fatalf("retry FinalizeStateVersion: %v", retryErr)
	}

	cur, err := s.CurrentStateVersion(ctx, ws.ID)
	if err != nil {
		t.Fatalf("CurrentStateVersion: %v", err)
	}
	if cur.ID != sv.ID {
		t.Fatalf("current state version = %s, want %s", cur.ID, sv.ID)
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
