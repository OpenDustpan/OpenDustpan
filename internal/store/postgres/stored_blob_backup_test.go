package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// storedDigestEraser clears the check values of the rows that name a blob, with
// direct SQL on a second connection, as the rows of an earlier version hold them.
type storedDigestEraser struct{ url string }

// EraseLogChunkDigests implements storetest.StoredDigestEraser.
func (e storedDigestEraser) EraseLogChunkDigests(ctx context.Context) error {
	return e.erase(ctx, `UPDATE job_log_chunks SET stored_size = 0, stored_md5 = ''`)
}

// EraseRegistryReleaseFiles implements storetest.StoredDigestEraser.
func (e storedDigestEraser) EraseRegistryReleaseFiles(ctx context.Context) error {
	return e.erase(ctx, `UPDATE registry_releases SET stored_files_json = ''`)
}

// erase runs stmt on a second connection of the database.
func (e storedDigestEraser) erase(ctx context.Context, stmt string) error {
	conn, err := pgx.Connect(ctx, e.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			_ = closeErr
		}
	}()

	if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
		return fmt.Errorf("erase stored digests: %w", execErr)
	}

	return nil
}

// newStoredBlobRig returns a rig over a fresh schema and a fresh live blob store.
func newStoredBlobRig(base string) storetest.StoredBlobBackupFactory {
	return func(t *testing.T, in storetest.StoredBlobBackupInput) *storetest.StoredBlobBackupRig {
		t.Helper()

		st := openBackup(t, base)

		if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
			t.Fatalf("claim the installation kind: %v", err)
		}

		live, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open live blob store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := live.Close(); closeErr != nil {
				t.Errorf("close live blob store: %v", closeErr)
			}
		})

		backups, restores := newRestoreAuditServices(t, st, live, in.Target, in.Key)
		jobs := st.Jobs()
		ops := st.DurableOperationsPort()

		return &storetest.StoredBlobBackupRig{
			Recorder: jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
				Harness: jobtest.Harness{
					Claims:     nil,
					Jobs:       jobs,
					Durable:    ops,
					Freezes:    jobs,
					Claimer:    job.NewClaimer(jobs, ops, st.Store, jobtest.NoGates{}),
					Namespaces: st.Store,
					Domain:     &jobDomain{s: st.Store},
					WriteState: nil,
					FailAudit:  nil,
					AgeJob:     nil,
				},
				Records: jobs, ItemKeys: st.Store, Blobs: live, Wrapper: in.Key,
			}),
			Registry: st.Registry(),
			Keys:     st.Store,
			Backups:  backups,
			Restores: restores,
			Eraser:   storedDigestEraser{url: st.url},
		}
	}
}

func TestStoredBlobBackup(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	storetest.RunStoredBlobBackup(t, newStoredBlobRig(base))
}

func TestStoredBlobBackupUnverified(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	storetest.RunStoredBlobBackupUnverified(t, newStoredBlobRig(base))
}

func TestStoredBlobBackupRegistryMissing(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	storetest.RunStoredBlobBackupRegistryMissing(t, newStoredBlobRig(base))
}

func TestStoredBlobBackupRegistryFiles(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	storetest.RunStoredBlobBackupRegistryFiles(t, newStoredBlobRig(base))
}
