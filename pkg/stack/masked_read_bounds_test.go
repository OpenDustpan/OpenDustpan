package stack_test

// Hostile tests for the bounds of the masked read: the nesting depth, the
// number of markers, the bytes of the marker paths, the deadline, the shape
// of the version, and the stack that a name maps to. Each guard has a test
// that fails when the guard is removed.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

// bigLimit is the checkpoint size limit of the fixtures that hold a version
// over the limit of the masked read.
const bigLimit = 16 << 20

// fiveMB is the size that the hostile documents of these tests stay under.
const fiveMB = 5 << 20

// plantDeployment replaces the content of the one checkpoint row of the
// fixture with raw, stored as an unsequenced save, so a document that the
// save refuses can reach the read. The version record states recorded bytes,
// or len(raw) when recorded is negative.
func plantDeployment(t *testing.T, f updatesFixture, raw []byte, recorded int) {
	t.Helper()

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	var (
		id stack.UpdateID
		cp stack.Checkpoint
	)

	for k := range f.store.checkpoints {
		id, cp = k, f.store.checkpoints[k]
	}

	text := cp.BlobKey.String()
	cut := strings.LastIndex(text, "/")

	if !strings.HasPrefix(text[cut+1:], "1-") {
		t.Fatalf("blob key %q: want the name of sequence 1", text)
	}

	key := blob.Key(text[:cut+1] + "0-" + text[cut+3:])

	var gz bytes.Buffer

	w := gzip.NewWriter(&gz)
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("gzip: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	if err := f.blobs.Put(t.Context(), key, bytes.NewReader(gz.Bytes())); err != nil {
		t.Fatalf("plant blob: %v", err)
	}

	cp.BlobKey, cp.Sequence, cp.Digest = key, 0, sha256.Sum256(raw)

	cp.Deployment = raw
	if recorded >= 0 {
		cp.Deployment = make([]byte, recorded)
	}

	f.store.checkpoints[id] = cp
}

// plantedReader returns the reader of version 1 of a fixture whose content
// is raw.
func plantedReader(t *testing.T, raw []byte, recorded int) (maskedReader, *countingBlob) {
	t.Helper()

	f, blobs := countFixture(t, bigLimit)
	saveVersion(t, f, `{"resources":[]}`)
	plantDeployment(t, f, raw, recorded)

	obj := unitObj(t)

	return maskedReader{
		f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
		p: person(), authz: readGrant(obj),
	}, blobs
}

// deepDoc returns a valid deployment with exactly depth levels of objects
// and arrays.
func deepDoc(depth int) []byte {
	return []byte(`{"resources":[],"d":` + strings.Repeat("[", depth-1) + strings.Repeat("]", depth-1) + `}`)
}

// manySecretsDoc returns a deployment with n secret values in one array.
func manySecretsDoc(n int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"resources":[],"s":[`)

	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}

		sb.WriteString(secretJSON("p"))
	}

	sb.WriteString(`]}`)

	return []byte(sb.String())
}

// longKeyDoc returns a deployment with m secret values below one key of keyLen
// bytes, so each marker path holds that key.
func longKeyDoc(keyLen, m int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"resources":[],"` + strings.Repeat("k", keyLen) + `":{`)

	for i := range m {
		if i > 0 {
			sb.WriteString(",")
		}

		fmt.Fprintf(&sb, `"s%d":%s`, i, secretJSON("p"))
	}

	sb.WriteString(`}}`)

	return []byte(sb.String())
}

// wantRefused fails the test unless err wraps want and the sink holds one
// refusal with reason. The answer must hold no marker.
func (c *countingSink) wantRefused(t *testing.T, page stack.MaskedPage, err, want error, reason string) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}

	if len(page.Markers) != 0 || len(page.ValuePaths) != 0 {
		t.Fatalf("page = %+v, want no marker and no path", page)
	}

	c.wantOne(t, reason)
}

