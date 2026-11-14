package workspace_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"crypto/rand"
	"encoding/hex"
	"io"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

const (
	// memGCHeadroomDivisor sets the peak allowance for collector lag on a
	// grown reservation: one part in this many of the reservation.
	memGCHeadroomDivisor = 4

	memLineage          = "mem-lineage-01"
	memHolder           = "alice"
	memLockReason       = "memory test"
	memLockID           = "mem-lock-1"
	memTargetBytes      = 33554432
	memOneStringBytes   = 24 << 20
	memSmallValueBytes  = 1 << 10
	memOutputValueBytes = 4 << 10
	memNumOutputs       = 2000
	memNumResourcesA    = 3000
	memAttrsPerResource = 10
	memMaxStateBytes    = 64 << 20
)

// memStateGen streams one deterministic state document without holding it.
// Literal pieces are small; fill runs emit repeated bytes without storing
// them, so the test never holds the state.
type memStateGen struct {
	variant  string
	serial   int64
	lineage  string
	stage    int
	res      int
	attr     int
	out      int
	lit      []byte
	off      int
	fillByte byte
	fillLeft int
	finished bool
}

func newMemStateGen(variant string, serial int64) *memStateGen {
	return &memStateGen{
		variant:  variant,
		serial:   serial,
		lineage:  memLineage,
		stage:    0,
		res:      0,
		attr:     0,
		out:      0,
		lit:      nil,
		off:      0,
		fillByte: 0,
		fillLeft: 0,
		finished: false,
	}
}

// Read fills p from the next pieces of the document.
func (g *memStateGen) Read(p []byte) (int, error) {
	n := 0

	for n < len(p) {
		if c := g.drainPending(p[n:]); c > 0 {
			n += c

			continue
		}

		if g.finished {
			break
		}

		if !g.advance() {
			g.finished = true

			break
		}
	}

	if n == 0 {
		return 0, io.EOF
	}

	return n, nil
}

// drainPending copies the pending literal or fill bytes into p.
func (g *memStateGen) drainPending(p []byte) int {
	if g.off < len(g.lit) {
		copied := copy(p, g.lit[g.off:])
		g.off += copied

		return copied
	}

	if g.fillLeft > 0 {
		take := min(len(p), g.fillLeft)
		for i := range take {
			p[i] = g.fillByte
		}

		g.fillLeft -= take

		return take
	}

	return 0
}

func (g *memStateGen) advance() bool {
	switch g.variant {
	case "resources":
		return g.advanceResources()
	case "one-string":
		return g.advanceOneString()
	case "outputs":
		return g.advanceOutputs()
	default:
		return false
	}
}

func memHeader(serial int64, lineage, tail string) []byte {
	head := `{"version":4,"terraform_version":"1.9.0","serial":`
	head += strconv.FormatInt(serial, 10)
	head += `,"lineage":` + strconv.Quote(lineage)
	head += `,"outputs":` + tail

	return []byte(head)
}

func (g *memStateGen) advanceResources() bool {
	switch g.stage {
	case 0:
		g.lit = append(memHeader(g.serial, g.lineage, `{},`), []byte(`"resources":[`)...)
		g.off = 0
		g.stage = 1

		return true
	case 1:
		if g.res >= memNumResourcesA {
			g.lit = []byte(`],"check_results":null}`)
			g.off = 0
			g.stage = 99

			return true
		}

		name := "r" + strconv.Itoa(g.res)
		prefix := `{"mode":"managed","type":"null_resource","name":` + strconv.Quote(name)
		prefix += `,"provider":"provider[\"registry.terraform.io/hashicorp/null\"]",`
		prefix += `"instances":[{"schema_version":1,"attributes":{"id":` + strconv.Quote(name)

		if g.res > 0 {
			prefix = "," + prefix
		}

		g.lit = []byte(prefix)
		g.off = 0
		g.attr = 0
		g.stage = 2

		return true
	case 2:
		key := "k" + strconv.Itoa(g.attr)
		g.lit = []byte("," + strconv.Quote(key) + ":\"")
		g.off = 0
		g.stage = 3

		return true
	case 3:
		g.fillByte = 'v'
		g.fillLeft = memSmallValueBytes
		g.stage = 4

		return true
	case 4:
		g.lit = []byte(`"`)
		g.off = 0
		g.attr++

		if g.attr >= memAttrsPerResource {
			g.stage = 5
		} else {
			g.stage = 2
		}

		return true
	case 5:
		g.lit = []byte(`}}]}`)
		g.off = 0
		g.res++
		g.stage = 1

		return true
	default:
		return false
	}
}

