package dustpan_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// sealContent holds a sensitive state value, and sealOutputs a sensitive
// output value: neither may reach the blob store, the outputs column or the
// backup target sealed.
const (
	sealContent = `{"version":4,"serial":1,"outputs":{"secret":{"sensitive":true,"value":"s3cr3t-state-value"}}}`
	sealOutputs = `{"secret":{"sensitive":true,"value":"s3cr3t-output-value","type":"string"}}`
)

// TestRunSealsUnsealedStateAtStartup proves the server seals rows written
// before the seal change when it starts: every row reads sealed, every old
// blob is gone, and the state download answers the same bytes with the same
// MD5. It then proves a backup after the seal holds no plain OpenTofu state.
func TestRunSealsUnsealedStateAtStartup(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	target := openBackupTarget(t)
	namespaces := sealNamespaces(t, st)

	sv := seedUnsealedState(t, st, blobs, namespaces)
	old := sv.Content

	srv, cancel, done := startSealingServer(t, st, blobs, authn, target)
	defer cancel()

	stored := waitForSealedRow(t, st, sv.ID)

	checkStartupSeal(t, srv, blobs, stored, old)
	checkSealedBackup(t, srv, target)

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run server: %v", err)
	}
}

// sealNamespaces returns a namespace service holding acme homed here.
func sealNamespaces(t *testing.T, st *sqlite.Store) *namespace.Service {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: region})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ns := namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	err = st.CreateNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return namespaces
}

// seedUnsealedState writes one finalized version as the code before the seal
// change did: a plain blob and plain outputs without the sealed flag.
func seedUnsealedState(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, namespaces *namespace.Service,
) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	// The plain service writes rows as the code before the seal change
	// did: plain blobs and outputs without the sealed flag.
	plain, err := workspace.NewService(workspace.Config{
		Workspaces:         st,
		States:             st,
		Blobs:              blobs,
		Namespaces:         namespaces,
		Sealer:             nil,
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("plain service: %v", err)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	ws, err := plain.Create(ctx, workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	_, err = plain.Lock(ctx, ws.ID, "alice", "apply")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	sum := md5.Sum([]byte(sealContent)) //nolint:gosec // The TFE protocol names state by MD5.

	pending, err := plain.BeginState(ctx, ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: []byte(sealOutputs),
		Serial:  1,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	sv, err := plain.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(sealContent))
	if err != nil {
		t.Fatalf("upload state: %v", err)
	}

	_, err = plain.Unlock(ctx, ws.ID, "alice")
	if err != nil {
		t.Fatalf("unlock: %v", err)
	}

	return sv
}

// startSealingServer starts a server with a key-encryption key in the
// background, returning it with its stop function and result channel. The
// server seals unsealed rows during start-up.
func startSealingServer(
	t *testing.T,
	st *sqlite.Store,
	blobs *fs.Store,
	authn *identity.StaticToken,
	target *fs.Store,
) (*dustpan.Server, context.CancelFunc, chan error) {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
		dustpan.WithListenAddr("127.0.0.1:0"),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	return srv, cancel, done
}

// waitForSealedRow waits until id reads sealed, failing when the deadline
// passes first: the server seals it during start-up.
func waitForSealedRow(
	t *testing.T, st *sqlite.Store, id workspace.StateVersionID,
) workspace.StateVersion {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		stored, err := st.GetStateVersion(t.Context(), id)
		if err != nil {
			t.Fatalf("get state: %v", err)
		}

		if stored.Sealed {
			return stored
		}

		if time.Now().After(deadline) {
			t.Fatalf("state %s still unsealed after start", id)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended before the server sealed the state")
		}
	}
}

// checkStartupSeal proves the sealed row names a new blob, its old blob is
// gone, its outputs column holds no plain value, and its download answers
// the same bytes with the same MD5.
func checkStartupSeal(
	t *testing.T, srv *dustpan.Server, blobs *fs.Store, stored workspace.StateVersion, old blob.Key,
) {
	t.Helper()

	if stored.Content == old {
		t.Fatal("state still names its old blob after start")
	}

	// The row reads sealed once the seal commits, just before the seal
	// deletes the old blob: wait for the delete instead of racing it.
	waitForBlobGone(t, blobs, old)

	if bytes.Contains(stored.Outputs, []byte("s3cr3t-output-value")) {
		t.Fatal("outputs column holds the plain output value after start")
	}

	download, err := srv.Workspaces().Open(t.Context(), stored)
	if err != nil {
		t.Fatalf("open sealed state: %v", err)
	}

	got, readErr := io.ReadAll(download)
	closeErr := download.Close()

	if readErr != nil {
		t.Fatalf("read sealed state: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close sealed state: %v", closeErr)
	}

	if string(got) != sealContent {
		t.Fatalf("download = %q, want %q", got, sealContent)
	}

	//nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	if md5.Sum(got) != stored.MD5 {
		t.Fatal("download MD5 differs from the stored MD5")
	}
}

