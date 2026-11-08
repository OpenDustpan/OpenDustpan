package workspace_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The test checks the MD5 the service records, which the protocol names.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// streamDoc is a state document that declares its own serial and lineage. The
// shared fixture caps state at 64 bytes, so the document stays short.
func streamDoc(lineage string, serial int) string {
	return `{"serial":` + strconv.Itoa(serial) + `,"lineage":"` + lineage + `","outputs":{"n":{"value":"web"}}}`
}

// md5Hex returns the hex MD5 of doc, the value a client declares.
func md5Hex(doc string) string {
	sum := md5.Sum([]byte(doc)) //nolint:gosec // Test helper for the protocol's MD5.

	return hex.EncodeToString(sum[:])
}

// lockedFixture returns a fixture whose unit alice holds under lock ID lock-1.
func lockedFixture(t *testing.T) fixture {
	t.Helper()

	f := newFixture(t)

	if _, err := f.svc.LockWithID(t.Context(), f.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	return f
}

// TestWriteStateTakesSerialAndLineageFromTheBody proves a streamed write stores
// the serial, the lineage, the MD5 and the outputs the body declares, with no
// metadata declared before it streams (ADR 49, decisions 7 and 8).
func TestWriteStateTakesSerialAndLineageFromTheBody(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()
	doc := streamDoc("lin-w", 1)

	sv, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(doc),
	)
	if err != nil {
		t.Fatalf("write state: %v", err)
	}

	if sv.Serial != 1 || sv.Lineage != "lin-w" {
		t.Fatalf("serial %d lineage %q, want 1 and lin-w", sv.Serial, sv.Lineage)
	}

	if sv.MD5 != md5Sum(doc) {
		t.Fatalf("MD5 %x, want the MD5 of the bytes %x", sv.MD5, md5Sum(doc))
	}

	if !strings.Contains(string(sv.Outputs), `"web"`) {
		t.Fatalf("outputs %q do not carry the body's outputs", sv.Outputs)
	}

	current, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if current.ID != sv.ID {
		t.Fatalf("current = %s, want the streamed version %s", current.ID, sv.ID)
	}
}

// TestWriteStateRefusesLockIDsThatDoNotMatch proves a write names the current
// lock: another ID is ErrLockMismatch, and no ID on a locked unit is ErrLocked.
// A write under another holder is refused as locked by someone else.
func TestWriteStateRefusesLockIDsThatDoNotMatch(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()
	doc := streamDoc("lin-l", 1)

	cases := []struct {
		name   string
		holder string
		lockID string
		want   error
	}{
		{name: "other lock ID", holder: "alice", lockID: "lock-2", want: workspace.ErrLockMismatch},
		{name: "no lock ID on a locked unit", holder: "alice", lockID: "", want: workspace.ErrLocked},
		{name: "other holder", holder: "bob", lockID: "lock-1", want: workspace.ErrLockedByOther},
	}

	for _, tc := range cases {
		_, err := f.svc.WriteState(
			ctx,
			f.ws.ID,
			workspace.StreamInput{Holder: tc.holder, LockID: tc.lockID, MD5: ""},
			streamAuthority(noOverrides(), nil),
			strings.NewReader(doc),
		)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	if _, err := f.svc.Current(ctx, f.ws.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("refused writes left a current state: %v", err)
	}
}

// TestWriteStateRefusesAStaleSerialAndDeletesItsBlob proves the serial and
// lineage checks of ADR 25 run in the finalize transaction, and that the
// refused write deletes the blob it stored.
func TestWriteStateRefusesAStaleSerialAndDeletesItsBlob(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()

	first, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("lin-s", 5)),
	)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	_, err = f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("lin-s", 5)),
	)
	if !errors.Is(err, workspace.ErrStaleSerial) {
		t.Fatalf("equal serial: err = %v, want ErrStaleSerial", err)
	}

	_, err = f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("other", 6)),
	)
	if !errors.Is(err, workspace.ErrLineageMismatch) {
		t.Fatalf("other lineage: err = %v, want ErrLineageMismatch", err)
	}

	current, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if current.ID != first.ID {
		t.Fatalf("current = %s after refusals, want %s", current.ID, first.ID)
	}

	assertNoOrphanBlobs(t, f)
}

