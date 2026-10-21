package storetest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// keepBackupInstallation is the installation that the backup assigns to
	// the namespace acme. seedBackupGitHost writes it.
	keepBackupInstallation = "4242"
	// keepLiveInstallation is the installation that only the live store
	// assigns, after the backup.
	keepLiveInstallation = "7777"
)

// keepGitHostPorts holds the Git host store and the namespace that the
// assignments name, or a nil store when the store keeps no Git host data.
type keepGitHostPorts struct {
	store forge.AssignmentStore
	ns    namespace.Name
}

// keepGitHostStores returns the Git host ports of s. A store with no Git host
// port gives a value that every step skips: the fake of this package's own
// tests is one.
func keepGitHostStores(s RestoreStore, ns namespace.Name) keepGitHostPorts {
	keeper, ok := s.(interface{ GitHost() forge.Store })
	if !ok {
		return keepGitHostPorts{store: nil, ns: ns}
	}

	return keepGitHostPorts{store: keeper.GitHost(), ns: ns}
}

func keepGitHostEntry(action string) audit.Entry {
	return gateEntry(audit.ServerScope, action)
}

// mutateKeepGitHost changes the installation-to-namespace map after the backup:
// it removes the assignment of the backup and adds one that the backup does
// not hold. The keep restore must keep both changes, and the replacing
// restore must undo both.
func mutateKeepGitHost(t *testing.T, ports keepGitHostPorts) {
	t.Helper()

	if ports.store == nil {
		return
	}

	ctx := t.Context()

	unassign := keepGitHostEntry("git-host-installation:unassign")

	err := ports.store.DeleteAssignment(ctx, keepBackupInstallation, 1, unassign)
	if err != nil {
		t.Fatalf("delete the assignment of the backup: %v", err)
	}

	live := forge.Assignment{
		CreatedAt: now(), UpdatedAt: now(), InstallationID: keepLiveInstallation, Namespace: ports.ns, Version: 1,
	}

	if _, err := ports.store.PutAssignment(ctx, live, 0, keepGitHostEntry("git-host-installation:assign")); err != nil {
		t.Fatalf("assign the live installation: %v", err)
	}
}

// checkKeptGitHost proves that a restore without server:restore-access kept
// the live map: the removal and the new assignment both stay. Without
// git_host_installations in the access tables the backup row would be back.
func checkKeptGitHost(t *testing.T, ports keepGitHostPorts) {
	t.Helper()

	if ports.store == nil {
		return
	}

	ctx := t.Context()

	if _, err := ports.store.GetAssignment(ctx, keepBackupInstallation); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get the assignment of the backup after a keep restore = %v, want ErrAssignmentNotFound", err)
	}

	got, err := ports.store.GetAssignment(ctx, keepLiveInstallation)
	if err != nil {
		t.Fatalf("get the live assignment after a keep restore: %v", err)
	}

	if got.Namespace != ports.ns {
		t.Fatalf("live assignment namespace = %q, want %q", got.Namespace, ports.ns)
	}
}

// checkReplacedGitHost proves that a restore with server:restore-access
// brought the map of the backup back.
func checkReplacedGitHost(t *testing.T, ports keepGitHostPorts) {
	t.Helper()

	if ports.store == nil {
		return
	}

	ctx := t.Context()

	got, err := ports.store.GetAssignment(ctx, keepBackupInstallation)
	if err != nil {
		t.Fatalf("get the assignment of the backup after a replacing restore: %v", err)
	}

	if got.Namespace != ports.ns {
		t.Fatalf("restored assignment namespace = %q, want %q", got.Namespace, ports.ns)
	}

	if _, err := ports.store.GetAssignment(ctx, keepLiveInstallation); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get the live assignment after a replacing restore = %v, want ErrAssignmentNotFound", err)
	}
}
