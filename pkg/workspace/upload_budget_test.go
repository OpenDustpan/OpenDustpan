package workspace_test

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// stubBudget records the sum of Grow and can refuse after a total.
type stubBudget struct {
	mu       sync.Mutex
	sum      int64
	limit    int64
	capacity int64
}

func newAcceptBudget() *stubBudget {
	return &stubBudget{mu: sync.Mutex{}, sum: 0, limit: -1, capacity: math.MaxInt64}
}

func newRefuseBudget(limit int64) *stubBudget {
	return &stubBudget{mu: sync.Mutex{}, sum: 0, limit: limit, capacity: math.MaxInt64}
}

func newTooSmallBudget(limit, capacity int64) *stubBudget {
	return &stubBudget{mu: sync.Mutex{}, sum: 0, limit: limit, capacity: capacity}
}

func (b *stubBudget) Grow(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit >= 0 && b.sum+n > b.limit {
		return false
	}

	b.sum += n

	return true
}

// Held reports the recorded sum.
func (b *stubBudget) Held() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.sum
}

// Capacity reports the total budget the stub pretends to hold.
func (b *stubBudget) Capacity() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.capacity
}

func (b *stubBudget) total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.sum
}

// smallOutputsDoc returns a state with about 4 KiB of outputs.
func smallOutputsDoc() string {
	return `{"version":4,"serial":1,"lineage":"` + memLineage + `","outputs":{"o":{"value":"` +
		strings.Repeat("x", 4<<10) + `","type":"string"}},"resources":[]}`
}

// blobCount returns how many blobs the workspace holds.
func blobCount(t *testing.T, base memBase, ws workspace.Workspace) int {
	t.Helper()

	listed, listErr := base.blobs.List(t.Context(), "tofu/"+ws.ID.String()+"/")
	if listErr != nil {
		t.Fatalf("list blobs: %v", listErr)
	}

	return len(listed)
}

// lockOutputsWorkspace locks the workspace for an outputs upload.
func lockOutputsWorkspace(t *testing.T, svc *workspace.Service, ws workspace.Workspace) {
	t.Helper()

	if _, lockErr := svc.Lock(t.Context(), ws.ID, memHolder, memLockReason); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}
}

// declareOutputsVersion declares one outputs version for hexSum.
func declareOutputsVersion(
	t *testing.T,
	svc *workspace.Service,
	ws workspace.Workspace,
	serial int64,
	hexSum string,
) workspace.PendingState {
	t.Helper()

	pending, beginErr := svc.BeginState(t.Context(), ws.ID, memHolder, workspace.NewState{
		Lineage: memLineage,
		MD5:     hexSum,
		Outputs: nil,
		Serial:  serial,
		Force:   false,
	}, authority(noOverrides(), nil))
	if beginErr != nil {
		t.Fatalf("begin state: %v", beginErr)
	}

	return pending
}

// checkBudgetBounds proves the recorded sum sits near the outputs size.
func checkBudgetBounds(t *testing.T, budget *stubBudget, size int64) {
	t.Helper()

	got := budget.total()
	if got < size {
		t.Fatalf("budget sum = %d, want at least outputs size %d", got, size)
	}

	if got > 6*size {
		t.Fatalf("budget sum = %d, want at most 6 times outputs size %d", got, size)
	}
}

// testNoBudget proves an outputs upload works without a budget.
func testNoBudget(t *testing.T) {
	t.Helper()

	svc, ws := newMemPlainFixture(t)
	lockOutputsWorkspace(t, svc, ws)

	const serial = 2

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)
	pending := declareOutputsVersion(t, svc, ws, serial, hexSum)

	gen := newMemStateGen("outputs", serial)

	_, uploadErr := svc.Upload(t.Context(), pending.Version.ID, pending.Upload, gen)
	if uploadErr != nil {
		t.Fatalf("upload without budget: %v", uploadErr)
	}
}

// testOneRecordingBudget checks one sealed flag with an accepting budget.
func testOneRecordingBudget(t *testing.T, makeFixture func(testing.TB) (*workspace.Service, workspace.Workspace)) {
	t.Helper()

	svc, ws := makeFixture(t)
	lockOutputsWorkspace(t, svc, ws)

	const serial = 2

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)
	pending := declareOutputsVersion(t, svc, ws, serial, hexSum)

	budget := newAcceptBudget()
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, uploadErr := svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, gen)
	if uploadErr != nil {
		t.Fatalf("upload with budget: %v", uploadErr)
	}

	read, readErr := svc.ReadState(t.Context(), pending.Version.ID)
	if readErr != nil {
		t.Fatalf("read state: %v", readErr)
	}

	size := int64(len(read.Outputs))
	if size == 0 {
		t.Fatal("outputs are empty, want the outputs document")
	}

	checkBudgetBounds(t, budget, size)
}