func (g *memStateGen) advanceOneString() bool {
	switch g.stage {
	case 0:
		g.lit = append(memHeader(g.serial, g.lineage, `{},`), []byte(`"resources":[`)...)
		g.off = 0
		g.stage = 1

		return true
	case 1:
		g.lit = []byte(`{"mode":"managed","type":"null_resource","name":"only",` +
			`"provider":"provider[\"registry.terraform.io/hashicorp/null\"]",` +
			`"instances":[{"schema_version":1,"attributes":{"id":"only","big":"`)
		g.off = 0
		g.stage = 2

		return true
	case 2:
		g.fillByte = 'b'
		g.fillLeft = memOneStringBytes
		g.stage = 3

		return true
	case 3:
		g.lit = []byte(`","s1":"a","s2":"b"}}]}`)
		g.off = 0
		g.stage = 4

		return true
	case 4:
		g.lit = []byte(`],"check_results":null}`)
		g.off = 0
		g.stage = 99

		return true
	default:
		return false
	}
}

func (g *memStateGen) advanceOutputs() bool {
	switch g.stage {
	case 0:
		g.lit = memHeader(g.serial, g.lineage, `{`)
		g.off = 0
		g.stage = 1

		return true
	case 1:
		if g.out >= memNumOutputs {
			g.lit = []byte(`},"resources":[],"check_results":null}`)
			g.off = 0
			g.stage = 99

			return true
		}

		key := "o" + strconv.Itoa(g.out)
		doc := strconv.Quote(key) + `:{"value":"`

		if g.out > 0 {
			doc = "," + doc
		}

		g.lit = []byte(doc)
		g.off = 0
		g.stage = 2

		return true
	case 2:
		g.fillByte = 'o'
		g.fillLeft = memOutputValueBytes
		g.stage = 3

		return true
	case 3:
		g.lit = []byte(`","type":"string"}`)
		g.off = 0
		g.out++
		g.stage = 1

		return true
	default:
		return false
	}
}

// memBodyMD5 streams one generator pass into an MD5, returning its hex, its
// size and its raw sum. A second instance streams the same bytes to the
// upload, so the upload never holds the state.
func memBodyMD5(tb testing.TB, variant string, serial int64) (string, int64, [md5.Size]byte) {
	tb.Helper()

	var sum [md5.Size]byte

	hash := md5.New() //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.

	n, copyErr := io.Copy(hash, newMemStateGen(variant, serial))
	if copyErr != nil {
		tb.Fatalf("hash state: %v", copyErr)
	}

	copy(sum[:], hash.Sum(nil))

	return hex.EncodeToString(sum[:]), n, sum
}

func memSumHex(doc string) string {
	sum := md5.Sum([]byte(doc)) //nolint:gosec // The test names the protocol MD5 of a small document.

	return hex.EncodeToString(sum[:])
}

// memBase holds the stores behind one memory fixture.
type memBase struct {
	blobs      *fs.Store
	store      *sqlite.Store
	namespaces *namespace.Service
}

// newMemBase opens a filesystem blob store and a SQLite store with namespace
// acme.
func newMemBase(tb testing.TB) memBase {
	tb.Helper()

	blobs, blobErr := fs.Open(tb.TempDir())
	if blobErr != nil {
		tb.Fatalf("blob store: %v", blobErr)
	}

	tb.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			tb.Errorf("close blob store: %v", closeErr)
		}
	})

	store, storeErr := sqlite.Open(tb.Context(), filepath.Join(tb.TempDir(), "dustpan.db"))
	if storeErr != nil {
		tb.Fatalf("open store: %v", storeErr)
	}

	tb.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			tb.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, nsErr := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if nsErr != nil {
		tb.Fatalf("namespaces: %v", nsErr)
	}

	if _, createErr := namespaces.Create(tb.Context(), "acme"); createErr != nil {
		tb.Fatalf("create namespace: %v", createErr)
	}

	return memBase{blobs: blobs, store: store, namespaces: namespaces}
}

