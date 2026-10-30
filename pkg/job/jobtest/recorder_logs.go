package jobtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

// logCases returns the cases of AppendLog and of the close of a log.
func logCases() []recorderCase {
	return []recorderCase{
		{"logs: chunks 0 to n make the log version and EndStep closes it", testLogLifecycle},
		{"logs: a chunk with the same number and hash changes nothing", testLogDuplicate},
		{"logs: a chunk with the same number and another hash is ErrConflict", testLogConflict},
		{"logs: a chunk that is not the next one, or is too large, is ErrInvalid", testLogInvalid},
		{"logs: a step that is not open takes no chunk", testLogStepNotOpen},
		{"logs: an empty chunk and a chunk of the largest size are stored", testLogSizes},
		{"logs: a step with no chunk ends with no log", testLogNone},
		{"logs: a chunk is stored during a cancel", testLogCancel},
		{"logs: the item key of a log version is of the kind job-log", testLogItemKey},
		{"logs: parallel writers of one chunk with one content give one chunk", testLogParallelSame},
		{"logs: parallel writers of one chunk with other contents give one winner", testLogParallelOther},
	}
}

// logCaseStep opens an apply job with a step called stepPrepare that began, and
// returns its assignment.
func logCaseStep(t *testing.T, h RecorderHarness) *job.Assignment {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")
	mustBegin(t, h, a, stepPrepare)

	return a
}

// appendChunk appends the chunk seq of the step prepare.
func appendChunk(t *testing.T, h RecorderHarness, a *job.Assignment, seq int, content string) error {
	t.Helper()

	return h.Recorder.AppendLog(t.Context(), a.Claim, stepPrepare, seq, strings.NewReader(content))
}

// mustAppend appends the chunk and fails t on an error.
func mustAppend(t *testing.T, h RecorderHarness, a *job.Assignment, seq int, content string) {
	t.Helper()

	if err := appendChunk(t, h, a, seq, content); err != nil {
		t.Fatalf("AppendLog(%d) = %v", seq, err)
	}
}

// logOf returns the log of the step prepare of the job of a, or nil.
func logOf(t *testing.T, h RecorderHarness, a *job.Assignment) *job.LogRef {
	t.Helper()

	for _, r := range stepsOf(t, h, a) {
		if r.Name == stepPrepare {
			return r.Log
		}
	}

	t.Fatal("no record of the step prepare")

	return nil
}

// hashOf returns the SHA-256 of s in hexadecimal.
func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// readLog opens the log version with the reader and reads it.
func readLog(t *testing.T, h RecorderHarness, version string) ([]byte, error) {
	t.Helper()

	rc, err := h.Reader.OpenLog(t.Context(), "acme", version)
	if err != nil {
		return nil, err
	}

	return readAll(rc)
}

func testLogLifecycle(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	chunks := []string{"first line\n", "second line\nthird line\n", strings.Repeat("x", bigChunk)}
	started := time.Now().Add(-time.Minute)

	for i, c := range chunks {
		mustAppend(t, h, a, i, c)
		checkOpenLog(t, h, a, chunks[:i+1])
	}

	log := logOf(t, h, a)
	if !strings.HasPrefix(log.ID, job.LogVersionIDPrefix) || log.Version != 1 || log.CreatedAt.Before(started) ||
		!log.ClosedAt.IsZero() || log.SHA256 != "" || log.ItemKey == "" ||
		!log.ExpiresAt.Equal(history.ExpiresAt(log.CreatedAt, h.Retention.JobLog)) {
		t.Fatalf("open log = %+v, want version 1 of jlv-, open, with the end of the retention period", log)
	}

	mustEnd(t, h, a, stepPrepare, 0)
	checkClosedLog(t, h, a, chunks)

	// A closed log takes no chunk.
	err := appendChunk(t, h, a, len(chunks), "late")
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("AppendLog to a closed step = %v, want ErrInvalid", err)
	}
}

// checkOpenLog fails t unless the log of the step has one counted chunk for each
// of stored, and a reader sees them while the step runs.
func checkOpenLog(t *testing.T, h RecorderHarness, a *job.Assignment, stored []string) {
	t.Helper()

	log := logOf(t, h, a)
	if log == nil || log.Chunks != len(stored) {
		t.Fatalf("the log = %+v, want %d chunks", log, len(stored))
	}

	got, err := readLog(t, h, log.ID)
	if err != nil || string(got) != strings.Join(stored, "") {
		t.Fatalf("open log = %d bytes, %v; want the %d chunks", len(got), err, len(stored))
	}
}

