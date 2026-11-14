package secret

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// streamLegacyVersion is the format byte of the streams that the
	// previous format sealed. They still open. Nothing writes them.
	streamLegacyVersion byte = 2
	// streamVersion is the format byte of a sealed stream. Each stream has a
	// key of its own, derived from the data key and a random salt in the
	// header, so the chunk counter can be the whole nonce.
	streamVersion byte = 3
	// streamChunkPlain bounds one plaintext chunk of a sealed stream.
	streamChunkPlain = 64 << 10
	// streamSaltSize is the random salt of a stream, stored in its header.
	streamSaltSize = 32
	// streamLegacyNonceSize is the random prefix of a legacy stream.
	streamLegacyNonceSize = 8
	// streamFrameHeader is the 4-byte ciphertext length and the final flag.
	streamFrameHeader = 5
	// streamGCMNonceSize is the GCM nonce of one chunk.
	streamGCMNonceSize = 12
	// streamMaxChunks bounds the chunks of one stream. Every chunk counter
	// stays below this bound, so no nonce repeats under one stream key.
	streamMaxChunks = uint64(1) << 32
	// streamMaxBinding bounds the binding of a stream, which is a length
	// prefix of one byte in the associated data.
	streamMaxBinding = 255
	// streamAADSep separates the scope binding from the stream binding in
	// the associated data of a chunk.
	streamAADSep = 0x00
	// streamAADKind names stream chunks in their associated data.
	streamAADKind = 's'
	// streamKeyInfo is the HKDF info of a stream key. It names the purpose
	// only. The associated data of each chunk binds the stream to its scope,
	// or to its item and the ID of the item key record.
	streamKeyInfo = "opendustpan/stream-key/v1"
)

// ErrStreamLimit reports a stream that would exceed streamMaxChunks chunks.
var ErrStreamLimit = errors.New("sealed stream exceeds the chunk limit")

// ErrInvalidBinding reports a stream binding that is empty or too long.
var ErrInvalidBinding = errors.New("invalid stream binding")

// ErrStreamClosed reports a read of an opened stream after its Close.
var ErrStreamClosed = errors.New("sealed stream reader is closed")

// streamLast marks whether a chunk ends the stream. It is a byte, not a
// flag, so the chunk position and its end travel as one value.
type streamLast byte

const (
	// streamMore marks a chunk with a successor.
	streamMore streamLast = 0
	// streamFinal marks the last chunk of a stream.
	streamFinal streamLast = 1
)

// streamAAD binds one chunk of a legacy stream to its scope, stream,
// position and end.
func streamAAD(scope aadSource, streamNonce []byte, counter uint64, last streamLast) []byte {
	aad := make([]byte, 0, len(scope.aad())+1+len(streamNonce)+8+1)
	aad = append(aad, scope.aad()...)
	aad = append(aad, streamAADSep, streamAADKind)
	aad = append(aad, streamNonce...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], counter)
	aad = append(aad, n[:]...)
	aad = append(aad, byte(last))

	return aad
}

// streamAADv3 binds one chunk to its scope, the stream header, the binding,
// its position and its end. The binding is length-prefixed.
func streamAADv3(scope aadSource, header, binding []byte, counter uint64, last streamLast) []byte {
	aad := make([]byte, 0, len(scope.aad())+2+len(header)+1+len(binding)+8+1)
	aad = append(aad, scope.aad()...)
	aad = append(aad, streamAADSep, streamAADKind)
	aad = append(aad, header...)
	//nolint:gosec // The binding length is checked against streamMaxBinding before use.
	aad = append(aad, byte(len(binding)))
	aad = append(aad, binding...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], counter)
	aad = append(aad, n[:]...)
	aad = append(aad, byte(last))

	return aad
}

// streamNonceOfLegacy returns the GCM nonce of one chunk of a legacy stream:
// the stream prefix and the 32-bit chunk counter.
func streamNonceOfLegacy(streamNonce []byte, counter uint64) []byte {
	nonce := make([]byte, streamGCMNonceSize)
	copy(nonce, streamNonce)
	// The counter fits in 32 bits for every state the size limit admits;
	// the high bits stay zero.
	//nolint:gosec // The counter fits in 32 bits for every state the size limit admits.
	binary.BigEndian.PutUint32(nonce[8:], uint32(counter))

	return nonce
}