// TestMaskedReadBoundedWork proves each bound of the work of one read: the
// refusal comes with ErrTooLarge and one refusal for the sink, and the
// document just inside the bound still answers.
func TestMaskedReadBoundedWork(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		allowed []byte
		refused []byte
	}{
		{"nesting depth above 100", deepDoc(100), deepDoc(101)},
		{"more than 10000 markers", manySecretsDoc(10000), manySecretsDoc(10001)},
		{"more than 8 MiB of marker path bytes", longKeyDoc(4_000_000, 2), longKeyDoc(4_000_000, 3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.refused) >= fiveMB {
				t.Fatalf("hostile document has %d bytes, want less than 5 MB", len(tc.refused))
			}

			ok, _ := plantedReader(t, tc.allowed, -1)

			if _, err := ok.read(t, nil, ""); err != nil {
				t.Fatalf("document inside the bound: err = %v, want nil", err)
			}

			r, _ := plantedReader(t, tc.refused, -1)
			sink := newSink()

			page, err := r.read(t, sink.refusal, "")
			sink.wantRefused(t, page, err, stack.ErrTooLarge, stack.RefusalTooLarge)
		})
	}
}

// TestMaskedReadNestingScanIgnoresStrings proves a bracket inside a string,
// also behind an escaped quote, does not count as nesting.
func TestMaskedReadNestingScanIgnoresStrings(t *testing.T) {
	t.Parallel()

	raw := `{"resources":[],"a":"` + strings.Repeat("[", 500) + `","b":"x\"` +
		strings.Repeat("[", 500) + `","c":"back\\"}`

	r, _ := plantedReader(t, []byte(raw), -1)

	if _, err := r.read(t, nil, ""); err != nil {
		t.Fatalf("brackets inside strings: err = %v, want nil", err)
	}
}

// allocDuring returns the bytes that fn allocates. The caller must not run in
// parallel, because the counter holds the allocations of the whole process.
func allocDuring(fn func()) uint64 {
	runtime.GC()

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)

	return after.TotalAlloc - before.TotalAlloc
}