// TestWriteStateChecksTheDeclaredMD5 proves a body that does not match its
// Content-MD5 is refused, and its blob is deleted.
func TestWriteStateChecksTheDeclaredMD5(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()

	_, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: md5Hex("other")},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("lin-m", 1)),
	)
	if !errors.Is(err, workspace.ErrChecksumMismatch) {
		t.Fatalf("wrong MD5: err = %v, want ErrChecksumMismatch", err)
	}

	assertNoOrphanBlobs(t, f)
}

// TestWriteStateKeepsClientEncryptedStateWithoutOutputs proves a state the
// client encrypted stores no outputs, so the console shows none (ADR 49,
// decision 8).
func TestWriteStateKeepsClientEncryptedStateWithoutOutputs(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()
	doc := `{"serial":1,"lineage":"l","encrypted_data":"c"}`

	sv, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(doc),
	)
	if err != nil {
		t.Fatalf("write encrypted state: %v", err)
	}

	if len(sv.Outputs) != 0 {
		t.Fatalf("outputs of an encrypted state = %q, want none", sv.Outputs)
	}

	current, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if len(current.Outputs) != 0 {
		t.Fatalf("stored outputs of an encrypted state = %q, want none", current.Outputs)
	}
}

// TestWriteStateNeedsTheUnlockedOverride proves a write without the lock is
// refused until the switches and the authority allow it, and then it records
// the override it uses.
func TestWriteStateNeedsTheUnlockedOverride(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	doc := streamDoc("lin-u", 1)

	_, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(doc),
	)
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("unlocked write without the override: err = %v, want ErrNotLocked", err)
	}

	on := namespace.Overrides{Unlocked: true, Forced: false}

	if _, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, on); err != nil {
		t.Fatalf("namespace switch: %v", err)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, on); err != nil {
		t.Fatalf("unit switch: %v", err)
	}

	var recorded []workspace.Override

	sv, err := f.svc.WriteState(ctx, f.ws.ID, workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
		streamAuthority(on, func(o workspace.Override, _ int64) error {
			recorded = append(recorded, o)

			return nil
		}),
		strings.NewReader(doc))
	if err != nil {
		t.Fatalf("unlocked write with the override: %v", err)
	}

	if !sv.UnlockedWrite {
		t.Fatal("version written without the lock does not carry the unlocked-write flag")
	}

	if len(recorded) != 1 || recorded[0] != workspace.OverrideUnlocked {
		t.Fatalf("recorded overrides = %v, want one unlocked override", recorded)
	}
}

// TestWriteStateRecordsTheAttemptBeforeTheFirstByte proves the blob key is
// stored on the row before the body streams, so a write that stops midway
// still leaves a row that names its blob for the sweep.
func TestWriteStateRecordsTheAttemptBeforeTheFirstByte(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()
	doc := streamDoc("lin-r", 1)

	var named bool

	body := newFirstReadHook(strings.NewReader(doc), func() {
		f.store.mu.Lock()
		defer f.store.mu.Unlock()

		for id := range f.store.versions {
			row := f.store.versions[id]

			if row.Status == workspace.StatusPending && row.Content != "" {
				named = true
			}
		}
	})

	_, err := f.svc.WriteState(
		ctx,
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		body,
	)
	if err != nil {
		t.Fatalf("write state: %v", err)
	}

	if !named {
		t.Fatal("the attempt was not recorded before the first byte streamed")
	}
}

// firstReadHook runs onFirst once, before the first read of r.
type firstReadHook struct {
	r       io.Reader
	onFirst func()
	started bool
}

// newFirstReadHook returns a reader of r that runs onFirst before its first read.
func newFirstReadHook(r io.Reader, onFirst func()) *firstReadHook {
	return &firstReadHook{r: r, onFirst: onFirst, started: false}
}

// Read calls onFirst on the first call, then reads r.
func (h *firstReadHook) Read(p []byte) (int, error) {
	if !h.started {
		h.started = true
		h.onFirst()
	}

	return h.r.Read(p) //nolint:wrapcheck // Transparent reader: callers must see io.EOF unchanged.
}

// md5Sum returns the MD5 of doc as the service stores it.
func md5Sum(doc string) [md5.Size]byte {
	return md5.Sum([]byte(doc)) //nolint:gosec // Test helper for the protocol's MD5.
}

// assertNoOrphanBlobs proves that every pending row whose blob the store still
// names has no blob behind it: a refused write deleted its own blob.
func assertNoOrphanBlobs(t *testing.T, f fixture) {
	t.Helper()

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	for id := range f.store.versions {
		row := f.store.versions[id]

		if row.Status != workspace.StatusPending || row.Content == "" {
			continue
		}

		r, err := f.blobs.Get(t.Context(), row.Content)
		if err == nil {
			_ = r.Close() //nolint:errcheck // Test cleanup.

			t.Fatalf("refused write left blob %q behind", row.Content)
		}
	}
}

