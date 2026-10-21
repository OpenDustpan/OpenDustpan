package secrettest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
	"opendustpan.org/opendustpan/pkg/secret"
)

// SealerRig is everything RunItemSealer needs: the sealer under test, a
// sealer over the same records and blobs with another key-encryption key, and
// the stores that the cases read and damage.
type SealerRig struct {
	// Sealer is the sealer under test.
	Sealer secret.ItemSealer
	// Foreign has the same index and blobs as Sealer and another
	// key-encryption key.
	Foreign secret.ItemSealer
	// Unkeyed has the same index and blobs as Sealer and no key-encryption
	// key.
	Unkeyed secret.ItemSealer
	Index   secret.ItemKeyIndex
	Blobs   blob.Store
	// Acme and Other are two namespaces of the store.
	Acme, Other namespace.Namespace
}

// SealerFactory returns a fresh rig for one subtest.
type SealerFactory func(t *testing.T) *SealerRig

// noRefusals is a scopeaudit.Recorder that drops every refusal. The suite
// reads the refusal as the returned error.
type noRefusals struct{}

func (noRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

// NewSealerRig builds the sealers of a rig over the item key store index and
// the blob store blobs, and creates the namespaces "acme" and "other" in it.
func NewSealerRig(t *testing.T, index ItemKeyStore, blobs blob.Store) *SealerRig {
	t.Helper()

	rig := &SealerRig{
		Sealer: nil, Foreign: nil, Unkeyed: nil, Index: index, Blobs: blobs,
		Acme: namespace.Namespace{}, Other: namespace.Namespace{},
	}

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: itemKeyTime(), Name: name, HomeRegion: "local",
			FailoverVersion: itemKeyFenceVersion, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
		if err := index.CreateNamespace(t.Context(), ns); err != nil {
			t.Fatalf("create namespace %s: %v", name, err)
		}

		if name == "acme" {
			rig.Acme = ns
		} else {
			rig.Other = ns
		}
	}

	itemBlobs, err := secret.NewItemKeyBlobs(blobs, noRefusals{})
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	build := func(w secret.Wrapper) secret.ItemSealer {
		s, buildErr := secret.NewItemKeySealer(secret.ItemSealerConfig{
			Index: index, Blobs: itemBlobs, Wrapper: w, Namespaces: index,
			Now: itemKeyTime,
		})
		if buildErr != nil {
			t.Fatalf("item key sealer: %v", buildErr)
		}

		return s
	}

	rig.Sealer = build(newTestWrapper(t))
	rig.Foreign = build(newTestWrapper(t))
	rig.Unkeyed = build(nil)

	return rig
}

// newTestWrapper returns a built-in wrapper over a random key.
func newTestWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("key: %v", err)
	}

	w, err := secret.NewBuiltinWrapper(key)
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	return w
}

// RunItemSealer runs the conformance and hostile cases for secret.ItemSealer
// against the rigs that newRig returns:
//
//	secrettest.RunItemSealer(t, func(t *testing.T) *secrettest.SealerRig { return newRig(t) })
func RunItemSealer(t *testing.T, newRig SealerFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, r *SealerRig)
		name string
	}{
		{name: "content seals and opens, also over many chunks and empty", test: testSealerRoundTrip},
		{name: "a second NewItemKey returns the first record", test: testSealerNewKeyTwice},
		{name: "writers of one item agree on one key and leave one blob", test: testSealerConcurrentNewKey},
		{name: "an item with no key does not seal or open", test: testSealerNoKey},
		{name: "a stale fence writes no record and no blob", test: testSealerStaleFence},
		{name: "a fence of another namespace writes no record and no blob", test: testSealerOtherNamespaceFence},
		{name: "the binding must match", test: testSealerBinding},
		{name: "content of one item does not open as another item", test: testSealerOtherItem},
		{name: "a tampered, cut or extended stream does not open", test: testSealerTamper},
		{name: "a legacy stream header is refused", test: testSealerLegacyHeader},
		{name: "a shredded key answers before any blob read", test: testSealerShredded},
		{name: "a wrapped key blob of another item does not unwrap", test: testSealerSwappedBlob},
		{name: "a wrapped key blob of another namespace does not unwrap", test: testSealerOtherNamespaceBlob},
		{name: "another key-encryption key does not open", test: testSealerForeignWrapper},
		{name: "no key-encryption key fails closed", test: testSealerUnkeyed},
		{name: "a malformed reference is refused", test: testSealerBadRef},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newRig(t))
		})
	}
}

