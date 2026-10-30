package jobtest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

// chunkKeyCases returns the cases of the blob key that the row of a log chunk
// holds: the first confirm wins, the key of a row is checked, and a log from
// the first key layout reads.
func chunkKeyCases() []recorderCase {
	return []recorderCase{
		{"chunk keys: the first confirm of a chunk wins and the row keeps its blob key", testChunkConfirmFirstWins},
		{"chunk keys: a confirm with a key that is not an attempt of the chunk is ErrInvalid", testChunkConfirmBadKey},
		{"chunk keys: a log from the first key layout reads", testChunkLegacyLayout},
		{"chunk keys: a log with chunks from both key layouts reads", testChunkMixedLayouts},
		{"chunk keys: a chunk the recorder stores has the key of its attempt", testChunkAttemptKey},
	}
}

// reserveFirstChunk reserves chunk 0 of the step prepare with a new log version
// and returns the version.
func reserveFirstChunk(t *testing.T, h RecorderHarness, a *job.Assignment, content string) string {
	t.Helper()

	nl := newLogFor(t, h, a, h.Sealer.NewItemKey)

	plan, err := h.Records.ReserveLogChunk(
		t.Context(), a.Claim, stepPrepare,
		job.ChunkReserve{Seq: 0, SHA256: hashOf(content), Size: int64(len(content))}, nl,
	)
	if err != nil {
		t.Fatalf("ReserveLogChunk(0) = %v", err)
	}

	return plan.Version
}

// attemptKey returns the key of an attempt of chunk seq.
func attemptKey(t *testing.T, a *job.Assignment, version string, seq int, attempt string) string {
	t.Helper()

	key, err := job.LogChunkAttemptKey(a.Claim.Job, version, seq, job.LogAttemptIDPrefix+attempt)
	if err != nil {
		t.Fatalf("LogChunkAttemptKey(%q) = %v", attempt, err)
	}

	return key.String()
}

// confirmWith confirms chunk seq of the version with the blob key key.
func confirmWith(
	t *testing.T, h RecorderHarness, a *job.Assignment, version string, seq int, key string,
) (bool, error) {
	t.Helper()

	return h.Records.ConfirmLogChunk(t.Context(), a.Claim, job.ChunkConfirm{
		Step: stepPrepare, Version: version, Seq: seq, Stored: blob.Digest{Size: 1, MD5: [16]byte{1}}, BlobKey: key,
	})
}

func testChunkConfirmFirstWins(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	version := reserveFirstChunk(t, h, a, "x")

	first := attemptKey(t, a, version, 0, "first")
	second := attemptKey(t, a, version, 0, "second")

	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"the first attempt wins", first, true},
		{"a second attempt loses", second, false},
		{"the first attempt again is the same confirm", first, true},
		{"the second attempt again loses", second, false},
	}

	for _, tc := range cases {
		won, err := confirmWith(t, h, a, version, 0, tc.key)
		if err != nil || won != tc.want {
			t.Errorf("%s: ConfirmLogChunk = %v, %v; want %v, nil", tc.name, won, err, tc.want)
		}
	}

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, version)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	if !slices.Equal(rec.ChunkKeys, []string{first}) || rec.Log.Chunks != 1 {
		t.Fatalf("log = %d chunks with keys %q; want 1 chunk with key %q", rec.Log.Chunks, rec.ChunkKeys, first)
	}
}

func testChunkConfirmBadKey(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	version := reserveFirstChunk(t, h, a, "x")

	good := attemptKey(t, a, version, 0, "good")
	base := strings.TrimSuffix(good, "-"+job.LogAttemptIDPrefix+"good")

	cases := []struct {
		name string
		key  string
	}{
		{"the key of another chunk", attemptKey(t, a, version, 1, "good")},
		{"the key of another log version", attemptKey(t, a, job.LogVersionIDPrefix+"other", 0, "good")},
		{"a key with no attempt", base + "-"},
		{"a key with an attempt of another kind", base + "-xyz-good"},
		{"a key with a longer path", good + "/more"},
		{"a key that leaves the log", "job/" + string(a.Claim.Job) + "/log/../log/" + version + "/00000000-jla-good"},
		{"the first layout with a suffix", base + "x"},
		{"a text that is no key", "not a key"},
	}

	for _, tc := range cases {
		won, err := confirmWith(t, h, a, version, 0, tc.key)
		if !errors.Is(err, job.ErrInvalid) || won {
			t.Errorf("%s: ConfirmLogChunk = %v, %v; want false, ErrInvalid", tc.name, won, err)
		}
	}

	// None of the refusals counted the chunk: the good key still wins.
	if won, err := confirmWith(t, h, a, version, 0, good); err != nil || !won {
		t.Fatalf("ConfirmLogChunk after the refusals = %v, %v; want true, nil", won, err)
	}
}

