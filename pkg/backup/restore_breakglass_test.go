package backup_test

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestBreakGlassRestoreAndRace proves that a restore whose swap refuses the
// break-glass floor ends as a failed restore, names the catalog code of the
// refusal and changes no live data.
func TestBreakGlassRestoreAndRace(t *testing.T) {
	t.Parallel()

	t.Run("restore refusal below floor", func(t *testing.T) {
		t.Parallel()

		setup := newRestoreServiceSetup(t)
		setup.live.applyErr = fmt.Errorf("swap the staged data: %w", principal.ErrNoWorkingBreakGlass)

		id, err := startRestoreOf(t, setup, plainRestore())
		if err != nil {
			t.Fatalf("start restore: %v", err)
		}

		row := waitRestoreEnd(t, setup.regs, id)
		if row.State != backup.RestoreFailed {
			t.Fatalf("restore state = %q, want %q", row.State, backup.RestoreFailed)
		}

		if row.DevReport == nil || row.DevReport.ErrorCode != backup.CodeNoWorkingBreakGlass {
			t.Fatalf("restore code = %+v, want %q", row.DevReport, backup.CodeNoWorkingBreakGlass)
		}

		if got := setup.live.counts(); got.applied != 0 || got.kept != 0 {
			t.Fatalf("applied = %d, kept = %d, want no swap", got.applied, got.kept)
		}
	})
}