func (r *SealerRig) fence() namespace.Fence { return r.Acme.Fence() }

// ref returns a valid reference in the namespace "acme".
func sealerRef(t *testing.T, scope string, kind secret.ItemKind, item string) secret.ItemRef {
	t.Helper()

	return itemRef(t, scope, kind, item)
}

// sealerBinding returns the binding of the cases.
func sealerBinding() []byte { return []byte("pln-1") }

// mustNewKey makes the item key of ref.
func mustNewKey(t *testing.T, r *SealerRig, ref secret.ItemRef) secret.ItemKey {
	t.Helper()

	k, err := r.Sealer.NewItemKey(t.Context(), ref, r.fence())
	if err != nil {
		t.Fatalf("new item key %s: %v", ref, err)
	}

	return k
}

// seal returns the sealed bytes of plain.
func sealItem(t *testing.T, r *SealerRig, ref secret.ItemRef, binding, plain []byte) []byte {
	t.Helper()

	rd, err := r.Sealer.SealContent(t.Context(), ref, binding, bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal %s: %v", ref, err)
	}

	out, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read sealed %s: %v", ref, err)
	}

	return out
}

// openAll opens sealed with s and reads it to the end.
func openSealed(ctx context.Context, s secret.ItemSealer, ref secret.ItemRef, binding, sealed []byte) ([]byte, error) {
	rd, err := s.OpenContent(ctx, ref, binding, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}

	out, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// keyBlobs lists the object keys of the item key directory of ns.
func keyBlobs(t *testing.T, r *SealerRig, ns namespace.ID) []blob.Object {
	t.Helper()

	objects, err := r.Blobs.List(t.Context(), "namespaces/"+ns.String()+"/item-keys/")
	if err != nil {
		t.Fatalf("list item key blobs: %v", err)
	}

	return objects
}

// blobOf returns the blob key of the one wrapped key of the record k.
func blobOf(t *testing.T, r *SealerRig, ns namespace.ID, k secret.ItemKey) blob.Key {
	t.Helper()

	for _, o := range keyBlobs(t, r, ns) {
		if strings.Contains(o.Key.String(), "/"+string(k.ID)+"/") {
			return o.Key
		}
	}

	t.Fatalf("no blob for item key %s", k.ID)

	return ""
}

func readBlob(t *testing.T, r *SealerRig, key blob.Key) []byte {
	t.Helper()

	rc, err := r.Blobs.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	defer func() { _ = rc.Close() }() //nolint:errcheck // Test read.

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}

	return data
}

// replaceBlob overwrites the blob under key.
func replaceBlob(t *testing.T, r *SealerRig, key blob.Key, data []byte) {
	t.Helper()

	if err := r.Blobs.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete %s: %v", key, err)
	}

	if err := r.Blobs.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func testSealerRoundTrip(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	k := mustNewKey(t, r, ref)

	if k.Ref != ref || k.Shredded() || k.WrapperID == "" {
		t.Fatalf("record = %+v, want a live record of %s", k, ref)
	}

	// Chunks are 64 KiB: the sizes cross zero, one, and several chunks.
	for _, size := range []int{0, 1, 64 << 10, 64<<10 + 1, 200 << 10} {
		plain := make([]byte, size)
		if _, err := rand.Read(plain); err != nil {
			t.Fatalf("plain: %v", err)
		}

		sealed := sealItem(t, r, ref, sealerBinding(), plain)
		if size >= 16 && bytes.Contains(sealed, plain[:16]) {
			t.Errorf("size %d: the sealed stream holds plaintext", size)
		}

		got, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed)
		if err != nil {
			t.Fatalf("size %d: open: %v", size, err)
		}

		if !bytes.Equal(got, plain) {
			t.Errorf("size %d: opened content differs", size)
		}
	}

	// Two seals of one content differ: every stream has a random salt.
	a, b := sealItem(t, r, ref, sealerBinding(), []byte("same")), sealItem(t, r, ref, sealerBinding(), []byte("same"))
	if bytes.Equal(a, b) {
		t.Error("two seals of one content are equal")
	}
}

