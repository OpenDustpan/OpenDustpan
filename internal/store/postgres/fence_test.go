package postgres_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestFenceRejectsStaleWrite(t *testing.T) {
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

	stale := namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion - 1}
	err = s.FinalizeStateVersion(ctx, sv, stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("FinalizeStateVersion with stale fence = %v, want namespace.ErrFenced", err)
	}
}
