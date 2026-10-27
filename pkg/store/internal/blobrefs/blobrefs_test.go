package blobrefs_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/internal/blobrefs"
)

const (
	nsID    = "ns-aaaaaaaaaaaaaaaaaaaa"
	jobID   = "job-bbbbbbbbbbbbbbbbbbbb"
	planID  = "pln-cccccccccccccccccccc"
	logID   = "jlv-dddddddddddddddddddd"
	hashA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	md5Hex  = "0123456789abcdef0123456789abcdef"
	noTime  = "0001-01-01T00:00:00Z"
	display = "plan-display"
	apply   = "plan-apply"
)

// md5Bytes is the value of md5Hex.
func md5Bytes(t *testing.T) []byte {
	t.Helper()

	b, err := hex.DecodeString(md5Hex)
	if err != nil {
		t.Fatalf("decode md5: %v", err)
	}

	return b
}

// planBody returns the JSON body of a plan row with the given parts and
// redaction time.
func planBody(id, parts, redacted string) string {
	return fmt.Sprintf(
		`{"id":%q,"itemKey":"","stateVersion":"","parts":[%s],`+
			`"createdAt":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z","redactedAt":%q}`,
		id, parts, redacted,
	)
}

// part returns the JSON of one plan part with the given stored MD5 and size.
func part(kind, md5, size string) string {
	return fmt.Sprintf(`{"kind":%q,"size":5,"sha256":%q,"secret":false,"storedSize":%s,"storedMd5":%q}`,
		kind, hashA, size, md5)
}

// chunkRow returns a chunk row of the standard namespace, job and log.
func chunkRow(seq int64, confirmed, shredded bool, size int64, md5 []byte) blobrefs.ChunkRow {
	return blobrefs.ChunkRow{
		NamespaceID: nsID, JobID: jobID, LogID: logID, Seq: seq, Confirmed: confirmed, Shredded: shredded,
		StoredSize: size, StoredMD5: md5,
	}
}

// planRow returns a plan row of the standard namespace and job.
func planRow(id, body string, shredded bool) blobrefs.PlanRow {
	return blobrefs.PlanRow{NamespaceID: nsID, JobID: jobID, ID: id, Body: body, Shredded: shredded}
}

// collected is what a case expects from the collector: the keys of the
// references and the keys listed as not copied.
type collected struct {
	keys       []string
	unverified []string
}

func TestCollectorChunk(t *testing.T) {
	t.Parallel()

	good := md5Bytes(t)
	prefix := "namespaces/" + nsID + "/job/" + jobID + "/log/" + logID + "/"

	tests := []struct {
		name string
		row  blobrefs.ChunkRow
		want collected
	}{
		{
			name: "a confirmed chunk with stored values is copied",
			row:  chunkRow(3, true, false, 9, good),
			want: collected{keys: []string{prefix + "00000003"}, unverified: nil},
		},
		{
			name: "a confirmed chunk without values is listed as not copied",
			row:  chunkRow(0, true, false, 0, nil),
			want: collected{keys: nil, unverified: []string{prefix + "00000000"}},
		},
		{
			name: "a confirmed chunk with a short MD5 is listed as not copied",
			row:  chunkRow(1, true, false, 4, good[:8]),
			want: collected{keys: nil, unverified: []string{prefix + "00000001"}},
		},
		{
			name: "a reserved chunk has no blob",
			row:  chunkRow(0, false, false, 9, good),
			want: collected{keys: nil, unverified: nil},
		},
		{
			name: "a chunk of a shredded log version has no blob",
			row:  chunkRow(0, true, true, 9, good),
			want: collected{keys: nil, unverified: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			if err := c.Chunk(tt.row); err != nil {
				t.Fatalf("Chunk = %v", err)
			}

			assertCollected(t, &c, tt.want)
		})
	}
}

// TestCollectorChunkHostile sends hostile IDs to the collector, as if every
// other layer had failed: no key leaves the namespace.
func TestCollectorChunkHostile(t *testing.T) {
	t.Parallel()

	good := md5Bytes(t)

	withNamespace := func(ns string) blobrefs.ChunkRow {
		row := chunkRow(0, true, false, 1, good)
		row.NamespaceID = ns

		return row
	}

	withJob := func(job string) blobrefs.ChunkRow {
		row := chunkRow(0, true, false, 1, good)
		row.JobID = job

		return row
	}

	withLog := func(log string) blobrefs.ChunkRow {
		row := chunkRow(0, true, false, 1, good)
		row.LogID = log

		return row
	}

	tests := []struct {
		name string
		row  blobrefs.ChunkRow
	}{
		{"a namespace ID with a slash", withNamespace("ns-a/../b")},
		{"a job ID with a dot segment", withJob("../other")},
		{"a log ID with an upper case letter", withLog("JLV-x")},
		{"an empty log ID", withLog("")},
		{"a negative chunk number", chunkRow(-1, true, false, 1, good)},
		{"a chunk number above the limit", chunkRow(1<<20, true, false, 1, good)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			err := c.Chunk(tt.row)
			if !errors.Is(err, blobrefs.ErrRow) || !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("Chunk = %v, want ErrRow that matches ErrCorrupt", err)
			}

			assertCollected(t, &c, collected{keys: nil, unverified: nil})
		})
	}
}

