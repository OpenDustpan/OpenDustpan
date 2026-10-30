package jobtest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/job"
)

// chunkKeyCases returns the cases of the blob key that the row of a log chunk
// holds: the first confirm wins, the key of a row is checked, and the recorder
// stores each chunk under the key of its attempt.
func chunkKeyCases() []recorderCase {
	return []recorderCase{
		{"chunk keys: the first confirm of a chunk wins and the row keeps its blob key", testChunkConfirmFirstWins},
		{"chunk keys: a confirm with a key that is not an attempt of the chunk is ErrInvalid", testChunkConfirmBadKey},
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
		{"the shared prefix with no attempt", base},
		{"the shared prefix with a suffix", base + "x"},
		{"no key", ""},
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

func testChunkAttemptKey(t *testing.T, h RecorderHarness) {
	t.Helper()

	a := logCaseStep(t, h)
	mustAppend(t, h, a, 0, "one\n")
	mustAppend(t, h, a, 1, "two\n")
	mustEnd(t, h, a, stepPrepare, 0)

	log := logOf(t, h, a)

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, log.ID)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	if len(rec.ChunkKeys) != chunksPair {
		t.Fatalf("chunk keys = %q, want 2", rec.ChunkKeys)
	}

	var held []string

	for _, o := range blobsUnder(t, h, a.Claim.Fence.Namespace) {
		held = append(held, o.Key.String())
	}

	for seq, key := range rec.ChunkKeys {
		if _, err := job.ChunkBlobKey(a.Claim.Job, log.ID, seq, key); err != nil {
			t.Errorf("chunk %d key = %q, %v; want the key of an attempt", seq, key, err)
		}

		if !slices.ContainsFunc(held, func(k string) bool { return strings.HasSuffix(k, key) }) {
			t.Errorf("no blob for the key %q in %q", key, held)
		}
	}

	if got, err := readLog(t, h, log.ID); err != nil || string(got) != "one\ntwo\n" {
		t.Fatalf("OpenLog = %q, %v; want %q", got, err, "one\ntwo\n")
	}
}