func testSealerNewKeyTwice(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindJobLog, "jlv-1")
	first := mustNewKey(t, r, ref)
	second := mustNewKey(t, r, ref)

	if !sameItemKey(first, second) {
		t.Errorf("second record = %+v, want %+v", second, first)
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 1 {
		t.Errorf("item key blobs = %d, want 1", n)
	}

	other := mustNewKey(t, r, sealerRef(t, "unit-one", secret.ItemKindJobLog, "jlv-2"))
	if other.ID == first.ID {
		t.Error("two items share one item key ID")
	}
}

func testSealerConcurrentNewKey(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-race")

	const writers = 8

	ids := make([]secret.ItemKeyID, writers)
	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			k, err := r.Sealer.NewItemKey(t.Context(), ref, r.fence())
			ids[i], errs[i] = k.ID, err
		})
	}

	wg.Wait()

	for i := range writers {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}

		if ids[i] != ids[0] {
			t.Errorf("writer %d got key %s, writer 0 got %s", i, ids[i], ids[0])
		}
	}

	// The losers delete their blobs, so one blob is left and it opens.
	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 1 {
		t.Errorf("item key blobs after the race = %d, want 1", n)
	}

	sealed := sealItem(t, r, ref, sealerBinding(), []byte("after the race"))
	if got, err := openSealed(
		t.Context(),
		r.Sealer,
		ref,
		sealerBinding(),
		sealed,
	); err != nil ||
		string(got) != "after the race" {
		t.Errorf("open after the race = %q, %v", got, err)
	}
}

func testSealerNoKey(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-none")

	if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrKeyNotFound,
	) {
		t.Errorf("seal with no key err = %v, want ErrKeyNotFound", err)
	}

	if _, err := r.Sealer.OpenContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrKeyNotFound,
	) {
		t.Errorf("open with no key err = %v, want ErrKeyNotFound", err)
	}
}

func testSealerStaleFence(t *testing.T, r *SealerRig) {
	t.Helper()

	stale := namespace.Fence{Namespace: "acme", FailoverVersion: itemKeyFenceVersion - 1}
	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-stale")

	if _, err := r.Sealer.NewItemKey(t.Context(), ref, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("new item key with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := r.Index.GetItemKey(t.Context(), ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("a fenced create left a record: err = %v", err)
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 0 {
		t.Errorf("a fenced create left %d key blobs", n)
	}
}

func testSealerOtherNamespaceFence(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-x")

	// The reference names "acme", the fence names "other".
	if _, err := r.Sealer.NewItemKey(t.Context(), ref, r.Other.Fence()); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("new item key under the fence of another namespace err = %v, want ErrFenced", err)
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)) + len(keyBlobs(t, r, r.Other.ID)); n != 0 {
		t.Errorf("a refused create left %d key blobs", n)
	}
}

func testSealerBinding(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	mustNewKey(t, r, ref)

	sealed := sealItem(t, r, ref, sealerBinding(), []byte("content"))

	if _, err := openSealed(t.Context(), r.Sealer, ref, []byte("pln-2"), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with another binding err = %v, want ErrDecrypt", err)
	}

	for _, binding := range [][]byte{nil, {}, bytes.Repeat([]byte("b"), 256)} {
		if _, err := r.Sealer.SealContent(t.Context(), ref, binding, strings.NewReader("x")); !errors.Is(
			err, secret.ErrInvalidBinding,
		) {
			t.Errorf("seal with a binding of %d bytes err = %v, want ErrInvalidBinding", len(binding), err)
		}

		if _, err := r.Sealer.OpenContent(t.Context(), ref, binding, strings.NewReader("x")); !errors.Is(
			err, secret.ErrInvalidBinding,
		) {
			t.Errorf("open with a binding of %d bytes err = %v, want ErrInvalidBinding", len(binding), err)
		}
	}
}