// checkClosedLog fails t unless the closed log of the step has the chunks, their
// size and the hash of their content, and reads as that content.
func checkClosedLog(t *testing.T, h RecorderHarness, a *job.Assignment, chunks []string) {
	t.Helper()

	all := strings.Join(chunks, "")
	closed := logOf(t, h, a)

	if closed.Chunks != len(chunks) || closed.SHA256 != hashOf(all) || closed.Size != int64(len(all)) ||
		closed.ClosedAt.IsZero() {
		t.Fatalf("closed log = %+v, want %d chunks, %d bytes, the hash of the content", closed, len(chunks), len(all))
	}

	if got, err := readLog(t, h, closed.ID); err != nil || string(got) != all {
		t.Fatalf("closed log reads %d bytes, %v; want %d", len(got), err, len(all))
	}
}

func testLogDuplicate(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")
	mustAppend(t, h, a, 1, "two\n")

	cur := versionOf(t, h, a)
	before := len(blobsUnder(t, h, a.Job.Spec.Unit.Namespace()))

	// A retry of chunk 0 and of the last chunk changes nothing.
	mustAppend(t, h, a, 0, "one\n")
	mustAppend(t, h, a, 1, "two\n")

	if got := versionOf(t, h, a); got != cur {
		t.Fatalf("job version after retries = %d, want %d", got, cur)
	}

	if got := len(blobsUnder(t, h, a.Job.Spec.Unit.Namespace())); got != before {
		t.Fatalf("blobs after retries = %d, want %d", got, before)
	}

	if log := logOf(t, h, a); log.Chunks != chunksPair {
		t.Fatalf("chunks after retries = %d, want 2", log.Chunks)
	}
}

func testLogConflict(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")
	mustAppend(t, h, a, 1, "two\n")

	cur := versionOf(t, h, a)

	for _, seq := range []int{0, 1} {
		if err := appendChunk(t, h, a, seq, "other\n"); !errors.Is(err, job.ErrConflict) {
			t.Fatalf("AppendLog(%d) with another hash = %v, want ErrConflict", seq, err)
		}
	}

	if got := versionOf(t, h, a); got != cur {
		t.Fatalf("job version after refused chunks = %d, want %d", got, cur)
	}

	mustEnd(t, h, a, stepPrepare, 0)

	if got, err := readLog(t, h, logOf(t, h, a).ID); err != nil || string(got) != "one\ntwo\n" {
		t.Fatalf("log after refused chunks = %q, %v; want the first content", got, err)
	}
}

func testLogInvalid(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)

	// The first chunk of a log is chunk 0.
	for _, seq := range []int{1, 2, job.MaxLogChunks} {
		if err := appendChunk(t, h, a, seq, "x"); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("AppendLog(%d) before chunk 0 = %v, want ErrInvalid", seq, err)
		}
	}

	if err := appendChunk(t, h, a, -1, "x"); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("AppendLog(-1) = %v, want ErrInvalid", err)
	}

	if log := logOf(t, h, a); log != nil {
		t.Fatalf("a refused chunk made a log: %+v", log)
	}

	mustAppend(t, h, a, 0, "x")

	// A gap, and a chunk above the largest size.
	if err := appendChunk(t, h, a, gapSeq, "x"); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("AppendLog(2) after chunk 0 = %v, want ErrInvalid", err)
	}

	big := strings.Repeat("y", job.MaxLogChunk+1)
	if err := appendChunk(t, h, a, 1, big); !errors.Is(err, job.ErrInvalid) {
		t.Errorf("AppendLog of a chunk above MaxLogChunk = %v, want ErrInvalid", err)
	}

	if log := logOf(t, h, a); log.Chunks != 1 {
		t.Fatalf("chunks after refused chunks = %d, want 1", log.Chunks)
	}

	// The refused big chunk left no blob. The next chunk is still chunk 1.
	mustAppend(t, h, a, 1, "fine")
}

func testLogStepNotOpen(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := openApply(t, h, setUp(t, h.Harness), "unit-0")

	// The step did not begin.
	if err := appendChunk(t, h, a, 0, "x"); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("AppendLog of a step that did not begin = %v, want ErrInvalid", err)
	}

	// A name that is not a step.
	err := h.Recorder.AppendLog(t.Context(), a.Claim, "Not A Step", 0, strings.NewReader("x"))
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("AppendLog of a bad step name = %v, want ErrInvalid", err)
	}

	mustBegin(t, h, a, stepPrepare)
	mustEnd(t, h, a, stepPrepare, 0)

	if err = appendChunk(t, h, a, 0, "x"); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("AppendLog of a step that ended = %v, want ErrInvalid", err)
	}
}

func testLogSizes(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	largest := strings.Repeat("z", job.MaxLogChunk)

	mustAppend(t, h, a, 0, "")
	mustAppend(t, h, a, 1, largest)
	mustAppend(t, h, a, thirdChunk, "")
	mustEnd(t, h, a, stepPrepare, 0)

	log := logOf(t, h, a)
	if log.Chunks != 3 || log.Size != int64(len(largest)) || log.SHA256 != hashOf(largest) {
		t.Fatalf("log = %+v, want 3 chunks of %d bytes in all", log, len(largest))
	}

	if got, err := readLog(t, h, log.ID); err != nil || string(got) != largest {
		t.Fatalf("log reads %d bytes, %v; want %d", len(got), err, len(largest))
	}
}

