package backup

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// TestSQLVerifierKeepsBytesSentWithEOF proves the verifier passes on the
// last bytes of a reader that returns them together with io.EOF, and still
// reports a mismatch on such a reader.
func TestSQLVerifierKeepsBytesSentWithEOF(t *testing.T) {
	t.Parallel()

	data := []byte("CREATE TABLE t (x INTEGER);")
	sum := sha256.Sum256(data)

	tests := []struct {
		name     string
		wantSHA  []byte
		wantSize int64
		wantErr  error
	}{
		{name: "match", wantSHA: sum[:], wantSize: int64(len(data)), wantErr: nil},
		{name: "size differs", wantSHA: sum[:], wantSize: int64(len(data)) + 1, wantErr: ErrCorrupt},
		{name: "hash differs", wantSHA: make([]byte, sha256.Size), wantSize: int64(len(data)), wantErr: ErrCorrupt},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			verifier := &sqlVerifier{
				reader:   io.NopCloser(iotest.DataErrReader(bytes.NewReader(data))),
				hash:     sha256.New(),
				wantSHA:  tc.wantSHA,
				wantSize: tc.wantSize,
				size:     0,
				err:      nil,
			}

			got, err := io.ReadAll(verifier)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("read error = %v, want %v", err, tc.wantErr)
			}

			if tc.wantErr == nil && !bytes.Equal(got, data) {
				t.Fatalf("read %q, want %q", got, data)
			}
		})
	}
}
