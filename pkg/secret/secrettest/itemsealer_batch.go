package secrettest

import (
	"bytes"
	"errors"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/secret"
)

// batchParts returns n parts with the bindings b0, b1, ... and the data
// plain-0, plain-1, ...
func batchParts(n int) []secret.ContentPart {
	parts := make([]secret.ContentPart, 0, n)
	for i := range n {
		parts = append(parts, secret.ContentPart{
			Binding: []byte("b" + strconv.Itoa(i)), Data: []byte("plain-" + strconv.Itoa(i)),
		})
	}

	return parts
}

// sealedParts returns parts with the Data replaced by sealed.
func sealedParts(parts []secret.ContentPart, sealed [][]byte) []secret.ContentPart {
	out := make([]secret.ContentPart, 0, len(parts))
	for i := range parts {
		out = append(out, secret.ContentPart{Binding: parts[i].Binding, Data: sealed[i]})
	}

	return out
}

// sealBatch seals parts for ref and checks that the batch read the key blob
// one time and that no part holds its plain text.
func sealBatch(t *testing.T, r *SealerRig, ref secret.ItemRef, parts []secret.ContentPart) [][]byte {
	t.Helper()

	reads := r.Counts.Reads()

	sealed, err := r.Sealer.SealContents(t.Context(), ref, parts)
	if err != nil || len(sealed) != len(parts) {
		t.Fatalf("seal batch = %d parts, %v; want %d", len(sealed), err, len(parts))
	}

	if got := r.Counts.Reads() - reads; got != 1 {
		t.Errorf("seal batch read the blob store %d times, want 1", got)
	}

	for i := range parts {
		if bytes.Contains(sealed[i], parts[i].Data) {
			t.Fatalf("sealed part %d holds its plain text", i)
		}
	}

	return sealed
}

// openBatch opens the sealed parts for ref, checks that the batch read the
// key blob one time, and compares each part with parts.
func openBatch(t *testing.T, r *SealerRig, ref secret.ItemRef, parts []secret.ContentPart, sealed [][]byte) {
	t.Helper()

	reads := r.Counts.Reads()

	opened, err := r.Sealer.OpenContents(t.Context(), ref, sealedParts(parts, sealed))
	if err != nil || len(opened) != len(parts) {
		t.Fatalf("open batch = %d parts, %v; want %d", len(opened), err, len(parts))
	}

	if got := r.Counts.Reads() - reads; got != 1 {
		t.Errorf("open batch read the blob store %d times, want 1", got)
	}

	for i := range parts {
		if !bytes.Equal(opened[i], parts[i].Data) {
			t.Fatalf("open part %d = %q, want %q", i, opened[i], parts[i].Data)
		}
	}
}

// testSealerBatchRoundTrip checks that a batch seals and opens with one read
// of the key blob each, and that a part of a batch opens with OpenContent
// and the reverse.
func testSealerBatchRoundTrip(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindState, "u1")
	mustNewKey(t, r, ref)

	parts := batchParts(50)
	sealed := sealBatch(t, r, ref, parts)
	openBatch(t, r, ref, parts, sealed)

	for i := range parts {
		single, openErr := openSealed(t.Context(), r.Sealer, ref, parts[i].Binding, sealed[i])
		if openErr != nil || !bytes.Equal(single, parts[i].Data) {
			t.Fatalf("open part %d alone = %q, %v; want %q", i, single, openErr, parts[i].Data)
		}
	}

	alone := sealItem(t, r, ref, []byte("alone"), []byte("one part"))

	got, err := r.Sealer.OpenContents(t.Context(), ref, []secret.ContentPart{{Binding: []byte("alone"), Data: alone}})
	if err != nil || len(got) != 1 || string(got[0]) != "one part" {
		t.Fatalf("open batch of a part that SealContent sealed = %q, %v", got, err)
	}

	if empty, err := r.Sealer.SealContents(t.Context(), ref, nil); err != nil || len(empty) != 0 {
		t.Fatalf("seal of an empty batch = %d parts, %v; want none", len(empty), err)
	}
}

// testSealerBatchMoved checks that a batch with a part moved to another
// binding, or opened as another item, fails as a whole.
func testSealerBatchMoved(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindState, "u1")
	other := sealerRef(t, "unit-two", secret.ItemKindState, "u1")

	mustNewKey(t, r, ref)
	mustNewKey(t, r, other)

	parts := batchParts(3)
	sealed := sealBatch(t, r, ref, parts)

	moved := sealedParts(parts, sealed)
	moved[0].Data, moved[1].Data = moved[1].Data, moved[0].Data

	if out, err := r.Sealer.OpenContents(t.Context(), ref, moved); !errors.Is(err, secret.ErrDecrypt) || out != nil {
		t.Errorf("open of moved parts = %d parts, %v; want none and ErrDecrypt", len(out), err)
	}

	out, err := r.Sealer.OpenContents(t.Context(), other, sealedParts(parts, sealed))
	if !errors.Is(err, secret.ErrDecrypt) || out != nil {
		t.Errorf("open as another item = %d parts, %v; want none and ErrDecrypt", len(out), err)
	}
}

// testSealerBatchRefusals checks that a batch with a bad binding, no key or a
// shredded key fails, and that a shredded key reads no blob.
func testSealerBatchRefusals(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindState, "u1")
	missing := sealerRef(t, "unit-one", secret.ItemKindState, "u-missing")

	mustNewKey(t, r, ref)

	parts := batchParts(3)
	sealed := sealBatch(t, r, ref, parts)

	bad := []secret.ContentPart{{Binding: nil, Data: []byte("x")}}
	if _, err := r.Sealer.SealContents(t.Context(), ref, bad); !errors.Is(err, secret.ErrInvalidBinding) {
		t.Errorf("seal with an empty binding err = %v, want ErrInvalidBinding", err)
	}

	if _, err := r.Sealer.OpenContents(t.Context(), ref, bad); !errors.Is(err, secret.ErrInvalidBinding) {
		t.Errorf("open with an empty binding err = %v, want ErrInvalidBinding", err)
	}

	if _, err := r.Sealer.SealContents(t.Context(), missing, parts); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("seal with no key err = %v, want ErrKeyNotFound", err)
	}

	if _, err := r.Index.ShredItemKey(t.Context(), ref, itemKeyTime(), r.fence()); err != nil {
		t.Fatalf("shred: %v", err)
	}

	reads := r.Counts.Reads()

	if _, err := r.Sealer.SealContents(t.Context(), ref, parts); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("seal batch after a shred err = %v, want ErrShredded", err)
	}

	_, err := r.Sealer.OpenContents(t.Context(), ref, sealedParts(parts, sealed))
	if !errors.Is(err, secret.ErrShredded) {
		t.Errorf("open batch after a shred err = %v, want ErrShredded", err)
	}

	if r.Counts.Reads() != reads {
		t.Errorf("a shredded key reached the blob store: %d reads", r.Counts.Reads()-reads)
	}
}
