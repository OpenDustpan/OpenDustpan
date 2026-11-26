package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestWriteNewUnitRuleFailsClosedWithoutSettingRow proves the branch of the
// missing row alone: when the row of the setting is missing, the write returns
// namespace.ErrNotFound and writes no rule. It must not succeed silently.
func TestWriteNewUnitRuleFailsClosedWithoutSettingRow(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	got := writeNewUnitRule(t.Context(), s.queries, newUnit{
		At: time.UnixMilli(1_700_000_000_000).UTC(), Namespace: "ghost", Tool: inventoryTool, Name: "web/prod",
		ID: "stk-1", Cause: causeOfCreate(namespace.Fence{Namespace: "ghost", FailoverVersion: 1}),
	})
	if !errors.Is(got, namespace.ErrNotFound) {
		t.Fatalf("writeNewUnitRule without the row of the setting = %v, want namespace.ErrNotFound", got)
	}
}