// newMemSealer returns a sealer with a fresh key-encryption key.
func newMemSealer(tb testing.TB, namespaces *namespace.Service) *secret.Service {
	tb.Helper()

	kek := make([]byte, 32)

	if _, randErr := rand.Read(kek); randErr != nil {
		tb.Fatalf("generate key-encryption key: %v", randErr)
	}

	block, blockErr := aes.NewCipher(kek)
	if blockErr != nil {
		tb.Fatalf("new cipher: %v", blockErr)
	}

	aead, aeadErr := cipher.NewGCM(block)
	if aeadErr != nil {
		tb.Fatalf("new GCM: %v", aeadErr)
	}

	secrets, secretsErr := secret.NewService(secret.Config{
		Keys:          newSealedKeys(),
		Wrapper:       &sealedWrapper{aead: aead, id: "kek-mem", kek: nil},
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if secretsErr != nil {
		tb.Fatalf("secrets: %v", secretsErr)
	}

	return secrets
}

// memService builds a workspace service over base with one workspace.
func memService(tb testing.TB, base memBase, sealer workspace.Sealer) (*workspace.Service, workspace.Workspace) {
	tb.Helper()

	svc, svcErr := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         base.store,
		States:             base.store,
		Blobs:              base.blobs,
		Namespaces:         base.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             sealer,
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      memMaxStateBytes,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if svcErr != nil {
		tb.Fatalf("service: %v", svcErr)
	}

	name, nameErr := workspace.ParseName("mem-workspace")
	if nameErr != nil {
		tb.Fatalf("name: %v", nameErr)
	}

	acme, getErr := base.namespaces.Get(tb.Context(), "acme")
	if getErr != nil {
		tb.Fatalf("namespace: %v", getErr)
	}

	ws, wsErr := svc.Create(
		tb.Context(),
		workspace.Ref{Namespace: "acme", Name: name},
		placementtest.Workspace(tb, base.store, acme.Fence()),
	)
	if wsErr != nil {
		tb.Fatalf("create: %v", wsErr)
	}

	return svc, ws
}

// newMemSealedFixture builds a memory fixture that seals stored blobs.
func newMemSealedFixture(tb testing.TB) (*workspace.Service, workspace.Workspace) {
	tb.Helper()

	base := newMemBase(tb)

	return memService(tb, base, newMemSealer(tb, base.namespaces))
}

// memUsage reports one measured upload: the version it stored with its
// TotalAlloc growth and its HeapInuse peak growth.
type memUsage struct {
	version workspace.StateVersion
	total   uint64
	peak    uint64
}

// memMeasure runs fn and reports its TotalAlloc growth and its HeapInuse peak
// growth. The sampler goroutine ends before the call returns.
func memMeasure(t *testing.T, fn func() (workspace.StateVersion, error)) memUsage {
	t.Helper()

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	baseInuse := before.HeapInuse
	baseAlloc := before.TotalAlloc

	var mu sync.Mutex

	peak := baseInuse
	done := make(chan struct{})

	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var m runtime.MemStats

				runtime.ReadMemStats(&m)

				mu.Lock()

				if m.HeapInuse > peak {
					peak = m.HeapInuse
				}

				mu.Unlock()
			}
		}
	})

	measured, measureErr := fn()

	close(done)
	wg.Wait()

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if measureErr != nil {
		t.Fatalf("upload: %v", measureErr)
	}

	mu.Lock()
	held := peak
	mu.Unlock()

	var growth uint64

	if held > baseInuse {
		growth = held - baseInuse
	}

	return memUsage{version: measured, total: after.TotalAlloc - baseAlloc, peak: growth}
}

// memVerify proves the stored version reads back the hashed bytes.
func memVerify(t *testing.T, svc *workspace.Service, sv workspace.StateVersion, want [md5.Size]byte) {
	t.Helper()

	r, openErr := svc.Open(t.Context(), sv)
	if openErr != nil {
		t.Fatalf("open state %s: %v", sv.ID, openErr)
	}

	hash := md5.New() //nolint:gosec // The test checks the MD5 the protocol names.

	_, copyErr := io.Copy(hash, r)
	closeErr := r.Close()

	if copyErr != nil {
		t.Fatalf("read state %s: %v", sv.ID, copyErr)
	}

	if closeErr != nil {
		t.Fatalf("close state %s: %v", sv.ID, closeErr)
	}

	var got [md5.Size]byte

	copy(got[:], hash.Sum(nil))

	if got != want {
		t.Fatalf("download MD5 of %s differs from the uploaded MD5", sv.ID)
	}
}

func memWarmDoc() string {
	return `{"version":4,"serial":1,"lineage":"` + memLineage + `","outputs":{},"resources":[]}`
}