func testSealerOtherItem(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	mustNewKey(t, r, ref)

	sealed := sealItem(t, r, ref, sealerBinding(), []byte("content of pln-1"))

	others := []secret.ItemRef{
		sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-2"),
		sealerRef(t, "unit-two", secret.ItemKindPlan, "pln-1"),
		sealerRef(t, "unit-one", secret.ItemKindJobLog, "pln-1"),
	}

	for _, other := range others {
		mustNewKey(t, r, other)

		if _, err := openSealed(
			t.Context(),
			r.Sealer,
			other,
			sealerBinding(),
			sealed,
		); !errors.Is(
			err,
			secret.ErrDecrypt,
		) {
			t.Errorf("content of %s opened as %s: err = %v, want ErrDecrypt", ref, other, err)
		}
	}
}

func testSealerTamper(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindJobLog, "jlv-1")
	mustNewKey(t, r, ref)

	plain := bytes.Repeat([]byte("log line\n"), 20000)
	sealed := sealItem(t, r, ref, sealerBinding(), plain)

	flip := func(at int) []byte {
		c := bytes.Clone(sealed)
		c[at] ^= 0x01

		return c
	}

	cases := []struct {
		name   string
		sealed []byte
	}{
		{"a flipped byte in the header", flip(3)},
		{"a flipped byte in the first chunk", flip(60)},
		{"a flipped byte in the last chunk", flip(len(sealed) - 3)},
		{"a cut stream", sealed[:len(sealed)/2]},
		{"a stream cut at a chunk end", sealed[:1+32+5+(64<<10)+16]},
		{"an extended stream", append(bytes.Clone(sealed), 0x00)},
		{"an empty stream", nil},
	}

	for _, tc := range cases {
		if _, err := openSealed(
			t.Context(),
			r.Sealer,
			ref,
			sealerBinding(),
			tc.sealed,
		); !errors.Is(
			err,
			secret.ErrDecrypt,
		) {
			t.Errorf("%s: err = %v, want ErrDecrypt", tc.name, err)
		}
	}
}

func testSealerLegacyHeader(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindState, "upd-1")
	mustNewKey(t, r, ref)

	// Version byte 2 is the legacy format, which has no binding. An item
	// never had it, so it must not open, whatever follows.
	legacy := append([]byte{2}, bytes.Repeat([]byte{0x01}, 8+5+16)...)

	if _, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), legacy); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open of a legacy stream err = %v, want ErrDecrypt", err)
	}
}

func testSealerShredded(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	k := mustNewKey(t, r, ref)
	sealed := sealItem(t, r, ref, sealerBinding(), []byte("secret plan"))

	// The blob stays: the record alone must stop the read.
	if _, err := r.Index.ShredItemKey(t.Context(), ref, itemKeyTime(), r.fence()); err != nil {
		t.Fatalf("shred: %v", err)
	}

	if _, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("open after a shred err = %v, want ErrShredded", err)
	}

	if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrShredded,
	) {
		t.Errorf("seal after a shred err = %v, want ErrShredded", err)
	}

	// NewItemKey never brings a shredded key back.
	got, err := r.Sealer.NewItemKey(t.Context(), ref, r.fence())
	if !errors.Is(err, secret.ErrShredded) || got.ID != k.ID {
		t.Errorf("new item key after a shred = %v, %v, want the shredded record and ErrShredded", got.ID, err)
	}

	// With the blob gone too, the answer stays ErrShredded.
	if err := r.Blobs.Delete(t.Context(), blobOf(t, r, r.Acme.ID, k)); err != nil {
		t.Fatalf("delete blob: %v", err)
	}

	if _, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("open after a shred and a blob delete err = %v, want ErrShredded", err)
	}
}