// deepSecretsDoc returns a document with a chain of levels objects, where
// the first withSecret levels each hold a secret value.
func deepSecretsDoc(levels, withSecret int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"resources":[],"d":`)

	for i := range levels {
		if i < withSecret {
			sb.WriteString(`{"s":` + secretJSON("p") + `,"n":`)
		} else {
			sb.WriteString(`{"n":`)
		}
	}

	sb.WriteString(`1`)
	sb.WriteString(strings.Repeat(`}`, levels))
	sb.WriteString(`}`)

	return []byte(sb.String())
}

// chainSecretsDoc returns a document where n secret values sit in an array
// below depth objects with long keys: the path of each marker is long, and a
// build of the paths would allocate megabytes.
func chainSecretsDoc(depth, n int) []byte {
	var sb strings.Builder

	sb.WriteString(`{"resources":[],`)

	key := strings.Repeat("a", 40)
	for range depth - 2 {
		sb.WriteString(`"` + key + `":{`)
	}

	sb.WriteString(`"s":[`)

	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}

		sb.WriteString(secretJSON("p"))
	}

	sb.WriteString(`]`)
	sb.WriteString(strings.Repeat(`}`, depth-2))
	sb.WriteString(`}`)

	return []byte(sb.String())
}

// TestMaskedReadRefusedWorkAllocatesLittle proves a refused read builds no
// path: the deep document and the document with too many markers allocate
// less than the paths would take. It runs alone, because the counter of
// allocations holds the whole process.
func TestMaskedReadRefusedWorkAllocatesLittle(t *testing.T) {
	// The counter holds the allocations of the whole process, so no other test
	// may run at the same time. t.Setenv forbids t.Parallel, so the test runs alone.
	t.Setenv("DUSTPAN_ALLOC_TEST", "1")

	// The decode of the document alone allocates about 11 MB. A build of the
	// paths adds more than 30 MB, so the bound sits between the two.
	const bound = 16 << 20

	for _, tc := range []struct {
		name string
		doc  []byte
	}{
		{"depth 8000 with 5000 secret objects", deepSecretsDoc(8000, 5000)},
		{"10001 markers at depth 90", chainSecretsDoc(90, 10001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.doc) >= fiveMB {
				t.Fatalf("hostile document has %d bytes, want less than 5 MB", len(tc.doc))
			}

			r, _ := plantedReader(t, tc.doc, -1)

			var err error

			got := allocDuring(func() { _, err = r.read(t, nil, "") })

			if !errors.Is(err, stack.ErrTooLarge) {
				t.Fatalf("err = %v, want the size error of the read", err)
			}

			if got > bound {
				t.Fatalf("the refused read allocated %d bytes, want at most %d", got, bound)
			}
		})
	}
}

// shotContext reports a deadline on exactly the k-th call of Err, and no
// error on the other calls. It counts the calls.
type shotContext struct {
	calls *atomic.Int64
	k     int64
}

// Deadline implements context.Context: the context has no deadline of its own.
func (c shotContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done implements context.Context: the context is never canceled by itself.
func (c shotContext) Done() <-chan struct{} { return nil }

// Value implements context.Context: the context holds no value.
func (c shotContext) Value(any) any { return nil }

// Err implements context.Context.
func (c shotContext) Err() error {
	if c.calls.Add(1) == c.k {
		return context.DeadlineExceeded
	}

	return nil
}

// minErrCalls is the number of calls of Err that one uninterrupted read of
// the document of the sweep makes at least: the check at the start of the
// read, the checks of the stores that it calls, and the checks after the
// decode and after the walk. Without one of the last two the count is lower.
const minErrCalls = 4

// TestMaskedReadDeadlineSweep proves a deadline that ends the context at any
// one call of Err never lets the read answer a page. The last two calls are
// the checks after the decode and after the walk: they refuse with the
// deadline error and report it to the sink.
func TestMaskedReadDeadlineSweep(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, deploymentWith(3))

	count := &atomic.Int64{}

	counted := shotContext{calls: count, k: 0}

	args := stack.ReadMaskedArgs{
		Ref: r.f.ref, Object: r.obj, Selector: r.sel,
		Principal: r.p, Authorizer: r.authz, Key: testCursorKey(), Sink: nil, Cursor: "",
	}

	if _, err := r.f.svc.ReadMasked(counted, args); err != nil {
		t.Fatalf("uninterrupted read: %v", err)
	}

	total := count.Load()
	if total < minErrCalls {
		t.Fatalf("a read makes %d calls of Err, want at least %d: a deadline check is missing", total, minErrCalls)
	}

	for k := int64(1); k <= total; k++ {
		sink := newSink()
		args.Sink = sink.refusal

		ctx := shotContext{calls: &atomic.Int64{}, k: k}

		page, err := r.f.svc.ReadMasked(ctx, args)
		if err == nil || len(page.Markers) != 0 {
			t.Fatalf("deadline at call %d of %d: page %+v, err %v, want a refusal", k, total, page, err)
		}

		if k >= total-1 {
			sink.wantRefused(t, page, err, stack.ErrReadDeadline, stack.RefusalDeadline)
		}
	}
}

// TestMaskedReadCanceledAtStart proves a context that is done at the start
// refuses before the read does any work: no authorization and no content.
func TestMaskedReadCanceledAtStart(t *testing.T) {
	t.Parallel()

	f, blobs := countFixture(t, maskedCheckpointLimit)
	saveVersion(t, f, deploymentWith(2))

	obj := unitObj(t)

	asked := 0
	r := maskedReader{
		f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
		p: person(), authz: hookAuthorizer{inner: readGrant(obj), hook: func() { asked++ }},
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	before := blobs.calls()
	sink := newSink()

	page, err := f.svc.ReadMasked(ctx, stack.ReadMaskedArgs{
		Ref: f.ref, Object: obj, Selector: r.sel,
		Principal: r.p, Authorizer: r.authz, Key: testCursorKey(), Sink: sink.refusal, Cursor: "",
	})
	sink.wantRefused(t, page, err, stack.ErrReadDeadline, stack.RefusalDeadline)

	if got := blobs.calls(); got != before {
		t.Fatalf("blob calls = %d, want %d: a canceled read must load nothing", got, before)
	}

	if asked != 0 {
		t.Fatalf("the read asked the authorizer %d times, want 0: a canceled read must do no work", asked)
	}
}

// TestMaskedReadSizeRecordUnderstatesContent proves the length check after
// the load: the version record states a size under the limit, the content is
// over it.
func TestMaskedReadSizeRecordUnderstatesContent(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"resources":[],"pad":"` + strings.Repeat("a", 6<<20) + `"}`)
	r, blobs := plantedReader(t, raw, 100)

	before := blobs.calls()
	sink := newSink()

	page, err := r.read(t, sink.refusal, "")
	sink.wantRefused(t, page, err, stack.ErrTooLarge, stack.RefusalTooLarge)

	if blobs.calls() == before {
		t.Fatal("the read loaded no content: the record does not understate it")
	}
}

