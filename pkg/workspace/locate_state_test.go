package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

var errLocateKeyStore = errors.New("key store is down")

// faultSealer is a Sealer whose every open fails with fault and counts the
// calls, so a test proves which service calls open sealed content.
type faultSealer struct {
	fault error
	opens *atomic.Int32
}

func (f faultSealer) SealState(context.Context, namespace.Name, string, string, io.Reader) (io.Reader, error) {
	return nil, f.fault
}

func (f faultSealer) OpenState(
	context.Context, namespace.Name, string, string, io.Reader,
) (io.ReadCloser, error) {
	f.opens.Add(1)

	return nil, f.fault
}

func (f faultSealer) SealOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	return nil, f.fault
}

func (f faultSealer) OpenOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	f.opens.Add(1)

	return nil, f.fault
}

// faultService returns a service over the stores of f whose sealer fails
// every open with fault.
func faultService(t *testing.T, f sealFixture, fault error) (*workspace.Service, *atomic.Int32) {
	t.Helper()

	opens := &atomic.Int32{}

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             faultSealer{fault: fault, opens: opens},
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
		Retention:          0,
		OnDeleteWait:       nil,
	})
	if err != nil {
		t.Fatalf("fault service: %v", err)
	}

	return svc, opens
}

// TestStateVersionWorkspaceOpensNothing proves the lookup of the workspace of
// a state version never opens a sealed output, also when every open fails,
// and that a version that does not exist, is malformed or is not finalized
// answers ErrStateVersionNotFound. It fails when the lookup calls ReadState.
func TestStateVersionWorkspaceOpensNothing(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	content := `{"version":4,"lineage":"lin-old","serial":1,"outputs":{"greeting":{"value":"v","type":"string"}}}`
	f.pushSealed(t, content, sealedOutputsDoc("v"), 1)

	cur, err := f.sealed.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if _, lockErr := f.sealed.Lock(t.Context(), f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	pending, err := f.sealed.BeginState(t.Context(), f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1", MD5: sum(content), Outputs: sealedOutputsDoc("w"), Serial: 2, Force: false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	faults := map[string]error{
		"decrypt fault":   fmt.Errorf("open: %w", secret.ErrDecrypt),
		"key-store fault": errLocateKeyStore,
	}

	for faultName, fault := range faults {
		svc, opens := faultService(t, f, fault)

		// The read opens outputs, so the same fault reaches the caller here.
		// This row shows the fault is live: without it the zero count would
		// prove nothing.
		if _, readErr := svc.ReadState(t.Context(), cur.ID); readErr == nil {
			t.Errorf("%s: ReadState did not fail, so the fault is not live", faultName)
		}
		opens.Store(0)

		t.Cleanup(func() { checkNoOpens(t, faultName, opens) })
		t.Run(faultName, func(t *testing.T) {
			t.Parallel()

			runLocateRows(t, svc, []locateRow{
				{"finalized", cur.ID, f.ws.ID, nil},
				{"unknown", "sv-unknown", "", workspace.ErrStateVersionNotFound},
				{"malformed", "../x\x00", "", workspace.ErrStateVersionNotFound},
				{"empty", "", "", workspace.ErrStateVersionNotFound},
				{"pending", pending.Version.ID, "", workspace.ErrStateVersionNotFound},
			})
		})
	}
}

// locateRow is one lookup of TestStateVersionWorkspaceOpensNothing.
type locateRow struct {
	name    string
	id      workspace.StateVersionID
	wantWS  workspace.ID
	wantErr error
}

func runLocateRows(t *testing.T, svc *workspace.Service, rows []locateRow) {
	t.Helper()

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, lookupErr := svc.StateVersionWorkspace(t.Context(), tc.id)
			if !errors.Is(lookupErr, tc.wantErr) || got != tc.wantWS {
				t.Fatalf("lookup = %q, %v; want %q, %v", got, lookupErr, tc.wantWS, tc.wantErr)
			}
		})
	}
}

func checkNoOpens(t *testing.T, fault string, opens *atomic.Int32) {
	t.Helper()

	if n := opens.Load(); n != 0 {
		t.Errorf("%s: the lookup opened sealed content %d times, want 0", fault, n)
	}
}
