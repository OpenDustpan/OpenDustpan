package stack

// White-box benchmark: applyCheckpointDelta has no exported caller without a
// store, so the benchmark lives in the internal test file, as the lint rules
// allow for _internal_test.go.

import (
	"strconv"
	"strings"
	"testing"
)

// deltaApplyBenchBody builds a deployment-shaped document of about size
// bytes for the delta apply benchmark.
func deltaApplyBenchBody(b *testing.B, size int) []byte {
	b.Helper()

	const padSize = 512

	var sb strings.Builder

	sb.WriteString(`{"manifest":{"version":3},"resources":[`)
	sb.WriteString(`{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack","outputs":{}}`)

	pad := strings.Repeat("x", padSize)

	for n := 0; sb.Len() < size; n++ {
		sb.WriteByte(',')
		sb.WriteString(`{"urn":"urn:pulumi:dev::web::aws:s3/bucket:Bucket::bucket-`)
		sb.WriteString(strconv.Itoa(n))
		sb.WriteString(`","type":"aws:s3/bucket:Bucket","custom":true,"inputs":{"pad":"`)
		sb.WriteString(pad)
		sb.WriteString(`"},"outputs":{}}`)
	}

	sb.WriteString(`]}`)

	return []byte(sb.String())
}

// benchApplyOne runs one single-edit delta apply per iteration: the edit
// replaces 64 bytes at three quarters of base, as the load harness sends.
func benchApplyOne(b *testing.B, size int) {
	b.Helper()

	const editLen = 64

	base := deltaApplyBenchBody(b, size)
	at := len(base) * 3 / 4

	delta := []byte(`[{"span":{"start":{"offset":` + strconv.Itoa(at) +
		`},"end":{"offset":` + strconv.Itoa(at+editLen) +
		`}},"newText":` + strconv.Quote(strings.Repeat("y", editLen)) + `}]`)

	limit := len(base) * 2

	b.SetBytes(int64(len(base)))
	b.ResetTimer()

	for b.Loop() {
		if _, err := applyCheckpointDelta(base, delta, limit); err != nil {
			b.Fatalf("apply delta: %v", err)
		}
	}
}

// BenchmarkDeltaApply256K applies one single-edit delta to a 256 KB
// checkpoint per iteration: the small load harness checkpoint.
func BenchmarkDeltaApply256K(b *testing.B) {
	benchApplyOne(b, 262144)
}

// BenchmarkDeltaApply1M applies one single-edit delta to a 1 MB checkpoint
// per iteration: the middle load harness checkpoint.
func BenchmarkDeltaApply1M(b *testing.B) {
	benchApplyOne(b, 1048576)
}