// streamNonce returns the GCM nonce of one chunk of a stream: the chunk
// counter in the last 8 bytes. Each stream has its own key, so the counter
// is unique under that key.
func streamNonce(counter uint64) []byte {
	nonce := make([]byte, streamGCMNonceSize)
	binary.BigEndian.PutUint64(nonce[4:], counter)

	return nonce
}

// deriveStreamKey returns the key of one stream: HKDF-SHA-256 over the data
// key, with the stream salt and the purpose label streamKeyInfo. The key does
// not bind the scope: the associated data of each chunk does.
func deriveStreamKey(dek, salt []byte) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, dek, salt, streamKeyInfo, dataKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("derive stream key: %w", err)
	}

	return key, nil
}

// checkBinding refuses a binding that is empty or too long.
func checkBinding(binding []byte) error {
	if len(binding) == 0 || len(binding) > streamMaxBinding {
		return fmt.Errorf("%w: length %d", ErrInvalidBinding, len(binding))
	}

	return nil
}

// SealStream seals the plaintext in r with the data key of scope, creating
// the key on first use, and returns a reader of the sealed stream. The
// binding is authenticated with every chunk, so the stream opens only with
// the same binding. The caller streams the result without holding either
// side whole.
func (s *Service) SealStream(ctx context.Context, scope Scope, binding []byte, r io.Reader) (io.Reader, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	dek, err := s.unwrapDataKey(ctx, scope, true)
	if err != nil {
		return nil, err
	}

	defer clear(dek)

	return sealStreamWith(dek, scope, binding, r)
}

// sealStreamWith seals r with a stream key that it derives from the key dek
// and a random salt. The associated data of every chunk holds the data of
// scope and the binding. The caller owns dek and clears it. The binding must
// pass checkBinding.
func sealStreamWith(dek []byte, scope aadSource, binding []byte, r io.Reader) (io.Reader, error) {
	salt := make([]byte, streamSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate stream salt: %w", err)
	}

	key, err := deriveStreamKey(dek, salt)
	if err != nil {
		return nil, err
	}

	defer clear(key)

	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	header := make([]byte, 0, 1+streamSaltSize)
	header = append(header, streamVersion)
	header = append(header, salt...)

	return &sealReader{
		aead: aead, scope: scope, header: header, binding: bytes.Clone(binding), src: r,
		counter: 0, pending: nil, done: false, srcDone: false,
		plainBuf: nil, sealBuf: nil, frameBuf: nil,
	}, nil
}

// OpenStream opens a stream sealed by SealStream for the same scope and
// binding, and returns a reader of the plaintext. Any cut, reorder,
// extension or tamper fails with ErrDecrypt, with no key, scope or content in
// the error. A legacy stream opens for its scope and does not check the
// binding, because the legacy format did not bind it. The reader can hold the
// raw data key until it reads the stream header. The caller closes the
// reader: Close clears the key, also when the reader was never read.
func (s *Service) OpenStream(ctx context.Context, scope Scope, binding []byte, r io.Reader) (io.ReadCloser, error) {
	if err := checkBinding(binding); err != nil {
		return nil, err
	}

	dek, err := s.unwrapDataKey(ctx, scope, false)
	if err != nil {
		return nil, err
	}

	return openStreamWith(dek, scope, binding, r, false), nil
}

// openStreamWith opens a stream with the key dek. The reader takes dek and
// clears it after the header, or at Close when no header was read. When
// current is true, only the current stream format opens: the legacy format
// has no binding, and an item never had it.
func openStreamWith(dek []byte, scope aadSource, binding []byte, r io.Reader, current bool) *openReader {
	return &openReader{
		mu: sync.Mutex{}, closed: false, reading: false,
		dek: dek, scope: scope, binding: bytes.Clone(binding), src: r, currentOnly: current,
		aead: nil, version: 0, nonce: nil, header: nil, counter: 0,
		pending: nil, final: false, sealedBuf: nil, plainBuf: nil,
	}
}

// bindingOf returns the stream binding of a state version ID. The ID must
// be set: a stream without one would open under any state version.
func bindingOf(versionID string) ([]byte, error) {
	if versionID == "" {
		return nil, fmt.Errorf("%w: empty state version ID", ErrInvalidBinding)
	}

	return []byte(versionID), nil
}