// testRecordingBudget checks unsealed and sealed uploads with a budget.
func testRecordingBudget(t *testing.T) {
	t.Helper()

	testOneRecordingBudget(t, newMemPlainFixture)
	testOneRecordingBudget(t, newMemSealedFixture)
}

// testRefusedBusy proves a refused budget fails with ErrBusy.
func testRefusedBusy(t *testing.T) {
	t.Helper()

	svc, ws := newMemPlainFixture(t)
	lockOutputsWorkspace(t, svc, ws)

	const serial = 2

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)
	pending := declareOutputsVersion(t, svc, ws, serial, hexSum)

	budget := newRefuseBudget(1 << 20)
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, uploadErr := svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, gen)
	if !errors.Is(uploadErr, workspace.ErrBusy) {
		t.Fatalf("upload with refused budget: %v, want ErrBusy", uploadErr)
	}
}

// testRefusedBudgetLeavesNoBlob proves a refused upload fails with ErrBusy,
// leaves no blob, and the version is not readable as state.
func testRefusedBudgetLeavesNoBlob(t *testing.T) {
	t.Helper()

	base := newMemBase(t)
	svc, ws := memService(t, base, nil)

	lockOutputsWorkspace(t, svc, ws)

	const serial = 1

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)
	pending := declareOutputsVersion(t, svc, ws, serial, hexSum)

	budget := newRefuseBudget(1 << 20)
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, uploadErr := svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, gen)
	if !errors.Is(uploadErr, workspace.ErrBusy) {
		t.Fatalf("upload: %v, want ErrBusy", uploadErr)
	}

	if got := blobCount(t, base, ws); got != 0 {
		t.Fatalf("blobs = %d, want none after a refused upload", got)
	}

	_, readErr := svc.ReadState(t.Context(), pending.Version.ID)
	if !errors.Is(readErr, workspace.ErrStateVersionNotFound) {
		t.Fatalf("read refused version: %v, want ErrStateVersionNotFound", readErr)
	}
}

// checkTooSmall proves err is the permanent budget refusal: ErrBudgetTooSmall
// with the flag name and the byte counts.
func checkTooSmall(t *testing.T, err error, capacity int64) {
	t.Helper()

	if !errors.Is(err, workspace.ErrBudgetTooSmall) {
		t.Fatalf("upload with too-small budget: %v, want ErrBudgetTooSmall", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "--max-body-bytes-in-flight") {
		t.Fatalf("error %q lacks the flag name", msg)
	}

	if !strings.Contains(msg, strconv.FormatInt(capacity, 10)) {
		t.Fatalf("error %q lacks the capacity %d", msg, capacity)
	}

	if !strings.Contains(msg, "bytes") {
		t.Fatalf("error %q lacks the byte counts", msg)
	}
}

// testTooSmallUpload proves an upload that can never fit the budget fails
// with ErrBudgetTooSmall and leaves no blob.
func testTooSmallUpload(t *testing.T) {
	t.Helper()

	base := newMemBase(t)
	svc, ws := memService(t, base, nil)

	lockOutputsWorkspace(t, svc, ws)

	const serial = 1

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)
	pending := declareOutputsVersion(t, svc, ws, serial, hexSum)

	const capacity = 1 << 20

	budget := newTooSmallBudget(1<<20, capacity)
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, uploadErr := svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, gen)
	checkTooSmall(t, uploadErr, capacity)

	if got := blobCount(t, base, ws); got != 0 {
		t.Fatalf("blobs = %d, want none after a too-small upload", got)
	}

	_, readErr := svc.ReadState(t.Context(), pending.Version.ID)
	if !errors.Is(readErr, workspace.ErrStateVersionNotFound) {
		t.Fatalf("read refused version: %v, want ErrStateVersionNotFound", readErr)
	}
}

