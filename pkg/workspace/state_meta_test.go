package workspace //nolint:testpackage // The meta reader is unexported; its test drives it directly.

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// TestReadStateMetaInAnyKeyOrder proves the reader finds serial, lineage and
// outputs wherever they sit in the document (ADR 49, decision 8).
func TestReadStateMetaInAnyKeyOrder(t *testing.T) {
	t.Parallel()

	doc := `{"outputs":{"greeting":{"value":"hello","type":"string","sensitive":false}},` +
		`"resources":[{"type":"x"}],"lineage":"lin-1","version":4,"serial":7}`

	meta, err := readStateMeta(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("read state meta: %v", err)
	}

	if !meta.HasSerial || meta.Serial != 7 || !meta.HasLineage || meta.Lineage != "lin-1" {
		t.Fatalf(
			"serial %d (%t), lineage %q (%t), want 7 and lin-1",
			meta.Serial,
			meta.HasSerial,
			meta.Lineage,
			meta.HasLineage,
		)
	}

	if !strings.Contains(string(meta.Outputs), `"greeting"`) {
		t.Fatalf("outputs = %s, want the greeting output", meta.Outputs)
	}

	if meta.ClientEncrypted {
		t.Fatal("client encryption marked for a plain state")
	}
}

// TestReadStateMetaMarksClientEncryption proves an encrypted_data member marks
// the state client-encrypted, wherever it sits, and gives no outputs.
func TestReadStateMetaMarksClientEncryption(t *testing.T) {
	t.Parallel()

	meta, err := readStateMeta(strings.NewReader(`{"encrypted_data":"ciphertext","serial":1,"lineage":"l"}`))
	if err != nil {
		t.Fatalf("read encrypted state meta: %v", err)
	}

	if !meta.ClientEncrypted {
		t.Fatal("encrypted_data did not mark the state client-encrypted")
	}

	if meta.Outputs != nil {
		t.Fatalf("outputs = %s, want none for a client-encrypted state", meta.Outputs)
	}
}

// TestReadStateMetaRefusesBadDocuments proves a document that is not an object,
// or an outputs value above the limit, fails with the domain sentinels.
func TestReadStateMetaRefusesBadDocuments(t *testing.T) {
	t.Parallel()

	if _, err := readStateMeta(strings.NewReader(`[1,2]`)); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("array document: %v, want ErrInvalidState", err)
	}

	big := `{"outputs":"` + strings.Repeat("x", maxOutputsBytes+1) + `"}`
	if _, err := readStateMeta(strings.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized outputs: %v, want ErrTooLarge", err)
	}
}

// TestMetaStreamReadsWhileTheStoreReads proves the bytes that the store reads
// also reach the decoder, and that finish returns the meta of the same bytes.
func TestMetaStreamReadsWhileTheStoreReads(t *testing.T) {
	t.Parallel()

	doc := `{"serial":3,"lineage":"lin-3","outputs":{"a":{"value":1}}}`

	reader, ms := newMetaStream(strings.NewReader(doc))

	copied, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read through the stream: %v", err)
	}

	if string(copied) != doc {
		t.Fatalf("store read %q, want the document", copied)
	}

	meta, err := ms.finish(nil)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}

	if meta.Serial != 3 || meta.Lineage != "lin-3" {
		t.Fatalf("meta = %+v, want serial 3 and lineage lin-3", meta)
	}
}

// TestMetaStreamFailsWithTheStoreError proves a failed store write ends the
// stream with that error, and the decoder does not wait forever.
// errStoreRefused is the store failure that a meta stream must return.
var errStoreRefused = errors.New("store refused")

func TestMetaStreamFailsWithTheStoreError(t *testing.T) {
	t.Parallel()

	_, ms := newMetaStream(strings.NewReader(`{"serial":1`))

	storeErr := errStoreRefused

	if _, err := ms.finish(storeErr); !errors.Is(err, storeErr) {
		t.Fatalf("finish with a store error: %v, want the store error", err)
	}
}

// fillReader yields prefix, then n filler bytes, then suffix. It holds no more
// than one of its parts, so a test can stream a large document it never builds.
type fillReader struct {
	prefix string
	suffix string
	n      int
}

// Read yields the next bytes of the document.
func (r *fillReader) Read(p []byte) (int, error) {
	if r.prefix != "" {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]

		return n, nil
	}

	if r.n > 0 {
		k := min(len(p), r.n)
		for i := range k {
			p[i] = 'x'
		}

		r.n -= k

		return k, nil
	}

	if r.suffix != "" {
		n := copy(p, r.suffix)
		r.suffix = r.suffix[n:]

		return n, nil
	}

	return 0, io.EOF
}

// totalAllocated returns the bytes allocated so far by the process.
func totalAllocated() uint64 {
	var m runtime.MemStats

	runtime.ReadMemStats(&m)

	return m.TotalAlloc
}

// TestReadStateMetaBoundsAllocations proves the meta reader holds no large value
// it does not keep. A 64 MiB member that the domain skips costs almost nothing:
// the reader never buffers a skipped token. An outputs value over its limit is
// refused once the limit is reached, so the reader allocates about the limit
// and not the size of the document. The test runs alone, because it reads the
// process-wide allocation count.
//
//nolint:paralleltest // It reads the process-wide allocation count, so it runs with no other test.
func TestReadStateMetaBoundsAllocations(t *testing.T) {
	const size = 64 << 20

	t.Run("a skipped large string", func(t *testing.T) {
		src := &fillReader{prefix: `{"skipped":"`, n: size, suffix: `","serial":3,"lineage":"l"}`}

		before := totalAllocated()

		meta, err := readStateMeta(src)
		if err != nil {
			t.Fatalf("read state with a large skipped string: %v", err)
		}

		if grew := totalAllocated() - before; grew > 4<<20 {
			t.Fatalf("skipping a 64 MiB string allocated %d bytes, want under 4 MiB", grew)
		}

		if !meta.HasSerial || meta.Serial != 3 {
			t.Fatalf("serial = %d (%t), want 3", meta.Serial, meta.HasSerial)
		}
	})

	t.Run(
		"an outputs value over the limit",
		func(t *testing.T) {
			src := &fillReader{prefix: `{"outputs":"`, n: size, suffix: `","serial":1,"lineage":"l"}`}

			before := totalAllocated()

			_, err := readStateMeta(src)
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("outputs of 64 MiB: err = %v, want ErrTooLarge", err)
			}

			if grew := totalAllocated() - before; grew > 2*maxOutputsBytes+(4<<20) {
				t.Fatalf(
					"refusing 64 MiB of outputs allocated %d bytes, want about the %d-byte limit",
					grew,
					maxOutputsBytes,
				)
			}
		},
	)
}
