package secrettest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// MemorySealer is a secret.ItemSealer for the tests of other packages that
// use stores with no item key records. It keeps each item key in memory and
// seals content with AES-GCM, with the reference and the binding as the
// associated data, so a test can check that stored bytes hold no plaintext
// and that a payload of another item or binding does not open. It writes no
// record through the transaction that NewItemKey gets, and accepts a nil
// transaction. A store adapter runs secret.ItemKeySealer in its own suites.
type MemorySealer struct {
	keys map[secret.ItemRef]*memoryKey
	mu   sync.Mutex
}

// memoryKey is one item key of a MemorySealer.
type memoryKey struct {
	aead     cipher.AEAD
	record   secret.ItemKey
	shredded bool
}

var _ secret.ItemSealer = (*MemorySealer)(nil)

// NewMemorySealer returns an empty MemorySealer.
func NewMemorySealer() *MemorySealer {
	return &MemorySealer{keys: map[secret.ItemRef]*memoryKey{}, mu: sync.Mutex{}}
}

// NewItemKey implements secret.ItemSealer. It returns namespace.ErrFenced when
// fence names another namespace than ref, and ErrShredded for a shredded key.
func (m *MemorySealer) NewItemKey(
	_ context.Context, _ secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence,
) (secret.ItemKey, error) {
	if fence.Namespace != ref.Scope.Namespace {
		return secret.ItemKey{}, namespace.ErrFenced
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if k, ok := m.keys[ref]; ok {
		if k.shredded {
			return k.record, secret.ErrShredded
		}

		return k.record, nil
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return secret.ItemKey{}, fmt.Errorf("item key: %w", err)
	}

	block, err := aes.NewCipher(raw)
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("item key: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("item key: %w", err)
	}

	rec := secret.ItemKey{
		CreatedAt: time.Now().UTC(), ShreddedAt: time.Time{}, Ref: ref, ID: secret.NewItemKeyID(),
		WrapperID: "memory", Blob: secret.ItemKeyBlobInfo{MD5: [16]byte{}, Size: 0, Version: 0, Attempt: ""},
	}
	m.keys[ref] = &memoryKey{aead: aead, record: rec, shredded: false}

	return rec, nil
}

// Shred marks the item key of ref as shredded. Seal and open then fail with
// secret.ErrShredded.
func (m *MemorySealer) Shred(ref secret.ItemRef) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if k, ok := m.keys[ref]; ok {
		k.shredded = true
	}
}

// Has reports whether the sealer holds an item key for ref.
func (m *MemorySealer) Has(ref secret.ItemRef) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.keys[ref]

	return ok
}

// memoryAAD binds sealed content to its reference and its binding.
func memoryAAD(ref secret.ItemRef, binding []byte) []byte {
	return append([]byte(ref.String()+"\x00"), binding...)
}

// SealContent implements secret.ItemSealer.
func (m *MemorySealer) SealContent(
	_ context.Context, ref secret.ItemRef, binding []byte, r io.Reader,
) (io.Reader, error) {
	aead, err := m.key(ref)
	if err != nil {
		return nil, err
	}

	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read content: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	return bytes.NewReader(aead.Seal(nonce, nonce, plain, memoryAAD(ref, binding))), nil
}

// OpenContent implements secret.ItemSealer. It returns secret.ErrDecrypt for
// content of another item or binding, or changed content.
func (m *MemorySealer) OpenContent(
	_ context.Context, ref secret.ItemRef, binding []byte, r io.Reader,
) (io.ReadCloser, error) {
	aead, err := m.key(ref)
	if err != nil {
		return nil, err
	}

	sealed, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read content: %w", err)
	}

	if len(sealed) < aead.NonceSize() {
		return nil, secret.ErrDecrypt
	}

	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], memoryAAD(ref, binding))
	if err != nil {
		return nil, secret.ErrDecrypt
	}

	return io.NopCloser(bytes.NewReader(plain)), nil
}

// SealContents implements secret.ItemSealer: it seals each part as
// SealContent does.
func (m *MemorySealer) SealContents(
	ctx context.Context, ref secret.ItemRef, parts []secret.ContentPart,
) ([][]byte, error) {
	out := make([][]byte, 0, len(parts))

	for i := range parts {
		r, err := m.SealContent(ctx, ref, parts[i].Binding, bytes.NewReader(parts[i].Data))
		if err != nil {
			return nil, err
		}

		sealed, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("read sealed part %d: %w", i, err)
		}

		out = append(out, sealed)
	}

	return out, nil
}

// OpenContents implements secret.ItemSealer: it opens each part as
// OpenContent does.
func (m *MemorySealer) OpenContents(
	ctx context.Context, ref secret.ItemRef, parts []secret.ContentPart,
) ([][]byte, error) {
	out := make([][]byte, 0, len(parts))

	for i := range parts {
		r, err := m.OpenContent(ctx, ref, parts[i].Binding, bytes.NewReader(parts[i].Data))
		if err != nil {
			return nil, err
		}

		plain, err := io.ReadAll(r)
		if err = errors.Join(err, r.Close()); err != nil {
			return nil, fmt.Errorf("read opened part %d: %w", i, err)
		}

		out = append(out, plain)
	}

	return out, nil
}

// key returns the item key of ref, with the errors of the port.
func (m *MemorySealer) key(ref secret.ItemRef) (cipher.AEAD, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[ref]

	switch {
	case !ok:
		return nil, secret.ErrKeyNotFound
	case k.shredded:
		return nil, secret.ErrShredded
	default:
		return k.aead, nil
	}
}