// SealState seals the plaintext in r with the data key of the workspace
// scope (the namespace and the workspace ID), bound to the state version
// versionID, creating the key on first use.
func (s *Service) SealState(
	ctx context.Context, ns namespace.Name, workspaceID, versionID string, r io.Reader,
) (io.Reader, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	binding, err := bindingOf(versionID)
	if err != nil {
		return nil, err
	}

	return s.SealStream(ctx, scope, binding, r)
}

// OpenState opens a stream sealed by SealState for the same workspace scope
// and state version. The caller closes the reader, as for OpenStream.
func (s *Service) OpenState(
	ctx context.Context, ns namespace.Name, workspaceID, versionID string, r io.Reader,
) (io.ReadCloser, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	binding, err := bindingOf(versionID)
	if err != nil {
		return nil, err
	}

	return s.OpenStream(ctx, scope, binding, r)
}

// SealOutputs seals one outputs document with the data key of the workspace
// scope, bound to the state version versionID, creating the key on first
// use. It uses the stream seal, so a document up to the workspace outputs
// limit seals whatever its size.
func (s *Service) SealOutputs(
	ctx context.Context, ns namespace.Name, workspaceID, versionID string, plain []byte,
) ([]byte, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	binding, err := bindingOf(versionID)
	if err != nil {
		return nil, err
	}

	sealedReader, err := s.SealStream(ctx, scope, binding, bytes.NewReader(plain))
	if err != nil {
		return nil, err
	}

	sealed, err := io.ReadAll(sealedReader)
	if err != nil {
		return nil, fmt.Errorf("seal outputs: %w", err)
	}

	return sealed, nil
}

// OpenOutputs opens one outputs document sealed by SealOutputs for the same
// state version.
func (s *Service) OpenOutputs(
	ctx context.Context, ns namespace.Name, workspaceID, versionID string, sealed []byte,
) ([]byte, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	binding, err := bindingOf(versionID)
	if err != nil {
		return nil, err
	}

	opened, err := s.OpenStream(ctx, scope, binding, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}

	plain, err := io.ReadAll(opened)
	if err = errors.Join(err, opened.Close()); err != nil {
		return nil, fmt.Errorf("open outputs: %w", err)
	}

	return plain, nil
}

// sealReader seals one stream chunk at a time.
type sealReader struct {
	aead    cipher.AEAD
	scope   aadSource
	header  []byte
	binding []byte
	src     io.Reader

	counter uint64
	pending []byte
	done    bool
	srcDone bool

	// plainBuf, sealBuf and frameBuf are reused for every chunk. A chunk
	// allocates them once, so a large upload does not churn the heap by three
	// chunk-sized buffers per chunk. The collector counts that churn as live
	// until its next cycle, and the count grows with the number of Ps.
	plainBuf []byte
	sealBuf  []byte
	frameBuf []byte
}

// Read seals the next chunk when its buffer runs dry.
func (r *sealReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if r.done {
			return 0, io.EOF
		}

		if err := r.sealNext(); err != nil {
			return 0, err
		}
	}

	n := copy(p, r.pending)
	r.pending = r.pending[n:]

	return n, nil
}

// sealNext reads one plaintext chunk and buffers its sealed frame. The
// buffers are reused, so pending aliases frameBuf until the next chunk; Read
// drains pending before sealNext runs again.
func (r *sealReader) sealNext() error {
	if r.counter >= streamMaxChunks {
		return ErrStreamLimit
	}

	var plain []byte

	if !r.srcDone {
		if r.plainBuf == nil {
			r.plainBuf = make([]byte, streamChunkPlain)
		}

		buf := r.plainBuf

		n, err := io.ReadFull(r.src, buf)
		switch err {
		case nil:
			plain = buf[:n]
		case io.EOF:
			plain = nil
			r.srcDone = true
		case io.ErrUnexpectedEOF:
			plain = buf[:n]
			r.srcDone = true
		default:
			return fmt.Errorf("read sealed stream source: %w", err)
		}
	}

	final := r.srcDone

	last := streamMore
	if final {
		last = streamFinal
	}

	aad := streamAADv3(r.scope, r.header, r.binding, r.counter, last)
	r.sealBuf = r.aead.Seal(r.sealBuf[:0], streamNonce(r.counter), plain, aad)
	sealed := r.sealBuf

	var ln [4]byte
	// A sealed chunk holds one plaintext chunk plus the GCM overhead, so
	// its length always fits the 4-byte frame field.
	//nolint:gosec // A sealed chunk holds one plaintext chunk plus the GCM overhead.
	binary.BigEndian.PutUint32(ln[:], uint32(len(sealed)))

	pending := r.frameBuf[:0]

	if r.counter == 0 {
		pending = append(pending, r.header...)
	}

	pending = append(pending, ln[:]...)
	pending = append(pending, byte(last))
	pending = append(pending, sealed...)
	r.frameBuf = pending
	r.pending = pending

	r.counter++

	if final {
		r.done = true
	}

	return nil
}

