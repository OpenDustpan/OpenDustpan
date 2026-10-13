package workspace_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errStopSeal stops the seal job after its blob write and before its SQL
// change, as a crash would.
var errStopSeal = errors.New("stop the seal before its SQL change")

// sealFixture holds one plain service and one sealing service over the same
// stores, so tests write rows as the code before the seal change did and
// seal them afterwards.
type sealFixture struct {
	store      *memStore
	namespaces *namespace.Service
	blobs      blob.Store
	plain      *workspace.Service
	sealed     *workspace.Service
	sealer     workspace.Sealer
	now        *time.Time
	ws         workspace.Workspace
}

func newSealFixture(t *testing.T) sealFixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		CreatedAt:       now,
		Name:            "acme",
		HomeRegion:      "local",
		FailoverVersion: 1,
		Overrides:       noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	blobs := openFixtureBlobs(t)

	plain, err := workspace.NewService(workspace.Config{
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		Sealer:             nil,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("plain service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: newSealedKeys(), Wrapper: newSealedWrapper(t), Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	sealed, err := workspace.NewService(workspace.Config{
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		Sealer:             secrets,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := plain.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return sealFixture{
		store:      store,
		namespaces: namespaces,
		blobs:      blobs,
		plain:      plain,
		sealed:     sealed,
		sealer:     secrets,
		now:        &now,
		ws:         ws,
	}
}

// pushPlain locks the fixture workspace and uploads content with outputs
// through the plain service, returning the finalized version.
func (f sealFixture) pushPlain(
	t *testing.T, content string, outputs []byte, serial int64,
) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	if _, err := f.plain.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.plain.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: outputs,
		Serial:  serial,
		Force:   false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := f.plain.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if sv.Sealed {
		t.Fatal("plain upload reads sealed, want unsealed")
	}

	if _, err := f.plain.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	return sv
}

// openDownload returns the state download of sv through svc.
func openDownload(t *testing.T, svc *workspace.Service, sv workspace.StateVersion) string {
	t.Helper()

	r, err := svc.Open(t.Context(), sv)
	if err != nil {
		t.Fatalf("open state %s: %v", sv.ID, err)
	}

	got, readErr := io.ReadAll(r)
	closeErr := r.Close()

	if readErr != nil {
		t.Fatalf("read state %s: %v", sv.ID, readErr)
	}

	if closeErr != nil {
		t.Fatalf("close state %s: %v", sv.ID, closeErr)
	}

	return string(got)
}

// listWorkspaceKeys returns every blob key under the workspace prefix.
func listWorkspaceKeys(t *testing.T, blobs blob.Store, id workspace.ID) []blob.Key {
	t.Helper()

	listed, err := blobs.List(t.Context(), "tofu/"+id.String()+"/")
	if err != nil {
		t.Fatalf("list blobs of %s: %v", id, err)
	}

	keys := make([]blob.Key, 0, len(listed))
	for i := range listed {
		keys = append(keys, listed[i].Key)
	}

	return keys
}

// sealedRow holds one sealed row with the content and output it must serve.
type sealedRow struct {
	id      workspace.StateVersionID
	content string
	output  string
	old     blob.Key
}

// checkSealedRow proves one row reads sealed with a new blob key, its old
// blob is gone, its outputs column holds no plain value, and its download
// answers the same bytes with the same MD5.
func checkSealedRow(t *testing.T, f sealFixture, want sealedRow) {
	t.Helper()

	ctx := t.Context()

	stored, err := f.store.GetStateVersion(ctx, want.id)
	if err != nil {
		t.Fatalf("get state %s: %v", want.id, err)
	}

	if !stored.Sealed {
		t.Fatalf("state %s reads unsealed, want sealed", want.id)
	}

	if stored.Content == want.old {
		t.Fatalf("state %s still names its old blob", want.id)
	}

	assertBlobGone(t, f.blobs, want.old, want.id)

	if bytes.Contains(stored.Outputs, []byte(want.output)) {
		t.Fatalf("outputs column of %s holds the plain output value", want.id)
	}

	got := openDownload(t, f.sealed, stored)
	if got != want.content {
		t.Fatalf("download of %s = %q, want %q", want.id, got, want.content)
	}

	sum := md5.Sum([]byte(got)) //nolint:gosec // The TFE protocol names state by MD5.
	if sum != stored.MD5 {
		t.Fatalf("download MD5 of %s differs from the stored MD5", want.id)
	}
}

// assertBlobGone proves key opens no blob.
func assertBlobGone(t *testing.T, blobs blob.Store, key blob.Key, id workspace.StateVersionID) {
	t.Helper()

	r, err := blobs.Get(t.Context(), key)
	if !errors.Is(err, blob.ErrNotFound) {
		if r != nil {
			_ = r.Close() //nolint:errcheck // The test fails on the get result below.
		}

		t.Fatalf("old blob of %s still opens: err = %v, want ErrNotFound", id, err)
	}
}

// TestSealUnsealedStatesSealsOldRows proves the job seals every unsealed
// row: each row reads sealed, each old blob is gone, and the download
// answers the same bytes with the same MD5.
func TestSealUnsealedStatesSealsOldRows(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)

	firstContent := `{"version":4,"serial":1,"outputs":{"secret":{"sensitive":true,"value":"s3cr3t-one"}}}`
	secondContent := `{"version":4,"serial":2,"outputs":{"secret":{"sensitive":true,"value":"s3cr3t-two"}}}`

	first := f.pushPlain(t, firstContent, sealedOutputsDoc("output-one"), 1)
	second := f.pushPlain(t, secondContent, sealedOutputsDoc("output-two"), 2)

	res, err := f.sealed.SealUnsealedStates(t.Context())
	if err != nil {
		t.Fatalf("seal unsealed state: %v", err)
	}

	if res.Sealed != 2 || res.Skipped != 0 {
		t.Fatalf("seal result = %+v, want 2 sealed and none skipped", res)
	}

	checkSealedRow(t, f, sealedRow{
		id:      first.ID,
		content: firstContent,
		output:  "output-one",
		old:     first.Content,
	})
	checkSealedRow(t, f, sealedRow{
		id:      second.ID,
		content: secondContent,
		output:  "output-two",
		old:     second.Content,
	})

	current, err := f.sealed.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if !bytes.Contains(current.Outputs, []byte("output-two")) {
		t.Fatalf("current outputs lack the output value: %q", current.Outputs)
	}

	again, err := f.sealed.SealUnsealedStates(t.Context())
	if err != nil {
		t.Fatalf("reseal: %v", err)
	}

	if again.Sealed != 0 || again.Skipped != 0 {
		t.Fatalf("reseal result = %+v, want nothing sealed and nothing skipped", again)
	}
}

