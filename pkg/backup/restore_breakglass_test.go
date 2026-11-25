package backup_test

import (
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestBreakGlassRestoreAndRace proves that a restore whose swap refuses the
// break-glass floor ends as a failed restore, names the catalog code of the
// refusal and changes no live data. It does not run a race. The parallel
// disable of the last break-glass accounts is TestBreakGlassGuardRace in
// pkg/principal. The fake of this test does not count the staged rows: it
// injects the swap error and checks the failed state, the code and that no
// swap was applied or kept. The count of the staged rows is the case "replace
// refuses two live working accounts that become one" of the restore
// conformance suite in pkg/store/storetest/restore.go: it stages a backup with
// one working account over two live ones, expects ErrNoWorkingBreakGlass from
// the apply and checks that the live accounts stay unchanged.
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
