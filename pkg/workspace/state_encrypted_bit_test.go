package workspace_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errBlobReadRefused is the error of a blob read that a test refuses, as a
// missing or unreadable object gives.
var errBlobReadRefused = errors.New("the test refused the blob read")

// countingBlobs wraps a blob store and counts every read (Get), so a test
// proves that a read of the latest state answers from the row. refuseReads
// makes every later read fail, so a test proves that a read needs no blob.
type countingBlobs struct {
	blob.Store

	mu      sync.Mutex
	gets    int
	failGet bool
}

// wantBit is the client-encryption bit that a check expects of the latest
// state.
type wantBit uint8

// The two bits a check expects. A row that no server wrote holds neither.
const (
	wantPlaintext wantBit = iota
	wantEncrypted
)

// encrypted reports whether the wanted bit is the encrypted one.
func (w wantBit) encrypted() bool {
	return w == wantEncrypted
}

// Get counts the read and then reads the object, or fails once the test
// refused the reads.
func (c *countingBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	c.mu.Lock()
	c.gets++
	refuse := c.failGet
	c.mu.Unlock()

	if refuse {
		return nil, errBlobReadRefused
	}

	return c.Store.Get(ctx, key)
}

// reads returns the number of reads so far.
func (c *countingBlobs) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.gets
}

// refuseReads makes every later read fail.
func (c *countingBlobs) refuseReads() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failGet = true
}

// bitCheck checks the latest state of one workspace through the outputs ports,
// with a blob store that counts the reads.
type bitCheck struct {
	t       *testing.T
	f       fixture
	states  *workspace.OutputStates
	u       identity.Object
	counted *countingBlobs
}

// latest checks that the latest state is id with the bit want, and that the
// read made reads blob reads.
func (c *bitCheck) latest(id workspace.StateVersionID, want wantBit, reads int) {
	c.t.Helper()

	before := c.counted.reads()

	state, err := c.states.Latest(c.t.Context(), c.u)
	if err != nil {
		c.t.Fatalf("latest of %s: %v", id, err)
	}

	if state.ID != id.String() || state.ClientEncrypted != want.encrypted() {
		c.t.Fatalf("latest = %+v, want %s with encrypted %t", state, id, want.encrypted())
	}

	c.t.Logf("latest of %s: %d blob reads", id, c.counted.reads()-before)

	if got := c.counted.reads() - before; got != reads {
		c.t.Fatalf("latest of %s read the blob %d times, want %d", id, got, reads)
	}
}

// outputsStreamTheBlob checks that the outputs read of the state id answers
// its outputs and streams the state blob once. The read decides the
// client-encryption bit from the blob, so it never trusts the stored row.
func (c *bitCheck) outputsStreamTheBlob(id workspace.StateVersionID) {
	c.t.Helper()

	before := c.counted.reads()

	outputs, err := c.states.Outputs(c.t.Context(), c.u, id.String())
	if err != nil {
		c.t.Fatalf("outputs of %s: %v", id, err)
	}

	if len(outputs) != 1 || outputs[0].Name != "greeting" {
		c.t.Fatalf("outputs = %+v, want the greeting output", outputs)
	}

	c.t.Logf("the outputs read of %s: %d blob reads", id, c.counted.reads()-before)

	if got := c.counted.reads() - before; got != 1 {
		c.t.Fatalf("the outputs read of %s read the blob %d times, want 1", id, got)
	}
}

// storedBitStaysNull checks that the stored row of id still holds the null bit
// after a fallback read.
func (c *bitCheck) storedBitStaysNull(id workspace.StateVersionID) {
	c.t.Helper()

	sv, err := c.f.store.GetStateVersion(c.t.Context(), id)
	if err != nil {
		c.t.Fatalf("get state version %s: %v", id, err)
	}

	if sv.ClientEncrypted != workspace.ClientEncryptedUnknown {
		c.t.Fatalf("the bit of %s after the fallback = %d, want the null bit", id, sv.ClientEncrypted)
	}
}

