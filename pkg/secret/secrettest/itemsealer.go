package secrettest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	Index   secret.ItemKeyStore
	// withIndex builds a sealer with the key-encryption key of Sealer, the
	// blobs of the rig and the given index.
	withIndex func(secret.ItemKeyIndex) secret.ItemSealer
	Blobs     blob.Store
	// ItemBlobs is the item key blob view of the sealers.
	ItemBlobs *secret.ItemKeyBlobs
	// Counts counts the calls that the sealers make to Blobs.
	Counts *BlobCounts
	// Mismatches counts the blobs that the sealers refused because they do
	// not match their record.
	Mismatches *MismatchCounts
	// Acme and Other are two namespaces of the store.
	Acme, Other namespace.Namespace
}

// SealerFactory returns a fresh rig for one subtest.
type SealerFactory func(t *testing.T) *SealerRig

// BlobCounts counts the calls that the sealers of a rig make to the blob
// store. A case reads it to show that a check refused a call before the call
// reached the store.
type BlobCounts struct {
	puts  atomic.Int64
	reads atomic.Int64
}

// Puts returns the number of Put calls.
func (c *BlobCounts) Puts() int64 { return c.puts.Load() }

// Reads returns the number of Get and List calls.
func (c *BlobCounts) Reads() int64 { return c.reads.Load() }

// countingBlobs counts the calls to the blob store that it wraps.
type countingBlobs struct {
	blob.Store

	counts *BlobCounts
}

func (c *countingBlobs) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	c.counts.puts.Add(1)

	return c.Store.Put(ctx, key, r)
}

func (c *countingBlobs) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	c.counts.reads.Add(1)

	return c.Store.Get(ctx, key)
}

func (c *countingBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	c.counts.reads.Add(1)

	return c.Store.ListPage(ctx, req)
}

// errMismatchOp reports a mismatch record of an operation that is not a read.
var errMismatchOp = errors.New("a mismatch record needs the operation get")

// MismatchCounts is a scopeaudit.IntegrityRecorder that counts the refusals
// of blobs that do not match their record, for each namespace.
type MismatchCounts struct {
	mu     sync.Mutex
	counts map[namespace.ID]int
}

// RecordMismatch implements scopeaudit.IntegrityRecorder.
func (m *MismatchCounts) RecordMismatch(_ context.Context, ns namespace.ID, op string) error {
	if op != "get" {
		return fmt.Errorf("%w: %q", errMismatchOp, op)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.counts[ns]++

	return nil
}

// Of returns the number of refusals in the namespace ns.
func (m *MismatchCounts) Of(ns namespace.ID) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.counts[ns]
}

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

	rig := new(SealerRig)
	rig.Index, rig.Blobs = index, blobs

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: itemKeyTime(), Name: name, PrimaryInstallation: "local",
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

	rig.Counts = &BlobCounts{puts: atomic.Int64{}, reads: atomic.Int64{}}

	rig.Mismatches = &MismatchCounts{mu: sync.Mutex{}, counts: map[namespace.ID]int{}}

	itemBlobs, err := secret.NewItemKeyBlobs(
		&countingBlobs{Store: blobs, counts: rig.Counts}, noRefusals{}, rig.Mismatches,
	)
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	rig.ItemBlobs = itemBlobs

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

	wrapper := newTestWrapper(t)

	rig.withIndex = func(idx secret.ItemKeyIndex) secret.ItemSealer {
		s, buildErr := secret.NewItemKeySealer(secret.ItemSealerConfig{
			Index: idx, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: index, Now: itemKeyTime,
		})
		if buildErr != nil {
			t.Fatalf("item key sealer: %v", buildErr)
		}

		return s
	}

	rig.Sealer = build(wrapper)
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
		{
			name: "two writers of one item, many rounds: one record wins, the loser deletes only its own blob",
			test: testSealerRaceRounds,
		},
		{name: "an item with no key does not seal or open", test: testSealerNoKey},
		{name: "a stale fence writes no record and no blob", test: testSealerStaleFence},
		{name: "a fence of another namespace writes no record and no blob", test: testSealerOtherNamespaceFence},
		{name: "a rolled-back item transaction leaves no record", test: testSealerRollback},
		{name: "a record of another item does not open", test: testSealerOtherRecord},
		{name: "the binding must match", test: testSealerBinding},
		{name: "content of one item does not open as another item", test: testSealerOtherItem},
		{name: "a tampered, cut or extended stream does not open", test: testSealerTamper},
		{name: "a legacy stream header is refused", test: testSealerLegacyHeader},
		{name: "a shredded key answers before any blob read", test: testSealerShredded},
		{name: "a wrapped key blob of another item is refused before the unwrap", test: testSealerSwappedBlob},
		{name: "a blob that matches a lying record does not unwrap", test: testSealerSwappedBlobAndRecord},
		{name: "a replaced wrapped key blob is refused and recorded", test: testSealerReplacedBlob},
		{name: "a wrapped key moved to another record ID does not unwrap", test: testSealerMovedRecordID},
		{name: "a wrapped key blob of another namespace does not unwrap", test: testSealerOtherNamespaceBlob},
		{name: "only the blob version and attempt of the record are read", test: testSealerRecordVersion},
		{name: "two item key writes never share a blob key", test: testSealerAttemptKeys},
		{name: "the reaper keeps a blob until its record commits", test: testSealerGarbageWaitsForTheCommit},
		{name: "another key-encryption key does not open", test: testSealerForeignWrapper},
		{name: "no key-encryption key fails closed", test: testSealerUnkeyed},
		{name: "a malformed reference is refused", test: testSealerBadRef},
		{name: "a batch seals and opens with one read of the key", test: testSealerBatchRoundTrip},
		{name: "a batch with a moved part or of another item fails as a whole", test: testSealerBatchMoved},
		{name: "a batch with a bad binding, no key or a shredded key fails", test: testSealerBatchRefusals},
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

