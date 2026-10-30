package storecheck

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// ErrBudget reports a write that would pass the budget of the check. The check
// stops at the first one.
var ErrBudget = errors.New("store check budget ended")

var (
	errPrefixNotCleared = errors.New("objects of an earlier run stayed under the prefix")
	errNoRunResult      = errors.New("the run step has no result")
)

// failure is the text of a case that did not pass. It is fixed text of this
// package, so it holds no endpoint, no bucket name and no credential.
type failure string

func (f failure) Error() string { return string(f) }

// describe returns the fixed text for err. An error from the store, which can
// hold an endpoint or a credential, never reaches the text.
func describe(err error) string {
	if f, ok := errors.AsType[failure](err); ok {
		return string(f)
	}

	switch {
	case errors.Is(err, ErrBudget):
		return "the check budget ended"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "the case ran out of time"
	case errors.Is(err, blob.ErrThrottled):
		return "the store asked the client to slow down"
	case errors.Is(err, blob.ErrNotFound):
		return "the store did not find an object that the check wrote"
	case errors.Is(err, blob.ErrExists):
		return "the store answered that an object exists"
	case errors.Is(err, blob.ErrOutOfScope), errors.Is(err, blob.ErrInvalidKey):
		return "the check refused its own key"
	default:
		return "the store request failed"
	}
}

// meter wraps the view of the prefix. It counts the budget, and it sees the
// throttling answers of the store.
type meter struct {
	inner     blob.Store
	limits    Limits
	objects   atomic.Int64
	total     atomic.Int64
	throttled atomic.Bool
}

var _ blob.Store = (*meter)(nil)

// budgetReader stops a Put that passes the size of one object or the total.
type budgetReader struct {
	r        io.Reader
	m        *meter
	n        int64
	exceeded bool
}

func (b *budgetReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)

	if b.n > b.m.limits.MaxObjectBytes || b.m.total.Load()+b.n > b.m.limits.MaxTotalBytes {
		b.exceeded = true

		return 0, ErrBudget
	}

	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		// The end of the data is the one error that a reader returns as it is.
		return n, io.EOF
	default:
		return n, fmt.Errorf("read the object: %w", err)
	}
}

func (m *meter) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	if m.objects.Add(1) > int64(m.limits.MaxObjects) {
		return ErrBudget
	}

	br := &budgetReader{r: r, m: m, n: 0, exceeded: false}
	err := m.inner.Put(ctx, key, br)
	m.total.Add(br.n)

	if br.exceeded {
		return ErrBudget
	}

	return m.see(err)
}

func (m *meter) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	rc, err := m.inner.Get(ctx, key)

	return rc, m.see(err)
}

func (m *meter) Delete(ctx context.Context, key blob.Key) error {
	return m.see(m.inner.Delete(ctx, key))
}

func (m *meter) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	objects, err := m.inner.List(ctx, prefix)

	return objects, m.see(err)
}

func (m *meter) Ping(ctx context.Context) error {
	return m.see(m.inner.Ping(ctx))
}

// see records a throttling answer and returns err with the context of the
// request. A caller tests it with errors.Is.
func (m *meter) see(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, blob.ErrThrottled) {
		m.throttled.Store(true)
	}

	return fmt.Errorf("store request: %w", err)
}

// run is one execution of the cases.
type run struct {
	view   *blob.Scoped
	m      *meter
	limits Limits
	now    func() time.Time
	prefix string
	// conditional is what the second-put case saw.
	conditional ConditionalWrites
}

func newRun(view *blob.Scoped, limits Limits, now func() time.Time) *run {
	return &run{
		view: view, m: &meter{
			inner: view, limits: limits, objects: atomic.Int64{}, total: atomic.Int64{}, throttled: atomic.Bool{},
		},
		limits: limits, now: now, prefix: view.Prefix(), conditional: ConditionalWritesUnknown,
	}
}

type caseSpec struct {
	run  func(ctx context.Context, r *run, dir string) error
	name string
}

// caseSpecs returns the cases in the order of the report. The names follow
// the blobtest suite.
func caseSpecs() []caseSpec {
	return []caseSpec{
		{name: "put then get returns the same bytes", run: caseRoundTrip},
		{name: "get of a missing key is ErrNotFound", run: caseMissing},
		{name: "delete removes and is idempotent", run: caseDelete},
		{name: "second put on one key replaces it or fails with ErrExists", run: caseSecondPut},
		{name: "parallel put on one key leaves one whole object", run: caseParallelPut},
		{name: "large object round-trips", run: caseLargeObject},
		{name: "failed put leaves no object", run: caseFailedPut},
		{name: "list returns the objects under a prefix ordered by key", run: caseList},
		{name: "list of a missing prefix is empty", run: caseListMissing},
		{name: "ping answers on a healthy store", run: casePing},
	}
}

// deleteCaseName is the name of the last case: the delete of the prefix.
const deleteCaseName = "delete of the prefix removes every object"