func testLogNone(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustEnd(t, h, a, stepPrepare, 0)

	if log := logOf(t, h, a); log != nil {
		t.Fatalf("a step with no chunk has the log %+v", log)
	}

	if got := blobsUnder(t, h, a.Job.Spec.Unit.Namespace()); len(got) != 0 {
		t.Fatalf("blobs of a step with no chunk = %d, want 0", len(got))
	}
}

func testLogCancel(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)
	mustAppend(t, h, a, 0, "before the cancel\n")
	cancelJob(t, h.Harness, f, a.Claim.Job)

	// The executor still sends what the step wrote until it stopped.
	mustAppend(t, h, a, 1, "after the cancel\n")
	mustEnd(t, h, a, stepPrepare, -1)

	log := logOf(t, h, a)
	if got, err := readLog(t, h, log.ID); err != nil || string(got) != "before the cancel\nafter the cancel\n" {
		t.Fatalf("log of a canceled step = %q, %v", got, err)
	}
}

func testLogItemKey(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "x")

	j := jobOf(t, h.Harness, a.Claim.Job)
	log := logOf(t, h, a)

	k, err := h.ItemKeys.GetItemKey(t.Context(), itemKeyOf(t, &j, secret.ItemKindJobLog, log.ID))
	if err != nil || k.ID != log.ItemKey || k.Shredded() {
		t.Fatalf("item key of the log = %+v, %v; want the live key %s", k, err, log.ItemKey)
	}

	// The log has its own key: the second step has another one.
	mustBegin(t, h, a, stepApply)

	if err = h.Recorder.AppendLog(t.Context(), a.Claim, stepApply, 0, strings.NewReader("y")); err != nil {
		t.Fatalf("AppendLog of the second step = %v", err)
	}

	var second *job.LogRef

	for _, r := range stepsOf(t, h, a) {
		if r.Name == stepApply {
			second = r.Log
		}
	}

	if second == nil || second.ItemKey == log.ItemKey || second.ID == log.ID {
		t.Fatalf("second log = %+v, want its own version and item key", second)
	}
}

func testLogParallelSame(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)

	const writers = 8

	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() { errs[i] = appendChunk(t, h, a, 0, "same\n") })
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: AppendLog(0) = %v", i, err)
		}
	}

	// A second round on chunk 1, with the log already there.
	for i := range writers {
		wg.Go(func() { errs[i] = appendChunk(t, h, a, 1, "again\n") })
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: AppendLog(1) = %v", i, err)
		}
	}

	mustEnd(t, h, a, stepPrepare, 0)

	log := logOf(t, h, a)
	if got, err := readLog(t, h, log.ID); err != nil || string(got) != "same\nagain\n" || log.Chunks != chunksPair {
		t.Fatalf("log = %q (%d chunks), %v; want each chunk once", got, log.Chunks, err)
	}
}

func testLogParallelOther(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "head\n")

	const writers = 8

	errs := make([]error, writers)
	contents := make([]string, writers)

	var wg sync.WaitGroup

	for i := range writers {
		contents[i] = strings.Repeat(string(rune('a'+i)), repeatSmall) + "\n"

		wg.Go(func() { errs[i] = appendChunk(t, h, a, 1, contents[i]) })
	}

	wg.Wait()

	winners := 0
	winner := ""

	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			winner = contents[i]
		case !errors.Is(err, job.ErrConflict):
			t.Errorf("writer %d: AppendLog(1) = %v, want nil or ErrConflict", i, err)
		default:
		}
	}

	if winners != 1 {
		t.Fatalf("writers of one chunk that succeeded = %d, want 1", winners)
	}

	mustEnd(t, h, a, stepPrepare, 0)

	// The content of the chunk is the one of the winner, whole.
	log := logOf(t, h, a)
	if got, err := readLog(t, h, log.ID); err != nil || string(got) != "head\n"+winner || log.Chunks != chunksPair {
		t.Fatalf("log = %d bytes (%d chunks), %v; want head and the content of the winner", len(got), log.Chunks, err)
	}
}

func testLogReadMissing(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")
	mustEnd(t, h, a, stepPrepare, 0)

	log := logOf(t, h, a)

	objects := blobsUnder(t, h, a.Job.Spec.Unit.Namespace())
	if len(objects) != 1 {
		t.Fatalf("log blobs = %d, want 1", len(objects))
	}

	if err := h.Blobs.Delete(t.Context(), objects[0].Key); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	if _, err := readLog(t, h, log.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("read of a log with a missing chunk blob = %v, want ErrNotFound", err)
	}
}