// newKeyVia makes the item key of ref with s in one fenced transaction of the
// index, as a tool store does for a new item: the record commits when the
// function returns nil and rolls back otherwise.
func newKeyVia(
	ctx context.Context, r *SealerRig, s secret.ItemSealer, ref secret.ItemRef, fence namespace.Fence,
) (secret.ItemKey, error) {
	var k secret.ItemKey

	err := r.Index.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		got, err := s.NewItemKey(ctx, tx, ref, fence)
		k = got

		return err
	})

	return k, err
}

// mustNewKey makes the item key of ref.
func mustNewKey(t *testing.T, r *SealerRig, ref secret.ItemRef) secret.ItemKey {
	t.Helper()

	k, err := newKeyVia(t.Context(), r, r.Sealer, ref, r.fence())
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

// openSealed opens sealed with s and reads it to the end.
func openSealed(ctx context.Context, s secret.ItemSealer, ref secret.ItemRef, binding, sealed []byte) ([]byte, error) {
	rd, err := s.OpenContent(ctx, ref, binding, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}

	out, err := io.ReadAll(rd)
	if err = errors.Join(err, rd.Close()); err != nil {
		return nil, fmt.Errorf("read opened content: %w", err)
	}

	return out, nil
}

// openRefused opens content that the case expects to be refused, and returns
// the error. When the open succeeds, it closes the reader, so that the failed
// case does not also keep a key.
func openRefused(t *testing.T, s secret.ItemSealer, ref secret.ItemRef, binding []byte) error {
	t.Helper()

	rd, err := s.OpenContent(t.Context(), ref, binding, strings.NewReader("x"))
	if rd != nil {
		if closeErr := rd.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}

	return err
}

// keyBlobs lists the object keys of the item key directory of ns.
func keyBlobs(t *testing.T, r *SealerRig, ns namespace.ID) []blob.Object {
	t.Helper()

	objects, err := listBlobs(t.Context(), r.Blobs, "namespaces/"+ns.String()+"/item-keys/")
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
			k, err := newKeyVia(t.Context(), r, r.Sealer, ref, r.fence())
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

	got, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed)
	if err != nil || string(got) != "after the race" {
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

	if err := openRefused(t, r.Sealer, ref, sealerBinding()); !errors.Is(
		err, secret.ErrKeyNotFound,
	) {
		t.Errorf("open with no key err = %v, want ErrKeyNotFound", err)
	}
}

func testSealerStaleFence(t *testing.T, r *SealerRig) {
	t.Helper()

	stale := namespace.Fence{Namespace: "acme", FailoverVersion: itemKeyFenceVersion - 1}
	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-stale")

	if _, err := newKeyVia(t.Context(), r, r.Sealer, ref, stale); !errors.Is(err, namespace.ErrFenced) {
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
	if _, err := newKeyVia(t.Context(), r, r.Sealer, ref, r.Other.Fence()); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("new item key under the fence of another namespace err = %v, want ErrFenced", err)
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)) + len(keyBlobs(t, r, r.Other.ID)); n != 0 {
		t.Errorf("a refused create left %d key blobs", n)
	}

	// The sealer checks the namespace of the fence before it writes a blob.
	// A key blob that is written and deleted afterwards would leave no blob
	// either, so only the count of puts shows that this check refused the call.
	if n := r.Counts.Puts(); n != 0 {
		t.Errorf("a create under the fence of another namespace reached the blob store with %d puts", n)
	}
}