// memCheckSize proves the body stays within the measured budget.
func memCheckSize(t *testing.T, variant string, size int64) {
	t.Helper()

	if size > memTargetBytes {
		t.Fatalf("variant %s size = %d, want at most %d", variant, size, memTargetBytes)
	}
}

// memWarmTFE locks the workspace and uploads one small state through the TFE
// flow, so the measured upload runs warm.
func memWarmTFE(t *testing.T, svc *workspace.Service, ws workspace.Workspace) {
	t.Helper()

	ctx := t.Context()

	if _, lockErr := svc.Lock(ctx, ws.ID, memHolder, memLockReason); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	warm := memWarmDoc()

	pending, beginErr := svc.BeginState(ctx, ws.ID, memHolder, workspace.NewState{
		Lineage: memLineage,
		MD5:     memSumHex(warm),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, authority(noOverrides(), nil))
	if beginErr != nil {
		t.Fatalf("begin warm state: %v", beginErr)
	}

	reader := strings.NewReader(warm)

	if _, uploadErr := svc.Upload(ctx, pending.Version.ID, pending.Upload, reader); uploadErr != nil {
		t.Fatalf("upload warm state: %v", uploadErr)
	}
}

// memWarmHTTP locks the workspace and writes one small state through the
// http backend flow, so the measured write runs warm.
func memWarmHTTP(t *testing.T, svc *workspace.Service, ws workspace.Workspace) {
	t.Helper()

	ctx := t.Context()

	if _, lockErr := svc.LockWithID(ctx, ws.ID, memHolder, memLockReason, memLockID); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	warm := memWarmDoc()

	reader := strings.NewReader(warm)

	_, writeErr := svc.WriteState(ctx, ws.ID,
		workspace.StreamInput{Holder: memHolder, LockID: memLockID, MD5: ""},
		streamAuthority(noOverrides(), nil),
		reader,
	)
	if writeErr != nil {
		t.Fatalf("write warm state: %v", writeErr)
	}
}

// memReport holds one measured case for its log line and its guard.
type memReport struct {
	variant string
	entry   string
	sealed  string
	size    int64
	total   uint64
	peak    uint64
	budget  int64
}

// memBudget records the bytes the upload asks to grow.
type memBudget struct {
	mu       sync.Mutex
	total    int64
	capacity int64
}

// Grow records n and always accepts.
func (b *memBudget) Grow(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.total += n

	return true
}

// Held reports the recorded sum.
func (b *memBudget) Held() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.total
}

// Capacity reports the total budget the stub pretends to hold.
func (b *memBudget) Capacity() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.capacity
}

// totalBytes returns the recorded sum.
func (b *memBudget) totalBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.total
}

// finishMemCase verifies the upload, prints its line and checks its guard.
func finishMemCase(
	t *testing.T,
	svc *workspace.Service,
	sv workspace.StateVersion,
	sum [md5.Size]byte,
	report memReport,
) {
	t.Helper()

	memVerify(t, svc, sv, sum)

	const mib = 1 << 20

	t.Logf(
		"memory variant=%s entry=%s sealed=%s size=%.1fMiB total=%.1fMiB peak=%.1fMiB budget=%.1fMiB",
		report.variant,
		report.entry,
		report.sealed,
		float64(report.size)/mib,
		float64(report.total)/mib,
		float64(report.peak)/mib,
		float64(report.budget)/mib,
	)

	// A state whose body streams peaks under the fixed cost that a streaming
	// upload reserves in the shared budget. An upload whose outputs document
	// is large asks the budget for more bytes as the document grows, so its
	// peak stays under the reservation plus the grown bytes. Under -race the
	// heap also holds detector tracking and delayed garbage, so allow extra.
	bound := uint64(bodygate.StreamCost) + uint64(max(report.budget, 0)) + uint64(raceHeapExtra)
	if report.budget > 0 {
		// The collector lets the heap grow past the live bytes before it
		// frees the garbage of the sealing: allow a quarter more.
		bound += bound / memGCHeadroomDivisor
	}

	if report.peak > bound {
		t.Errorf(
			"peak of variant %s entry %s sealed %s = %.1fMiB, want at most %.1fMiB",
			report.variant,
			report.entry,
			report.sealed,
			float64(report.peak)/mib,
			float64(bound)/mib,
		)
	}
}

