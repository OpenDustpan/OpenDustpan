package stack_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"strconv"
	"strings"
	"testing"
)

// gzipBenchBody builds a deployment-shaped document of about size bytes: one
// stack entry plus padded resources, so the compressor meets realistic JSON.
func gzipBenchBody(b *testing.B, size int) []byte {
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

// gzipBenchSize is the document size of the gzip benchmarks: the small load
// harness checkpoint, so the numbers track the nightly run.
const gzipBenchSize = 262144

// BenchmarkGzipBestSpeedCompress compresses one small checkpoint per
// iteration at gzip.BestSpeed, the level the checkpoint save path uses.
func BenchmarkGzipBestSpeedCompress(b *testing.B) {
	body := gzipBenchBody(b, gzipBenchSize)

	b.SetBytes(int64(len(body)))
	b.ResetTimer()

	for b.Loop() {
		var zipped bytes.Buffer

		w, err := gzip.NewWriterLevel(&zipped, gzip.BestSpeed)
		if err != nil {
			b.Fatalf("new writer: %v", err)
		}

		if _, err = w.Write(body); err != nil {
			b.Fatalf("write gzip: %v", err)
		}

		if err = w.Close(); err != nil {
			b.Fatalf("close gzip: %v", err)
		}
	}
}

// BenchmarkGzipBestSpeedDecompress expands one small checkpoint per
// iteration: the read path of the stored blob the save path writes.
func BenchmarkGzipBestSpeedDecompress(b *testing.B) {
	body := gzipBenchBody(b, gzipBenchSize)

	var zipped bytes.Buffer

	w, err := gzip.NewWriterLevel(&zipped, gzip.BestSpeed)
	if err != nil {
		b.Fatalf("new writer: %v", err)
	}

	if _, err = w.Write(body); err != nil {
		b.Fatalf("write gzip: %v", err)
	}

	if err = w.Close(); err != nil {
		b.Fatalf("close gzip: %v", err)
	}

	stored := zipped.Bytes()

	b.SetBytes(int64(len(stored)))
	b.ResetTimer()

	for b.Loop() {
		gz, err := gzip.NewReader(bytes.NewReader(stored))
		if err != nil {
			b.Fatalf("new reader: %v", err)
		}

		// The benchmark expands only the fixed body it compressed above.
		_, err = io.Copy(io.Discard, gz) //nolint:gosec // No foreign input reaches the reader.
		if err != nil {
			b.Fatalf("read gzip: %v", err)
		}

		if err = gz.Close(); err != nil {
			b.Fatalf("close reader: %v", err)
		}
	}
}