// TestMaskedReadUnreadableShape proves a version of a shape that the server
// cannot read is refused with no content, and that a deployment with no
// resources stays valid.
func TestMaskedReadUnreadableShape(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`[]`, `null`, `"s3cr3t"`, `42`, `true`,
		`{"resources":{}}`, `{"resources":"s3cr3t"}`, `{"resources":null}`, `{"resources":42}`,
		`{"resources":[`, `{"a":}`, `s3cr3t`, ``,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			r, _ := plantedReader(t, []byte(raw), -1)
			sink := newSink()

			page, err := r.read(t, sink.refusal, "")
			sink.wantRefused(t, page, err, stack.ErrUnreadableState, stack.RefusalUnreadable)

			if strings.Contains(err.Error(), "s3cr3t") {
				t.Fatalf("the error holds bytes of the content: %v", err)
			}
		})
	}

	t.Run("no resources", func(t *testing.T) {
		t.Parallel()

		r, _ := plantedReader(t, []byte(`{}`), -1)

		page, err := r.read(t, nil, "")
		if err != nil || len(page.Markers) != 0 {
			t.Fatalf("page %+v, err %v, want an empty page", page, err)
		}
	})
}

// hookAuthorizer runs hook, then asks inner. The hook stands for a change
// that lands between the lookup of the handler and the read.
type hookAuthorizer struct {
	inner identity.Authorizer
	hook  func()
}

// Authorize implements identity.Authorizer.
func (h hookAuthorizer) Authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	h.hook()

	return h.inner.Authorize(ctx, p, action, obj)
}

// ListObjects implements identity.Authorizer.
func (h hookAuthorizer) ListObjects(
	ctx context.Context, p identity.Principal, a identity.Action,
) (identity.Scope, error) {
	return h.inner.ListObjects(ctx, p, a)
}