// openReader opens one sealed stream chunk at a time. It is not safe for
// concurrent reads, but Close can run at the same time as a Read.
type openReader struct {
	// mu guards dek, closed and reading. Close changes them.
	mu     sync.Mutex
	closed bool
	// reading is true while a Read runs. Close then leaves the stream
	// state to that Read, which releases it when it returns.
	reading bool
	// currentOnly refuses the legacy stream format.
	currentOnly bool
	// dek is the raw key until the header is read or until Close. Then it is
	// cleared and nil.
	dek     []byte
	aead    cipher.AEAD
	scope   aadSource
	binding []byte
	src     io.Reader

	// version is the format byte of the stream, set by its header.
	version byte
	// nonce is the random prefix of a legacy stream.
	nonce []byte
	// header is the format byte and the salt of a stream.
	header []byte

	counter uint64
	pending []byte
	final   bool

	// sealedBuf and plainBuf are reused for every frame, as in sealReader.
	// A frame's plaintext aliases plainBuf until Read drains pending, and
	// openNext runs only after that.
	sealedBuf []byte
	plainBuf  []byte
}

// Close clears the raw key that the reader still holds, also when the reader
// was never read. A Read after Close returns ErrStreamClosed. Close does not
// close the source. A second Close does nothing.
func (r *openReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	clear(r.dek)
	r.dek = nil

	if !r.reading {
		r.release()
	}

	return nil
}

// Read opens the next chunk when its buffer runs dry.
func (r *openReader) Read(p []byte) (int, error) {
	if !r.begin() {
		return 0, ErrStreamClosed
	}

	defer r.end()

	for len(r.pending) == 0 {
		done, err := r.openNext()
		if err != nil {
			return 0, err
		}

		if done {
			return 0, io.EOF
		}
	}

	n := copy(p, r.pending)
	r.pending = r.pending[n:]

	return n, nil
}

// dropKey clears the raw key that the reader still holds. A stream whose
// header fails never needs it.
func (r *openReader) dropKey() {
	r.mu.Lock()
	defer r.mu.Unlock()

	clear(r.dek)
	r.dek = nil
}

// begin marks a Read as running. It reports false after Close.
func (r *openReader) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		r.release()

		return false
	}

	r.reading = true

	return true
}

// end marks the Read as done. When Close ran during the Read, end releases
// the stream state that Close left to it.
func (r *openReader) end() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reading = false

	if r.closed {
		r.release()
	}
}

// release drops the stream cipher, which holds the key schedule, and clears
// and drops the buffers of sealed and plain bytes. The caller holds mu, and no
// Read runs.
func (r *openReader) release() {
	r.aead = nil
	r.pending = nil
	clear(r.plainBuf[:cap(r.plainBuf)])
	r.plainBuf = nil
	clear(r.sealedBuf[:cap(r.sealedBuf)])
	r.sealedBuf = nil
}

// takeKey returns the raw key and removes it from the reader, so that only
// the caller holds it and clears it. It fails after Close.
func (r *openReader) takeKey() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Defense in depth: Read checks for Close before each chunk too. This
	// check stays so that a Close that runs while Read waits for the header
	// bytes leaves no key to use.
	if r.closed || r.dek == nil {
		return nil, ErrStreamClosed
	}

	dek := r.dek
	r.dek = nil

	return dek, nil
}

// openNext reads one sealed frame and buffers its plaintext. It reports
// done when the stream ends after its final chunk.
func (r *openReader) openNext() (bool, error) {
	if r.final {
		return r.checkTrailing()
	}

	if r.aead == nil {
		if err := r.readHeader(); err != nil {
			return false, err
		}
	}

	return r.readFrame()
}

