package storetest

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// StagedAreaStore is a store with the ports of a restore that runs as a
// durable operation, the portable export, and the installation state.
type StagedAreaStore interface {
	backup.RestoreOperations
	backup.RestoreStaging
	backup.PortableExporter
	maintenance.Store
	DurableOperationsPort() durable.Store
}

// StagedArea is a running restore of a store test, with its area staged from
// an export of the store, in maintenance mode: the state just before its
// swap.
type StagedArea struct {
	ID   backup.RestoreID
	Area backup.StagingArea
	// Swap is the fence of the swap step of the claim that staged the area.
	Swap durable.StepFence
}

// StageRestoreArea starts a restore operation on s, claims it, stages an
// export of s into the area of the claim token, and enters maintenance mode.
// A store test calls ApplyRestoreArea with the result to test the swap.
func StageRestoreArea(t *testing.T, s StagedAreaStore) StagedArea {
	t.Helper()

	ctx := t.Context()
	sink := &portableSink{files: make(map[string]*bytes.Buffer)}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	id := mustStartRestoreOp(t, s)
	held := mustClaimRestoreOp(t, s.DurableOperationsPort(), "instance-a")

	area, err := backup.NewStagingArea(id, held.Claim.Token)
	if err != nil {
		t.Fatalf("staging area: %v", err)
	}

	in := backup.RestoreStageInput{
		Open: sink.read, Want: sum, Files: files, Options: StageAs(t, s), Strip: false,
		StripMode: backup.DevStripDetect, DropIssuerKeys: false,
	}

	if _, _, err := s.StageRestore(ctx, area, in); err != nil {
		t.Fatalf("stage restore area %s: %v", area, err)
	}

	if err := s.SetState(ctx, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	return StagedArea{ID: id, Area: area, Swap: held.Claim.Step(backup.RestoreStepSwap)}
}