// TestMaskedReadRefusesAnotherStack proves the read serves only the stack
// that the caller authorized by its stable ID. The name maps to another
// stack after the lookup, so the read refuses with no content. A read with
// no stable ID refuses too.
func TestMaskedReadRefusesAnotherStack(t *testing.T) {
	t.Parallel()

	t.Run("the stack is made again with other content", func(t *testing.T) {
		t.Parallel()

		r := newMaskedReader(t, deploymentWith(1))
		r.sel = stack.StateSelector{Latest: true, Version: 0}
		r.authz = hookAuthorizer{inner: r.authz, hook: func() {
			r.f.store.addStack(r.f.ref, "stk-other")

			s := r.f.started(t, stack.KindUpdate)
			body := `{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
				`"type":"pulumi:pulumi:Stack","outputs":{"leak":` + secretJSON("x") + `}}]}`

			in := checkpoint(1, envelope(body))

			if err := r.f.svc.SaveCheckpoint(t.Context(), r.f.ref, s.Update.ID, s.Token, in); err != nil {
				t.Errorf("save: %v", err)
			}

			done := stack.StatusSucceeded

			if _, err := r.f.svc.Complete(t.Context(), r.f.ref, s.Update.ID, s.Token, done); err != nil {
				t.Errorf("complete: %v", err)
			}
		}}

		sink := newSink()

		page, err := r.read(t, sink.refusal, "")
		sink.wantRefused(t, page, err, stack.ErrNotFound, stack.RefusalDenied)
	})

	t.Run("the name maps to a stack with another ID", func(t *testing.T) {
		t.Parallel()

		f, blobs := countFixture(t, maskedCheckpointLimit)
		saveVersion(t, f, deploymentWith(1))

		obj := unitObj(t)
		r := maskedReader{
			f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
			p: person(), authz: nil,
		}
		r.authz = hookAuthorizer{inner: readGrant(obj), hook: func() { f.store.addStack(f.ref, "stk-renamed") }}

		before := blobs.calls()
		sink := newSink()

		page, err := r.read(t, sink.refusal, "")
		sink.wantRefused(t, page, err, stack.ErrNotFound, stack.RefusalDenied)

		if got := blobs.calls(); got != before {
			t.Fatalf("blob calls = %d, want %d: the read must load no content of another stack", got, before)
		}
	})

	t.Run("the unit has no stable ID", func(t *testing.T) {
		t.Parallel()

		f, blobs := countFixture(t, maskedCheckpointLimit)
		saveVersion(t, f, deploymentWith(1))

		// The stack row has the empty ID too, so only the check of the
		// authorized ID stops the read.
		f.store.addStack(f.ref, "")

		obj, err := identity.UnitObject("pulumi", "acme", "web/dev")
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		r := maskedReader{
			f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
			p: person(), authz: readGrant(obj),
		}

		before := blobs.calls()
		sink := newSink()

		page, readErr := r.read(t, sink.refusal, "")
		sink.wantRefused(t, page, readErr, stack.ErrNotFound, stack.RefusalDenied)

		if got := blobs.calls(); got != before {
			t.Fatalf("blob calls = %d, want %d", got, before)
		}
	})
}

// TestMaskedReadNegativeOffset proves the page guard refuses a negative
// offset, which only a caller that skips the cursor check could pass.
func TestMaskedReadNegativeOffset(t *testing.T) {
	t.Parallel()

	page, err := stack.MaskedPageAt(testCursorKey(), []string{"/a", "/b"}, -1)
	if !errors.Is(err, stack.ErrBadPath) || len(page.Markers) != 0 {
		t.Fatalf("page %+v, err %v, want the bad path of the read", page, err)
	}
}

// uncalledReplayer is a journal replayer that fails the test when the read
// calls it: the masked read must never replay a journal.
type uncalledReplayer struct {
	t *testing.T
}

// Replay implements stack.JournalReplayer.
func (r uncalledReplayer) Replay(
	context.Context, func(context.Context) (io.ReadCloser, error), [][]byte, io.Writer,
) error {
	r.t.Error("the masked read called the journal replayer")

	return errors.New("replayer must not be called")
}

// TestMaskedReadSkipsRunningJournal proves the masked read serves stored
// checkpoint rows only: the version of a running journaled update has no
// row, so a pinned read of it is refused with no state, and the latest read
// answers the stored version. No journal batch is read or replayed, so the
// size of a version record is the size of the content that is loaded.
func TestMaskedReadSkipsRunningJournal(t *testing.T) {
	t.Parallel()

	clk := newClock()
	f := newBuildFixture(t, clk, uncalledReplayer{t: t})
	running := f.startJournaled(t)

	obj, err := identity.UnitObject("pulumi", "acme", "web/dev")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	obj = obj.WithUnitID(string(f.stackID))

	read := func(sel stack.StateSelector) (stack.MaskedPage, error) {
		return f.svc.ReadMasked(t.Context(), stack.ReadMaskedArgs{
			Ref: f.ref, Object: obj, Selector: sel,
			Principal: person(), Authorizer: readGrant(obj), Key: testCursorKey(), Sink: nil, Cursor: "",
		})
	}

	_, err = read(stack.StateSelector{Latest: false, Version: running.Update.Version})
	if !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("pinned read of the running journaled version: err = %v, want stack.ErrNoState", err)
	}

	page, err := read(stack.StateSelector{Latest: true, Version: 0})
	if err != nil || page.Version != "1" {
		t.Fatalf("latest read: page %+v, err %v, want the stored version 1", page, err)
	}
}
