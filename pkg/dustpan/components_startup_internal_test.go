package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/session"
)

// TestApiOnlyStartLeavesTheMigrationPending proves that the start of a
// process without the worker component claims no durable operation: a
// migration of the audit chain that an earlier instance left stays pending,
// and the start reads the stored record. The control case shows that the same
// start with the worker component runs it.
func TestApiOnlyStartLeavesTheMigrationPending(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		runs        bool
		wantPending bool
	}{
		{name: "api only", runs: false, wantPending: true},
		{name: "with the worker", runs: true, wantPending: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain, _, op := setupTakeoverChain(t)
			chain.runsMigrations = tc.runs

			if _, err := chain.startUpRecord(t.Context()); err != nil {
				t.Fatalf("start-up record: %v", err)
			}

			got, err := chain.durableOps.Get(t.Context(), op.ID)
			if err != nil {
				t.Fatalf("get operation: %v", err)
			}

			if pending := got.State == durable.StatePending; pending != tc.wantPending {
				t.Fatalf("operation state %q after the start, pending = %v, want %v",
					got.State, pending, tc.wantPending)
			}

			if !tc.runs && got.Attempt != 0 {
				t.Fatalf("operation attempt = %d after an api-only start, want 0: nothing claimed it", got.Attempt)
			}
		})
	}
}

// registrySpy records the calls that the start-up steps make.
type registrySpy struct {
	backup.Registry
	backup.RestoreRegistry

	failedBackups  int
	failedRestores int
	fenced         int
	fenceErr       error
}

func (r *registrySpy) FailRunningBackups(context.Context, time.Time, string) error {
	r.failedBackups++

	return nil
}

func (r *registrySpy) FailRunningRestores(context.Context, time.Time, string) error {
	r.failedRestores++

	return nil
}

func (r *registrySpy) ListRestores(context.Context) ([]backup.RestoreRecord, error) {
	return []backup.RestoreRecord{{
		StartedAt: time.Time{}, EndedAt: time.Time{}, ID: "", BackupID: "", PreBackupID: "", RequestedBy: "",
		Error: "", State: backup.RestoreApplying, DevReport: nil,
	}}, nil
}

func (r *registrySpy) BumpFailoverVersions(context.Context) (int64, error) {
	r.fenced++

	return 1, r.fenceErr
}

// TestStartFailsBackupsAndRestoresOnlyWithAPI proves that a process without
// the api component, which takes no backup and no restore, leaves the running
// backups and restores of the api processes alone at start, and fences no
// namespace.
func TestStartFailsBackupsAndRestoresOnlyWithAPI(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  Components
		want int
	}{
		{name: "worker only", set: workerOnly(), want: 0},
		{name: "api only", set: apiOnly(), want: 1},
		{name: "all", set: AllComponents(), want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spy := &registrySpy{
				Registry: nil, RestoreRegistry: nil, failedBackups: 0, failedRestores: 0, fenced: 0, fenceErr: nil,
			}
			srv := testServer(nil, "a")
			srv.components, srv.now, srv.backupRegistry, srv.restoreRegistry = tc.set, time.Now, spy, spy

			if err := srv.failStoppedBackups(t.Context()); err != nil {
				t.Fatalf("fail stopped backups: %v", err)
			}

			if err := srv.failStoppedRestores(t.Context()); err != nil {
				t.Fatalf("fail stopped restores: %v", err)
			}

			if spy.failedBackups != tc.want || spy.failedRestores != tc.want || spy.fenced != tc.want {
				t.Fatalf("failed backups %d, failed restores %d, fences %d; want %d of each",
					spy.failedBackups, spy.failedRestores, spy.fenced, tc.want)
			}
		})
	}
}

// failingBindings is a provider store that fails each bind, so that the
// result of the bind step shows whether it ran.
type failingBindings struct {
	session.ProviderStore
}

func (failingBindings) BindProvider(context.Context, string, string, time.Time) (string, error) {
	return "", errBindFailed
}

var errBindFailed = errors.New("bind failed")

// TestWorkerOnlyStartBindsNoSignInProviders proves that a process without
// the api component neither binds the sign-in providers nor fails at one.
func TestWorkerOnlyStartBindsNoSignInProviders(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		set  Components
		want bool
	}{
		{name: "worker only", set: workerOnly(), want: false},
		{name: "api only", set: apiOnly(), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := testServer(nil, "a")
			srv.components, srv.now = tc.set, time.Now
			srv.signInProviders = providerBinding{
				store:     failingBindings{ProviderStore: nil},
				providers: []session.Provider{{ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: ""}},
			}

			err := srv.bindSignInProviders(t.Context())
			if ran := err != nil; ran != tc.want {
				t.Fatalf("bind step error = %v, want an error = %v", err, tc.want)
			}
		})
	}
}