func testSealerSwappedBlob(t *testing.T, r *SealerRig) {
	t.Helper()

	a := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	b := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-2")
	ka, kb := mustNewKey(t, r, a), mustNewKey(t, r, b)
	sealed := sealItem(t, r, b, sealerBinding(), []byte("content of pln-2"))

	// An attacker with write access to the blob store puts the wrapped key of
	// pln-1 where the wrapped key of pln-2 was.
	replaceBlob(t, r, blobOf(t, r, r.Acme.ID, kb), readBlob(t, r, blobOf(t, r, r.Acme.ID, ka)))

	if _, err := openSealed(t.Context(), r.Sealer, b, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with a swapped wrapped key err = %v, want ErrDecrypt", err)
	}
}

func testSealerOtherNamespaceBlob(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	acme := mustNewKey(t, r, ref)

	otherScope, err := secret.NewScope("other", "unit-one")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	otherRef, err := secret.NewItemRef(otherScope, secret.ItemKindPlan, "pln-1")
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	other, err := r.Sealer.NewItemKey(t.Context(), otherRef, r.Other.Fence())
	if err != nil {
		t.Fatalf("new item key in other: %v", err)
	}

	sealedOther := sealItem(t, r, otherRef, sealerBinding(), []byte("content of other"))

	// The wrapped key of "acme" in the place of the key of "other".
	replaceBlob(t, r, blobOf(t, r, r.Other.ID, other), readBlob(t, r, blobOf(t, r, r.Acme.ID, acme)))

	if _, err := openSealed(t.Context(), r.Sealer, otherRef, sealerBinding(), sealedOther); !errors.Is(
		err, secret.ErrDecrypt,
	) {
		t.Errorf("open with a wrapped key of another namespace err = %v, want ErrDecrypt", err)
	}
}

func testSealerForeignWrapper(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	mustNewKey(t, r, ref)
	sealed := sealItem(t, r, ref, sealerBinding(), []byte("content"))

	if _, err := openSealed(t.Context(), r.Foreign, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with another key-encryption key err = %v, want ErrDecrypt", err)
	}

	if _, err := r.Foreign.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrDecrypt,
	) {
		t.Errorf("seal with another key-encryption key err = %v, want ErrDecrypt", err)
	}
}

func testSealerUnkeyed(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")

	if _, err := r.Unkeyed.NewItemKey(t.Context(), ref, r.fence()); !errors.Is(err, secret.ErrKeyNotConfigured) {
		t.Errorf("new item key with no wrapper err = %v, want ErrKeyNotConfigured", err)
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 0 {
		t.Errorf("a refused create left %d key blobs", n)
	}

	mustNewKey(t, r, ref)

	if _, err := r.Unkeyed.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrKeyNotConfigured,
	) {
		t.Errorf("seal with no wrapper err = %v, want ErrKeyNotConfigured", err)
	}

	if _, err := r.Unkeyed.OpenContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrKeyNotConfigured,
	) {
		t.Errorf("open with no wrapper err = %v, want ErrKeyNotConfigured", err)
	}
}

func testSealerBadRef(t *testing.T, r *SealerRig) {
	t.Helper()

	scope, err := secret.NewScope("acme", "unit-one")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	bad := []secret.ItemRef{
		{Scope: scope, Kind: "unknown", Item: "pln-1"},
		{Scope: scope, Kind: secret.ItemKindPlan, Item: "../x"},
		{Scope: scope, Kind: secret.ItemKindPlan, Item: ""},
		{Scope: secret.Scope{Namespace: "acme", ID: "a/b"}, Kind: secret.ItemKindPlan, Item: "pln-1"},
	}

	for _, ref := range bad {
		if _, err := r.Sealer.NewItemKey(t.Context(), ref, r.fence()); err == nil {
			t.Errorf("new item key of %+v succeeded", ref)
		}

		if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); err == nil {
			t.Errorf("seal of %+v succeeded", ref)
		}

		if _, err := r.Sealer.OpenContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); err == nil {
			t.Errorf("open of %+v succeeded", ref)
		}
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 0 {
		t.Errorf("a refused create left %d key blobs", n)
	}
}