// streamAuthority returns the caller's side of a streamed write.
func streamAuthority(
	allow namespace.Overrides,
	record func(workspace.Override, int64) error,
) workspace.StreamAuthority {
	return workspace.StreamAuthority{Allow: allow, Record: record}
}

// TestWriteStateRecordsWithTheSerialAndKeepsNoVersionWhenRecordFails proves the
// audit entry of an override carries the serial of the body, and that a failed
// entry refuses the write: no version becomes current and the blob is deleted
// (ADR 41, decision 5).
func TestWriteStateRecordsWithTheSerialAndKeepsNoVersionWhenRecordFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	on := namespace.Overrides{Unlocked: true, Forced: false}

	if _, err := f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, on); err != nil {
		t.Fatalf("namespace switch: %v", err)
	}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, on); err != nil {
		t.Fatalf("unit switch: %v", err)
	}

	var serials []int64

	_, err := f.svc.WriteState(ctx, f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "", MD5: ""},
		streamAuthority(on, func(_ workspace.Override, serial int64) error {
			serials = append(serials, serial)

			return errRecordRefused
		}),
		strings.NewReader(streamDoc("lin-a", 4)))
	if !errors.Is(err, errRecordRefused) {
		t.Fatalf("write with a failed record: err = %v, want the record error", err)
	}

	if len(serials) != 1 || serials[0] != 4 {
		t.Fatalf("record serials = %v, want [4] from the body", serials)
	}

	if _, err := f.svc.Current(t.Context(), f.ws.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("current after a failed record: err = %v, want ErrNoState", err)
	}

	assertNoOrphanBlobs(t, f)
}

// errRecordRefused is the audit failure that a write must refuse on.
var errRecordRefused = errors.New("audit refused")

// errDroppedBody is the failure of a body whose connection drops mid-stream.
var errDroppedBody = errors.New("connection dropped")

// droppedBody reads nothing and fails, as a dropped connection does.
type droppedBody struct{}

// Read returns errDroppedBody.
func (droppedBody) Read([]byte) (int, error) {
	return 0, errDroppedBody
}

// TestRefusedWritesDoNotFillThePendingCap proves a refused streamed write leaves
// no placeholder pending. Eleven refusals, one of them a dropped body, leave the
// cap of ten free, so the valid write after them succeeds (ADR 49, decision 7).
func TestRefusedWritesDoNotFillThePendingCap(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctx := t.Context()
	in := workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""}

	for range 10 {
		_, err := f.svc.WriteState(ctx, f.ws.ID, in, streamAuthority(noOverrides(), nil), strings.NewReader("not json"))
		if err == nil {
			t.Fatal("a body that is not JSON was accepted")
		}
	}

	_, err := f.svc.WriteState(ctx, f.ws.ID, in, streamAuthority(noOverrides(), nil), droppedBody{})
	if !errors.Is(err, errDroppedBody) {
		t.Fatalf("dropped body: err = %v, want the body error", err)
	}

	_, err = f.svc.WriteState(ctx, f.ws.ID, in, streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("lin-p", 1)))
	if err != nil {
		t.Fatalf("valid write after eleven refusals: %v", err)
	}

	assertNoOrphanBlobs(t, f)
}

// ctxStates fails the writes of a state store whose context is done, as a SQL
// store does when the request that made the call has gone away.
type ctxStates struct {
	workspace.StateStore
}

// CreateStateVersion fails when ctx is done.
func (c ctxStates) CreateStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store write: %w", err)
	}

	return c.StateStore.CreateStateVersion(
		ctx,
		sv,
		fence,
		maxPending,
	)
}

// DiscardStateVersion fails when ctx is done.
func (c ctxStates) DiscardStateVersion(ctx context.Context, id workspace.StateVersionID, fence namespace.Fence) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store write: %w", err)
	}

	return c.StateStore.DiscardStateVersion(
		ctx,
		id,
		fence,
	)
}

// cancelingBody sends the start of a state, then cancels the request and fails,
// as a client that disconnects mid-body does.
type cancelingBody struct {
	cancel context.CancelFunc
	sent   bool
}

// Read sends the start of a state on the first call, and then the cancellation.
func (b *cancelingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, context.Canceled
	}

	b.sent = true
	b.cancel()

	return copy(p, `{"serial":1,"lineage":"l"`), nil
}

