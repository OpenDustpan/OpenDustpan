package secret

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// streamVersion is the format byte of a sealed stream.
	streamVersion byte = 2
	// streamChunkPlain bounds one plaintext chunk of a sealed stream.
	streamChunkPlain = 64 << 10
	// streamNonceSize is the random prefix of a sealed stream.
	streamNonceSize = 8
	// streamFrameHeader is the 4-byte ciphertext length and the final flag.
	streamFrameHeader = 5
	// streamGCMNonceSize is the GCM nonce of one chunk: the stream prefix
	// and the chunk counter.
	streamGCMNonceSize = 12
	// streamAADSep separates the scope binding from the stream binding in
	// the associated data of a chunk.
	streamAADSep = 0x00
	// streamAADKind names stream chunks in their associated data.
	streamAADKind = 's'
)

// streamLast marks whether a chunk ends the stream. It is a byte, not a
// flag, so the chunk position and its end travel as one value.
type streamLast byte

const (
	// streamMore marks a chunk with a successor.
	streamMore streamLast = 0
	// streamFinal marks the last chunk of a stream.
	streamFinal streamLast = 1
)

// streamAAD binds one chunk to its scope, stream, position and end.
func streamAAD(scope Scope, streamNonce []byte, counter uint64, last streamLast) []byte {
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

// streamNonceOf returns the GCM nonce of one chunk: the stream prefix and
// the chunk counter, so every chunk of every stream uses one nonce.
func streamNonceOf(streamNonce []byte, counter uint64) []byte {
	nonce := make([]byte, streamGCMNonceSize)
	copy(nonce, streamNonce)
	// The counter fits in 32 bits for every state the size limit admits;
	// the high bits stay zero.
	//nolint:gosec // The counter fits in 32 bits for every state the size limit admits.
	binary.BigEndian.PutUint32(nonce[8:], uint32(counter))

	return nonce
}

// SealStream seals the plaintext in r with the data key of scope, creating
// the key on first use, and returns a reader of the sealed stream. The
// caller streams the result without holding either side whole.
func (s *Service) SealStream(ctx context.Context, scope Scope, r io.Reader) (io.Reader, error) {
	aead, err := s.dataKey(ctx, scope, true)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, streamNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate stream nonce: %w", err)
	}

	return &sealReader{
		aead: aead, scope: scope, nonce: nonce, src: r,
		counter: 0, header: nil, pending: nil, done: false, srcDone: false,
		plainBuf: nil, sealBuf: nil, frameBuf: nil,
	}, nil
}

// OpenStream opens a stream sealed by SealStream for the same scope and
// returns a reader of the plaintext. Any cut, reorder, extension or tamper
// fails with ErrDecrypt, with no key, scope or content in the error.
func (s *Service) OpenStream(ctx context.Context, scope Scope, r io.Reader) (io.Reader, error) {
	aead, err := s.dataKey(ctx, scope, false)
	if err != nil {
		return nil, err
	}

	return &openReader{
		aead: aead, scope: scope, src: r,
		nonce: nil, counter: 0, pending: nil, final: false, header: false,
	}, nil
}

// SealState seals the plaintext in r with the data key of the workspace
// scope (the namespace and the workspace ID), creating the key on first use.
func (s *Service) SealState(
	ctx context.Context, ns namespace.Name, workspaceID string, r io.Reader,
) (io.Reader, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	return s.SealStream(ctx, scope, r)
}

// OpenState opens a stream sealed by SealState for the same workspace scope.
func (s *Service) OpenState(
	ctx context.Context, ns namespace.Name, workspaceID string, r io.Reader,
) (io.Reader, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	return s.OpenStream(ctx, scope, r)
}

// SealOutputs seals one outputs document with the data key of the workspace
// scope, creating the key on first use. It uses the stream seal, so a
// document up to the workspace outputs limit seals whatever its size.
func (s *Service) SealOutputs(
	ctx context.Context, ns namespace.Name, workspaceID string, plain []byte,
) ([]byte, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	sealedReader, err := s.SealStream(ctx, scope, bytes.NewReader(plain))
	if err != nil {
		return nil, err
	}

	sealed, err := io.ReadAll(sealedReader)
	if err != nil {
		return nil, fmt.Errorf("seal outputs: %w", err)
	}

	return sealed, nil
}

