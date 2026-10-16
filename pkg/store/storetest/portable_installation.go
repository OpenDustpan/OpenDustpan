package storetest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/secret"
)

// installationKeyScope is the installation scope that the cases of this file
// seal a value in.
const installationKeyScope = "sign-in-corp"

// otherKeyWrapper is a key-encryption key with the ID of another key: the
// restore check finds the wrapper ID in the backup, and the key does not open
// the data key.
type otherKeyWrapper struct {
	secret.Wrapper

	id string
}

// ID implements secret.Wrapper.
func (w otherKeyWrapper) ID() string { return w.id }

// keyPeekerOnly hides every method of a store but PeekPortableKey, as a store
// adapter that cannot read installation keys.
type keyPeekerOnly struct {
	backup.KeyPeeker
}

// newBuiltinKey returns a built-in key-encryption key made from seed.
func newBuiltinKey(t *testing.T, seed byte) *secret.BuiltinWrapper {
	t.Helper()

	w, err := secret.NewBuiltinWrapper(bytes.Repeat([]byte{seed}, 32))
	if err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	return w
}

// sealInstallationValue seals one value in an installation scope of s under
// wrapper, so s holds one installation key of wrapper.
func sealInstallationValue(t *testing.T, s PortableStore, wrapper secret.Wrapper) {
	t.Helper()

	keys, ok := s.(secret.InstallationKeyStore)
	if !ok {
		t.Fatalf("store %T does not implement secret.InstallationKeyStore", s)
	}

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: keys, Wrapper: wrapper, Now: now})
	if err != nil {
		t.Fatalf("installation secrets: %v", err)
	}

	if _, err := secrets.Seal(t.Context(), installationKeyScope, []byte("client secret")); err != nil {
		t.Fatalf("seal an installation value: %v", err)
	}
}

// testPortablePeekInstallationKey peeks the installation key of the seed from
// the installation_secret_keys file of the export through an empty store: the
// key must equal the stored row, a wrapper with no key is ErrNoDataKey, and
// the peeking store must stay empty.
func testPortablePeekInstallationKey(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	seedBackupStore(t, src)
	sink, _, files := exportPortable(t, src)
	keyFile := portableFileNamed(t, files, "installation_secret_keys")

	keys, ok := src.(secret.InstallationKeyStore)
	if !ok {
		t.Fatalf("store %T does not implement secret.InstallationKeyStore", src)
	}

	want, err := keys.GetInstallationKey(t.Context(), installationKeyScope)
	if err != nil {
		t.Fatalf("get installation key: %v", err)
	}

	dst := newStore(t)

	peeker, ok := dst.(backup.InstallationKeyPeeker)
	if !ok {
		t.Fatalf("store %T does not implement backup.InstallationKeyPeeker", dst)
	}

	got, err := peeker.PeekPortableInstallationKey(t.Context(), sink.read, keyFile, want.WrapperID)
	if err != nil {
		t.Fatalf("peek installation key: %v", err)
	}

	if got.Scope != want.Scope || got.WrapperID != want.WrapperID ||
		!got.CreatedAt.Equal(want.CreatedAt) || !bytes.Equal(got.Ciphertext, want.Ciphertext) {
		t.Fatalf("peeked installation key = %+v, want %+v", got, want)
	}

	_, err = peeker.PeekPortableInstallationKey(t.Context(), sink.read, keyFile, "kek-unknown")
	if !errors.Is(err, backup.ErrNoDataKey) || !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("peek installation key of an unknown wrapper: err = %v, want ErrNoDataKey", err)
	}

	assertBackupEmpty(t, dst)
}

// testPortableKeyOnlyInstallation exports a store whose key-encryption key
// wraps installation keys only: the summary names the key, and the restore
// check opens an installation key with it.
func testPortableKeyOnlyInstallation(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	key := newBuiltinKey(t, 1)
	sealInstallationValue(t, src, key)

	sink, sum, files := exportPortable(t, src)
	if !slices.Equal(sum.WrapperIDs, []string{key.ID()}) {
		t.Fatalf("summary wrapper IDs = %v, want [%s]", sum.WrapperIDs, key.ID())
	}

	dst := newStore(t)

	if err := backup.OpenPortableKey(t.Context(), dst, sink.read, files, key); err != nil {
		t.Fatalf("open the key of a backup with installation keys only: %v", err)
	}

	assertBackupEmpty(t, dst)
}

// testPortableKeyBothKinds exports a store with a namespace key of one
// wrapper and an installation key of another: the summary names both, and the
// restore check opens the installation key when secret_keys holds no key of
// the key-encryption key.
func testPortableKeyBothKinds(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	f := newFixture(t, src)
	seedBackupKey(t, src, mustGetNamespace(t, src, "acme"), mustGetStack(t, src, f.ref), "kek-1")

	key := newBuiltinKey(t, 2)
	sealInstallationValue(t, src, key)

	sink, sum, files := exportPortable(t, src)

	want := []string{"kek-1", key.ID()}
	slices.Sort(want)

	if !slices.Equal(sum.WrapperIDs, want) {
		t.Fatalf("summary wrapper IDs = %v, want %v", sum.WrapperIDs, want)
	}

	dst := newStore(t)

	if err := backup.OpenPortableKey(t.Context(), dst, sink.read, files, key); err != nil {
		t.Fatalf("open the installation key of a backup with both kinds of keys: %v", err)
	}

	assertBackupEmpty(t, dst)
}

// testPortableKeyWrongKey refuses a key-encryption key with the ID of the key
// of the installation keys and other key bytes, before any write. A store
// that cannot read installation keys refuses the backup too.
func testPortableKeyWrongKey(t *testing.T, newStore PortableFactory) {
	t.Helper()

	src := newStore(t)
	key := newBuiltinKey(t, 3)
	sealInstallationValue(t, src, key)

	sink, _, files := exportPortable(t, src)
	dst := newStore(t)
	wrong := otherKeyWrapper{Wrapper: newBuiltinKey(t, 4), id: key.ID()}

	err := backup.OpenPortableKey(t.Context(), dst, sink.read, files, wrong)
	if !errors.Is(err, secret.ErrDecrypt) || !strings.Contains(err.Error(), "DUSTPAN_KEY_ENCRYPTION_KEY") {
		t.Fatalf("open with the wrong key: err = %v, want ErrDecrypt that names DUSTPAN_KEY_ENCRYPTION_KEY", err)
	}

	err = backup.OpenPortableKey(t.Context(), keyPeekerOnly{KeyPeeker: dst}, sink.read, files, key)
	if !errors.Is(err, backup.ErrKeyUnchecked) {
		t.Fatalf("open through a store that cannot read installation keys: err = %v, want ErrKeyUnchecked", err)
	}

	assertBackupEmpty(t, dst)
}
