package backup

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// applyRecorder records the decisions that reach ApplyRestoreArea. The
// embedded interface is nil: a call that needs another method panics.
type applyRecorder struct {
	RestoreLive

	applied int
}

// ApplyRestoreArea implements RestoreStaging.
func (a *applyRecorder) ApplyRestoreArea(
	context.Context, durable.StepFence, StagingArea, SwapDecision,
) (KeepAccessResult, error) {
	a.applied++

	return KeepAccessResult{DroppedTempAdmins: 0, ExpiredUpdateLeases: 0}, nil
}

// TestStepSwapRefusesAnInvalidDecision proves that the swap step checks the
// decision itself: a decision that keeps or widens the access tables without
// the live row of each kept setting never reaches the store, and the step
// returns ErrCorrupt, marked permanent in the code, so the restore fails.
func TestStepSwapRefusesAnInvalidDecision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		decision SwapDecision
	}{
		{name: "keeps the access tables and no setting row", decision: SwapDecision{
			KeepAccessTables: true, Widened: false, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
			SettingStatic: false,
		}},
		{name: "widens and keeps no setting row", decision: SwapDecision{
			KeepAccessTables: false, Widened: true, KeepSettingKeys: nil, SettingBefore: "", SettingAfter: "",
			SettingStatic: false,
		}},
		{name: "both widens and keeps", decision: SwapDecision{
			KeepAccessTables: true, Widened: true, KeepSettingKeys: KeptSettingKeys(), SettingBefore: "",
			SettingAfter: "", SettingStatic: false,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := MintRestoreID()

			area, err := NewStagingArea(id, 1)
			if err != nil {
				t.Fatalf("make staging area: %v", err)
			}

			live := &applyRecorder{RestoreLive: nil, applied: 0}
			svc := &RestoreService{
				live: live, operations: nil, canceler: nil, backups: nil, maintenance: nil, blobs: nil,
				leases: nil, target: nil, key: nil, installation: "", afterApply: nil, admit: nil,
				allowDev: false, devHint: "", lease: 0, lockWait: 0, now: nil,
				logger: slog.New(slog.DiscardHandler), runner: nil, accessSetting: nil,
				accessSettingStatic: nil,
				decide:              func(context.Context, restoreInput) SwapDecision { return tc.decision },
			}

			var op durable.Operation

			op.Steps = []durable.StepRecord{{
				Name: RestoreStepStage, Result: []byte(`{"area":"` + area.String() + `"}`), DoneAt: time.Time{},
			}}

			var (
				sc durable.StepContext
				in restoreInput
			)

			sc.Operation, in.ID = op, id

			_, err = svc.stepSwap(t.Context(), sc, in)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("stepSwap error = %v, want ErrCorrupt", err)
			}

			if live.applied != 0 {
				t.Errorf("the store applied %d invalid decisions, want 0", live.applied)
			}
		})
	}
}