// OpenOutputs opens one outputs document sealed by SealOutputs.
func (s *Service) OpenOutputs(
	ctx context.Context, ns namespace.Name, workspaceID string, sealed []byte,
) ([]byte, error) {
	scope, err := NewScope(ns, workspaceID)
	if err != nil {
		return nil, err
	}

	opened, err := s.OpenStream(ctx, scope, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}

	plain, err := io.ReadAll(opened)
	if err != nil {
		return nil, fmt.Errorf("open outputs: %w", err)
	}

	return plain, nil
}

// sealReader seals one stream chunk at a time.
type sealReader struct {
	aead  cipher.AEAD
	scope Scope
	nonce []byte
	src   io.Reader

	counter uint64
	header  []byte
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
	} else {
		plain = nil
	}

	final := r.srcDone

	last := streamMore
	if final {
		last = streamFinal
	}

	aad := streamAAD(r.scope, r.nonce, r.counter, last)
	r.sealBuf = r.aead.Seal(r.sealBuf[:0], streamNonceOf(r.nonce, r.counter), plain, aad)
	sealed := r.sealBuf

	var ln [4]byte
	// A sealed chunk holds one plaintext chunk plus the GCM overhead, so
	// its length always fits the 4-byte frame field.
	//nolint:gosec // A sealed chunk holds one plaintext chunk plus the GCM overhead.
	binary.BigEndian.PutUint32(ln[:], uint32(len(sealed)))

	pending := r.frameBuf[:0]

	if r.header == nil {
		head := make([]byte, 0, 1+len(r.nonce))
		head = append(head, streamVersion)
		head = append(head, r.nonce...)
		r.header = head
		pending = append(pending, head...)
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

// openReader opens one sealed stream chunk at a time.
type openReader struct {
	aead  cipher.AEAD
	scope Scope
	src   io.Reader

	nonce   []byte
	counter uint64
	pending []byte
	final   bool
	header  bool
}

// Read opens the next chunk when its buffer runs dry.
func (r *openReader) Read(p []byte) (int, error) {
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

// openNext reads one sealed frame and buffers its plaintext. It reports
// done when the stream ends after its final chunk.
func (r *openReader) openNext() (bool, error) {
	if r.final {
		return r.checkTrailing()
	}

	if !r.header {
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

// readHeader reads the stream header: the format byte and the stream nonce.
func (r *openReader) readHeader() error {
	head := make([]byte, 1+streamNonceSize)
	if _, err := io.ReadFull(r.src, head); err != nil {
		return fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	if head[0] != streamVersion {
		return fmt.Errorf("%w: sealed stream header", ErrDecrypt)
	}

	r.nonce = bytes.Clone(head[1:])
	r.header = true

	return nil
}

// readFrame reads one sealed frame and buffers its plaintext.
func (r *openReader) readFrame() (bool, error) {
	frame := make([]byte, streamFrameHeader)
	if _, err := io.ReadFull(r.src, frame); err != nil {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	length := binary.BigEndian.Uint32(frame[:4])

	last := streamMore
	if frame[4] == 1 {
		last = streamFinal
	}

	if frame[4] != 0 && frame[4] != 1 {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	// A sealed frame holds one plaintext chunk plus the GCM overhead, which
	// is non-negative, so the bound fits the comparison.
	//nolint:gosec // The GCM overhead is non-negative, so the bound fits the comparison.
	if uint64(length) > uint64(streamChunkPlain)+uint64(r.aead.Overhead()) {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	sealed := make([]byte, length)
	if _, err := io.ReadFull(r.src, sealed); err != nil {
		return false, fmt.Errorf("%w: sealed stream frame", ErrDecrypt)
	}

	aad := streamAAD(r.scope, r.nonce, r.counter, last)

	plain, err := r.aead.Open(nil, streamNonceOf(r.nonce, r.counter), sealed, aad)
	if err != nil {
		return false, fmt.Errorf("%w: sealed content", ErrDecrypt)
	}

	r.counter++
	r.final = last == streamFinal
	r.pending = plain

	// An empty final chunk buffers no plaintext: the caller still sees EOF
	// only after this call returns. Report not done so Read loops into the
	// trailing check above.
	return false, nil
}