// all runs the cases and the measurements. It returns the report so far and,
// when the run stopped before its end, the fixed text of the reason.
func (r *run) all(ctx context.Context, in input) (Report, string) {
	report := Report{
		StartedAt: r.now().
			UTC(),
		FinishedAt:        time.Time{},
		Target:            in.Target,
		Prefix:            blob.Key(strings.TrimSuffix(r.prefix, "/")),
		Passed:            false,
		ConditionalWrites: ConditionalWritesUnknown,
		Cases:             nil,
		PutLatency: Latency{
			P50: 0,
			P99: 0,
		},
		GetLatency:        Latency{P50: 0, P99: 0},
		DeleteLatency:     Latency{P50: 0, P99: 0},
		PutBytesPerSecond: 0,
		GetBytesPerSecond: 0,
		DeletesPerSecond:  0,
		ThrottlingSeen:    false,
		PrefixDeleted:     false,
	}

	stop := ""

	for i, spec := range caseSpecs() {
		err := spec.run(ctx, r, fmt.Sprintf("%scase-%02d/", r.prefix, i+1))
		report.Cases = append(report.Cases, Case{Name: spec.name, Message: messageOf(err), Passed: err == nil})

		if halts(err) {
			stop = describe(err)

			break
		}
	}

	if stop == "" {
		if err := r.measure(ctx, &report); err != nil {
			stop = describe(err)
		}
	}

	report.ConditionalWrites = r.conditional

	return report, stop
}

// halts reports whether err ends the check: the budget or the time limit.
func halts(err error) bool {
	return errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func messageOf(err error) string {
	if err == nil {
		return ""
	}

	return describe(err)
}

// finishPrefix deletes every object under the prefix and adds the last case
// and the closing figures to report. It returns the count of objects that
// stayed, and the error of the first delete that failed.
func (r *run) finishPrefix(ctx context.Context, report *Report) (int, error) {
	remaining, err := deletePrefix(ctx, r.m, r.prefix)

	report.PrefixDeleted = err == nil && remaining == 0
	report.ThrottlingSeen = r.m.throttled.Load()
	report.FinishedAt = r.now().UTC()

	delCase := Case{Name: deleteCaseName, Message: "", Passed: report.PrefixDeleted}
	if !delCase.Passed {
		delCase.Message = "the store did not delete every object under the prefix; the objects stay until the next check or the reaper removes them"
	}

	report.Cases = append(report.Cases, delCase)

	report.Passed = !slices.ContainsFunc(report.Cases, func(c Case) bool { return !c.Passed })

	return remaining, err
}

// deletePrefix deletes each object under prefix with one Delete call, then
// lists the prefix again. It returns the count that the second listing shows
// and the first delete error. A caller passes the view of the prefix or the
// meter around it, so the store refuses a key outside the prefix. A delete
// that fails once is not tried again here: the prefix reaper deletes what
// stays.
func deletePrefix(ctx context.Context, st blob.Store, prefix string) (int, error) {
	objects, err := st.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list the prefix: %w", err)
	}

	var first error

	for _, obj := range objects {
		if delErr := st.Delete(ctx, obj.Key); delErr != nil && first == nil {
			first = delErr
		}
	}

	left, err := st.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list the prefix again: %w", err)
	}

	return len(left), first
}

// payload returns n bytes that depend on label, so two labels give two
// payloads. The bytes come from a chain of SHA-256 digests.
func payload(label string, n int) []byte {
	sum := sha256.Sum256([]byte(label))
	out := make([]byte, 0, n+sha256.Size)

	for len(out) < n {
		out = append(out, sum[:]...)
		sum = sha256.Sum256(sum[:])
	}

	return out[:n]
}

// The sizes of the payloads of the cases, in bytes.
const (
	sizeTiny     = 64
	sizeSmall    = 512
	sizeParallel = 2048
	sizeBasic    = 4096
	sizeFailing  = 8192
	sizeList     = 16
)

func (r *run) put(ctx context.Context, key blob.Key, data []byte) error {
	return r.m.Put(ctx, key, bytes.NewReader(data))
}

func (r *run) read(ctx context.Context, key blob.Key) ([]byte, error) {
	rc, err := r.m.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	data, readErr := io.ReadAll(rc)
	closeErr := rc.Close()

	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read object: %w", errors.Join(readErr, closeErr))
	}

	return data, nil
}

func (r *run) key(dir, name string) (blob.Key, error) {
	key, err := blob.ParseKey(dir + name)
	if err != nil {
		return "", fmt.Errorf("make key: %w", err)
	}

	return key, nil
}

func caseRoundTrip(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "a")
	if keyErr != nil {
		return keyErr
	}

	want := payload("round trip", sizeBasic)
	if putErr := r.put(ctx, key, want); putErr != nil {
		return putErr
	}

	got, err := r.read(ctx, key)
	if err != nil {
		return err
	}

	if !bytes.Equal(got, want) {
		return failure("the bytes read differ from the bytes written")
	}

	return nil
}