// failFirstSealStore fails the first SealStateVersion call, so the test
// stops the job after its blob write and before its SQL change.
type failFirstSealStore struct {
	*memStore

	mu    sync.Mutex
	calls int
}

func (s *failFirstSealStore) SealStateVersion(
	ctx context.Context,
	id workspace.StateVersionID,
	key blob.Key,
	outputs []byte,
	fence namespace.Fence,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	if s.calls == 1 {
		return errStopSeal
	}

	return s.memStore.SealStateVersion(ctx, id, key, outputs, fence)
}

// assertOnlyReferencedBlobs proves every blob under the workspace prefix is
// named by a version, and counts them.
func assertOnlyReferencedBlobs(t *testing.T, f sealFixture, want int) {
	t.Helper()

	ctx := t.Context()

	refs, err := f.store.ReferencedContentKeys(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("referenced keys: %v", err)
	}

	named := make(map[blob.Key]struct{}, len(refs))
	for _, key := range refs {
		named[key] = struct{}{}
	}

	keys := listWorkspaceKeys(t, f.blobs, f.ws.ID)

	for _, key := range keys {
		if _, ok := named[key]; !ok {
			t.Fatalf("blob %q stays unreferenced after the reseal", key)
		}
	}

	if len(keys) != want {
		t.Fatalf("stored blobs = %d, want %d", len(keys), want)
	}
}

