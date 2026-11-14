package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The vectors come from an independent Python run (RFC 5869 HKDF written out
// with hmac, and AES-GCM from the cryptography package), not from this code.
const (
	katScopeAAD = "6f70656e6475737470616e2f7365637265742f76310061636d650073636f70652d31"
	katSubkey   = "26d1d015e83aa3d477c58429cc50f07a45ae3a662b7f2e43bb2e6023e51f3f2f"
	katAAD      = "6f70656e6475737470616e2f7365637265742f76310061636d650073636f70652d31007303404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f0673762d6b6174000000000000000001"
	katNonce    = "000000000000000000000000"
	katCipher   = "284379de39830200777e4eddaabc2d801d2700f1167084e0b08d3666a78b8f48437f"
)

// mustHex decodes a hex vector, failing the test on a malformed one.
func mustHex(t *testing.T, s string) []byte {
	t.Helper()

	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode vector: %v", err)
	}

	return b
}

// katScope returns the scope of the known-answer vectors.
func katScope(t *testing.T) Scope {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	scope, err := NewScope(ns, "scope-1")
	if err != nil {
		t.Fatalf("new scope: %v", err)
	}

	return scope
}

// TestStreamFormatKnownAnswer pins the version 3 key derivation, the chunk
// associated data, the nonce and the sealed bytes of one final chunk.
func TestStreamFormatKnownAnswer(t *testing.T) {
	t.Parallel()

	dek := make([]byte, dataKeyBytes)
	for i := range dek {
		dek[i] = byte(i)
	}

	salt := make([]byte, streamSaltSize)
	for i := range salt {
		salt[i] = 0x40 + byte(i)
	}

	scope := katScope(t)
	binding := []byte("sv-kat")
	header := append([]byte{streamVersion}, salt...)

	if got := scope.aad(); !bytes.Equal(got, mustHex(t, katScopeAAD)) {
		t.Fatalf("scope label = %x", got)
	}

	subkey, err := deriveStreamKey(dek, salt)
	if err != nil {
		t.Fatalf("derive stream key: %v", err)
	}

	if !bytes.Equal(subkey, mustHex(t, katSubkey)) {
		t.Fatalf("stream key = %x", subkey)
	}

	aad := streamAADv3(scope, header, binding, 0, streamFinal)
	if !bytes.Equal(aad, mustHex(t, katAAD)) {
		t.Fatalf("chunk associated data = %x", aad)
	}

	nonce := streamNonce(0)
	if !bytes.Equal(nonce, mustHex(t, katNonce)) {
		t.Fatalf("nonce = %x", nonce)
	}

	aead, err := newAEAD(subkey)
	if err != nil {
		t.Fatalf("new AEAD: %v", err)
	}

	sealed := aead.Seal(nil, nonce, []byte("known answer chunk"), aad)
	if !bytes.Equal(sealed, mustHex(t, katCipher)) {
		t.Fatalf("sealed chunk = %x", sealed)
	}
}

// TestOpenLegacyStream proves a version 2 stream, which the previous format
// sealed, still opens. Nothing writes the legacy format.
func TestOpenLegacyStream(t *testing.T) {
	t.Parallel()

	dek := make([]byte, dataKeyBytes)
	for i := range dek {
		dek[i] = 0xa0 + byte(i)
	}

	scope := katScope(t)
	prefix := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	plain := []byte("state sealed by the previous format")

	block, err := aes.NewCipher(dek)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}

	sealed := aead.Seal(nil, streamNonceOfLegacy(prefix, 0), plain, streamAAD(scope, prefix, 0, streamFinal))

	stream := make([]byte, 0, 1+len(prefix)+4+1+len(sealed))
	stream = append(stream, streamLegacyVersion)
	stream = append(stream, prefix...)

	var length [4]byte
	//nolint:gosec // The sealed chunk is one chunk, far below 4 GiB.
	binary.BigEndian.PutUint32(length[:], uint32(len(sealed)))
	stream = append(stream, length[:]...)
	stream = append(stream, byte(streamFinal))
	stream = append(stream, sealed...)

	reader := &openReader{
		mu: sync.Mutex{}, closed: false, reading: false,
		currentOnly: false,
		dek:         bytes.Clone(dek), scope: scope, binding: nil, src: bytes.NewReader(stream),
		aead: nil, version: 0, nonce: nil, header: nil, counter: 0,
		pending: nil, final: false, sealedBuf: nil, plainBuf: nil,
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("open legacy stream: %v", err)
	}

	if !bytes.Equal(got, plain) {
		t.Fatalf("legacy stream opened %q", got)
	}
}

// TestSealChunkLimit proves a seal refuses the chunk after streamMaxChunks,
// so no nonce of one stream key repeats.
func TestSealChunkLimit(t *testing.T) {
	t.Parallel()

	key := make([]byte, dataKeyBytes)

	aead, err := newAEAD(key)
	if err != nil {
		t.Fatalf("new AEAD: %v", err)
	}

	reader := &sealReader{
		aead: aead, scope: katScope(t), header: []byte{streamVersion}, binding: []byte("sv-limit"),
		src: bytes.NewReader([]byte("x")), counter: streamMaxChunks,
		pending: nil, done: false, srcDone: false, plainBuf: nil, sealBuf: nil, frameBuf: nil,
	}

	if err := reader.sealNext(); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("sealNext at the limit: err = %v, want ErrStreamLimit", err)
	}
}