// TestRestoreOfOldBackupSealsRows proves a restore of a backup that holds
// unsealed rows passes and seals them through the production after-apply
// step: the backup manifest names the state blob unsealed, and after the
// restore every row reads sealed, the old blob is gone, and the state
// download answers the same bytes with the same MD5.
func TestRestoreOfOldBackupSealsRows(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	target := openBackupTarget(t)
	namespaces := sealNamespaces(t, st)

	sv := seedUnsealedState(t, st, blobs, namespaces)
	old := sv.Content

	srv := newUnstartedSealingServer(t, st, blobs, authn, target)

	ctx := t.Context()

	backupID, err := backup.ParseID("bkp-seal00000000000002")
	if err != nil {
		t.Fatalf("parse backup ID: %v", err)
	}

	if _, err = srv.Backups().Run(ctx, backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	manifest, err := srv.Backups().ReadManifest(ctx, backupID)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	assertOldBackupUnsealed(t, manifest)

	restoreID := backup.RestoreID("rst-seal00000000000001")

	err = srv.Restores().Start(ctx, restoreID, backup.RestoreRequest{
		BackupID:    backupID,
		PreBackupID: "",
		CheckOnly:   false,
		Force:       false,
		RequestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("start restore %s: %v", restoreID, err)
	}

	waitForRestore(t, srv.Restores(), restoreID)

	stored, err := st.GetStateVersion(ctx, sv.ID)
	if err != nil {
		t.Fatalf("get restored state: %v", err)
	}

	checkRestoredSeal(t, &restoredSeal{
		srv:    srv,
		st:     st,
		blobs:  blobs,
		before: sv,
		stored: stored,
		old:    old,
	})
}

// newUnstartedSealingServer returns a server with a key-encryption key and a
// backup target without running it: the caller drives backup and restore
// itself, so the start-up seal never runs first.
func newUnstartedSealingServer(
	t *testing.T,
	st *sqlite.Store,
	blobs *fs.Store,
	authn *identity.StaticToken,
	target *fs.Store,
) *dustpan.Server {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if srv.Restores() == nil {
		t.Fatal("server with a backup target builds no restore service")
	}

	return srv
}

// assertOldBackupUnsealed proves the backup holds the state blob unsealed,
// as a backup from before the seal change does.
func assertOldBackupUnsealed(t *testing.T, manifest backup.Manifest) {
	t.Helper()

	sealedBlobs := 0

	for _, ref := range manifest.Blobs {
		if !strings.HasPrefix(ref.Key, "tofu/") {
			continue
		}

		sealedBlobs++

		if ref.Sealed {
			t.Fatalf("backup blob %q reads sealed, want unsealed", ref.Key)
		}
	}

	if sealedBlobs != 1 {
		t.Fatalf("backup holds %d state blobs, want 1", sealedBlobs)
	}
}

// waitForRestore polls the restore service until id leaves checking and
// applying, failing unless it succeeded.
func waitForRestore(t *testing.T, service *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	deadline := time.Now().Add(60 * time.Second)

	for {
		row, err := service.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			if row.State != backup.RestoreSucceeded {
				t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
			}

			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("restore %s never finished", id)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended before the restore finished")
		}
	}
}

// waitForBlobGone waits until key stops opening, failing when the deadline
// passes first: the seal deletes the old blob just after its row commits.
func waitForBlobGone(t *testing.T, blobs *fs.Store, key blob.Key) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		r, err := blobs.Get(t.Context(), key)
		if errors.Is(err, blob.ErrNotFound) {
			return
		}

		if r != nil {
			_ = r.Close() //nolint:errcheck // The test fails on the get result below.
		}

		if time.Now().After(deadline) {
			t.Fatalf("old blob still opens: err = %v, want ErrNotFound", err)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test ended before the old blob went away")
		}
	}
}

// checkRestoredSeal proves the restored row reads sealed under a new blob
// key with the same MD5 and size, its old blob is gone with no other orphan
// under its workspace, its outputs column holds no plain value, and its
// download answers the same bytes.
func checkRestoredSeal(t *testing.T, rs *restoredSeal) {
	t.Helper()

	checkRestoredRow(t, rs.before, rs.stored, rs.old)
	checkRestoredBlobs(t, rs.st, rs.blobs, rs.stored, rs.old)
	checkRestoredDownload(t, rs.srv, rs.stored)
}

// restoredSeal carries one restored row with the stores that prove its seal.
type restoredSeal struct {
	srv    *dustpan.Server
	st     *sqlite.Store
	blobs  *fs.Store
	before workspace.StateVersion
	stored workspace.StateVersion
	old    blob.Key
}