// TestSealUnsealedStatesResumesAfterCrash proves the job resumes after a
// crash between its blob write and its SQL change: the second run seals the
// row and no blob the job made stays unreferenced.
func TestSealUnsealedStatesResumesAfterCrash(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	content := `{"version":4,"serial":1}`

	sv := f.pushPlain(t, content, sealedOutputsDoc("output-one"), 1)
	old := sv.Content

	faulty := &failFirstSealStore{memStore: f.store, mu: sync.Mutex{}, calls: 0}

	sealed, err := workspace.NewService(workspace.Config{
		Workspaces:         faulty,
		States:             faulty,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		Sealer:             f.sealer,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	if _, err = sealed.SealUnsealedStates(ctx); !errors.Is(err, errStopSeal) {
		t.Fatalf("seal with a faulted SQL change: err = %v, want errStopSeal", err)
	}

	stored, err := f.store.GetStateVersion(ctx, sv.ID)
	if err != nil {
		t.Fatalf("get state %s: %v", sv.ID, err)
	}

	if stored.Sealed {
		t.Fatalf("state %s reads sealed after a faulted seal, want unsealed", sv.ID)
	}

	// The failed SQL change may hide a commit with an unknown outcome, so
	// the job keeps its new sealed blob next to the old one; the sweep of
	// the next run removes it once no row names it.
	if keys := listWorkspaceKeys(t, f.blobs, f.ws.ID); len(keys) != 2 {
		t.Fatalf("stored blobs after a failed seal = %q, want the old blob and the kept sealed blob", keys)
	}

	res, err := f.sealed.SealUnsealedStates(ctx)
	if err != nil {
		t.Fatalf("reseal: %v", err)
	}

	if res.Sealed != 1 || res.Skipped != 0 {
		t.Fatalf("reseal result = %+v, want 1 sealed and none skipped", res)
	}

	final, err := f.store.GetStateVersion(ctx, sv.ID)
	if err != nil {
		t.Fatalf("get sealed state %s: %v", sv.ID, err)
	}

	if !final.Sealed {
		t.Fatalf("state %s reads unsealed after the reseal, want sealed", sv.ID)
	}

	assertOnlyReferencedBlobs(t, f, 1)

	if got := openDownload(t, f.sealed, final); got != content {
		t.Fatalf("download after the reseal = %q, want %q", got, content)
	}

	assertBlobGone(t, f.blobs, old, sv.ID)
}

// seedRemoteVersion writes one finalized unsealed version with plain content
// into a namespace homed in another region, returning its version ID, old
// blob key and content.
func seedRemoteVersion(t *testing.T, f sealFixture) (workspace.StateVersionID, blob.Key, string) {
	t.Helper()

	ctx := t.Context()

	remote := namespace.Namespace{
		CreatedAt:       *f.now,
		Name:            "remote",
		HomeRegion:      "far-away",
		FailoverVersion: 1,
		Overrides:       noOverrides(),
	}

	if err := f.store.CreateNamespace(ctx, remote); err != nil {
		t.Fatalf("create remote namespace: %v", err)
	}

	name, err := workspace.ParseName("remote-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	id := workspace.ID("ws-remote000000000001")
	ref := workspace.Ref{Namespace: "remote", Name: name}

	unlocked := workspace.Workspace{
		CreatedAt: *f.now,
		UpdatedAt: *f.now,
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        id,
		Ref:       ref,
		Revision:  1,
		Overrides: noOverrides(),
	}

	if err = f.store.CreateWorkspace(ctx, unlocked, remote.Fence()); err != nil {
		t.Fatalf("create remote workspace: %v", err)
	}

	locked := unlocked
	locked.Lock = workspace.Lock{LockedAt: *f.now, ID: "lock-remote", Holder: "alice", Reason: "apply"}
	locked.UpdatedAt = *f.now
	locked.Revision++

	if err = f.store.SaveWorkspace(ctx, locked, remote.Fence()); err != nil {
		t.Fatalf("lock remote workspace: %v", err)
	}

	content := `{"version":4,"serial":1}`
	sum := md5.Sum([]byte(content)) //nolint:gosec // The TFE protocol names state by MD5.

	pending := workspace.StateVersion{
		CreatedAt:       *f.now,
		FinalizedAt:     time.Time{},
		UploadExpiresAt: f.now.Add(workspace.UploadWindow),
		Upload:          [32]byte{},
		ID:              "sv-remote000000000001",
		Workspace:       id,
		Holder:          "alice",
		LockID:          "lock-remote",
		Lineage:         "l1",
		Status:          workspace.StatusPending,
		Content:         "",
		Outputs:         nil,
		MD5:             sum,
		Serial:          1,
		Size:            0,
		Force:           false,
		Sealed:          false,
		RestoredFrom:    "",
		SensitiveMarks:  nil,
		FenceBy:         "",
		UnlockedWrite:   false,
	}

	if err = f.store.CreateStateVersion(ctx, pending, remote.Fence(), 10); err != nil {
		t.Fatalf("create remote state: %v", err)
	}

	old, err := blob.ParseKey("tofu/" + id.String() + "/" + pending.ID.String() + "/a1")
	if err != nil {
		t.Fatalf("parse old key: %v", err)
	}

	if _, err = f.store.RecordUploadAttempt(ctx, pending.ID, old, remote.Fence()); err != nil {
		t.Fatalf("record remote attempt: %v", err)
	}

	if err = f.blobs.Put(ctx, old, strings.NewReader(content)); err != nil {
		t.Fatalf("put remote blob: %v", err)
	}

	finalized := pending
	finalized.Status = workspace.StatusFinalized
	finalized.FinalizedAt = *f.now
	finalized.Content = old
	finalized.Size = int64(len(content))

	if err = f.store.FinalizeStateVersion(ctx, finalized, remote.Fence()); err != nil {
		t.Fatalf("finalize remote state: %v", err)
	}

	return pending.ID, old, content
}

// TestSealUnsealedStatesSkipsForeignRegions proves the job seals only rows
// of namespaces that are writable here: it skips the others with a count,
// and their rows and blobs stay as they are.
func TestSealUnsealedStatesSkipsForeignRegions(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	id, old, content := seedRemoteVersion(t, f)

	res, err := f.sealed.SealUnsealedStates(ctx)
	if err != nil {
		t.Fatalf("seal unsealed state: %v", err)
	}

	if res.Sealed != 0 || res.Skipped != 1 {
		t.Fatalf("seal result = %+v, want nothing sealed and 1 skipped", res)
	}

	stored, err := f.store.GetStateVersion(ctx, id)
	if err != nil {
		t.Fatalf("get remote state: %v", err)
	}

	if stored.Sealed {
		t.Fatal("remote state reads sealed, want unsealed")
	}

	raw, err := f.blobs.Get(ctx, old)
	if err != nil {
		t.Fatalf("get remote blob: %v", err)
	}

	got, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read remote blob: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close remote blob: %v", closeErr)
	}

	if string(got) != content {
		t.Fatalf("remote blob = %q, want %q", got, content)
	}
}

// raceBlobStore runs onList once before the next List answers, so the test
// records a live upload attempt with its blob while the sweep runs.
type raceBlobStore struct {
	blob.Store

	onList func()
}

func (s *raceBlobStore) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	if s.onList != nil {
		fn := s.onList
		s.onList = nil
		fn()
	}

	return s.Store.List(ctx, prefix)
}

// TestSealSweepKeepsConcurrentUpload proves the sweep lists the blobs before
// it reads the referenced keys: a blob put, with its row recorded, while the
// sweep runs survives it, because the later reference read names it.
func TestSealSweepKeepsConcurrentUpload(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	content := `{"version":4,"serial":1}`

	sv := f.pushPlain(t, content, sealedOutputsDoc("output-one"), 1)

	raceKey, err := blob.ParseKey("tofu/" + f.ws.ID.String() + "/sv-race000000000001/a1")
	if err != nil {
		t.Fatalf("parse race key: %v", err)
	}

	raced := &raceBlobStore{Store: f.blobs, onList: func() {
		if putErr := f.blobs.Put(ctx, raceKey, strings.NewReader("race")); putErr != nil {
			t.Errorf("put race blob: %v", putErr)

			return
		}

		race := sv
		race.ID = "sv-race000000000001"
		race.Status = workspace.StatusPending
		race.Content = raceKey
		race.Sealed = false

		f.store.mu.Lock()
		defer f.store.mu.Unlock()

		f.store.versions[race.ID] = race
	}}

	svc, err := workspace.NewService(workspace.Config{
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              raced,
		Namespaces:         f.namespaces,
		Sealer:             f.sealer,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	res, err := svc.SealUnsealedStates(ctx)
	if err != nil {
		t.Fatalf("seal unsealed state: %v", err)
	}

	if res.Sealed != 1 || res.Skipped != 0 {
		t.Fatalf("seal result = %+v, want 1 sealed and none skipped", res)
	}

	checkSealedRow(t, f, sealedRow{
		id:      sv.ID,
		content: content,
		output:  "output-one",
		old:     sv.Content,
	})

	raw, err := f.blobs.Get(ctx, raceKey)
	if err != nil {
		t.Fatalf("get race blob: %v, want the concurrent upload to survive", err)
	}

	got, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read race blob: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close race blob: %v", closeErr)
	}

	if string(got) != "race" {
		t.Fatalf("race blob = %q, want %q", got, "race")
	}
}

// deleteBeforeSealStore deletes the version before its seal change commits,
// as another instance pruning it between the listing and the seal would.
type deleteBeforeSealStore struct {
	*memStore
}

func (s *deleteBeforeSealStore) SealStateVersion(
	ctx context.Context,
	id workspace.StateVersionID,
	key blob.Key,
	outputs []byte,
	fence namespace.Fence,
) error {
	s.mu.Lock()
	delete(s.versions, id)
	s.mu.Unlock()

	return s.memStore.SealStateVersion(ctx, id, key, outputs, fence)
}

// TestSealSkipsVersionDeletedBeforeSeal proves a version deleted between the
// listing and its seal change counts as skipped, not as a start-up failure,
// and the new sealed blob goes with it.
func TestSealSkipsVersionDeletedBeforeSeal(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	sv := f.pushPlain(t, `{"version":4,"serial":1}`, sealedOutputsDoc("output-one"), 1)
	old := sv.Content

	faulty := &deleteBeforeSealStore{memStore: f.store}

	svc, err := workspace.NewService(workspace.Config{
		Workspaces:         faulty,
		States:             faulty,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		Sealer:             f.sealer,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	res, err := svc.SealUnsealedStates(ctx)
	if err != nil {
		t.Fatalf("seal unsealed state: %v", err)
	}

	if res.Sealed != 0 || res.Skipped != 1 {
		t.Fatalf("seal result = %+v, want nothing sealed and 1 skipped", res)
	}

	if _, err := f.store.GetStateVersion(ctx, sv.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("get deleted state: err = %v, want ErrStateVersionNotFound", err)
	}

	keys := listWorkspaceKeys(t, f.blobs, f.ws.ID)
	if len(keys) != 1 || keys[0] != old {
		t.Fatalf("stored blobs = %q, want only the old blob %q", keys, old)
	}
}

// pruneBeforeReadBlobs deletes the version with its blob on the next read of
// old, as another instance pruning it between the listing and the seal would.
type pruneBeforeReadBlobs struct {
	blob.Store

	store *memStore
	id    workspace.StateVersionID
	old   blob.Key
	fired bool
}

func (s *pruneBeforeReadBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if !s.fired && key == s.old {
		s.fired = true

		s.store.mu.Lock()
		delete(s.store.versions, s.id)
		s.store.mu.Unlock()

		// The Get below proves the blob is gone: it answers ErrNotFound,
		// so the test fails there when this best-effort delete misses.
		_ = s.Delete(ctx, s.old) //nolint:errcheck // The Get below proves the blob is gone.
	}

	return s.Store.Get(ctx, key)
}

// TestSealSkipsVersionAndBlobDeletedBeforeRead proves a version pruned with
// its blob between the listing and its blob read counts as skipped, not as a
// start-up failure.
func TestSealSkipsVersionAndBlobDeletedBeforeRead(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	sv := f.pushPlain(t, `{"version":4,"serial":1}`, sealedOutputsDoc("output-one"), 1)

	pruned := &pruneBeforeReadBlobs{Store: f.blobs, store: f.store, id: sv.ID, old: sv.Content, fired: false}

	svc, err := workspace.NewService(workspace.Config{
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              pruned,
		Namespaces:         f.namespaces,
		Sealer:             f.sealer,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
	})
	if err != nil {
		t.Fatalf("sealed service: %v", err)
	}

	res, err := svc.SealUnsealedStates(ctx)
	if err != nil {
		t.Fatalf("seal unsealed state: %v", err)
	}

	if res.Sealed != 0 || res.Skipped != 1 {
		t.Fatalf("seal result = %+v, want nothing sealed and 1 skipped", res)
	}

	if _, err := f.store.GetStateVersion(ctx, sv.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("get deleted state: err = %v, want ErrStateVersionNotFound", err)
	}

	if keys := listWorkspaceKeys(t, f.blobs, f.ws.ID); len(keys) != 0 {
		t.Fatalf("stored blobs = %q, want none", keys)
	}
}

// TestSealFailsOnMissingBlob proves a missing blob with the row still present
// stays an error: that is data loss and must stay loud.
func TestSealFailsOnMissingBlob(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	sv := f.pushPlain(t, `{"version":4,"serial":1}`, sealedOutputsDoc("output-one"), 1)

	if err := f.blobs.Delete(ctx, sv.Content); err != nil {
		t.Fatalf("delete old blob: %v", err)
	}

	_, err := f.sealed.SealUnsealedStates(ctx)
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("seal without its blob: err = %v, want ErrNotFound", err)
	}

	stored, err := f.store.GetStateVersion(ctx, sv.ID)
	if err != nil {
		t.Fatalf("get state %s: %v", sv.ID, err)
	}

	if stored.Sealed {
		t.Fatalf("state %s reads sealed after a failed seal, want unsealed", sv.ID)
	}
}