// runMemTFECase measures one large upload through the TFE flow.
func runMemTFECase(t *testing.T, variant, sealedName string, svc *workspace.Service, ws workspace.Workspace) {
	t.Helper()

	ctx := t.Context()

	memWarmTFE(t, svc, ws)

	const measuredSerial = 2

	hexSum, size, sum := memBodyMD5(t, variant, measuredSerial)
	memCheckSize(t, variant, size)

	pending, beginErr := svc.BeginState(ctx, ws.ID, memHolder, workspace.NewState{
		Lineage: memLineage,
		MD5:     hexSum,
		Outputs: nil,
		Serial:  measuredSerial,
		Force:   false,
	}, authority(noOverrides(), nil))
	if beginErr != nil {
		t.Fatalf("begin state: %v", beginErr)
	}

	budget := &memBudget{mu: sync.Mutex{}, total: 0, capacity: math.MaxInt64}
	budgetCtx := workspace.WithBudget(ctx, budget)

	used := memMeasure(t, func() (workspace.StateVersion, error) {
		return svc.Upload(budgetCtx, pending.Version.ID, pending.Upload, newMemStateGen(variant, measuredSerial))
	})

	if _, unlockErr := svc.Unlock(ctx, ws.ID, memHolder); unlockErr != nil {
		t.Fatalf("unlock: %v", unlockErr)
	}

	finishMemCase(t, svc, used.version, sum, memReport{
		variant: variant,
		entry:   "tfe",
		sealed:  sealedName,
		size:    size,
		total:   used.total,
		peak:    used.peak,
		budget:  budget.totalBytes(),
	})
}

// runMemHTTPCase measures one large upload through the http backend flow.
func runMemHTTPCase(t *testing.T, variant, sealedName string, svc *workspace.Service, ws workspace.Workspace) {
	t.Helper()

	ctx := t.Context()

	memWarmHTTP(t, svc, ws)

	const measuredSerial = 2

	hexSum, size, sum := memBodyMD5(t, variant, measuredSerial)
	memCheckSize(t, variant, size)

	budget := &memBudget{mu: sync.Mutex{}, total: 0, capacity: math.MaxInt64}
	budgetCtx := workspace.WithBudget(ctx, budget)

	used := memMeasure(t, func() (workspace.StateVersion, error) {
		return svc.WriteState(budgetCtx, ws.ID,
			workspace.StreamInput{Holder: memHolder, LockID: memLockID, MD5: hexSum},
			streamAuthority(noOverrides(), nil),
			newMemStateGen(variant, measuredSerial),
		)
	})

	finishMemCase(t, svc, used.version, sum, memReport{
		variant: variant,
		entry:   "http",
		sealed:  sealedName,
		size:    size,
		total:   used.total,
		peak:    used.peak,
		budget:  budget.totalBytes(),
	})
}

// TestUploadMemory measures one 32 MiB state upload through both entry
// points. It prints one line per case with the total and peak MiB, and it
// fails when a peak runs over its generous guard.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestUploadMemory(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	variants := []string{"resources", "one-string", "outputs"}

	for _, variant := range variants {
		svc, ws := newMemSealedFixture(t)
		runMemTFECase(t, variant, "sealed", svc, ws)

		httpSvc, httpWs := newMemSealedFixture(t)
		runMemHTTPCase(t, variant, "sealed", httpSvc, httpWs)
	}
}

// BenchmarkUpload32MiB uploads the resources variant through the TFE flow on
// a sealed service, so the number can be re-measured with go test -bench.
func BenchmarkUpload32MiB(b *testing.B) {
	b.ReportAllocs()

	svc, ws := newMemSealedFixture(b)
	ctx := b.Context()

	if _, lockErr := svc.Lock(ctx, ws.ID, memHolder, memLockReason); lockErr != nil {
		b.Fatalf("lock: %v", lockErr)
	}

	b.ResetTimer()

	for i := range b.N {
		serial := int64(i + 1)
		hexSum, _, _ := memBodyMD5(b, "resources", serial)

		pending, beginErr := svc.BeginState(ctx, ws.ID, memHolder, workspace.NewState{
			Lineage: memLineage,
			MD5:     hexSum,
			Outputs: nil,
			Serial:  serial,
			Force:   false,
		}, authority(noOverrides(), nil))
		if beginErr != nil {
			b.Fatalf("begin state: %v", beginErr)
		}

		upload := newMemStateGen("resources", serial)

		if _, uploadErr := svc.Upload(ctx, pending.Version.ID, pending.Upload, upload); uploadErr != nil {
			b.Fatalf("upload: %v", uploadErr)
		}
	}
}