// checkTrailing reports done when the stream ends after its final chunk. Any
// further byte is an extension. A zero-byte read retries, so a legal empty
// read cannot fail a valid download.
func (r *openReader) checkTrailing() (bool, error) {
	var probe [1]byte

	for {
		n, err := r.src.Read(probe[:])

		switch {
		case n > 0:
			return false, fmt.Errorf("%w: trailing content", ErrDecrypt)
		case err == nil:
			continue
		case err != io.EOF:
			return false, fmt.Errorf("read sealed stream: %w", err)
		default:
			return true, nil
		}
	}
}

// readHeader reads the format byte of the stream and what follows it, and
// sets up the key of the stream. A legacy stream uses the data key itself. It
// takes the raw key only after the header bytes are read, so a Close while
// the source blocks clears the key at once. The key is cleared when
// readHeader returns, also on an error.
func (r *openReader) readHeader() error {
	version, extra, err := r.readHeaderBytes()
	if err != nil {
		r.dropKey()

		return err
	}

	dek, err := r.takeKey()
	if err != nil {
		return err
	}

	defer clear(dek)

	var key []byte

	switch version {
	case streamVersion:
		key, err = deriveStreamKey(dek, extra)
		if err != nil {
			return fmt.Errorf("%w: sealed stream header", ErrDecrypt)
		}

		defer clear(key)

		r.header = append([]byte{streamVersion}, extra...)
	case streamLegacyVersion:
		key = dek
		r.nonce = extra
	default:
		// Defense in depth: readHeaderBytes refuses an unknown format byte
		// too. This check stays so that no key is set up for a format that
		// this reader does not know.
		return fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return err
	}

	r.aead = aead
	r.version = version

	return nil
}

// readHeaderBytes reads the format byte of the stream and the salt or the
// legacy nonce that follows it. It refuses an unknown format, and the legacy
// format when the reader opens only the current format.
func (r *openReader) readHeaderBytes() (byte, []byte, error) {
	var version [1]byte
	if _, err := io.ReadFull(r.src, version[:]); err != nil {
		return 0, nil, fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	var size int

	switch {
	case version[0] == streamVersion:
		size = streamSaltSize
	case version[0] == streamLegacyVersion && !r.currentOnly:
		size = streamLegacyNonceSize
	default:
		return 0, nil, fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	extra := make([]byte, size)
	if _, err := io.ReadFull(r.src, extra); err != nil {
		return 0, nil, fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	return version[0], extra, nil
}

// readFrame reads one sealed frame and buffers its plaintext.
func (r *openReader) readFrame() (bool, error) {
	if r.counter >= streamMaxChunks {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	frame := make([]byte, streamFrameHeader)
	if _, err := io.ReadFull(r.src, frame); err != nil {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	length := binary.BigEndian.Uint32(frame[:4])

	if frame[4] != 0 && frame[4] != 1 {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	last := streamMore
	if frame[4] == 1 {
		last = streamFinal
	}

	// A sealed frame holds one plaintext chunk plus the GCM overhead, which
	// is non-negative, so the bound fits the comparison.
	//nolint:gosec // The GCM overhead is non-negative, so the bound fits the comparison.
	if uint64(length) > uint64(streamChunkPlain)+uint64(r.aead.Overhead()) {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	if cap(r.sealedBuf) < int(length) {
		r.sealedBuf = make([]byte, length)
	}

	sealed := r.sealedBuf[:length]
	if _, err := io.ReadFull(r.src, sealed); err != nil {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	var (
		nonce []byte
		aad   []byte
	)

	if r.version == streamVersion {
		nonce = streamNonce(r.counter)
		aad = streamAADv3(r.scope, r.header, r.binding, r.counter, last)
	} else {
		nonce = streamNonceOfLegacy(r.nonce, r.counter)
		aad = streamAAD(r.scope, r.nonce, r.counter, last)
	}

	plain, err := r.aead.Open(r.plainBuf[:0], nonce, sealed, aad)
	if err != nil {
		return false, fmt.Errorf("%w: sealed content", ErrDecrypt)
	}

	r.plainBuf = plain
	r.counter++
	r.final = last == streamFinal
	r.pending = plain

	// An empty final chunk buffers no plaintext: the caller still sees EOF
	// only after this call returns. Report not done so Read loops into the
	// trailing check above.
	return false, nil
}
