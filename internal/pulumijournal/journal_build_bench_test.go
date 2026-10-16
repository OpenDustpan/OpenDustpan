package pulumijournal_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// benchState returns a resource state with a 64-byte pad, so the build meets
// realistic JSON.
func benchState(i int) string {
	return fmt.Sprintf(
		`{"urn":"urn:pulumi:dev::web::pkg:index:Type::res-%06d","custom":true,"type":"pkg:index:Type","id":"res-%06d","inputs":{"value":%q},"outputs":{}}`,
		i,
		i,
		strings.Repeat("x", 64),
	)
}

// benchStateChange returns the changed form of benchState.
func benchStateChange(i int) string {
	return fmt.Sprintf(
		`{"urn":"urn:pulumi:dev::web::pkg:index:Type::res-%06d","custom":true,"type":"pkg:index:Type","id":"res-%06d","inputs":{"value":%q},"outputs":{"value":2}}`,
		i,
		i,
		strings.Repeat("y", 64),
	)
}

// benchBase returns a base deployment with n resources.
func benchBase(n int) string {
	var sb strings.Builder
	sb.WriteString(`{"resources":[`)
	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(benchState(i))
	}
	sb.WriteString(`]}`)
	return sb.String()
}

// benchCreateEntries returns a batch body that creates n resources: a begin
// and a success entry for each one.
func benchCreateEntries(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		seq := int64(2*i + 1)
		op := int64(i + 1)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null},`,
			seq,
			op,
		)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":1,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null,"state":%s}`,
			seq+1,
			op,
			benchState(i),
		)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// benchChangeEntries returns a batch body that changes n resources of the
// base: a begin and a success entry with removeOld for each one.
func benchChangeEntries(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		seq := int64(2*i + 1)
		op := int64(i + 1)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":0,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null},`,
			seq,
			op,
		)
		fmt.Fprintf(
			&b,
			`{"version":1,"kind":1,"sequenceID":%d,"operationID":%d,"removeOld":%d,"removeNew":null,"state":%s}`,
			seq+1,
			op,
			i,
			benchStateChange(i),
		)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// benchBuild runs the replayer on base and entries and reports the heap after
// the build, with the entry count, the new-state bytes and the added links
// measured from the entries. The added links are the success entries that
// carry a state, which is what the link-index bound counts.
func benchBuild(b *testing.B, base string, entries []byte) {
	b.Helper()

	now := func() time.Time { return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC) }
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(base)), nil
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the benchmark collects first.
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	var out bytes.Buffer
	r := pulumijournal.NewReplayer(now, "3.268.0")
	if err := r.Replay(context.Background(), open, bytes.NewReader(entries), &out); err != nil {
		b.Fatalf("replay: %v", err)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	count, stateBytes, added := countBenchEntries(b, entries)

	b.ReportMetric(float64(after.HeapAlloc), "peak-heap-bytes")
	b.ReportMetric(float64(len(entries)), "batch-bytes")
	b.ReportMetric(float64(out.Len()), "out-bytes")
	b.ReportMetric(float64(count), "entries")
	b.ReportMetric(float64(stateBytes), "new-state-bytes")
	b.ReportMetric(float64(added), "link-index")
}

// countBenchEntries decodes entries and returns its entry count, its new-state
// bytes counted as the replay counts them, and its added links: the success
// entries that carry a state.
//
//nolint:nonamedreturns // The names keep the entry count, the new-state bytes and the added links in order at callers.
func countBenchEntries(b *testing.B, entries []byte) (count, stateBytes, added int) {
	b.Helper()
	err := pulumijournal.DecodeEntries(bytes.NewReader(entries), 2, func(entry pulumijournal.JournalEntry) error {
		count++
		stateBytes += pulumijournal.EntryStateBytes(entry)
		if entry.Kind == pulumijournal.JournalEntryKindSuccess && len(entry.State) > 0 &&
			string(entry.State) != "null" {
			added++
		}
		return nil
	})
	if err != nil {
		b.Fatalf("decode bench entries: %v", err)
	}
	return count, stateBytes, added
}

// BenchmarkJournalBuildCreate3000 builds a create of 3,000 resources and
// reports its peak heap.
func BenchmarkJournalBuildCreate3000(b *testing.B) {
	entries := benchCreateEntries(3000)
	b.SetBytes(int64(len(entries)))
	b.ResetTimer()
	for b.Loop() {
		benchBuild(b, `{"resources":[]}`, entries)
	}
}

// BenchmarkJournalBuildChange3000 builds a change of 3,000 resources and
// reports its peak heap.
func BenchmarkJournalBuildChange3000(b *testing.B) {
	base := benchBase(3000)
	entries := benchChangeEntries(3000)
	b.SetBytes(int64(len(entries)))
	b.ResetTimer()
	for b.Loop() {
		benchBuild(b, base, entries)
	}
}

// BenchmarkJournalBuildCreate10000 builds a create of 10,000 resources and
// reports its peak heap.
func BenchmarkJournalBuildCreate10000(b *testing.B) {
	entries := benchCreateEntries(10000)
	b.SetBytes(int64(len(entries)))
	b.ResetTimer()
	for b.Loop() {
		benchBuild(b, `{"resources":[]}`, entries)
	}
}

// BenchmarkJournalBuildChange10000 builds a change of 10,000 resources and
// reports its peak heap.
func BenchmarkJournalBuildChange10000(b *testing.B) {
	base := benchBase(10000)
	entries := benchChangeEntries(10000)
	b.SetBytes(int64(len(entries)))
	b.ResetTimer()
	for b.Loop() {
		benchBuild(b, base, entries)
	}
}

// BenchmarkJournalBuildChange10in10000 builds a change of 10 resources on a
// base of 10,000 resources and reports its peak heap. Its memory does not
// grow with the base: the build streams the base and keeps only the 10 added
// resources.
func BenchmarkJournalBuildChange10in10000(b *testing.B) {
	base := benchBase(10000)
	entries := benchChangeEntries(10)
	b.SetBytes(int64(len(entries)))
	b.ResetTimer()
	for b.Loop() {
		benchBuild(b, base, entries)
	}
}