func caseMissing(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "missing")
	if keyErr != nil {
		return keyErr
	}

	if _, err := r.m.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		if err == nil {
			return failure("a get of a missing key did not fail")
		}

		return failure("a get of a missing key did not answer ErrNotFound")
	}

	return nil
}

func caseDelete(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "a")
	if keyErr != nil {
		return keyErr
	}

	if err := r.put(ctx, key, payload("delete", sizeTiny)); err != nil {
		return err
	}

	if err := r.m.Delete(ctx, key); err != nil {
		return err
	}

	if _, err := r.m.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		return failure("an object stayed after the delete")
	}

	if err := r.m.Delete(ctx, key); err != nil {
		return failure("a delete of a missing key failed")
	}

	return nil
}

// caseSecondPut writes one key twice. A store that enforces If-None-Match
// refuses the second write with ErrExists and keeps the first object. A store
// that ignores it replaces the object. Both are correct: the database decides
// which write wins. The case only shows which one the store does.
func caseSecondPut(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "a")
	if keyErr != nil {
		return keyErr
	}

	first, second := payload("first write", sizeSmall), payload("second write", sizeSmall)
	if err := r.put(ctx, key, first); err != nil {
		return err
	}

	putErr := r.put(ctx, key, second)

	got, err := r.read(ctx, key)
	if err != nil {
		return err
	}

	switch {
	case errors.Is(putErr, blob.ErrExists):
		r.conditional = ConditionalWritesEnforced

		if !bytes.Equal(got, first) {
			return failure("a refused second write changed the object")
		}

		return nil
	case putErr == nil:
		r.conditional = ConditionalWritesIgnored

		if !bytes.Equal(got, second) {
			return failure("a second write succeeded and the object is not the new one")
		}

		return nil
	default:
		return putErr
	}
}

const parallelWriters = 8

func caseParallelPut(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "a")
	if keyErr != nil {
		return keyErr
	}

	payloads := make([][]byte, parallelWriters)
	errs := make([]error, parallelWriters)

	var wg sync.WaitGroup

	for i := range parallelWriters {
		payloads[i] = payload(fmt.Sprintf("writer %d", i), sizeParallel)

		wg.Go(func() { errs[i] = r.put(ctx, key, payloads[i]) })
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil && !errors.Is(err, blob.ErrExists) {
			return err
		}
	}

	got, err := r.read(ctx, key)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(payloads, func(p []byte) bool { return bytes.Equal(p, got) }) {
		return failure("the object after parallel writes is not one whole write")
	}

	return nil
}

const largeObjectBytes = 4 << 20

func caseLargeObject(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "large")
	if keyErr != nil {
		return keyErr
	}

	want := payload("large object", largeObjectBytes)
	if err := r.put(ctx, key, want); err != nil {
		return err
	}

	got, err := r.read(ctx, key)
	if err != nil {
		return err
	}

	if sha256.Sum256(got) != sha256.Sum256(want) {
		return failure("the bytes of the large object differ from the bytes written")
	}

	return nil
}

// failAfter fails after n bytes, as a client that loses its connection.
type failAfter struct {
	data []byte
	n    int
	off  int
}

var errClientLost = errors.New("client lost")

func (f *failAfter) Read(p []byte) (int, error) {
	if f.off >= f.n {
		return 0, errClientLost
	}

	n := copy(p, f.data[f.off:f.n])
	f.off += n

	return n, nil
}

func caseFailedPut(ctx context.Context, r *run, dir string) error {
	key, keyErr := r.key(dir, "a")
	if keyErr != nil {
		return keyErr
	}

	data := payload("failing writer", sizeFailing)

	if err := r.m.Put(ctx, key, &failAfter{data: data, n: sizeBasic, off: 0}); err == nil {
		return failure("a put with a failing reader did not fail")
	} else if errors.Is(err, ErrBudget) {
		return err
	}

	if _, err := r.m.Get(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		return failure("a failed put left an object")
	}

	return nil
}

func caseList(ctx context.Context, r *run, dir string) error {
	names := []string{"c", "a", "b"}
	for _, name := range names {
		key, keyErr := r.key(dir, name)
		if keyErr != nil {
			return keyErr
		}

		if err := r.put(ctx, key, payload("list", sizeList)); err != nil {
			return err
		}
	}

	objects, err := r.m.List(ctx, dir)
	if err != nil {
		return err
	}

	got := make([]string, 0, len(objects))
	for _, obj := range objects {
		got = append(got, obj.Key.String())
	}

	want := []string{dir + "a", dir + "b", dir + "c"}
	if !slices.Equal(got, want) {
		return failure("the list is not the written keys in key order")
	}

	return nil
}

func caseListMissing(ctx context.Context, r *run, dir string) error {
	objects, err := r.m.List(ctx, dir+"none/")
	if err != nil {
		return err
	}

	if len(objects) != 0 {
		return failure("the list of a missing prefix is not empty")
	}

	return nil
}

func casePing(ctx context.Context, r *run, _ string) error {
	return r.m.Ping(ctx)
}