// TestCancelledBodyLeavesNoPendingRow proves a body cut off by the client
// removes its placeholder row and its blob. The cleanup outlives the request,
// so ten cut-off bodies do not fill the pending cap (ADR 49, decision 7).
func TestCancelledBodyLeavesNoPendingRow(t *testing.T) {
	t.Parallel()

	f := lockedFixture(t)
	ctxService := ctxAwareService(t, f)
	in := workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""}

	for range 10 {
		ctx, cancel := context.WithCancel(t.Context())

		_, err := ctxService.WriteState(ctx, f.ws.ID, in, streamAuthority(noOverrides(), nil),
			&cancelingBody{cancel: cancel, sent: false})
		cancel()

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cut-off body: err = %v, want context.Canceled", err)
		}
	}

	if pending := pendingRows(f); pending != 0 {
		t.Fatalf("%d pending rows after ten cut-off bodies, want none", pending)
	}

	assertNoOrphanBlobs(t, f)

	_, err := f.svc.WriteState(t.Context(), f.ws.ID, in, streamAuthority(noOverrides(), nil),
		strings.NewReader(streamDoc("lin-c", 1)))
	if err != nil {
		t.Fatalf("valid write after ten cut-off bodies: %v", err)
	}
}

// ctxAwareService returns a service over the fixture stores whose state writes
// fail once their context is done.
func ctxAwareService(t *testing.T, f fixture) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             ctxStates{StateStore: f.store},
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
		OnDeleteWait:       nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// pendingRows counts the pending state versions of the fixture store.
func pendingRows(f fixture) int {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	count := 0

	for id := range f.store.versions {
		if f.store.versions[id].Status == workspace.StatusPending {
			count++
		}
	}

	return count
}

// TestForceUnlockRefusesAnotherHolderOfTheSameLockID proves a force-unlock that
// names a lock by ID and holder releases nothing when the lock ID repeats under
// another holder. The release frees carol's lock only if the holder is ignored.
func TestForceUnlockRefusesAnotherHolderOfTheSameLockID(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	admin := namespace.Overrides{Forced: true, Unlocked: false}

	if _, err := f.svc.LockWithID(ctx, f.ws.ID, "bob", "apply", "lock-x"); err != nil {
		t.Fatalf("lock as bob: %v", err)
	}

	_, _, err := f.svc.ForceUnlockLock(
		ctx,
		f.ws.ID,
		workspace.Lock{LockedAt: time.Time{}, ID: "lock-x", Holder: "bob", Reason: ""},
		"admin",
		admin,
		nil,
		forceEntry(),
	)
	if err != nil {
		t.Fatalf("force-unlock of bob's lock as seen: %v", err)
	}

	if _, lockErr := f.svc.LockWithID(ctx, f.ws.ID, "carol", "apply", "lock-x"); lockErr != nil {
		t.Fatalf("lock as carol with the same ID: %v", lockErr)
	}

	_, _, err = f.svc.ForceUnlockLock(
		ctx,
		f.ws.ID,
		workspace.Lock{LockedAt: time.Time{}, ID: "lock-x", Holder: "bob", Reason: ""},
		"admin",
		admin,
		nil,
		forceEntry(),
	)
	if !errors.Is(err, workspace.ErrLockMismatch) {
		t.Fatalf("stale force-unlock of bob's lock: err = %v, want ErrLockMismatch", err)
	}

	stored, err := f.store.GetWorkspaceByID(ctx, f.ws.ID)
	if err != nil || stored.Lock.Holder != "carol" {
		t.Fatalf("after the stale force-unlock: holder %q, err %v; want carol still holding", stored.Lock.Holder, err)
	}
}

// TestUploadRefusesABodyThatIsNotAState proves a TFE upload whose body the reader
// refuses is refused and stores nothing: the state stays unreadable by no one,
// and no version becomes current (ADR 49, decision 8).
func TestUploadRefusesABodyThatIsNotAState(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	body := `{"serial":1,"lineage":"l"} junk`

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l",
		MD5:     md5Hex(body),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, authority(noOverrides(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, err = f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(body))
	if !errors.Is(err, workspace.ErrInvalidState) {
		t.Fatalf("upload with trailing data: err = %v, want ErrInvalidState", err)
	}

	if _, err := f.svc.Current(ctx, f.ws.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("refused upload made a version current: %v", err)
	}

	assertNoOrphanBlobs(t, f)
}