// testSealerRollback shows that the record commits with the transaction of the
// caller: when the caller rolls back after NewItemKey, no record stays, and the
// item gets a new key at the next try.
func testSealerRollback(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-rollback")

	var first secret.ItemKey

	err := r.Index.InItemKeyTx(t.Context(), r.fence(), func(tx secret.ItemKeyTx) error {
		k, newErr := r.Sealer.NewItemKey(t.Context(), tx, ref, r.fence())
		if newErr != nil {
			return newErr
		}

		first = k

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rolled-back transaction err = %v, want the error of the caller", err)
	}

	if _, err = r.Index.GetItemKey(t.Context(), ref); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Fatalf("a rolled-back transaction left a record: err = %v", err)
	}

	second := mustNewKey(t, r, ref)
	if second.ID == first.ID {
		t.Error("the key of a rolled-back transaction came back")
	}

	sealed := sealItem(t, r, ref, sealerBinding(), []byte("after the rollback"))

	got, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed)
	if err != nil || string(got) != "after the rollback" {
		t.Errorf("open after the rollback = %q, %v", got, err)
	}
}

// relabelIndex answers each read with the stored record of the request, but
// with the reference ref in the record.
type relabelIndex struct {
	secret.ItemKeyIndex

	ref secret.ItemRef
}

func (i relabelIndex) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	k, err := i.ItemKeyIndex.GetItemKey(ctx, ref)
	k.Ref = i.ref

	return k, err
}

// testSealerOtherRecord sends a hostile record straight to the check of the
// sealer: the index answers a lookup of item A with a record that names item
// B, but that holds the ID of the key of item A. The blob unwraps for item A.
// Only the comparison of the reference in the record with the reference of the
// request refuses it.
func testSealerOtherRecord(t *testing.T, r *SealerRig) {
	t.Helper()

	a := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	b := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-2")
	mustNewKey(t, r, a)
	sealed := sealItem(t, r, a, sealerBinding(), []byte("content of pln-1"))

	lying := r.withIndex(relabelIndex{ItemKeyIndex: r.Index, ref: b})

	if _, err := openSealed(t.Context(), lying, a, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with a record that names another item err = %v, want ErrDecrypt", err)
	}

	if _, err := lying.SealContent(t.Context(), a, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrDecrypt,
	) {
		t.Errorf("seal with a record that names another item err = %v, want ErrDecrypt", err)
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

		if err := openRefused(t, r.Sealer, ref, binding); !errors.Is(
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

		_, err := openSealed(t.Context(), r.Sealer, other, sealerBinding(), sealed)
		if !errors.Is(err, secret.ErrDecrypt) {
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
		_, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), tc.sealed)
		if !errors.Is(err, secret.ErrDecrypt) {
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

	reads, puts := r.Counts.Reads(), r.Counts.Puts()

	if _, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrShredded) {
		t.Errorf("open after a shred err = %v, want ErrShredded", err)
	}

	if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrShredded,
	) {
		t.Errorf("seal after a shred err = %v, want ErrShredded", err)
	}

	// NewItemKey never brings a shredded key back.
	got, err := newKeyVia(t.Context(), r, r.Sealer, ref, r.fence())
	if !errors.Is(err, secret.ErrShredded) || got.ID != k.ID {
		t.Errorf("new item key after a shred = %v, %v, want the shredded record and ErrShredded", got.ID, err)
	}

	if r.Counts.Reads() != reads || r.Counts.Puts() != puts {
		t.Errorf("a shredded key reached the blob store: %d reads and %d puts after the shred",
			r.Counts.Reads()-reads, r.Counts.Puts()-puts)
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
	// pln-1 where the wrapped key of pln-2 was. Both have the same size, so
	// only the MD5 of the record shows the swap.
	replaceBlob(t, r, blobOf(t, r, r.Acme.ID, kb), readBlob(t, r, blobOf(t, r, r.Acme.ID, ka)))

	_, err := openSealed(t.Context(), r.Sealer, b, sealerBinding(), sealed)
	if !errors.Is(err, secret.ErrItemKeyBlobMismatch) {
		t.Errorf("open with a swapped wrapped key err = %v, want ErrItemKeyBlobMismatch", err)
	}

	if n := r.Mismatches.Of(r.Acme.ID); n != 1 {
		t.Errorf("mismatch records = %d, want 1", n)
	}
}

// blobInfoIndex answers each read with the stored record of the request, but
// with the size and the MD5 of info, so the digest check of the blob passes.
type blobInfoIndex struct {
	secret.ItemKeyIndex

	info secret.ItemKeyBlobInfo
}

func (i blobInfoIndex) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	k, err := i.ItemKeyIndex.GetItemKey(ctx, ref)
	k.Blob.Size, k.Blob.MD5 = i.info.Size, i.info.MD5

	return k, err
}

