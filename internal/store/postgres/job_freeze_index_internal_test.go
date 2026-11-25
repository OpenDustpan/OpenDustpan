package postgres

import (
	"errors"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pgtest"
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

	base := os.Getenv(membershipURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", membershipURLEnv)
	}

	st, err := Open(t.Context(), pgtest.MigratedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.Close)

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                namespace.Name("fzindex"),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	row := func(id string, endsMs int64) jobstore.FreezeRow {
		return jobstore.FreezeRow{
			ID: id, Namespace: ns.Name, Reason: "release week", StartsMs: 1_700_000_000_000, EndsMs: endsMs,
			CreatedBy: "al", EndedBy: "",
		}
	}

	runner := jobRunner{s: st}

	err = runner.Write(t.Context(), ns.Fence(), func(tx jobstore.Tx) error {
		// An ended freeze is not open: the index lets it stay beside the open one.
		if insertErr := tx.InsertFreeze(t.Context(), row("frz-ended", 1_700_000_100_000)); insertErr != nil {
			return insertErr
		}

		return tx.InsertFreeze(t.Context(), row("frz-one", 0))
	})
	if err != nil {
		t.Fatalf("first Write = %v", err)
	}

	err = runner.Write(t.Context(), ns.Fence(), func(tx jobstore.Tx) error {
		return tx.InsertFreeze(t.Context(), row("frz-two", 0))
	})
	if !errors.Is(err, job.ErrFreezeActive) {
		t.Fatalf("second open InsertFreeze = %v, want ErrFreezeActive", err)
	}
}