// TestLatestAnswersTheBitWithoutABlobRead proves an outputs read of the latest
// state version answers the stored client-encryption bit from the row: a
// plaintext and a client-encrypted state both answer with no blob read, and
// the read still answers when the blob is unreadable.
func TestLatestAnswersTheBitWithoutABlobRead(t *testing.T) {
	t.Parallel()

	counted := &countingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, gets: 0, failGet: false}
	f := newFixtureSized(t, counted, blobguardtest.None{}, 512)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	c := &bitCheck{t: t, f: f, states: workspace.NewOutputStates(f.svc), u: unitObjectOf(t, f), counted: counted}

	plainDoc := `{"serial":1,"lineage":"lineage-1","outputs":{"greeting":{"value":"hi"}}}`
	plain := pushAndCurrent(t, f, 1, plainDoc)

	c.latest(plain, wantPlaintext, 0)
	c.outputsStreamTheBlob(plain)

	encrypted := pushAndCurrent(t, f, 2, `{"serial":2,"lineage":"lineage-1","encrypted_data":"aa"}`)

	c.latest(encrypted, wantEncrypted, 0)

	// A present bit answers even when the blob is missing or unreadable.
	counted.refuseReads()

	c.latest(encrypted, wantEncrypted, 0)
}

// TestLatestStreamsTheBlobWhenTheBitIsNull proves a state version row from
// before the change, whose bit is null, has its bit determined by streaming
// the blob, that the read leaves the stored bit null, and that a legacy blob
// which holds both keys fails the read.
func TestLatestStreamsTheBlobWhenTheBitIsNull(t *testing.T) {
	t.Parallel()

	counted := &countingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, gets: 0, failGet: false}
	f := newFixtureSized(t, counted, blobguardtest.None{}, 512)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	c := &bitCheck{t: t, f: f, states: workspace.NewOutputStates(f.svc), u: unitObjectOf(t, f), counted: counted}

	// A legacy plaintext row: the fallback streams the blob once.
	plainDoc := `{"serial":1,"lineage":"lineage-1","outputs":{}}`
	plain := pushAndCurrent(t, f, 1, plainDoc)
	f.store.legacyRow(plain, plainDoc)

	c.latest(plain, wantPlaintext, 1)
	c.storedBitStaysNull(plain)

	// A legacy client-encrypted row: the fallback streams the blob once and
	// answers the encrypted bit.
	encDoc := `{"serial":2,"lineage":"lineage-1","encrypted_data":"aa"}`
	encrypted := pushAndCurrent(t, f, 2, encDoc)
	f.store.legacyRow(encrypted, encDoc)

	c.latest(encrypted, wantEncrypted, 1)
	c.storedBitStaysNull(encrypted)

	// A legacy blob that holds both keys fails the fallback.
	both := `{"version":4,"encrypted_data":"aa"}`
	f.store.legacyRow(encrypted, both)
	putBlob(t, f, encrypted, both)

	if _, err := c.states.Latest(t.Context(), c.u); !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("latest of a legacy blob with both keys: err = %v, want ErrInvalidState", err)
	}
}

// TestOutputsRefusesAClientEncryptedBlobBehindAPlaintextRow proves the outputs
// read decides the client-encryption bit from the state blob and never from the
// stored row: a row that says plaintext while its blob holds the
// client-encryption envelope must give ErrClientEncrypted, never an empty
// outputs list.
func TestOutputsRefusesAClientEncryptedBlobBehindAPlaintextRow(t *testing.T) {
	t.Parallel()

	counted := &countingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, gets: 0, failGet: false}
	f := newFixtureSized(t, counted, blobguardtest.None{}, 512)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	encDoc := `{"serial":1,"lineage":"lineage-1","encrypted_data":"aa"}`
	encrypted := pushAndCurrent(t, f, 1, encDoc)

	// The row is untrusted input: it claims the state is plaintext.
	f.store.plainRow(encrypted, encDoc)

	states := workspace.NewOutputStates(f.svc)

	_, err := states.Outputs(t.Context(), unitObjectOf(t, f), encrypted.String())
	if !errors.Is(err, outputshare.ErrClientEncrypted) {
		t.Fatalf("outputs of a client-encrypted blob behind a plaintext row: err = %v, want ErrClientEncrypted", err)
	}
}