// testTooSmallWriteState proves a WriteState that can never fit the budget
// fails with ErrBudgetTooSmall and leaves no blob.
func testTooSmallWriteState(t *testing.T) {
	t.Helper()

	base := newMemBase(t)
	svc, ws := memService(t, base, nil)

	if _, lockErr := svc.LockWithID(t.Context(), ws.ID, memHolder, memLockReason, memLockID); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	const serial = 1

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)

	const capacity = 1 << 20

	budget := newTooSmallBudget(1<<20, capacity)
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, writeErr := svc.WriteState(budgetCtx, ws.ID,
		workspace.StreamInput{Holder: memHolder, LockID: memLockID, MD5: hexSum},
		streamAuthority(noOverrides(), nil),
		gen,
	)
	checkTooSmall(t, writeErr, capacity)

	if got := blobCount(t, base, ws); got != 0 {
		t.Fatalf("blobs = %d, want none after a too-small write", got)
	}
}

// testRefusedWriteState proves a refused WriteState fails busy with no blob.
func testRefusedWriteState(t *testing.T) {
	t.Helper()

	base := newMemBase(t)
	svc, ws := memService(t, base, nil)

	if _, lockErr := svc.LockWithID(t.Context(), ws.ID, memHolder, memLockReason, memLockID); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	const serial = 1

	hexSum, _, _ := memBodyMD5(t, "outputs", serial)

	budget := newRefuseBudget(1 << 20)
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	gen := newMemStateGen("outputs", serial)

	_, writeErr := svc.WriteState(budgetCtx, ws.ID,
		workspace.StreamInput{Holder: memHolder, LockID: memLockID, MD5: hexSum},
		streamAuthority(noOverrides(), nil),
		gen,
	)
	if !errors.Is(writeErr, workspace.ErrBusy) {
		t.Fatalf("write state: %v, want ErrBusy", writeErr)
	}

	if got := blobCount(t, base, ws); got != 0 {
		t.Fatalf("blobs = %d, want none after a refused write", got)
	}
}

// testSmallOutputs proves small outputs charge little budget.
func testSmallOutputs(t *testing.T) {
	t.Helper()

	svc, ws := newMemPlainFixture(t)

	doc := smallOutputsDoc()

	pending := beginSmallOutputs(t, svc, ws, doc)

	budget := newAcceptBudget()
	budgetCtx := workspace.WithBudget(t.Context(), budget)

	_, uploadErr := svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, strings.NewReader(doc))
	if uploadErr != nil {
		t.Fatalf("upload small outputs: %v", uploadErr)
	}

	if got := budget.total(); got >= 64<<10 {
		t.Fatalf("budget sum = %d, want under 64 KiB for small outputs", got)
	}
}

// beginSmallOutputs locks and declares one small outputs version.
func beginSmallOutputs(
	t *testing.T,
	svc *workspace.Service,
	ws workspace.Workspace,
	doc string,
) workspace.PendingState {
	t.Helper()

	lockOutputsWorkspace(t, svc, ws)

	pending, beginErr := svc.BeginState(t.Context(), ws.ID, memHolder, workspace.NewState{
		Lineage: memLineage,
		MD5:     memSumHex(doc),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, authority(noOverrides(), nil))
	if beginErr != nil {
		t.Fatalf("begin state: %v", beginErr)
	}

	return pending
}

func TestUploadOutputsBudget(t *testing.T) {
	t.Parallel()

	t.Run("no budget works as before", func(t *testing.T) {
		t.Parallel()
		testNoBudget(t)
	})

	t.Run("recording budget bounds the charge", func(t *testing.T) {
		t.Parallel()
		testRecordingBudget(t)
	})

	t.Run("refused budget fails busy", func(t *testing.T) {
		t.Parallel()
		testRefusedBusy(t)
	})

	t.Run("refused budget leaves no blob", func(t *testing.T) {
		t.Parallel()
		testRefusedBudgetLeavesNoBlob(t)
	})

	t.Run("too-small budget fails permanent", func(t *testing.T) {
		t.Parallel()
		testTooSmallUpload(t)
	})

	t.Run("refused WriteState has no blob", func(t *testing.T) {
		t.Parallel()
		testRefusedWriteState(t)
	})

	t.Run("too-small WriteState fails permanent", func(t *testing.T) {
		t.Parallel()
		testTooSmallWriteState(t)
	})

	t.Run("small outputs charge little", func(t *testing.T) {
		t.Parallel()
		testSmallOutputs(t)
	})
}
