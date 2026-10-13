package workspace //nolint:testpackage // The meta reader is unexported; its test drives it directly.

import (
	"errors"
	"io"
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