func TestCollectorPlan(t *testing.T) {
	t.Parallel()

	prefix := "namespaces/" + nsID + "/job/" + jobID + "/plan/" + planID + "/"
	both := part(display, md5Hex, "20") + "," + part(apply, md5Hex, "30")

	tests := []struct {
		name string
		row  blobrefs.PlanRow
		want collected
	}{
		{
			name: "both parts with stored values are copied",
			row:  planRow(planID, planBody(planID, both, noTime), false),
			want: collected{keys: []string{prefix + display, prefix + apply}, unverified: nil},
		},
		{
			name: "a part without values is listed as not copied",
			row: planRow(
				planID,
				planBody(planID, part(display, md5Hex, "20")+","+part(apply, "", "0"), noTime),
				false,
			),
			want: collected{keys: []string{prefix + display}, unverified: []string{prefix + apply}},
		},
		{
			name: "a reserved plan has no part",
			row:  planRow(planID, planBody(planID, "", noTime), false),
			want: collected{keys: nil, unverified: nil},
		},
		{
			name: "a redacted plan has no blob",
			row:  planRow(planID, planBody(planID, part(display, md5Hex, "20"), "2026-02-01T00:00:00Z"), false),
			want: collected{keys: nil, unverified: nil},
		},
		{
			name: "a plan of a shredded key has no blob",
			row:  planRow(planID, planBody(planID, part(display, md5Hex, "20"), noTime), true),
			want: collected{keys: nil, unverified: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			if err := c.Plan(tt.row); err != nil {
				t.Fatalf("Plan = %v", err)
			}

			assertCollected(t, &c, tt.want)
		})
	}
}

// TestCollectorPlanHostile sends hostile rows to the collector, as if every
// other layer had failed: the collector refuses the row, or it gives no key
// outside the directory of the plan.
func TestCollectorPlanHostile(t *testing.T) {
	t.Parallel()

	okPart := part(display, md5Hex, "20")

	withJob := planRow(planID, planBody(planID, okPart, noTime), false)
	withJob.JobID = "../x"

	tests := []struct {
		name string
		row  blobrefs.PlanRow
	}{
		{"a job ID that leaves the directory", withJob},
		{"a plan ID with a slash", planRow("pln-a/b", planBody("pln-a/b", okPart, noTime), false)},
		{"a body that is no JSON", planRow(planID, "not json", false)},
		{"a body of another plan", planRow(planID, planBody("pln-other", okPart, noTime), false)},
		{
			"a part kind that is no plan output",
			planRow(planID, planBody(planID, part("../../etc", md5Hex, "20"), noTime), false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			err := c.Plan(tt.row)
			if !errors.Is(err, blobrefs.ErrRow) || !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("Plan = %v, want ErrRow that matches ErrCorrupt", err)
			}

			assertCollected(t, &c, collected{keys: nil, unverified: nil})
		})
	}
}

// TestCollectorPlanKeysStayInTheDirectory checks that no key of a plan holds a
// dot segment, whatever the kind says.
func TestCollectorPlanKeysStayInTheDirectory(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	if err := c.Plan(planRow(planID, planBody(planID, part(display, md5Hex, "20"), noTime), false)); err != nil {
		t.Fatalf("Plan = %v", err)
	}

	for _, ref := range c.Refs() {
		if !strings.HasPrefix(ref.Key, "namespaces/"+nsID+"/job/"+jobID+"/plan/") || strings.Contains(ref.Key, "..") {
			t.Errorf("key %q leaves the directory of the plan", ref.Key)
		}
	}
}

// release returns a module release with one archive and the given digests.
func release(files []registry.StoredFile) registry.Release {
	return registry.Release{
		PublishedAt: time.Unix(1, 0).UTC(),
		Address:     registry.Address{Namespace: "acme", Kind: registry.KindModule, Name: "net", System: "aws"},
		Version:     "1.0.0", SumsFile: "", SumsSHA256: "", SignatureFile: "", SignatureSHA256: "",
		KeyFingerprint: "", Protocols: nil, PublishedBy: "al",
		Archives: []registry.Archive{{Platform: "", FileName: "net.zip", SHA256: hashA, Size: 5}},
		Files:    files,
	}
}

func TestCollectorRelease(t *testing.T) {
	t.Parallel()

	key := "namespaces/" + nsID + "/registry/sha256/" + hashA
	stored := registry.StoredFile{SHA256: hashA, Size: 5, MD5: md5Hex}

	tests := []struct {
		name  string
		files []registry.StoredFile
		times int
		want  collected
	}{
		{
			name: "a file with stored values is copied", files: []registry.StoredFile{stored}, times: 1,
			want: collected{keys: []string{key}, unverified: nil},
		},
		{
			name: "a release without stored values is listed as not copied", files: nil, times: 1,
			want: collected{keys: nil, unverified: []string{key}},
		},
		{
			name: "a hash that two releases share is added once", files: []registry.StoredFile{stored}, times: 2,
			want: collected{keys: []string{key}, unverified: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c blobrefs.Collector

			for range tt.times {
				if err := c.Release(blobrefs.ReleaseRow{NamespaceID: nsID, Release: release(tt.files)}); err != nil {
					t.Fatalf("Release = %v", err)
				}
			}

			assertCollected(t, &c, tt.want)
		})
	}
}

