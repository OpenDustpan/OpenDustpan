package audit_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

func TestLagSecondsUpToDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	if got := audit.LagSeconds(time.Time{}, now); got != 0 {
		t.Fatalf("lag with no pending entry = %v, want 0", got)
	}

	future := now.Add(time.Minute)
	if got := audit.LagSeconds(future, now); got != 0 {
		t.Fatalf("lag with a future entry = %v, want 0", got)
	}
}

func TestLagSecondsBehind(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-90 * time.Second)

	if got := audit.LagSeconds(oldest, now); got != 90 {
		t.Fatalf("lag = %v, want 90", got)
	}
}
