package workspace //nolint:testpackage // The meta reader is unexported; its test drives it directly.

import (
	"encoding/json"
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

// v1State is the view of a state that encoding/json (v1) reads. OpenTofu reads a
// state this way, so the reader must refuse or agree with it.
type v1State struct {
	Serial    int64           `json:"serial"`
	Lineage   string          `json:"lineage"`
	Outputs   json.RawMessage `json:"outputs"`
	Encrypted json.RawMessage `json:"encrypted_data"`
}

// TestReadStateMetaAgreesWithEncodingJSON runs documents through encoding/json
// (v1) and the reader. Each document is refused by the reader, or read to the
// same serial, lineage, outputs and encryption marker that v1 reads. A key that
// v1 folds to a member, a repeated member and trailing data are refused.
func TestReadStateMetaAgreesWithEncodingJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		doc    string
		refuse bool
	}{
		{name: "plain", doc: `{"serial":1,"lineage":"a","outputs":{"n":1}}`, refuse: false},
		{name: "upper-case key folds to serial", doc: `{"serial":1,"lineage":"a","SERIAL":7}`, refuse: true},
		{name: "long s folds to serial", doc: `{"ſerial":7,"serial":1,"lineage":"a"}`, refuse: true},
		{name: "repeated outputs", doc: `{"outputs":{"n":1},"serial":1,"lineage":"a","outputs":null}`, refuse: true},
		{name: "repeated serial", doc: `{"serial":1,"serial":7,"lineage":"a"}`, refuse: true},
		{name: "folded encryption marker", doc: `{"serial":1,"lineage":"a","ENCRYPTED_DATA":"c"}`, refuse: true},
		{name: "trailing object", doc: `{"serial":1,"lineage":"a"}{"serial":99}`, refuse: true},
		{name: "trailing junk", doc: `{"serial":1,"lineage":"a"} junk`, refuse: true},
		{name: "trailing whitespace", doc: "{\"serial\":1,\"lineage\":\"a\"}\n \t", refuse: false},
		{name: "unrelated key is skipped", doc: `{"resources":[],"serial":2,"lineage":"b"}`, refuse: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			meta, readErr := readStateMeta(strings.NewReader(tc.doc))
			if tc.refuse {
				if readErr == nil {
					t.Fatalf("the reader accepted %s, want a refusal", tc.doc)
				}

				return
			}

			if readErr != nil {
				t.Fatalf("the reader refused %s: %v", tc.doc, readErr)
			}

			var want v1State
			if err := json.Unmarshal([]byte(tc.doc), &want); err != nil {
				t.Fatalf("encoding/json refused a document the reader accepted: %v", err)
			}

			if meta.Serial != want.Serial || meta.Lineage != want.Lineage {
				t.Fatalf("reader reads serial %d lineage %q, encoding/json reads %d %q",
					meta.Serial, meta.Lineage, want.Serial, want.Lineage)
			}

			if meta.ClientEncrypted != (want.Encrypted != nil) {
				t.Fatalf("client-encryption marker differs from encoding/json")
			}
		})
	}
}

// TestReadStateMetaDepthMatchesEncodingJSON proves the depth limit is the one of
// encoding/json: a state nested to 9990 levels is read, and one nested to 10000 is
// refused. No state that OpenTofu accepts is refused for depth.
func TestReadStateMetaDepthMatchesEncodingJSON(t *testing.T) {
	t.Parallel()

	nested := func(depth int) string {
		return `{"x":` + strings.Repeat("[", depth) + strings.Repeat("]", depth) + `,"serial":1,"lineage":"l"}`
	}

	if _, err := readStateMeta(strings.NewReader(nested(9990))); err != nil {
		t.Fatalf("nesting of 9990 levels refused: %v", err)
	}

	if _, err := readStateMeta(strings.NewReader(nested(10000))); err == nil {
		t.Fatal("nesting of 10000 levels accepted, want a refusal")
	}
}