// checkRestoredRow proves the restored row reads sealed under a new blob key
// with the same MD5 and size as before the restore.
func checkRestoredRow(t *testing.T, before, stored workspace.StateVersion, old blob.Key) {
	t.Helper()

	if !stored.Sealed {
		t.Fatal("restored row still reads unsealed after the restore")
	}

	if stored.Content == old {
		t.Fatal("restored row still names its old blob after the restore")
	}

	if stored.MD5 != before.MD5 {
		t.Fatal("restored MD5 differs from the pre-restore MD5")
	}

	if stored.Size != before.Size {
		t.Fatal("restored size differs from the pre-restore size")
	}
}

// checkRestoredBlobs proves the old blob is gone with no other orphan under
// the restored workspace, and no unsealed version is left.
func checkRestoredBlobs(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, stored workspace.StateVersion, old blob.Key,
) {
	t.Helper()

	// The restore seals synchronously before it reports success, so the old
	// blob is already gone: no wait.
	r, err := blobs.Get(t.Context(), old)
	if !errors.Is(err, blob.ErrNotFound) {
		if r != nil {
			_ = r.Close() //nolint:errcheck // The test fails on the get result below.
		}

		t.Fatalf("old blob still opens: err = %v, want ErrNotFound", err)
	}

	listed, err := blobs.List(t.Context(), "tofu/"+stored.Workspace.String()+"/")
	if err != nil {
		t.Fatalf("list state content: %v", err)
	}

	if len(listed) != 1 || listed[0].Key != stored.Content {
		t.Fatalf("state content holds %d blobs, want only %q", len(listed), stored.Content)
	}

	unsealed, err := st.ListUnsealedStateVersions(t.Context())
	if err != nil {
		t.Fatalf("list unsealed state: %v", err)
	}

	if len(unsealed) != 0 {
		t.Fatalf("unsealed versions = %d, want none", len(unsealed))
	}
}

// checkRestoredDownload proves the restored outputs column holds no plain
// value and the state download answers the same bytes with the same MD5.
func checkRestoredDownload(t *testing.T, srv *dustpan.Server, stored workspace.StateVersion) {
	t.Helper()

	if bytes.Contains(stored.Outputs, []byte("s3cr3t-output-value")) {
		t.Fatal("outputs column holds the plain output value after the restore")
	}

	download, err := srv.Workspaces().Open(t.Context(), stored)
	if err != nil {
		t.Fatalf("open restored state: %v", err)
	}

	got, readErr := io.ReadAll(download)
	closeErr := download.Close()

	if readErr != nil {
		t.Fatalf("read restored state: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close restored state: %v", closeErr)
	}

	if string(got) != sealContent {
		t.Fatalf("download = %q, want %q", got, sealContent)
	}

	//nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	if md5.Sum(got) != stored.MD5 {
		t.Fatal("download MD5 differs from the stored MD5")
	}
}

func checkSealedBackup(t *testing.T, srv *dustpan.Server, target *fs.Store) {
	t.Helper()

	ctx := t.Context()

	id, err := backup.ParseID("bkp-seal00000000000001")
	if err != nil {
		t.Fatalf("parse backup ID: %v", err)
	}

	_, err = srv.Backups().Run(ctx, id)
	if err != nil {
		t.Fatalf("run backup: %v", err)
	}

	manifest, err := srv.Backups().ReadManifest(ctx, id)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	sealedBlobs := 0

	for _, ref := range manifest.Blobs {
		if !strings.HasPrefix(ref.Key, "tofu/") {
			continue
		}

		sealedBlobs++

		if !ref.Sealed {
			t.Fatalf("backup blob %q reads unsealed, want sealed", ref.Key)
		}

		assertTargetBlobSealed(t, target, ref.Key)
	}

	if sealedBlobs != 1 {
		t.Fatalf("backup holds %d state blobs, want 1", sealedBlobs)
	}
}

// assertTargetBlobSealed proves the target object of key holds no plain
// OpenTofu state.
func assertTargetBlobSealed(t *testing.T, target *fs.Store, key string) {
	t.Helper()

	targetKey, err := blob.ParseKey("blobs/" + key)
	if err != nil {
		t.Fatalf("parse target key: %v", err)
	}

	raw, err := target.Get(t.Context(), targetKey)
	if err != nil {
		t.Fatalf("get target blob %q: %v", key, err)
	}

	data, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read target blob %q: %v", key, readErr)
	}

	if closeErr != nil {
		t.Fatalf("close target blob %q: %v", key, closeErr)
	}

	if bytes.Contains(data, []byte("s3cr3t-state-value")) ||
		bytes.Contains(data, []byte("s3cr3t-output-value")) {
		t.Fatalf("backup blob %q holds plain OpenTofu state", key)
	}
}
