package registrytest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// KeyHarness is one fresh, empty key store for one subtest. Namespace makes
// the namespace name and returns its current fence. Entries returns the audit
// entries that the store wrote for the namespace, oldest first.
type KeyHarness struct {
	Store     registry.KeyStore
	Namespace func(t *testing.T, name namespace.Name) namespace.Fence
	Entries   func(t *testing.T, name namespace.Name) []audit.Entry
}

// KeyFactory returns a fresh key harness for one subtest.
type KeyFactory func(t *testing.T) KeyHarness

type keyCase struct {
	name string
	test func(t *testing.T, h KeyHarness)
}

// RunKeyStore runs every case of the KeyStore suite.
func RunKeyStore(t *testing.T, newHarness KeyFactory) {
	t.Helper()

	for _, tc := range keyCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

// The numbers of the fixture keys.
const (
	keyOne   = 1
	keyTwo   = 2
	keyThree = 3
	keyFive  = 5
)

// keyFingerprint returns the fingerprint of the fixture key number n.
func keyFingerprint(n int) string {
	return fmt.Sprintf("%040X", n+1)
}

// keyRecord returns a valid key of ns. The bytes only look like an armored key:
// the store checks the form, and the service checks the key itself.
func keyRecord(ns namespace.Name, n int) registry.KeyRecord {
	return registry.KeyRecord{
		AddedAt:   time.UnixMilli(fixtureT).UTC(),
		Namespace: ns,
		Key: registry.TrustedKey{
			Fingerprint: keyFingerprint(n),
			Armored: []byte(
				fmt.Sprintf("-----BEGIN PGP PUBLIC KEY BLOCK-----\nkey %d\n-----END PGP PUBLIC KEY BLOCK-----\n", n),
			),
		},
		AddedBy: publisher,
	}
}

// keyEntry returns the audit entry of a change of the keys of ns.
func keyEntry(ns namespace.Name) audit.Entry {
	e := entry(ns)
	e.Action = "registry:manage-keys"

	return e
}

func keyCases() []keyCase {
	return []keyCase{
		{"add then list", testKeyAddList},
		{"list orders by fingerprint", testKeyOrder},
		{"list returns a copy", testKeyCopy},
		{"add of an invalid key", testKeyInvalid},
		{"add with a stale fence", testKeyStale},
		{"add with the fence of another namespace", testKeyOtherFence},
		{"repeat with the same bytes", testKeyRepeat},
		{"the same fingerprint with other bytes", testKeyConflict},
		{"the limit of keys", testKeyLimit},
		{"namespaces do not share keys", testKeyNamespaces},
		{"remove", testKeyRemove},
		{"remove of a missing key", testKeyRemoveMissing},
		{"remove with a stale fence", testKeyRemoveStale},
		{"add and remove write one audit entry each", testKeyAudit},
		{"list with a bad namespace", testKeyListBad},
	}
}

func mustAddKey(t *testing.T, h KeyHarness, k registry.KeyRecord, f namespace.Fence) {
	t.Helper()

	if err := h.Store.AddKey(t.Context(), k, f, keyEntry(k.Namespace)); err != nil {
		t.Fatalf("AddKey(%s): %v", k.Key.Fingerprint, err)
	}
}

// heldKeys returns the keys of acme and fails the test when the read fails.
func heldKeys(t *testing.T, h KeyHarness) []registry.KeyRecord {
	t.Helper()

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	return got
}

func testKeyAddList(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	k := keyRecord(acme, keyOne)
	mustAddKey(t, h, k, f)

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil || len(got) != 1 {
		t.Fatalf("Keys = %+v, %v", got, err)
	}

	if got[0].Key.Fingerprint != k.Key.Fingerprint || !bytes.Equal(got[0].Key.Armored, k.Key.Armored) ||
		got[0].AddedBy != k.AddedBy || got[0].Namespace != acme {
		t.Fatalf("Keys = %+v, want %+v", got[0], k)
	}
}

func testKeyOrder(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)

	for _, n := range []int{keyFive, keyOne, keyThree} {
		mustAddKey(t, h, keyRecord(acme, n), f)
	}

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	fingerprints := make([]string, 0, len(got))
	for _, k := range got {
		fingerprints = append(fingerprints, k.Key.Fingerprint)
	}

	if !slices.IsSorted(fingerprints) || len(fingerprints) != keyThree {
		t.Fatalf("fingerprints = %v, want 3 in order", fingerprints)
	}
}

