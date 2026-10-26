package blob

import (
	"crypto/md5" //nolint:gosec // The MD5 checks a backup copy of a blob against its source, for integrity, not security.
	"errors"
	"fmt"
	"hash"
	"io"
)

// Digest is the size and the MD5 of the bytes of one blob, as the blob store
// holds them. A backup checks its copy of a blob against these two values.
// The MD5 is an integrity check, not a security check.
type Digest struct {
	Size int64
	MD5  [md5.Size]byte
}

// IsZero reports whether d holds no value: no byte and an MD5 of all zero.
func (d Digest) IsZero() bool {
	return d.Size == 0 && d.MD5 == [md5.Size]byte{}
}

// DigestReader passes the bytes of a reader and measures them. Read the digest
// after the reader returned io.EOF.
type DigestReader struct {
	src  io.Reader
	hash hash.Hash
	size int64
}

// NewMD5 returns a new MD5 hash. It is the one place that makes an MD5 hash for
// the checks of stored bytes.
func NewMD5() hash.Hash {
	//nolint:gosec // The MD5 checks a backup copy of a blob against its source, for integrity, not security.
	return md5.New()
}

// NewDigestReader returns a DigestReader over src.
func NewDigestReader(src io.Reader) *DigestReader {
	return &DigestReader{src: src, hash: NewMD5(), size: 0}
}

// DigestBytes returns the digest of data.
func DigestBytes(data []byte) Digest {
	h := NewMD5()
	_, _ = h.Write(data) // A hash never fails to write.

	var sum [md5.Size]byte

	copy(sum[:], h.Sum(nil))

	return Digest{Size: int64(len(data)), MD5: sum}
}

// Read implements io.Reader.
func (d *DigestReader) Read(p []byte) (int, error) {
	n, err := d.src.Read(p)
	d.size += int64(n)
	_, _ = d.hash.Write(p[:n])

	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		return n, io.EOF
	default:
		return n, fmt.Errorf("read blob: %w", err)
	}
}

// Digest returns the size and the MD5 of the bytes read so far.
func (d *DigestReader) Digest() Digest {
	var sum [md5.Size]byte

	copy(sum[:], d.hash.Sum(nil))

	return Digest{Size: d.size, MD5: sum}
}

// DigestOf reads r to its end and returns the digest of its bytes.
func DigestOf(r io.Reader) (Digest, error) {
	d := NewDigestReader(r)
	if _, err := io.Copy(io.Discard, d); err != nil {
		return Digest{}, fmt.Errorf("measure blob: %w", err)
	}

	return d.Digest(), nil
}