// TestCollectorReleaseHostile sends a namespace ID that leaves the directory.
func TestCollectorReleaseHostile(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	err := c.Release(blobrefs.ReleaseRow{NamespaceID: "ns-a/../x", Release: release(nil)})
	if !errors.Is(err, blobrefs.ErrRow) {
		t.Fatalf("Release = %v, want ErrRow", err)
	}

	assertCollected(t, &c, collected{keys: nil, unverified: nil})
}

// assertCollected checks the keys of the references and the keys listed as not
// copied.
func assertCollected(t *testing.T, c *blobrefs.Collector, want collected) {
	t.Helper()

	keys := make([]string, 0, len(c.Refs()))
	for _, ref := range c.Refs() {
		keys = append(keys, ref.Key)
	}

	slices.Sort(keys)

	if wantKeys := slices.Sorted(slices.Values(want.keys)); !slices.Equal(keys, wantKeys) {
		t.Errorf("reference keys = %v, want %v", keys, wantKeys)
	}

	if wantUnver := slices.Sorted(slices.Values(want.unverified)); !slices.Equal(c.Unverified(), wantUnver) {
		t.Errorf("unverified keys = %v, want %v", c.Unverified(), wantUnver)
	}
}

// manyChunks is the number of chunks of the scale test and the benchmark.
const manyChunks = 200_000

// collectChunks adds n confirmed chunks of one log, each with stored values.
func collectChunks(tb testing.TB, n int) *blobrefs.Collector {
	tb.Helper()

	sum, err := hex.DecodeString(md5Hex)
	if err != nil {
		tb.Fatalf("decode md5: %v", err)
	}

	c := &blobrefs.Collector{}

	for i := range n {
		// Several logs, so that the keys differ in the log and in the number.
		row := chunkRow(int64(i%60_000), true, false, 9, sum)
		row.LogID = fmt.Sprintf("jlv-%020d", i/60_000)

		if addErr := c.Chunk(row); addErr != nil {
			tb.Fatalf("Chunk %d = %v", i, addErr)
		}
	}

	return c
}

// scaleRuns is the number of runs for each size in TestCollectorScales. The
// test keeps the fastest run of each size, so that a pause from load on the
// machine does not change the result.
const scaleRuns = 3

// scaleRatioLimit is the largest accepted ratio between the time for the large
// input and the time for the small input. The large input is four times the
// small input: linear work gives a ratio near 4, and quadratic work gives a
// ratio near 16.
const scaleRatioLimit = 10

// fastestCollect returns the shortest time of scaleRuns runs that add n chunks.
func fastestCollect(t *testing.T, n int) time.Duration {
	t.Helper()

	var fastest time.Duration

	for run := range scaleRuns {
		start := time.Now()
		c := collectChunks(t, n)
		elapsed := time.Since(start)

		if got := len(c.Refs()); got != n {
			t.Fatalf("references = %d, want %d", got, n)
		}

		if run == 0 || elapsed < fastest {
			fastest = elapsed
		}
	}

	return fastest
}

// TestCollectorScales proves that the collector adds many chunks in time that
// is linear in their number: with a scan of the earlier keys for each chunk, 200
// thousand chunks take minutes. The test compares two input sizes in the same
// run and checks the ratio of their times, not an absolute time, so that a slow
// or loaded machine does not change the result.
func TestCollectorScales(t *testing.T) {
	t.Parallel()

	small := fastestCollect(t, manyChunks/4)
	large := fastestCollect(t, manyChunks)

	// A zero small time cannot happen for this many chunks; the guard keeps the
	// division safe on a coarse clock.
	small = max(small, time.Microsecond)

	if ratio := float64(large) / float64(small); ratio > scaleRatioLimit {
		t.Errorf("collecting %d chunks took %v and %d chunks took %v (ratio %.1f), want a ratio of at most %d for linear work",
			manyChunks/4, small, manyChunks, large, ratio, scaleRatioLimit)
	}
}

func BenchmarkCollectorChunks(b *testing.B) {
	for range b.N {
		collectChunks(b, manyChunks)
	}
}

// TestCollectorKeepsUnverifiedUnique checks that a key listed twice appears
// once, in sorted order.
func TestCollectorKeepsUnverifiedUnique(t *testing.T) {
	t.Parallel()

	var c blobrefs.Collector

	for _, seq := range []int64{2, 1, 2, 1} {
		if err := c.Chunk(chunkRow(seq, true, false, 0, nil)); err != nil {
			t.Fatalf("Chunk = %v", err)
		}
	}

	prefix := "namespaces/" + nsID + "/job/" + jobID + "/log/" + logID + "/"
	assertCollected(t, &c, collected{keys: nil, unverified: []string{prefix + "00000001", prefix + "00000002"}})
}
