package sqlite_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestPortableRestoreClearsTheClientEncryptionBit proves a portable restore of
// the SQLite store clears the client-encryption bit of a state version: the
// restored row holds the unknown bit, so a reader streams the state blob to
// decide and never trusts a bit that a tampered or an inconsistent backup could
// have set wrong.
func TestPortableRestoreClearsTheClientEncryptionBit(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()

	fence, unit := stateWriteUnit(t, s)
	finalized := unitStateTofuFinalize(t, s, fence, unit)

	ps, ok := any(s).(storetest.PortableRestoreStore)
	if !ok {
		t.Fatal("the SQLite store does not export and stage the portable format")
	}

	files, sum, descs := storetest.PortableFiles(t, ps)

	_, stageErr := ps.StagePortable(
		ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, ps),
	)
	if stageErr != nil {
		t.Fatalf("stage portable backup = %v", stageErr)
	}

	t.Cleanup(func() {
		if discardErr := ps.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging = %v", discardErr)
		}
	})

	if err := ps.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup = %v", err)
	}

	sv, err := s.GetStateVersion(ctx, workspace.StateVersionID(finalized))
	if err != nil {
		t.Fatalf("GetStateVersion = %v", err)
	}

	if sv.ClientEncrypted != workspace.ClientEncryptedUnknown {
		t.Fatalf("client-encryption bit after the restore = %d, want the unknown bit", sv.ClientEncrypted)
	}
}