// storeFirstLayoutChunk stores the chunk 0 of a new log version as the first
// key layout did: sealed with the binding of the key job/<job>/log/<version>/<seq>,
// stored under that key, and confirmed with no blob key.
func storeFirstLayoutChunk(t *testing.T, h RecorderHarness, a *job.Assignment, content string) string {
	t.Helper()

	version := reserveFirstChunk(t, h, a, content)

	j := jobOf(t, h.Harness, a.Claim.Job)
	item := itemKeyOf(t, &j, secret.ItemKindJobLog, version)

	key, err := job.LogChunkKey(a.Claim.Job, version, 0)
	if err != nil {
		t.Fatalf("LogChunkKey = %v", err)
	}

	sealed, err := h.Sealer.SealContent(t.Context(), item, []byte(key.String()), bytes.NewReader([]byte(content)))
	if err != nil {
		t.Fatalf("SealContent = %v", err)
	}

	measured := blob.NewDigestReader(sealed)

	ns, err := h.Namespaces.GetNamespace(t.Context(), a.Claim.Fence.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	ref := job.LogChunkRef{Namespace: ns.ID, Job: a.Claim.Job, Version: version, Seq: 0, BlobKey: ""}
	if err = h.Artifacts.PutLogChunk(t.Context(), ref, measured); err != nil {
		t.Fatalf("PutLogChunk = %v", err)
	}

	won, err := h.Records.ConfirmLogChunk(t.Context(), a.Claim, job.ChunkConfirm{
		Step: stepPrepare, Version: version, Seq: 0, Stored: measured.Digest(), BlobKey: "",
	})
	if err != nil || !won {
		t.Fatalf("ConfirmLogChunk of the first layout = %v, %v; want true, nil", won, err)
	}

	return version
}

func testChunkLegacyLayout(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	version := storeFirstLayoutChunk(t, h, a, "old\n")

	// A reader of the open log reads the chunk of the first layout.
	if got, err := readLog(t, h, version); err != nil || string(got) != "old\n" {
		t.Fatalf("OpenLog of the first layout = %q, %v; want %q", got, err, "old\n")
	}

	// EndStep reads the log to close it, with the blob key of the row.
	mustEnd(t, h, a, stepPrepare, 0)

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, version)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	if !slices.Equal(rec.ChunkKeys, []string{""}) || rec.Log.ClosedAt.IsZero() {
		t.Fatalf("closed log = %+v with chunk keys %q; want closed, with the empty key of the first layout",
			rec.Log, rec.ChunkKeys)
	}

	if got, err := readLog(t, h, version); err != nil || string(got) != "old\n" {
		t.Fatalf("OpenLog of the closed first layout = %q, %v; want %q", got, err, "old\n")
	}
}

func testChunkMixedLayouts(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	version := storeFirstLayoutChunk(t, h, a, "old\n")

	// The recorder takes the next chunk with the key of an attempt. The log
	// then holds both layouts, and EndStep reads both to close it.
	mustAppend(t, h, a, 1, "new\n")
	mustEnd(t, h, a, stepPrepare, 0)

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, version)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	if len(rec.ChunkKeys) != chunksPair || rec.ChunkKeys[0] != "" || rec.ChunkKeys[1] == "" {
		t.Fatalf("chunk keys = %q; want the empty key of the first layout, then the key of an attempt", rec.ChunkKeys)
	}

	if got, err := readLog(t, h, version); err != nil || string(got) != "old\nnew\n" {
		t.Fatalf("OpenLog of both layouts = %q, %v; want %q", got, err, "old\nnew\n")
	}
}

func testChunkAttemptKey(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")
	mustAppend(t, h, a, 1, "two\n")

	log := logOf(t, h, a)

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, log.ID)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	if len(rec.ChunkKeys) != chunksPair {
		t.Fatalf("chunk keys = %q, want 2", rec.ChunkKeys)
	}

	for seq, key := range rec.ChunkKeys {
		if _, err := job.ChunkBlobKey(a.Claim.Job, log.ID, seq, key); err != nil || key == "" {
			t.Errorf("chunk %d key = %q, %v; want the key of an attempt", seq, key, err)
		}
	}

	// Each key that a row names has its blob.
	var held []string

	for _, o := range blobsUnder(t, h, a.Claim.Fence.Namespace) {
		held = append(held, o.Key.String())
	}

	for _, key := range rec.ChunkKeys {
		if !slices.ContainsFunc(held, func(k string) bool { return strings.HasSuffix(k, key) }) {
			t.Errorf("no blob for the key %q in %q", key, held)
		}
	}
}