func testKeyCopy(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustAddKey(t, h, keyRecord(acme, keyOne), f)

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil || len(got) != 1 {
		t.Fatalf("Keys = %+v, %v", got, err)
	}

	got[0].Key.Armored[0] = 'X'

	again, err := h.Store.Keys(t.Context(), acme)
	if err != nil || len(again) != 1 || again[0].Key.Armored[0] != '-' {
		t.Fatalf("a change of a result changed the store: %+v, %v", again, err)
	}
}

func testKeyInvalid(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)

	bad := map[string]registry.KeyRecord{}

	k := keyRecord(acme, keyOne)
	k.Key.Fingerprint = "abc"
	bad["short fingerprint"] = k

	k = keyRecord(acme, keyOne)
	k.Key.Fingerprint = "../../" + keyFingerprint(keyOne)[6:]
	bad["path in the fingerprint"] = k

	k = keyRecord(acme, keyOne)
	k.Key.Armored = nil
	bad["no bytes"] = k

	k = keyRecord(acme, keyOne)
	k.Key.Armored = make([]byte, registry.MaxKeyBytes+1)
	bad["too many bytes"] = k

	k = keyRecord(acme, keyOne)
	k.AddedBy = ""
	bad["no registrar"] = k

	for name, rec := range bad {
		err := h.Store.AddKey(t.Context(), rec, f, keyEntry(acme))
		if !errors.Is(err, registry.ErrInvalid) {
			t.Fatalf("%s: AddKey = %v, want ErrInvalid", name, err)
		}
	}

	if n := len(h.Entries(t, acme)); n != 0 {
		t.Fatalf("%d audit entries after refusals, want 0", n)
	}
}

func testKeyStale(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)

	err := h.Store.AddKey(t.Context(), keyRecord(acme, keyOne), stale(f), keyEntry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("AddKey = %v, want ErrFenced", err)
	}

	if got := heldKeys(t, h); len(got) != 0 {
		t.Fatalf("a refused key was stored: %+v", got)
	}
}

func testKeyOtherFence(t *testing.T, h KeyHarness) {
	t.Helper()

	h.Namespace(t, acme)
	other := h.Namespace(t, beta)

	err := h.Store.AddKey(t.Context(), keyRecord(acme, keyOne), other, keyEntry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("AddKey = %v, want ErrFenced", err)
	}
}

func testKeyRepeat(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	k := keyRecord(acme, keyOne)
	mustAddKey(t, h, k, f)

	// The registrar and the time of a repeat do not count.
	again := k
	again.AddedBy = "user-2"
	mustAddKey(t, h, again, f)

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil || len(got) != 1 || got[0].AddedBy != publisher {
		t.Fatalf("Keys = %+v, %v; want the first registration unchanged", got, err)
	}

	if n := len(h.Entries(t, acme)); n != 1 {
		t.Fatalf("%d audit entries after a repeat, want 1", n)
	}
}

func testKeyConflict(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	k := keyRecord(acme, keyOne)
	mustAddKey(t, h, k, f)

	other := k
	other.Key.Armored = []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nother\n-----END PGP PUBLIC KEY BLOCK-----\n")

	err := h.Store.AddKey(t.Context(), other, f, keyEntry(acme))
	if !errors.Is(err, registry.ErrKeyExists) {
		t.Fatalf("AddKey = %v, want ErrKeyExists", err)
	}

	got := heldKeys(t, h)
	if len(got) != 1 || !bytes.Equal(got[0].Key.Armored, k.Key.Armored) {
		t.Fatalf("Keys = %+v, want the first key unchanged", got)
	}
}