// TestBothKeys proves an uploaded state document that holds both encrypted_data
// and version is refused as invalid state, and that the refusal stores no
// version.
func TestBothKeys(t *testing.T) {
	t.Parallel()

	f := newFixtureSized(t, openFixtureBlobs(t), blobguardtest.None{}, 512)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	before := currentOrEmpty(t, f)

	both := `{"version":4,"encrypted_data":"aa","serial":1,"lineage":"lineage-1"}`

	if err := f.push(t, newState("lineage-1", 1, both), both); !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("upload of a document with both keys: err = %v, want ErrInvalidState", err)
	}

	if after := currentOrEmpty(t, f); after != before {
		t.Fatalf("current version after a refused upload = %q, want %q", after, before)
	}

	// The refused document leaves no state content behind.
	if left := listStateBlobs(t, f.blobs, f.ws.ID); len(left) != 0 {
		t.Fatalf("state blobs after a refused upload = %d, want 0", len(left))
	}
}

// TestBothKeysFoldedVersion proves the both-keys refusal reads the state
// format version member case-insensitively, as OpenTofu does: a document whose
// encrypted_data member sits next to a folded spelling of version is refused
// as invalid state and stores nothing.
func TestBothKeysFoldedVersion(t *testing.T) {
	t.Parallel()

	f := newFixtureSized(t, openFixtureBlobs(t), blobguardtest.None{}, 512)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	both := `{"VERSION":4,"encrypted_data":"aa","serial":1,"lineage":"lineage-1"}`

	if err := f.push(t, newState("lineage-1", 1, both), both); !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("upload of a document with a folded version key: err = %v, want ErrInvalidState", err)
	}

	if cur := currentOrEmpty(t, f); cur != "" {
		t.Fatalf("current version after a refused upload = %q, want none", cur)
	}

	if left := listStateBlobs(t, f.blobs, f.ws.ID); len(left) != 0 {
		t.Fatalf("state blobs after a refused upload = %d, want 0", len(left))
	}
}

// TestStreamedWriteRefusesBothKeys proves the streamed write path refuses a
// document that holds both encrypted_data and version as invalid state, as the
// two-step upload path does: no version becomes current and no state content
// stays behind.
func TestStreamedWriteRefusesBothKeys(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	both := `{"version":4,"encrypted_data":"aa","serial":1,"lineage":"l"}`

	_, err := f.svc.WriteState(
		t.Context(),
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(both),
	)
	if !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("streamed write of a document with both keys: err = %v, want ErrInvalidState", err)
	}

	if cur := currentOrEmpty(t, f); cur != "" {
		t.Fatalf("current version after a refused streamed write = %q, want none", cur)
	}

	if left := listStateBlobs(t, f.blobs, f.ws.ID); len(left) != 0 {
		t.Fatalf("state blobs after a refused streamed write = %d, want 0", len(left))
	}
}

// pushAndCurrent uploads content as the state of serial and returns the version
// the write made current.
func pushAndCurrent(t *testing.T, f fixture, serial int64, content string) workspace.StateVersionID {
	t.Helper()

	if err := f.push(t, newState("lineage-1", serial, content), content); err != nil {
		t.Fatalf("push serial %d: %v", serial, err)
	}

	sv, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current after serial %d: %v", serial, err)
	}

	return sv.ID
}

// unitObjectOf returns the identity object of the workspace of f for OpenTofu.
func unitObjectOf(t *testing.T, f fixture) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("opentofu", f.ws.Ref.Namespace, f.ws.Ref.Name.String())
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj.WithUnitID(f.ws.ID.String())
}

// currentOrEmpty returns the ID of the current version of the workspace of f,
// or "" when it has none.
func currentOrEmpty(t *testing.T, f fixture) string {
	t.Helper()

	sv, err := f.svc.Current(t.Context(), f.ws.ID)
	if errors.Is(err, workspace.ErrNoState) {
		return ""
	}

	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	return sv.ID.String()
}

// putBlob writes content under the content key of the stored version id, as a
// blob that an older server stored.
func putBlob(t *testing.T, f fixture, id workspace.StateVersionID, content string) {
	t.Helper()

	sv, err := f.store.GetStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("get state version %s: %v", id, err)
	}

	// The blob store keeps the object it holds, so the test replaces it.
	if err := f.blobs.Delete(t.Context(), sv.Content); err != nil {
		t.Fatalf("delete blob %s: %v", sv.Content, err)
	}

	if err := f.blobs.Put(t.Context(), sv.Content, strings.NewReader(content)); err != nil {
		t.Fatalf("put blob %s: %v", sv.Content, err)
	}
}