// logReadCases returns the cases of the reads of logs.
func logReadCases() []recorderCase {
	return []recorderCase{
		{"log reads: an unknown version, and a version of another namespace, are ErrNotFound", testLogReadUnknown},
		{"log reads: a shredded key is ErrShredded, also with no blob left", testLogReadShredded},
		{"log reads: a damaged, cut or moved chunk blob does not read as content", testLogReadDamaged},
		{"log reads: a missing chunk blob is ErrNotFound", testLogReadMissing},
	}
}

func testLogReadUnknown(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")

	log := logOf(t, h, a)

	for _, version := range []string{"jlv-unknown", "", "pln-x", "../x"} {
		if _, err := h.Reader.OpenLog(t.Context(), "acme", version); !errors.Is(err, job.ErrNotFound) &&
			!errors.Is(err, job.ErrInvalid) {
			t.Errorf("OpenLog(%q) = %v, want ErrNotFound", version, err)
		}
	}

	if _, err := h.Domain.CreateNamespace(t.Context(), "other"); err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	if _, err := h.Reader.OpenLog(t.Context(), "other", log.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("OpenLog of a version of another namespace = %v, want ErrNotFound", err)
	}
}

func testLogReadShredded(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openApply(t, h, f, "unit-0")
	mustBegin(t, h, a, stepPrepare)
	mustAppend(t, h, a, 0, "secret value\n")
	mustEnd(t, h, a, stepPrepare, 0)

	j := jobOf(t, h.Harness, a.Claim.Job)
	log := logOf(t, h, a)

	_, err := h.ItemKeys.ShredItemKey(t.Context(), itemKeyOf(t, &j, secret.ItemKindJobLog, log.ID), time.Now(), f.fence)
	if err != nil {
		t.Fatalf("ShredItemKey = %v", err)
	}

	if _, err := readLog(t, h, log.ID); !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("OpenLog of a shredded log = %v, want ErrShredded", err)
	}

	// A redaction deletes the blobs too. The answer stays the same.
	for _, o := range blobsUnder(t, h, f.fence.Namespace) {
		if err := h.Blobs.Delete(t.Context(), o.Key); err != nil {
			t.Fatalf("delete blob: %v", err)
		}
	}

	if _, err := readLog(t, h, log.ID); !errors.Is(err, secret.ErrShredded) {
		t.Fatalf("OpenLog of a shredded log with no blob = %v, want ErrShredded", err)
	}
}

func testLogReadDamaged(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, strings.Repeat("a", repeatMid))
	mustAppend(t, h, a, 1, strings.Repeat("b", repeatMid))
	mustEnd(t, h, a, stepPrepare, 0)

	log := logOf(t, h, a)
	ns := a.Job.Spec.Unit.Namespace()

	objects := blobsUnder(t, h, ns)
	if len(objects) != chunksPair {
		t.Fatalf("log blobs = %d, want 2", len(objects))
	}

	first, second := objects[0].Key, objects[1].Key
	if strings.Contains(first.String(), "/00000001") {
		first, second = second, first
	}

	good0, good1 := rawBlob(t, h, first), rawBlob(t, h, second)

	flipped := bytes.Clone(good0)
	flipped[len(flipped)/2] ^= 0x01

	cases := []struct {
		name  string
		first []byte
		next  []byte
	}{
		{"a flipped byte", flipped, good1},
		{"a cut chunk", good0[:len(good0)-10], good1},
		{"chunks swapped", good1, good0},
		{"a chunk twice", good0, good0},
	}

	for _, tc := range cases {
		replaceRaw(t, h, first, tc.first)
		replaceRaw(t, h, second, tc.next)

		_, err := readLog(t, h, log.ID)
		if !errors.Is(err, secret.ErrDecrypt) {
			t.Errorf("read of a log with %s = %v, want ErrDecrypt", tc.name, err)
		}
	}

	replaceRaw(t, h, first, good0)
	replaceRaw(t, h, second, good1)

	if got, err := readLog(t, h, log.ID); err != nil || len(got) != 2*repeatMid {
		t.Fatalf("read of the restored log = %d bytes, %v; want 10000", len(got), err)
	}
}

// rawBlob returns the stored bytes of the blob under key.
func rawBlob(t *testing.T, h RecorderHarness, key blob.Key) []byte {
	t.Helper()

	rc, err := h.Blobs.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	data, err := readAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	return data
}

// replaceRaw replaces the blob under key with data.
func replaceRaw(t *testing.T, h RecorderHarness, key blob.Key, data []byte) {
	t.Helper()

	if err := h.Blobs.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete %s: %v", key, err)
	}

	if err := h.Blobs.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}