func testKeyLimit(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)

	for n := range registry.MaxKeysPerNamespace {
		mustAddKey(t, h, keyRecord(acme, n), f)
	}

	err := h.Store.AddKey(t.Context(), keyRecord(acme, registry.MaxKeysPerNamespace), f,
		keyEntry(acme))
	if !errors.Is(err, registry.ErrKeyLimit) {
		t.Fatalf("AddKey = %v, want ErrKeyLimit", err)
	}

	if n := len(h.Entries(t, acme)); n != registry.MaxKeysPerNamespace {
		t.Fatalf("%d audit entries, want %d", n, registry.MaxKeysPerNamespace)
	}

	// A repeat of a held key is not a new key and passes at the limit.
	mustAddKey(t, h, keyRecord(acme, 0), f)
}

func testKeyNamespaces(t *testing.T, h KeyHarness) {
	t.Helper()

	fa := h.Namespace(t, acme)
	fb := h.Namespace(t, beta)

	mustAddKey(t, h, keyRecord(acme, keyOne), fa)
	mustAddKey(t, h, keyRecord(beta, keyTwo), fb)

	got, err := h.Store.Keys(t.Context(), beta)
	if err != nil || len(got) != 1 || got[0].Key.Fingerprint != keyFingerprint(keyTwo) {
		t.Fatalf("Keys(beta) = %+v, %v; want only the key of beta", got, err)
	}

	// The same key can be trusted by two namespaces.
	mustAddKey(t, h, keyRecord(beta, keyOne), fb)
}

func testKeyRemove(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustAddKey(t, h, keyRecord(acme, keyOne), f)
	mustAddKey(t, h, keyRecord(acme, keyTwo), f)

	err := h.Store.RemoveKey(t.Context(), acme, keyFingerprint(keyOne), f, keyEntry(acme))
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	got, err := h.Store.Keys(t.Context(), acme)
	if err != nil || len(got) != 1 || got[0].Key.Fingerprint != keyFingerprint(keyTwo) {
		t.Fatalf("Keys = %+v, %v; want only key 2", got, err)
	}
}

func testKeyRemoveMissing(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)

	err := h.Store.RemoveKey(t.Context(), acme, keyFingerprint(keyOne), f, keyEntry(acme))
	if !errors.Is(err, registry.ErrKeyNotFound) {
		t.Fatalf("RemoveKey = %v, want ErrKeyNotFound", err)
	}

	if n := len(h.Entries(t, acme)); n != 0 {
		t.Fatalf("%d audit entries after a refused remove, want 0", n)
	}
}

func testKeyRemoveStale(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustAddKey(t, h, keyRecord(acme, keyOne), f)

	err := h.Store.RemoveKey(t.Context(), acme, keyFingerprint(keyOne), stale(f), keyEntry(acme))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("RemoveKey = %v, want ErrFenced", err)
	}

	if got := heldKeys(t, h); len(got) != 1 {
		t.Fatalf("a refused remove changed the store: %+v", got)
	}
}

func testKeyAudit(t *testing.T, h KeyHarness) {
	t.Helper()

	f := h.Namespace(t, acme)
	mustAddKey(t, h, keyRecord(acme, keyOne), f)

	err := h.Store.RemoveKey(t.Context(), acme, keyFingerprint(keyOne), f, keyEntry(acme))
	if err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	got := h.Entries(t, acme)
	if len(got) != keyTwo {
		t.Fatalf("%d audit entries, want 2", len(got))
	}
}

func testKeyListBad(t *testing.T, h KeyHarness) {
	t.Helper()

	if _, err := h.Store.Keys(t.Context(), "../acme"); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Keys = %v, want ErrInvalid", err)
	}
}
