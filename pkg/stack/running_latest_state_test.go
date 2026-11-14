package stack_test

import (
	"bytes"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// runningLatestCase starts an update on the fixture, whose stack holds the
// finished base update, and returns the version and the deployment that the
// last saved checkpoint holds.
type runningLatestCase struct {
	name  string
	start func(t *testing.T, f *buildFixture) (version int64, deployment []byte)
}

// savedDeployment is the deployment that a running legacy update saves: the
// base with the resource that the journal batches add.
func savedDeployment() []byte {
	return []byte(`{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"resources":[` + baseResource + `,` + newResource + `]}`)
}

func runningLatestCases() []runningLatestCase {
	return []runningLatestCase{
		{
			name: "a running legacy update that saved no checkpoint",
			start: func(t *testing.T, f *buildFixture) (int64, []byte) {
				t.Helper()

				prior, err := f.svc.LatestStateVersion(t.Context(), f.ref)
				if err != nil {
					t.Fatalf("latest state version of the base: %v", err)
				}

				plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

				u, err := f.svc.Create(t.Context(), f.ref, plan)
				if err != nil {
					t.Fatalf("create: %v", err)
				}

				if _, err = f.svc.Start(t.Context(), f.ref, u.ID); err != nil {
					t.Fatalf("start: %v", err)
				}

				return prior, f.base
			},
		},
		{
			name: "a running legacy update that saved a checkpoint",
			start: func(t *testing.T, f *buildFixture) (int64, []byte) {
				t.Helper()

				plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

				u, err := f.svc.Create(t.Context(), f.ref, plan)
				if err != nil {
					t.Fatalf("create: %v", err)
				}

				s, err := f.svc.Start(t.Context(), f.ref, u.ID)
				if err != nil {
					t.Fatalf("start: %v", err)
				}

				saved := savedDeployment()
				in := stack.CheckpointInput{
					Features: nil, Deployment: saved, SchemaVersion: 3, Sequence: 0, Invalid: false,
				}

				if err = f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, in); err != nil {
					t.Fatalf("save checkpoint: %v", err)
				}

				return s.Update.Version, saved
			},
		},
		{
			name: "a running journaled update with batches",
			start: func(t *testing.T, f *buildFixture) (int64, []byte) {
				t.Helper()

				prior, err := f.svc.LatestStateVersion(t.Context(), f.ref)
				if err != nil {
					t.Fatalf("latest state version of the base: %v", err)
				}

				f.startJournaled(t)

				return prior, f.base
			},
		},
	}
}

// checkRunningLatest proves the answers of one case: the version, the
// deployment that the outputs source reads for it, and no rebuild from the
// batches.
func checkRunningLatest(t *testing.T, f *buildFixture, wantVersion int64, wantDeployment []byte) {
	t.Helper()

	version, err := f.svc.LatestStateVersion(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("LatestStateVersion: %v", err)
	}

	if version != wantVersion {
		t.Fatalf("LatestStateVersion = %d, want %d", version, wantVersion)
	}

	// The outputs source reads the deployment through this call.
	cp, err := f.svc.ExportVersion(t.Context(), f.ref, version)
	if err != nil {
		t.Fatalf("ExportVersion(%d): %v", version, err)
	}

	if !bytes.Equal(cp.Deployment, wantDeployment) {
		t.Fatalf("ExportVersion(%d) = %s, want the saved checkpoint %s", version, cp.Deployment, wantDeployment)
	}

	if built := f.replay(t); bytes.Equal(cp.Deployment, built) {
		t.Fatalf("ExportVersion(%d) rebuilt the state from the batches: %s", version, cp.Deployment)
	}
}

// TestRunningUpdateLatestStateIsTheLastSavedCheckpoint proves that while an
// update runs, the latest state is the last checkpoint that was saved: the
// version that LatestStateVersion answers, and the deployment that the outputs
// source reads for that version, are the saved ones and never a state that is
// built from the batches of a journaled update.
func TestRunningUpdateLatestStateIsTheLastSavedCheckpoint(t *testing.T) {
	t.Parallel()

	for _, tt := range runningLatestCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

			wantVersion, wantDeployment := tt.start(t, f)
			checkRunningLatest(t, f, wantVersion, wantDeployment)
		})
	}
}