// testSealerSwappedBlobAndRecord sends a swapped blob straight to the unwrap,
// as if the digest check had failed: the wrapped key of pln-1 is in the place
// of the wrapped key of pln-2, and the index answers with a record of pln-2
// that holds the size and the MD5 of the blob of pln-1. Only the associated
// data of the unwrap refuses it.
func testSealerSwappedBlobAndRecord(t *testing.T, r *SealerRig) {
	t.Helper()

	a := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	b := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-2")
	ka, kb := mustNewKey(t, r, a), mustNewKey(t, r, b)
	sealed := sealItem(t, r, b, sealerBinding(), []byte("content of pln-2"))

	replaceBlob(t, r, blobOf(t, r, r.Acme.ID, kb), readBlob(t, r, blobOf(t, r, r.Acme.ID, ka)))

	lying := r.withIndex(blobInfoIndex{ItemKeyIndex: r.Index, info: ka.Blob})

	if _, err := openSealed(t.Context(), lying, b, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with a swapped blob and a matching record err = %v, want ErrDecrypt", err)
	}

	if n := r.Mismatches.Of(r.Acme.ID); n != 0 {
		t.Errorf("a blob that matches its record wrote %d mismatch records", n)
	}
}

// testSealerReplacedBlob replaces the wrapped key blob of a record, as a store
// that ignores a conditional write does on a second write to the key. The
// sealer refuses the blob before it unwraps it, seals and opens nothing, and
// writes one mismatch record for each refusal.
func testSealerReplacedBlob(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindJobLog, "jlv-1")

	// A first write of the item rolls back and leaves its blob. Its wrapped
	// key has the associated data of the same item, so the unwrap alone
	// would accept it in the place of the committed blob.
	rollBackNewKey(t, r, ref)

	orphans := keyBlobs(t, r, r.Acme.ID)
	if len(orphans) != 1 {
		t.Fatalf("blobs after a rolled-back write = %d, want 1", len(orphans))
	}

	sameItem := readBlob(t, r, orphans[0].Key)

	k := mustNewKey(t, r, ref)
	sealed := sealItem(t, r, ref, sealerBinding(), []byte("log of jlv-1"))
	key := blobOf(t, r, r.Acme.ID, k)
	original := readBlob(t, r, key)

	cases := []struct {
		name string
		data []byte
	}{
		{name: "a wrapped key of the same item from another write", data: sameItem},
		{name: "one flipped byte", data: flipLast(original)},
		{name: "a cut blob", data: original[:len(original)-1]},
	}

	for i, tc := range cases {
		replaceBlob(t, r, key, tc.data)

		if _, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); !errors.Is(
			err, secret.ErrItemKeyBlobMismatch,
		) {
			t.Errorf("%s: open err = %v, want ErrItemKeyBlobMismatch", tc.name, err)
		}

		if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
			err, secret.ErrItemKeyBlobMismatch,
		) {
			t.Errorf("%s: seal err = %v, want ErrItemKeyBlobMismatch", tc.name, err)
		}

		if n := r.Mismatches.Of(r.Acme.ID); n != 2*(i+1) {
			t.Errorf("%s: mismatch records = %d, want %d", tc.name, n, 2*(i+1))
		}
	}

	// The original bytes open again: the refusal changed nothing.
	replaceBlob(t, r, key, original)

	if got, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); err != nil ||
		string(got) != "log of jlv-1" {
		t.Errorf("open with the original blob = %q, %v", got, err)
	}
}

