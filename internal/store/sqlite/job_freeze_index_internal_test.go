package sqlite

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Layer: the unique partial index job_freezes_one_open. The test inserts two
// open freezes of one namespace through the transaction, with no application
// check between them, so only the index can refuse the second. Remove the index
// and the second insert succeeds.
func TestJobFreezesOneOpenIndexRefusesSecondOpenFreeze(t *testing.T) {
	t.Parallel()

	st := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                namespace.Name("fzindex"),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	err := st.CreateNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	row := func(id string, endsMs int64) jobstore.FreezeRow {
		return jobstore.FreezeRow{
			ID: id, Namespace: ns.Name, Reason: "release week", StartsMs: 1_700_000_000_000, EndsMs: endsMs,
			CreatedBy: "al", EndedBy: "",
		}
	}

	err = jobRunner{s: st}.Write(t.Context(), ns.Fence(), func(tx jobstore.Tx) error {
		if insertErr := tx.InsertFreeze(t.Context(), row("frz-one", 0)); insertErr != nil {
			t.Errorf("first InsertFreeze = %v", insertErr)
		}

		// An ended freeze is not open: the index lets it stay beside the open one.
		if insertErr := tx.InsertFreeze(t.Context(), row("frz-ended", 1_700_000_100_000)); insertErr != nil {
			t.Errorf("InsertFreeze of an ended freeze = %v", insertErr)
		}

		if insertErr := tx.InsertFreeze(t.Context(), row("frz-two", 0)); !errors.Is(insertErr, job.ErrFreezeActive) {
			t.Errorf("second open InsertFreeze = %v, want ErrFreezeActive", insertErr)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Write = %v", err)
	}
}