// rollBackNewKey makes the item key of ref in a transaction that rolls back.
// The wrapped key blob of the write stays in the store as an orphan.
func rollBackNewKey(t *testing.T, r *SealerRig, ref secret.ItemRef) {
	t.Helper()

	err := r.Index.InItemKeyTx(t.Context(), r.fence(), func(tx secret.ItemKeyTx) error {
		if _, err := r.Sealer.NewItemKey(t.Context(), tx, ref, r.fence()); err != nil {
			return err
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rolled-back write of %s err = %v", ref, err)
	}
}

// flipLast returns a copy of data with its last byte changed.
func flipLast(data []byte) []byte {
	c := bytes.Clone(data)
	c[len(c)-1] ^= 0x01

	return c
}

// reidIndex answers each read with the stored record of the request, but
// with the record ID id.
type reidIndex struct {
	secret.ItemKeyIndex

	id secret.ItemKeyID
}

func (i reidIndex) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	k, err := i.ItemKeyIndex.GetItemKey(ctx, ref)
	k.ID = i.id

	return k, err
}

// testSealerMovedRecordID sends a hostile record straight to the unwrap: the
// record of the item names another record ID, and the wrapped key of the item
// is copied to the blob key of that ID. The reference, the wrapper and the
// blob version all match. Only the record ID in the associated data of the
// wrapped key refuses it.
func testSealerMovedRecordID(t *testing.T, r *SealerRig) {
	t.Helper()

	ref := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	k := mustNewKey(t, r, ref)
	sealed := sealItem(t, r, ref, sealerBinding(), []byte("content of pln-1"))

	if got, err := openSealed(t.Context(), r.Sealer, ref, sealerBinding(), sealed); err != nil ||
		string(got) != "content of pln-1" {
		t.Fatalf("open with the right record = %q, %v", got, err)
	}

	moved := secret.NewItemKeyID()
	from := blobOf(t, r, r.Acme.ID, k)

	to, err := blob.ParseKey(strings.Replace(from.String(), "/"+string(k.ID)+"/", "/"+string(moved)+"/", 1))
	if err != nil {
		t.Fatalf("blob key of the moved record: %v", err)
	}

	if err := r.Blobs.Put(t.Context(), to, bytes.NewReader(readBlob(t, r, from))); err != nil {
		t.Fatalf("put %s: %v", to, err)
	}

	lying := r.withIndex(reidIndex{ItemKeyIndex: r.Index, id: moved})

	if _, err := openSealed(t.Context(), lying, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with a wrapped key moved to another record err = %v, want ErrDecrypt", err)
	}

	if _, err := lying.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); !errors.Is(
		err, secret.ErrDecrypt,
	) {
		t.Errorf("seal with a wrapped key moved to another record err = %v, want ErrDecrypt", err)
	}

	bad := r.withIndex(reidIndex{ItemKeyIndex: r.Index, id: "ik-a\x00b"})

	if _, err := openSealed(t.Context(), bad, ref, sealerBinding(), sealed); !errors.Is(err, secret.ErrDecrypt) {
		t.Errorf("open with a record ID that is not valid err = %v, want ErrDecrypt", err)
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

	other, err := newKeyVia(t.Context(), r, r.Sealer, otherRef, r.Other.Fence())
	if err != nil {
		t.Fatalf("new item key in other: %v", err)
	}

	sealedOther := sealItem(t, r, otherRef, sealerBinding(), []byte("content of other"))

	// The wrapped key of "acme" in the place of the key of "other". The
	// digest check refuses it first.
	replaceBlob(t, r, blobOf(t, r, r.Other.ID, other), readBlob(t, r, blobOf(t, r, r.Acme.ID, acme)))

	if _, err := openSealed(t.Context(), r.Sealer, otherRef, sealerBinding(), sealedOther); !errors.Is(
		err, secret.ErrItemKeyBlobMismatch,
	) {
		t.Errorf("open with a wrapped key of another namespace err = %v, want ErrItemKeyBlobMismatch", err)
	}

	// With a record that matches the swapped blob, the unwrap refuses it.
	lying := r.withIndex(blobInfoIndex{ItemKeyIndex: r.Index, info: acme.Blob})

	if _, err := openSealed(t.Context(), lying, otherRef, sealerBinding(), sealedOther); !errors.Is(
		err, secret.ErrDecrypt,
	) {
		t.Errorf("open with a wrapped key of another namespace and a matching record err = %v, want ErrDecrypt", err)
	}
}

func testSealerRecordVersion(t *testing.T, r *SealerRig) {
	t.Helper()

	a := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-1")
	b := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-2")
	ka, kb := mustNewKey(t, r, a), mustNewKey(t, r, b)
	sealed := sealItem(t, r, a, sealerBinding(), []byte("content of pln-1"))

	// Other blobs of the item key of pln-1 that the record does not name: a
	// later version, another attempt of the same version, and a key with no
	// attempt part. Each holds the wrapped key of pln-2, then bytes that are
	// not a wrapped key.
	named := blobOf(t, r, r.Acme.ID, ka).String()
	dir := named[:strings.LastIndex(named, "/")+1]
	strays := []blob.Key{
		blob.Key(dir + "v2-" + strings.Repeat("a", 20)),
		blob.Key(dir + "v1-" + strings.Repeat("b", 20)),
		blob.Key(dir + "v1"),
	}

	for _, stray := range strays {
		for _, data := range [][]byte{readBlob(t, r, blobOf(t, r, r.Acme.ID, kb)), []byte("not a wrapped key")} {
			if err := r.Blobs.Delete(t.Context(), stray); err != nil {
				t.Fatalf("delete %s: %v", stray, err)
			}

			if err := r.Blobs.Put(t.Context(), stray, bytes.NewReader(data)); err != nil {
				t.Fatalf("put %s: %v", stray, err)
			}

			got, err := openSealed(t.Context(), r.Sealer, a, sealerBinding(), sealed)
			if err != nil || string(got) != "content of pln-1" {
				t.Errorf("open with the stray blob %s present = %q, %v, want the content", stray, got, err)
			}
		}
	}

	// With the blob of the record gone, the later version does not stand in.
	if err := r.Blobs.Delete(t.Context(), blob.Key(named)); err != nil {
		t.Fatalf("delete %s: %v", named, err)
	}

	if _, err := openSealed(t.Context(), r.Sealer, a, sealerBinding(), sealed); !errors.Is(err, secret.ErrKeyNotFound) {
		t.Errorf("open with the blob of the record gone err = %v, want ErrKeyNotFound", err)
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

	if _, err := newKeyVia(t.Context(), r, r.Unkeyed, ref, r.fence()); !errors.Is(err, secret.ErrKeyNotConfigured) {
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

	if err := openRefused(t, r.Unkeyed, ref, sealerBinding()); !errors.Is(
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
		if _, err := newKeyVia(t.Context(), r, r.Sealer, ref, r.fence()); err == nil {
			t.Errorf("new item key of %+v succeeded", ref)
		}

		if _, err := r.Sealer.SealContent(t.Context(), ref, sealerBinding(), strings.NewReader("x")); err == nil {
			t.Errorf("seal of %+v succeeded", ref)
		}

		if err := openRefused(t, r.Sealer, ref, sealerBinding()); err == nil {
			t.Errorf("open of %+v succeeded", ref)
		}
	}

	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 0 {
		t.Errorf("a refused create left %d key blobs", n)
	}
}

// testSealerAttemptKeys shows that each write of a wrapped key has its own
// blob key, which the record names: the records of many items, and the
// records of one item after rolled-back writes, never share a blob key.
func testSealerAttemptKeys(t *testing.T, r *SealerRig) {
	t.Helper()

	const items = 20

	seen := make(map[blob.Key]secret.ItemKeyID, items)

	for i := range items {
		ref := sealerRef(t, "unit-one", secret.ItemKindPlan, fmt.Sprintf("pln-%d", i))

		// A rolled-back write first: its blob stays as an orphan, and the next
		// write of the same item must not reuse its key.
		rollBackNewKey(t, r, ref)

		k := mustNewKey(t, r, ref)

		if !secret.ValidItemKeyAttempt(k.Blob.Attempt) {
			t.Fatalf("record of %s has the attempt part %q", ref, k.Blob.Attempt)
		}

		key, err := r.ItemBlobs.Key(r.Acme.ID, k.ID, k.Blob.Version, k.Blob.Attempt)
		if err != nil {
			t.Fatalf("key of %s: %v", ref, err)
		}

		if other, dup := seen[key]; dup {
			t.Fatalf("the records %s and %s name one blob key", other, k.ID)
		}

		seen[key] = k.ID

		data := readBlob(t, r, key)
		if d := blob.DigestBytes(data); d.Size != k.Blob.Size || d.MD5 != k.Blob.MD5 {
			t.Errorf("blob of %s does not match its record", ref)
		}
	}

	// Each item has its committed blob and the orphan of its rolled-back
	// write, under two keys.
	if n := len(keyBlobs(t, r, r.Acme.ID)); n != 2*items {
		t.Errorf("item key blobs = %d, want %d", n, 2*items)
	}
}

// namerOfScope returns an ItemKeyBlobNamer over the committed records of the
// scope of ref, as a reaper reads them.
func namerOfScope(r *SealerRig, scope secret.Scope) secret.ItemKeyBlobNamer {
	return func(ctx context.Context, part secret.ItemKeyBlob) (bool, error) {
		records, err := r.Index.ListItemKeys(ctx, scope, "", 1000)
		if err != nil {
			return false, fmt.Errorf("list item keys: %w", err)
		}

		for i := range records {
			k := &records[i]
			if k.ID == part.ID && k.Blob.Version == part.Version && k.Blob.Attempt == part.Attempt {
				return true, nil
			}
		}

		return false, nil
	}
}

// testSealerGarbageWaitsForTheCommit shows the reaper rule over a real store
// and a real transaction. While the write has not committed, its blob is in
// the grace period and is no candidate. After a commit the record names it.
// After a rollback, it is garbage once the grace period ends.
func testSealerGarbageWaitsForTheCommit(t *testing.T, r *SealerRig) {
	t.Helper()

	committed := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-commit")
	rolledBack := sealerRef(t, "unit-one", secret.ItemKindPlan, "pln-rollback")
	namer := namerOfScope(r, committed.Scope)

	var pending []blob.Key

	err := r.Index.InItemKeyTx(t.Context(), r.fence(), func(tx secret.ItemKeyTx) error {
		if _, err := r.Sealer.NewItemKey(t.Context(), tx, rolledBack, r.fence()); err != nil {
			return err
		}

		// The blob exists and no committed record names it yet. The grace
		// period keeps it out of the candidates, and the reaper reads no
		// record for it.
		got, garbageErr := r.ItemBlobs.Garbage(t.Context(), r.Acme.ID, namer, time.Now())
		if garbageErr != nil {
			return garbageErr
		}

		pending = got

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("rolled-back write err = %v", err)
	}

	if len(pending) != 0 {
		t.Errorf("garbage before the commit = %v, want none", pending)
	}

	k := mustNewKey(t, r, committed)

	committedKey, err := r.ItemBlobs.Key(r.Acme.ID, k.ID, k.Blob.Version, k.Blob.Attempt)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	// Inside the grace period nothing is garbage.
	if got, garbageErr := r.ItemBlobs.Garbage(t.Context(), r.Acme.ID, namer, time.Now()); garbageErr != nil ||
		len(got) != 0 {
		t.Errorf("garbage in the grace period = %v, %v, want none", got, garbageErr)
	}

	// After the grace period, only the orphan of the rolled-back write is
	// garbage. The committed blob stays. A view of the store makes the item
	// key blobs older than the grace period, and the server clock and the
	// probe stay in step.
	aged, err := secret.NewItemKeyBlobs(
		agedBlobs{Store: r.Blobs, age: secret.ItemKeyBlobGracePeriod + time.Minute},
		noRefusals{}, r.Mismatches,
	)
	if err != nil {
		t.Fatalf("aged item key blobs: %v", err)
	}

	got, err := aged.Garbage(t.Context(), r.Acme.ID, namer, time.Now())
	if err != nil {
		t.Fatalf("garbage after the grace period: %v", err)
	}

	if len(got) != 1 || got[0] == committedKey {
		t.Errorf("garbage after the grace period = %v, want the one orphan and not %s", got, committedKey)
	}
}

// raceRounds is the number of rounds of the race case.
const raceRounds = 25

// testSealerRaceRounds runs two concurrent first writes of one item, each with
// its own random key, in many rounds. In each round exactly one record wins:
// both writers get that record, as the port promises, and the loser deletes
// its own blob and no other. One blob stays for each item, and it is the blob
// that the record names, with the size and the MD5 of the record.
func testSealerRaceRounds(t *testing.T, r *SealerRig) {
	t.Helper()

	for round := range raceRounds {
		ref := sealerRef(t, "unit-race", secret.ItemKindPlan, fmt.Sprintf("pln-%d", round))

		var (
			wg   sync.WaitGroup
			got  [2]secret.ItemKey
			errs [2]error
		)

		for i := range got {
			wg.Go(func() {
				got[i], errs[i] = newKeyVia(t.Context(), r, r.Sealer, ref, r.fence())
			})
		}

		wg.Wait()

		for i := range got {
			if errs[i] != nil {
				t.Fatalf("round %d: writer %d: %v", round, i, errs[i])
			}
		}

		if !sameItemKey(got[0], got[1]) {
			t.Fatalf("round %d: the writers got two records: %s and %s", round, got[0].ID, got[1].ID)
		}

		checkOneBlobOf(t, r, got[0])

		// The loser wrote under its own item key ID. Its blob is gone, so the
		// namespace holds one blob for each round.
		if n := len(keyBlobs(t, r, r.Acme.ID)); n != round+1 {
			t.Fatalf("round %d: item key blobs = %d, want %d: a losing write left its blob", round, n, round+1)
		}
	}
}

// checkOneBlobOf checks that the directory of the item key of k holds exactly
// the blob that k names, with the size and the MD5 of k, and no orphan of a
// losing write.
func checkOneBlobOf(t *testing.T, r *SealerRig, k secret.ItemKey) {
	t.Helper()

	key, err := r.ItemBlobs.Key(r.Acme.ID, k.ID, k.Blob.Version, k.Blob.Attempt)
	if err != nil {
		t.Fatalf("key of %s: %v", k.ID, err)
	}

	var inDir []blob.Key

	for _, o := range keyBlobs(t, r, r.Acme.ID) {
		if strings.Contains(o.Key.String(), "/"+string(k.ID)+"/") {
			inDir = append(inDir, o.Key)
		}
	}

	if len(inDir) != 1 || inDir[0] != key {
		t.Fatalf("blobs of the winning item key %s = %v, want only %s", k.ID, inDir, key)
	}

	if d := blob.DigestBytes(readBlob(t, r, key)); d.Size != k.Blob.Size || d.MD5 != k.Blob.MD5 {
		t.Errorf("the blob of %s does not match its record", k.ID)
	}
}

// agedBlobs is a view of a blob store whose listing makes each item key blob
// older by age, as the time of the store gives it after that time passed. A
// probe of a reaper run keeps the time of the store, because the run writes it
// now.
type agedBlobs struct {
	blob.Store

	age time.Duration
}

func (a agedBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := a.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, fmt.Errorf("list: %w", err)
	}

	for i := range page.Objects {
		if _, parseErr := secret.ParseItemKeyBlobKey(page.Objects[i].Key); parseErr == nil {
			page.Objects[i].ModifiedAt = page.Objects[i].ModifiedAt.Add(-a.age)
		}
	}

	return page, nil
}

// listBlobs walks every page under prefix and returns the objects.
func listBlobs(ctx context.Context, store blob.Store, prefix string) ([]blob.Object, error) {
	var out []blob.Object

	err := blob.Walk(ctx, store, prefix, func(o blob.Object) error {
		out = append(out, o)

		return nil
	})

	return out, err
}
